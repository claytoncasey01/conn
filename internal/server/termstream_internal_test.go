package server

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"

	"github.com/coder/websocket"

	"github.com/claytoncasey01/conn/internal/pty"
)

// errBroken is a failure the terminal does not know.
var errBroken = errors.New("broken")

// The host's answer to input or a resize decides whether the terminal goes on: a program that
// has ended (its end is reported on the output side), is not taking input, or is no longer held
// leaves the terminal open; a size out of range closes it as the client's fault; anything else
// is the server's failure.
func TestOutcomeOfTheHostsAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want websocket.StatusCode
		keep bool
	}{
		{"taken", nil, 0, true},
		{"the program ended", fmt.Errorf("writing: %w", pty.ErrEnded), 0, true},
		{"not taking input", fmt.Errorf("writing: %w", pty.ErrInputBlocked), 0, true},
		{"no longer held", fmt.Errorf("writing: %w", pty.ErrNoSession), 0, true},
		{
			"a size out of range", fmt.Errorf("resizing: %w", pty.ErrInvalidSize),
			websocket.StatusPolicyViolation, false,
		},
		// The host turns these into ErrEnded and ErrInputBlocked; unturned, they are failures.
		{
			"an EIO the host did not turn", fmt.Errorf("w: %w", syscall.EIO),
			websocket.StatusInternalError, false,
		},
		{
			"a deadline the host did not turn", fmt.Errorf("w: %w", os.ErrDeadlineExceeded),
			websocket.StatusInternalError, false,
		},
		{"another failure", errBroken, websocket.StatusInternalError, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, _, keep := outcome(tt.err)
			if keep != tt.keep {
				t.Fatalf("keep %v, want %v", keep, tt.keep)
			}

			if code != tt.want {
				t.Fatalf("code %v, want %v", code, tt.want)
			}
		})
	}
}

// How a terminal ends once its viewer's Output has closed follows from why the host closed it.
func TestTheEndOfOutputFollowsTheHostsReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		end      pty.ViewerEnd
		sendExit bool
		want     websocket.StatusCode
	}{
		{
			"the program ended",
			pty.ViewerEnd{Dropped: false, Ended: true, ExitCode: 3},
			true, websocket.StatusNormalClosure,
		},
		{
			"the viewer fell behind",
			pty.ViewerEnd{Dropped: true, Ended: false, ExitCode: 0},
			false, websocket.StatusTryAgainLater,
		},
		{
			"the host said nothing",
			pty.ViewerEnd{Dropped: false, Ended: false, ExitCode: 0},
			false, websocket.StatusInternalError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sendExit, code, _ := endOfOutput(tt.end)
			if sendExit != tt.sendExit {
				t.Fatalf("send the exit %v, want %v", sendExit, tt.sendExit)
			}

			if code != tt.want {
				t.Fatalf("code %v, want %v", code, tt.want)
			}
		})
	}
}
