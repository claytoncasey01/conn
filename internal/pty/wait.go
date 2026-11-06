package pty

import (
	"context"
	"fmt"
	"strings"
)

// focusIn and focusOut are the reports a terminal sends when it gains or loses the focus, once
// the program has asked for them (mode 1004).
const (
	focusIn  = "\x1b[I"
	focusOut = "\x1b[O"
)

// onlyFocusReports is whether data is nothing but focus reports.
func onlyFocusReports(data []byte) bool {
	rest := string(data)
	for rest != "" {
		switch {
		case strings.HasPrefix(rest, focusIn), strings.HasPrefix(rest, focusOut):
			rest = rest[len(focusIn):]
		default:
			return false
		}
	}

	return len(data) > 0
}

// WaitScreen waits until the bottom count lines of the session's screen pass still, and returns
// them. It looks again each time what the screen shows changes, so it returns as soon as the
// program has drawn what was waited for. It ends with ctx's error when ctx ends first, and with
// ErrEnded when the session ends first.
func (h *Host) WaitScreen(
	ctx context.Context, sessionID ID, count int, still func(lines []string) bool,
) ([]string, error) {
	held, err := h.find(sessionID)
	if err != nil {
		return nil, err
	}

	for {
		held.screenMu.Lock()
		lines := held.screen.Lines(count)
		redrawn := held.redrawn
		held.screenMu.Unlock()

		if still(lines) {
			return lines, nil
		}

		select {
		case <-redrawn:
		case <-held.ended:
			return nil, ErrEnded
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for the screen: %w", ctx.Err())
		}
	}
}
