package pty_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// writtenWithin waits until the program has been written want, and fails if it never is.
func writtenWithin(t *testing.T, process *fakeProcess, want string) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(waitLimit))

	for time.Now().Before(deadline) {
		if bytes.Contains(process.writtenSoFar(), []byte(want)) {
			return
		}

		time.Sleep(waitPoll)
	}

	t.Fatalf("the program was written %q, not %q", process.writtenSoFar(), want)
}

// TestTheBackgroundAnswerFollowsTheTheme: a host made with a dark theme's background answers
// OSC 11 with it; once the page's theme turns light, a session asking from then on is answered
// with the light background, so Claude Code picks its light palette.
func TestTheBackgroundAnswerFollowsTheTheme(t *testing.T) {
	t.Parallel()

	spawner := &fakes{}
	dark := pty.RGB{Red: 0x0e, Green: 0x11, Blue: 0x17}
	host := pty.NewHost(pty.Config{
		Spawner: spawner.spawn, Limits: pty.DefaultLimits(),
		Answers: pty.Answers{Background: dark}, TurnWaited: nil,
	})

	t.Cleanup(func() { _ = host.StopAll(t.Context()) })

	_, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	first := spawner.last(t)
	first.feed(t, []byte("\x1b]11;?\x1b\\"))
	writtenWithin(t, first, "\x1b]11;rgb:0e0e/1111/1717\x1b\\")

	light := pty.RGB{Red: 0xf3, Green: 0xf5, Blue: 0xf9}
	host.SetAnswers(pty.Answers{Background: light})

	_, err = host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	second := spawner.last(t)
	second.feed(t, []byte("\x1b]11;?\x07"))
	writtenWithin(t, second, "\x1b]11;rgb:f3f3/f5f5/f9f9\x1b\\")

	// The first session asking again is answered with the light one too.
	first.feed(t, []byte("\x1b]11;?\x1b\\"))
	writtenWithin(t, first, "\x1b]11;rgb:f3f3/f5f5/f9f9\x1b\\")
}
