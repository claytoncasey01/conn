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

	buffer := make([]byte, readBufferBytes)

	for {
		count, err := reading.process.Read(buffer)
		if count > 0 {
			h.publish(reading, buffer[:count])
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

// awaitEnd is a session's waiter. Once the program has exited it stops whatever is left of its
// process group (a polite stop, then a kill after the grace), so a session is never recorded as
// ended while anything it started runs on. It then lets the reader drain for at most
// outputDrainTimeout, closes the terminal so the reader ends, and records the end.
func (h *Host) awaitEnd(waiting *session, readDone <-chan struct{}) {
	code := waiting.process.Wait()

	// The waiter never gives up on the group (a nil channel never delivers). A group that cannot
	// be signalled has no one left to signal it: the end is recorded anyway.
	_ = stopGroup(nil, waiting.process, h.limits.StopGrace)

	drain := time.NewTimer(outputDrainTimeout)
	select {
	case <-readDone:
	case <-drain.C:
	}

	drain.Stop()

	// The program has exited; a terminal that fails to close has nothing left to lose.
	_ = waiting.process.Close()

	<-readDone

	h.finish(waiting, code)
}

// finish records that a session ended with code, ends its viewers once they have been handed
// what was queued for them, frees its running slot and forgets the oldest ended session past
// endedMax.
func (h *Host) finish(ending *session, code int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ending.exitCode = &code

	for _, watching := range ending.viewers {
		watching.ending = true
		watching.wake()
	}

	ending.viewers = nil
	h.running--

	h.ended = append(h.ended, ending.sessionID)
	if len(h.ended) > endedMax {
		oldest := h.ended[0]
		h.ended = slices.Delete(h.ended, 0, 1)
		delete(h.sessions, oldest)
		h.order = slices.DeleteFunc(h.order, func(sessionID ID) bool { return sessionID == oldest })
	}

	close(ending.ended)
}
