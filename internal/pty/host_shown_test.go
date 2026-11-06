package pty_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claytoncasey01/conn/internal/pty"
)

// WriteIfShown types only while the screen is still the one read: a change since the read, or a
// session that has ended, refuses it with nothing typed.
func TestWriteIfShownTypesOnlyIntoTheScreenRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// changed is output the program prints between the read and the write.
		changed string
		// ended ends the program between the read and the write.
		ended   bool
		wantErr error
		want    string
	}{
		{"the same screen", "", false, nil, "1"},
		{"the screen changed", "moved on", false, pty.ErrScreenChanged, ""},
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

			_, generation, err := host.ScreenLines(sessionID, readScreenLines)
			if err != nil {
				t.Fatal(err)
			}

			if tt.changed != "" {
				process.feed(t, []byte(tt.changed))
				waitGenerationPast(t, host, sessionID, generation)
			}

			if tt.ended {
				err = host.Stop(t.Context(), sessionID)
				if err != nil {
					t.Fatal(err)
				}
			}

			err = host.WriteIfShown(sessionID, generation, []byte("1"))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("WriteIfShown = %v, want %v", err, tt.wantErr)
			}

			if got := string(process.writtenSoFar()); got != tt.want {
				t.Errorf("the program was sent %q, want %q", got, tt.want)
			}
		})
	}
}

// waitGenerationPast waits until what the program printed is on the screen, and checks that
// the screen's generation moved past generation.
func waitGenerationPast(t *testing.T, host *pty.Host, sessionID pty.ID, generation uint64) {
	t.Helper()

	waitLines(t, host, sessionID, []string{"a promptmoved on"})

	_, now, err := host.ScreenLines(sessionID, readScreenLines)
	if err != nil {
		t.Fatal(err)
	}

	if now == generation {
		t.Fatal("the screen changed and its generation did not")
	}
}

// WriteIfShown refuses an unknown session and too much input, as Write does.
func TestWriteIfShownRefusesAsWriteDoes(t *testing.T) {
	t.Parallel()

	spawner := &fakes{}
	host := pty.NewHost(pty.Config{
		Spawner: spawner.spawn, Limits: pty.DefaultLimits(), Answers: pty.Answers{},
	})

	t.Cleanup(func() { _ = host.StopAll(context.WithoutCancel(t.Context())) })

	err := host.WriteIfShown(pty.ID("0123456789abcdef0123456789abcdef"), 0, []byte("1"))
	if !errors.Is(err, pty.ErrNoSession) {
		t.Errorf("an unknown session: %v, want %v", err, pty.ErrNoSession)
	}

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	err = host.WriteIfShown(sessionID, 0, make([]byte, 64<<10+1))
	if !errors.Is(err, pty.ErrInputTooLarge) {
		t.Errorf("too much input: %v, want %v", err, pty.ErrInputTooLarge)
	}
}
