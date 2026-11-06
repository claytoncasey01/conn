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

// onlyFocusReports is whether data is nothing but focus reports. The loop takes one report a
// turn, so it runs at most len(data) / len(focusIn) times.
func onlyFocusReports(data []byte) bool {
	rest := string(data)
	for rest != "" {
		switch {
		case strings.HasPrefix(rest, focusIn):
			rest = rest[len(focusIn):]
		case strings.HasPrefix(rest, focusOut):
			rest = rest[len(focusOut):]
		default:
			return false
		}
	}

	return len(data) > 0
}

// redrawsWaitedMax bounds how many redraws one WaitScreen looks at: well above what a program
// draws within any wait Conn makes (Claude Code's spinner redraws about ten times a second).
const redrawsWaitedMax = 1 << 16

// WaitScreen waits until the bottom count lines of the session's screen pass still, and returns
// them. It looks again each time what the screen shows changes, so it returns as soon as the
// program has drawn what was waited for. It ends with ctx's error when ctx ends first, with
// ErrEnded when the session ends first, and with ErrScreenChanged after redrawsWaitedMax
// redraws. ctx must have a deadline: a wait without one is a mistake in the code.
func (h *Host) WaitScreen(
	ctx context.Context, sessionID ID, count int, still func(lines []string) bool,
) ([]string, error) {
	if _, bounded := ctx.Deadline(); !bounded {
		panic("pty: WaitScreen needs a context with a deadline")
	}

	held, err := h.find(sessionID)
	if err != nil {
		return nil, err
	}

	for range redrawsWaitedMax {
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

	return nil, fmt.Errorf("%w: %d redraws waited for", ErrScreenChanged, redrawsWaitedMax)
}
