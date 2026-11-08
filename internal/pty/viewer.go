package pty

import "slices"

// viewer is one Viewer's queue. Its pump goroutine hands what is pending to Output, a whole
// batch at a time, so many small writes cost one slot of the queue, not one each. The fields
// after output are guarded by Host.mu.
type viewer struct {
	output chan []byte
	// wakeup tells the pump that something is pending, or that the session ended.
	wakeup chan struct{}
	// stopped is closed when the viewer is closed or dropped: the pump closes Output at once.
	stopped chan struct{}

	pending []byte
	// ending is set when the session has ended: the pump hands over what is pending, then closes
	// Output.
	ending    bool
	isStopped bool
	// why is why Output closed, once the host has decided it.
	why ViewerEnd
}

func newViewer() *viewer {
	return &viewer{
		output:    make(chan []byte),
		wakeup:    make(chan struct{}, 1),
		stopped:   make(chan struct{}),
		pending:   nil,
		ending:    false,
		isStopped: false,
		why:       ViewerEnd{Dropped: false, Ended: false, ExitCode: 0},
	}
}

// wake tells the pump to look, without waiting. The caller holds Host.mu.
func (v *viewer) wake() {
	select {
	case v.wakeup <- struct{}{}:
	default:
	}
}

// stop ends the viewer once; what is pending is dropped. The caller holds Host.mu.
func (v *viewer) stop() {
	if v.isStopped {
		return
	}

	v.isStopped = true
	v.pending = nil
	close(v.stopped)
}

// Subscribe adds a viewer to the session. A session that has ended gives its scrollback and an
// Output that is already closed. A viewer holds one of the session's viewersMax places until it
// is closed, or dropped for falling behind: close every viewer when its client goes.
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

		ended := ViewerEnd{Dropped: false, Ended: true, ExitCode: *watched.exitCode}

		return Viewer{
			Replay: watched.replay(), Output: output,
			Ending: func() ViewerEnd { return ended }, close: nil,
		}, nil
	}

	if len(watched.viewers) >= h.limits.ViewersMax {
		return Viewer{}, ErrViewersFull
	}

	// The replay and the registration happen under one lock, so no output is lost or doubled
	// between them.
	added := newViewer()
	watched.viewers = append(watched.viewers, added)

	go h.pump(added)

	return Viewer{
		Replay: watched.replay(),
		Output: added.output,
		Ending: func() ViewerEnd { return h.viewerEnd(added) },
		close:  func() { h.unsubscribe(watched, added) },
	}, nil
}

// viewerEnd is why the viewer's Output closed, as far as the host has decided.
func (h *Host) viewerEnd(watching *viewer) ViewerEnd {
	h.mu.Lock()
	defer h.mu.Unlock()

	return watching.why
}

// pump is a viewer's goroutine: it hands each batch of pending output to Output, and closes
// Output when the viewer stops, or when the session has ended and everything pending is handed
// over. It ends then too.
func (h *Host) pump(watching *viewer) {
	defer close(watching.output)

	for {
		select {
		case <-watching.wakeup:
		case <-watching.stopped:
			return
		}

		batch, last := h.takePending(watching)
		if len(batch) > 0 {
			select {
			case watching.output <- batch:
			case <-watching.stopped:
				return
			}
		}

		if last {
			return
		}
	}
}

// takePending takes what is pending for the viewer, and whether the session has ended.
func (h *Host) takePending(watching *viewer) ([]byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	batch := watching.pending
	watching.pending = nil

	return batch, watching.ending
}

// unsubscribe stops a viewer and frees its place, whether or not the session has ended.
func (h *Host) unsubscribe(watched *session, leaving *viewer) {
	h.mu.Lock()
	defer h.mu.Unlock()

	leaving.stop()

	index := slices.Index(watched.viewers, leaving)
	if index >= 0 {
		watched.viewers = slices.Delete(watched.viewers, index, index+1)
	}
}

// replay is what a new viewer is sent first: the modes the program's screen is in, then the
// scrollback. The caller holds Host.mu; the screen's own lock is taken inside it, and nothing
// takes Host.mu while holding the screen's.
func (watched *session) replay() []byte {
	watched.screenMu.Lock()
	preamble := watched.screen.ModePreamble(watched.scrollback.windowStart())
	watched.screenMu.Unlock()

	return watched.scrollback.contentsAfter(preamble)
}
