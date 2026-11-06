package pty_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
)

// WriteIfStill types only while the screen, read once the write's turn comes, still passes the
// caller's check: a change the check cares about refuses it with nothing typed, a change it does
// not care about does not, and an ended session is refused.
func TestWriteIfStillTypesOnlyWhileTheScreenPassesTheCheck(t *testing.T) {
	t.Parallel()

	promptShown := func(lines []string) bool { return slices.Contains(lines, "a prompt") }

	tests := []struct {
		name string
		// printed is output the program prints before the write.
		printed string
		// ended ends the program before the write.
		ended   bool
		wantErr error
		want    string
	}{
		{"the same screen", "", false, nil, "1"},
		{"the prompt is gone", "\x1b[2J\x1b[Hmoved on", false, pty.ErrScreenChanged, ""},
		{"another line changed", "\r\nblink", false, nil, "1"},
		{"the session ended", "", true, pty.ErrEnded, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spawner := &fakes{}
			host := pty.NewHost(pty.Config{
				Spawner: spawner.spawn, Limits: pty.DefaultLimits(), Answers: pty.Answers{},
			})

			t.Cleanup(func() { _ = host.StopAll(context.WithoutCancel(t.Context())) })

			sessionID, err := host.Start(t.Context(), testSpec(t))
			if err != nil {
				t.Fatal(err)
			}

			process := spawner.last(t)
			process.feed(t, []byte("a prompt"))
			waitLines(t, host, sessionID, []string{"a prompt"})

			if tt.printed != "" {
				process.feed(t, []byte(tt.printed))
				waitChanged(t, host, sessionID)
			}

			if tt.ended {
				err = host.Stop(t.Context(), sessionID)
				if err != nil {
					t.Fatal(err)
				}
			}

			err = host.WriteIfStill(sessionID, readScreenLines, promptShown, []byte("1"))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("WriteIfStill = %v, want %v", err, tt.wantErr)
			}

			if got := string(process.writtenSoFar()); got != tt.want {
				t.Errorf("the program was sent %q, want %q", got, tt.want)
			}
		})
	}
}

// waitChanged waits until the screen shows more than the first prompt alone.
func waitChanged(t *testing.T, host *pty.Host, sessionID pty.ID) {
	t.Helper()

	for range waitLimit / waitPoll {
		lines, _, err := host.ScreenLines(sessionID, readScreenLines)
		if err != nil {
			t.Fatal(err)
		}

		if !slices.Equal(lines, []string{"a prompt"}) {
			return
		}

		time.Sleep(waitPoll)
	}

	t.Fatal("the screen never changed")
}

// WriteIfStill refuses an unknown session and too much input, as Write does, before it reads
// any screen.
func TestWriteIfStillRefusesAsWriteDoes(t *testing.T) {
	t.Parallel()

	spawner := &fakes{}
	host := pty.NewHost(pty.Config{
		Spawner: spawner.spawn, Limits: pty.DefaultLimits(), Answers: pty.Answers{},
	})

	t.Cleanup(func() { _ = host.StopAll(context.WithoutCancel(t.Context())) })

	always := func([]string) bool { return true }

	err := host.WriteIfStill(pty.ID("0123456789abcdef0123456789abcdef"), readScreenLines, always,
		[]byte("1"))
	if !errors.Is(err, pty.ErrNoSession) {
		t.Errorf("an unknown session: %v, want %v", err, pty.ErrNoSession)
	}

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	err = host.WriteIfStill(sessionID, readScreenLines, always, make([]byte, 64<<10+1))
	if !errors.Is(err, pty.ErrInputTooLarge) {
		t.Errorf("too much input: %v, want %v", err, pty.ErrInputTooLarge)
	}
}
