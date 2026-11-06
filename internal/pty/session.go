package pty

import (
	"bytes"
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
			h.publish(reading, bytes.Clone(buffer[:count]))
		}

		if err != nil {
			return
		}
	}
}

// publish adds chunk to the scrollback and to every viewer's queue, dropping each viewer whose
// queue is full.
func (h *Host) publish(target *session, chunk []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()

	target.scrollback.write(chunk)

	kept := target.viewers[:0]

	for _, watching := range target.viewers {
		select {
		case watching.output <- chunk:
			kept = append(kept, watching)
		default:
			close(watching.output)
		}
	}

	clear(target.viewers[len(kept):])
	target.viewers = kept
}

// awaitEnd is a session's waiter: it waits for the program to exit, lets the reader drain what is
// left for at most outputDrainTimeout, closes the terminal so the reader ends, and records the end.
func (h *Host) awaitEnd(waiting *session, readDone <-chan struct{}) {
	code := waiting.process.Wait()

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

// finish records that a session ended with code, closes its viewers, frees its running slot and
// forgets the oldest ended session past endedMax.
func (h *Host) finish(ending *session, code int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ending.exitCode = &code

	for _, watching := range ending.viewers {
		close(watching.output)
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

// Subscribe adds a viewer to the session. A session that has ended gives its scrollback and an
// Output that is already closed.
func (h *Host) Subscribe(sessionID ID) (Viewer, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	watched, found := h.sessions[sessionID]
	if !found {
		return Viewer{}, ErrNoSession
	}

	if watched.exitCode != nil {
		output := make(chan []byte)
		close(output)

		return Viewer{Replay: watched.scrollback.contents(), Output: output, close: nil}, nil
	}

	if len(watched.viewers) >= h.limits.ViewersMax {
		return Viewer{}, ErrViewersFull
	}

	// The replay and the registration happen under one lock, so no output is lost or doubled
	// between them.
	added := &viewer{output: make(chan []byte, viewerQueueChunks)}
	watched.viewers = append(watched.viewers, added)

	return Viewer{
		Replay: watched.scrollback.contents(),
		Output: added.output,
		close:  func() { h.unsubscribe(watched, added) },
	}, nil
}

// unsubscribe removes a viewer and closes its queue, unless it is already gone.
func (h *Host) unsubscribe(watched *session, leaving *viewer) {
	h.mu.Lock()
	defer h.mu.Unlock()

	index := slices.Index(watched.viewers, leaving)
	if index < 0 {
		return
	}

	close(leaving.output)

	watched.viewers = slices.Delete(watched.viewers, index, index+1)
}
