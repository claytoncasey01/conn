package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/claytoncasey01/conn/internal/pty"
)

// The reasons a terminal is closed with, in the server's own words: never the frame's content.
const (
	reasonExited       = "the program exited"
	reasonStopping     = "conn is stopping"
	reasonBehind       = "fell behind the program's output"
	reasonMalformed    = "malformed frame"
	reasonTextOnly     = "only text frames are taken"
	reasonInputRate    = "input over the session's rate"
	reasonInputBacklog = "input backed up behind the program"
	reasonBadSize      = "terminal size out of range"
	reasonHostFailed   = "the terminal failed"
)

// link is one open terminal: its connection, and the end of its goroutines' shared context.
// The first end wins; the others do nothing.
type link struct {
	conn   *websocket.Conn
	cancel context.CancelFunc
	once   sync.Once
}

// end closes the terminal with code and reason, then ends its goroutines. The close waits a
// bounded time (the library's) for the peer to close too.
func (l *link) end(code websocket.StatusCode, reason string) {
	l.once.Do(func() {
		// A peer that has gone cannot be told why; nothing else is to be done about it.
		_ = l.conn.Close(code, reason)

		l.cancel()
	})
}

// drop hangs up on the peer at once, for a peer that has gone or does not answer.
func (l *link) drop() {
	l.once.Do(func() {
		_ = l.conn.CloseNow()

		l.cancel()
	})
}

// terminal is one open terminal's stream: the viewer, the replay it joined with (handed on
// alone, so it is not kept once written), its session, and what to call once the replay is
// written.
type terminal struct {
	viewer        pty.Viewer
	replay        []byte
	sessionID     pty.ID
	replayWritten func()
}

// run is an open terminal: the program's output goes out (on this goroutine), the viewer's
// frames come in and wait in a queue for the program, pings check the peer is there, and the
// terminals stopping closes it, until one of them ends the terminal. It returns once all have
// ended.
func (t *Terminals) run(conn *websocket.Conn, streamed terminal) {
	// The request's context is not used once the connection is taken over; the terminal's own
	// ends with it.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	open := &link{conn: conn, cancel: cancel, once: sync.Once{}}
	queue := make(chan clientFrame, inputQueueFramesMax)

	var group sync.WaitGroup

	group.Go(func() { t.readFrames(ctx, open, streamed.sessionID, queue) })
	group.Go(func() { t.typeFrames(ctx, open, streamed.sessionID, queue) })
	group.Go(func() { t.ping(ctx, open) })
	group.Go(func() { t.watchStopping(ctx, open) })

	t.writeOutput(ctx, open, streamed)

	open.drop()
	group.Wait()
}

// watchStopping closes the terminal as going away once the terminals are stopping, whatever it
// is writing: the close is queued behind the frame being written, and nothing after it is.
func (t *Terminals) watchStopping(ctx context.Context, open *link) {
	select {
	case <-t.stopping:
		open.end(websocket.StatusGoingAway, reasonStopping)
	case <-ctx.Done():
	}
}

// writeOutput sends the viewer's replay, then its end, then the program's output as it comes,
// until the viewer's output closes (the terminal then ends as the host says why) or the
// terminal is ended.
func (t *Terminals) writeOutput(ctx context.Context, open *link, streamed terminal) {
	if !t.writeBinary(ctx, open, streamed.replay) {
		return
	}

	streamed.replayWritten()

	if !t.writeText(ctx, open, []byte(replayEndText)) {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case batch, more := <-streamed.viewer.Output:
			if !more {
				t.outputEnded(ctx, open, streamed.viewer)

				return
			}

			if !t.writeBinary(ctx, open, batch) {
				return
			}
		}
	}
}

// outputEnded ends the terminal once the viewer's output has closed, as the host says why: the
// program has ended, and its exit is reported, or the host dropped the viewer for falling behind.
func (t *Terminals) outputEnded(ctx context.Context, open *link, viewer pty.Viewer) {
	end := viewer.Ended()
	sendExit, code, reason := endOfOutput(end)

	if sendExit {
		if !t.writeText(ctx, open, exitText(end.ExitCode)) {
			return
		}
	}

	open.end(code, reason)
}

// endOfOutput is how a terminal ends once its viewer's output has closed for end's reason:
// whether to report the exit first, and the close. A program that ended is a normal close after
// its exit; a viewer that fell behind is told to come back (1013), which loses nothing it can
// replay; a host that says neither is a failure of the server's.
func endOfOutput(end pty.ViewerEnd) (bool, websocket.StatusCode, string) {
	if end.Ended {
		return true, websocket.StatusNormalClosure, reasonExited
	}

	if end.Dropped {
		return false, websocket.StatusTryAgainLater, reasonBehind
	}

	return false, websocket.StatusInternalError, reasonHostFailed
}

