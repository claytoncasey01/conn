package pty_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
)

// TestStopIfStillStopsOnlyWhileTheReadStands: a stop checked against a screen read is sent only
// if, in the write turn, no input came after the read, the program holds the terminal and does
// not read a line, and the screen still passes the check; otherwise nothing is signalled.
func TestStopIfStillStopsOnlyWhileTheReadStands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// after is what happens between the read and the stop.
		after   stillStep
		still   func(lines []string) bool
		wantErr error
	}{
		{"nothing", nil, emptyBoxShown, nil},
		{"a key typed", viewerKey, emptyBoxShown, pty.ErrOtherInput},
		{"a program in the foreground", backgrounded, emptyBoxShown, pty.ErrNotForeground},
		{"a line being read", lineMode, emptyBoxShown, pty.ErrLineMode},
		{
			"the box changed", drawnThen("\r\nbox: draft", "draft"), emptyBoxShown,
			pty.ErrScreenChanged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host, spawner := newFakeHost(t, pty.DefaultLimits())

			sessionID, err := host.Start(t.Context(), testSpec(t))
			if err != nil {
				t.Fatal(err)
			}

			process := spawner.last(t)
			process.feed(t, []byte("box: "))
			waitChanged(t, host, sessionID)
			time.Sleep(settleWait)

			_, mark, err := host.ScreenLines(sessionID, 24)
			if err != nil {
				t.Fatal(err)
			}

			if tt.after != nil {
				tt.after(t, host, sessionID, process)
			}

			err = host.StopIfStill(t.Context(), sessionID, mark, 24, tt.still)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err %v, want %v", err, tt.wantErr)
			}

			signals := process.signalsSoFar()
			if tt.wantErr != nil {
				if len(signals) != 0 {
					t.Errorf("a refused stop signalled %v", signals)
				}

				return
			}

			if !slices.Equal(signals[:1], []string{signalTerminate}) {
				t.Errorf("signals %v, want a polite stop first", signals)
			}
		})
	}
}

// emptyBoxShown is the fake's empty input box: its last line, "box:", with nothing after.
func emptyBoxShown(lines []string) bool {
	for _, line := range slices.Backward(lines) {
		if strings.TrimSpace(line) == "" {
			continue
		}

		return strings.TrimSpace(line) == "box:"
	}

	return false
}

// drawnThen is a step where the program prints output and the screen shows shown.
func drawnThen(output, shown string) stillStep {
	return func(t *testing.T, host *pty.Host, sessionID pty.ID, process *fakeProcess) {
		t.Helper()

		process.feed(t, []byte(output))

		for range waitLimit / waitPoll {
			lines, _, err := host.ScreenLines(sessionID, 24)
			if err != nil {
				t.Fatal(err)
			}

			if strings.Contains(strings.Join(lines, "\n"), shown) {
				return
			}

			time.Sleep(waitPoll)
		}

		t.Fatalf("the screen never showed %q", shown)
	}
}
