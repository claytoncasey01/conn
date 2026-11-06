package pty_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// errSomethingElse is a write failure the host does not know.
var errSomethingElse = errors.New("something else")

// A viewer's Output closes for one of two reasons, and the viewer says which, as the host knew it
// when it closed the Output: the program ended (with its code), or the viewer fell behind. A
// viewer dropped just before the program ends stays dropped.
func TestAViewerSaysWhyItsOutputClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, host *pty.Host, sessionID pty.ID, process *fakeProcess) pty.Viewer
		want pty.ViewerEnd
	}{
		{"the program ended", endWith(3), pty.ViewerEnd{Dropped: false, Ended: true, ExitCode: 3}},
		{"it fell behind", fallBehind, pty.ViewerEnd{Dropped: true, Ended: false, ExitCode: 0}},
		{
			"it fell behind, then the program ended", fallBehindThenEnd,
			pty.ViewerEnd{Dropped: true, Ended: false, ExitCode: 0},
		},
		{
			"it came after the end", subscribeAfterEnd,
			pty.ViewerEnd{Dropped: false, Ended: true, ExitCode: 5},
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

			viewer := tt.run(t, host, sessionID, spawner.last(t))
			defer viewer.Close()

			if got := viewer.Ended(); got != tt.want {
				t.Fatalf("Ended() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// endWith ends the program with code while a viewer watches, and returns the viewer once its
// Output has closed.
func endWith(code int) func(*testing.T, *pty.Host, pty.ID, *fakeProcess) pty.Viewer {
	return func(t *testing.T, host *pty.Host, sessionID pty.ID, process *fakeProcess) pty.Viewer {
		t.Helper()

		viewer, err := host.Subscribe(sessionID)
		if err != nil {
			t.Fatal(err)
		}

		process.feed(t, []byte("bye"))
		process.finish(code)
		drainUntilClosed(t, viewer.Output)

		return viewer
	}
}

// fallBehind feeds twice a viewer's queue to a viewer that never reads, and returns it once the
// host has dropped it.
func fallBehind(t *testing.T, host *pty.Host, sessionID pty.ID, process *fakeProcess) pty.Viewer {
	t.Helper()

	slow, err := host.Subscribe(sessionID)
	if err != nil {
		t.Fatal(err)
	}

	chunk := bytes.Repeat([]byte{'x'}, pty.ReadBufferBytes)
	for sent := 0; sent < 2*pty.ViewerQueueBytesMax; sent += len(chunk) {
		process.feed(t, chunk)
	}

	drainUntilClosed(t, slow.Output)

	return slow
}

func fallBehindThenEnd(
	t *testing.T, host *pty.Host, sessionID pty.ID, process *fakeProcess,
) pty.Viewer {
	t.Helper()

	slow := fallBehind(t, host, sessionID, process)
	process.finish(0)
	waitEnded(t, host, sessionID)

	return slow
}

func subscribeAfterEnd(
	t *testing.T, host *pty.Host, sessionID pty.ID, process *fakeProcess,
) pty.Viewer {
	t.Helper()

	process.finish(5)
	waitEnded(t, host, sessionID)

	viewer, err := host.Subscribe(sessionID)
	if err != nil {
		t.Fatal(err)
	}

	drainUntilClosed(t, viewer.Output)

	return viewer
}

// A Viewer made by hand, with no host behind it, says nothing about why it closed.
func TestAViewerWithNoHostSaysNothing(t *testing.T) {
	t.Parallel()

	var viewer pty.Viewer

	if got := viewer.Ended(); got != (pty.ViewerEnd{}) {
		t.Fatalf("Ended() = %+v, want the zero ViewerEnd", got)
	}
}

// What the terminal says when a Write fails is turned into what it means for a viewer: the
// program has gone (EIO once nothing holds the terminal's other side, or the terminal closed as
// the session ends), or it is not taking input (the write deadline). Anything else is the
// host's own failure and is neither.
func TestAFailedWriteSaysWhatItMeans(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		failure error
		want    error
	}{
		{"the program's side is gone", fmt.Errorf("write: %w", syscall.EIO), pty.ErrEnded},
		{"the terminal is closed", fmt.Errorf("write: %w", os.ErrClosed), pty.ErrEnded},
		{
			"the write deadline passed", fmt.Errorf("write: %w", os.ErrDeadlineExceeded),
			pty.ErrInputBlocked,
		},
		{"another failure", errSomethingElse, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spawner := &fakes{writeErr: tt.failure}
			host := pty.NewHost(pty.Config{Spawner: spawner.spawn, Limits: pty.DefaultLimits()})

			t.Cleanup(func() { _ = host.StopAll(t.Context()) })

			sessionID, err := host.Start(t.Context(), testSpec(t))
			if err != nil {
				t.Fatal(err)
			}

			err = host.Write(sessionID, []byte("x"))
			if err == nil {
				t.Fatal("a failed write was taken")
			}

			if tt.want == nil {
				for _, meaning := range []error{pty.ErrEnded, pty.ErrInputBlocked} {
					if errors.Is(err, meaning) {
						t.Fatalf("Write = %v, which is not %v", err, meaning)
					}
				}

				return
			}

			if !errors.Is(err, tt.want) {
				t.Fatalf("Write = %v, want %v", err, tt.want)
			}
		})
	}
}

// Once the program has exited, input and resizes are refused as ended at once, though the
// session is recorded as ended only after its group is stopped and its output drained.
func TestInputAfterTheProgramExitsIsEnded(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	process := spawner.last(t)

	// The program exits, but its output stays open: the host waits for it to drain.
	process.exit(0)

	deadline := time.Now().Add(testtime.Scaled(waitLimit))

	for !errors.Is(host.Write(sessionID, []byte("x")), pty.ErrEnded) {
		if time.Now().After(deadline) {
			t.Fatal("input after the program exited was not refused as ended")
		}

		time.Sleep(waitPoll)
	}

	// The host drains the output for a second before it records the end; the refusal comes
	// within that window.
	if entry, _ := info(t, host, sessionID); entry.ExitCode != nil {
		t.Fatal("input was refused as ended only once the end was recorded")
	}

	typed := len(process.writtenSoFar())

	err = host.Write(sessionID, []byte("y"))
	if !errors.Is(err, pty.ErrEnded) {
		t.Fatalf("Write after the program exited = %v, want ErrEnded", err)
	}

	err = host.Resize(sessionID, 100, 30)
	if !errors.Is(err, pty.ErrEnded) {
		t.Fatalf("Resize after the program exited = %v, want ErrEnded", err)
	}

	if len(process.writtenSoFar()) != typed {
		t.Fatal("input after the program exited reached it")
	}
}
