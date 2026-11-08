package main

import (
	"context"
	"testing"
)

// Closing the window hides it and keeps the app: desk.Run is not ended, so the server it owns
// and every hosted session run on, and nothing is recorded or stopped.
func TestClosingTheWindowOnlyHidesIt(t *testing.T) {
	t.Parallel()

	deskCtx, stopDesk := context.WithCancel(t.Context())
	defer stopDesk()

	opened := &shell{stopDesk: stopDesk, deskDone: make(chan struct{})}
	hidden, cancelled := false, false

	opened.windowClosing(func() { hidden = true }, func() { cancelled = true })

	if !hidden {
		t.Error("the window was not hidden")
	}

	if !cancelled {
		t.Error("the close was not cancelled, so the window would be destroyed")
	}

	if deskCtx.Err() != nil {
		t.Error("closing the window ended desk.Run, which stops the server and its sessions")
	}
}