// writeBinary sends output in frames of at most outputFrameBytesMax, each within
// frameWriteTimeout; a peer that cannot take one in time is dropped. It says whether all went.
func (t *Terminals) writeBinary(ctx context.Context, open *link, output []byte) bool {
	for len(output) > 0 {
		size := min(len(output), outputFrameBytesMax)

		if !t.writeFrame(ctx, open, websocket.MessageBinary, output[:size]) {
			return false
		}

		output = output[size:]
	}

	return true
}

// writeText sends one text frame within frameWriteTimeout, and says whether it went.
func (t *Terminals) writeText(ctx context.Context, open *link, text []byte) bool {
	return t.writeFrame(ctx, open, websocket.MessageText, text)
}

func (t *Terminals) writeFrame(
	ctx context.Context, open *link, kind websocket.MessageType, data []byte,
) bool {
	writeCtx, cancel := context.WithTimeout(ctx, t.times.frameWriteTimeout)
	defer cancel()

	err := open.conn.Write(writeCtx, kind, data)
	if err != nil {
		open.drop()

		return false
	}

	return true
}

// readFrames reads the viewer's frames, checks each and queues it for the program, until the
// peer goes or a frame ends the terminal. It never waits for the program, so the peer's pongs
// are always read.
func (t *Terminals) readFrames(
	ctx context.Context, open *link, sessionID pty.ID, queue chan<- clientFrame,
) {
	for {
		// The read limit is frameBytesMax: a longer frame closes the terminal (the library
		// closes it as too big) without being read whole.
		kind, data, err := open.conn.Read(ctx)
		if err != nil {
			open.drop()

			return
		}

		code, reason, keep := t.takeFrame(sessionID, kind, data, queue)
		if !keep {
			open.end(code, reason)

			return
		}
	}
}

// takeFrame checks one frame from the viewer and queues it for the program, and says how to
// close the terminal if it may not go on.
func (t *Terminals) takeFrame(
	sessionID pty.ID, kind websocket.MessageType, data []byte, queue chan<- clientFrame,
) (websocket.StatusCode, string, bool) {
	if kind != websocket.MessageText {
		return websocket.StatusUnsupportedData, reasonTextOnly, false
	}

	frame, err := parseClientFrame(data)
	if err != nil {
		return websocket.StatusPolicyViolation, reasonMalformed, false
	}

	if !t.budgets.spend(sessionID, frame.cost()) {
		return websocket.StatusPolicyViolation, reasonInputRate, false
	}

	select {
	case queue <- frame:
		return 0, "", true
	default:
		return websocket.StatusPolicyViolation, reasonInputBacklog, false
	}
}

// typeFrames hands the queued frames to the host in order, until the terminal ends or the host's
// answer ends it.
func (t *Terminals) typeFrames(
	ctx context.Context, open *link, sessionID pty.ID, queue <-chan clientFrame,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-queue:
			code, reason, keep := outcome(t.apply(sessionID, frame))
			if !keep {
				open.end(code, reason)

				return
			}
		}
	}
}

// apply hands one frame to the host.
func (t *Terminals) apply(sessionID pty.ID, frame clientFrame) error {
	switch frame.kind {
	case frameInput:
		err := t.host.Write(sessionID, frame.data)
		if err != nil {
			return fmt.Errorf("typing into a session: %w", err)
		}

		return nil
	case frameResize:
		err := t.host.Resize(sessionID, frame.columns, frame.rows)
		if err != nil {
			return fmt.Errorf("resizing a session: %w", err)
		}

		return nil
	default:
		panic("server: a parsed frame of no kind")
	}
}

// outcome is whether the terminal goes on after the host's answer to input or a resize. Input
// for a program that has ended (or is ending, or is no longer held), or that is not taking input,
// is lost, as on any terminal; an end is reported on the output side. A size out of range closes
// the terminal; anything else is a failure of the server's.
func outcome(err error) (websocket.StatusCode, string, bool) {
	switch {
	case err == nil:
		return 0, "", true
	case errors.Is(err, pty.ErrEnded):
		return 0, "", true
	case errors.Is(err, pty.ErrNoSession):
		return 0, "", true
	case errors.Is(err, pty.ErrInputBlocked):
		return 0, "", true
	case errors.Is(err, pty.ErrInvalidSize):
		return websocket.StatusPolicyViolation, reasonBadSize, false
	default:
		return websocket.StatusInternalError, reasonHostFailed, false
	}
}

// ping pings the peer every pingInterval and drops it if it does not answer within pingTimeout.
func (t *Terminals) ping(ctx context.Context, open *link) {
	ticker := time.NewTicker(t.times.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		pingCtx, cancel := context.WithTimeout(ctx, t.times.pingTimeout)
		err := open.conn.Ping(pingCtx)

		cancel()

		if err != nil {
			open.drop()

			return
		}
	}
}
