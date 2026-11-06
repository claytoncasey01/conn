package pty

import (
	"slices"
	"time"
)

// readOutput is a session's reader: it copies the program's output into the scrollback and the
// viewers' queues until the terminal reports an error, which it does once the program has hung up
// or the terminal is closed. It never waits for a viewer.
func (h *Host) readOutput(reading *session, readDone chan<- struct{}) {
	defer close(readDone)
	// The reader is the only one that queues answers, so it closes their queue.
	defer close(reading.answers)

	buffer := make([]byte, readBufferBytes)

	for {
		count, err := reading.process.Read(buffer)
		if count > 0 {
			h.publish(reading, buffer[:count])
			h.observe(reading, buffer[:count])
		}

		if err != nil {
			return
		}
	}
}

// publish copies chunk into the scrollback and into every viewer's queue, dropping each viewer
// whose queue it would take past viewerQueueBytesMax.
func (h *Host) publish(target *session, chunk []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()

	target.scrollback.write(chunk)

	kept := target.viewers[:0]

	for _, watching := range target.viewers {
		if len(watching.pending)+len(chunk) > viewerQueueBytesMax {
			watching.why = ViewerEnd{Dropped: true, Ended: false, ExitCode: 0}
			watching.stop()

			continue
		}

		watching.pending = append(watching.pending, chunk...)
		watching.wake()
		kept = append(kept, watching)
	}

	clear(target.viewers[len(kept):])
	target.viewers = kept
}

// observe feeds chunk to the session's screen and queues the reply to each query it noted. A
// reply the queue has no room for is dropped: the reader never waits for the program to take
// its input, since a program blocked writing output would then never read it.
func (h *Host) observe(reading *session, chunk []byte) {
	reading.screenMu.Lock()
	before := reading.screen.Generation()
	reading.screen.Feed(chunk)

	if reading.screen.Generation() != before {
		close(reading.redrawn)
		reading.redrawn = make(chan struct{})
	}

	queries := reading.screen.Queries()
	reading.screenMu.Unlock()

	for _, query := range queries {
		select {
		case reading.answers <- Reply(h.answers, query):
		default:
		}
	}
}

// resizeScreen makes the session's screen size and returns the size it was. size has passed
// checkSize, so a screen that refuses it is a mistake in the code.
func (held *session) resizeScreen(size Size) Size {
	held.screenMu.Lock()
	defer held.screenMu.Unlock()

	previous := held.screen.Size()

	err := held.screen.Resize(size)
	if err != nil {
		panic("pty: a screen refused a checked size: " + err.Error())
	}

	return previous
}

// answerQueries is a session's answerer: it types each queued reply into the program, in turn
// with the viewers' input, until the reader closes the queue.
func (h *Host) answerQueries(answering *session) {
	defer close(answering.answered)

	for reply := range answering.answers {
		// A reply the program does not take, or takes after it has ended, is lost, as it would
		// be on any terminal; nothing else is to be done about it.
		_, _ = h.typeInto(answering, reply, typing{check: nil, input: false, closes: false})
	}
}

// awaitEnd is a session's waiter. Once the program has exited it stops whatever is left of its
// process group (a polite stop, then a kill after the grace, then a wait for the group to
// empty), unless a Stop already found it empty, so a session is never retired while anything it
// started runs on. It then lets the reader drain for at most outputDrainTimeout, closes the
// terminal so the reader ends, and records the end, with the stop's failure if it failed.
func (h *Host) awaitEnd(waiting *session, readDone <-chan struct{}) {
	code := waiting.process.Wait()

	// From here input and resizes are refused as ended, though the end is recorded only once
	// the group is stopped and the output drained.
	h.markWaited(waiting)

	var stopErr error

	if !h.isGroupStopped(waiting) {
		// The waiter never gives up on the group: a nil channel never delivers.
		stopErr = stopGroup(nil, waiting.process, h.limits.StopGrace)
	}

	drain := time.NewTimer(outputDrainTimeout)
	select {
	case <-readDone:
	case <-drain.C:
	}

	drain.Stop()

	// The program has exited; a terminal that fails to close has nothing left to lose.
	_ = waiting.process.Close()

	<-readDone
	<-waiting.answered

	h.finish(waiting, code, stopErr)
}

// finish records that a session's program ended with code and ends its viewers once they have
// been handed what was queued for them. If its group is empty (stopErr is nil) the session is
// retired; otherwise the failure is recorded and the session keeps its running slot, and a
// later Stop that empties the group retires it.
func (h *Host) finish(ending *session, code int, stopErr error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ending.exitCode = &code

	for _, watching := range ending.viewers {
		watching.why = ViewerEnd{Dropped: false, Ended: true, ExitCode: code}
		watching.ending = true
		watching.wake()
	}

	ending.viewers = nil

	if stopErr == nil {
		ending.groupStopped = true
	}

	if !ending.groupStopped {
		ending.stopError = "stopping the program's group: " + stopErr.Error()
	}

	h.retire(ending)
	close(ending.ended)
}

// markWaited records that the session's program has exited.
func (h *Host) markWaited(held *session) {
	h.mu.Lock()
	defer h.mu.Unlock()

	held.waited = true
}

// isGroupStopped says whether the session's group is known to be empty.
func (h *Host) isGroupStopped(held *session) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	return held.groupStopped
}

// groupEmptied records that a Stop found the session's group empty, and retires the session if
// its program has ended.
func (h *Host) groupEmptied(held *session) {
	h.mu.Lock()
	defer h.mu.Unlock()

	held.groupStopped = true
	held.stopError = ""
	h.retire(held)
}

// retire gives back the session's running slot and adds it to the ended sessions, forgetting
// the oldest past endedMax, once its program has ended and its group is known to be empty; only
// once. The caller holds h.mu.
func (h *Host) retire(held *session) {
	if held.retired {
		return
	}

	if held.exitCode == nil {
		return
	}

	if !held.groupStopped {
		return
	}

	held.retired = true
	h.running--

	h.ended = append(h.ended, held.sessionID)
	if len(h.ended) > endedMax {
		oldest := h.ended[0]
		h.ended = slices.Delete(h.ended, 0, 1)
		delete(h.sessions, oldest)
		h.order = slices.DeleteFunc(h.order, func(sessionID ID) bool { return sessionID == oldest })
	}
}
