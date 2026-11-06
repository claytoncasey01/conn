package pty_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

const deviceAttributesReply = "\x1b[?62;22c"

// waitWritten waits until the program has been sent want, and fails if it was sent anything
// else.
func waitWritten(t *testing.T, process *fakeProcess, want string) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(waitLimit))

	for time.Now().Before(deadline) {
		got := string(process.writtenSoFar())
		if got == want {
			return
		}

		if !strings.HasPrefix(want, got) {
			t.Fatalf("the program was sent %q, want %q", got, want)
		}

		time.Sleep(waitPoll)
	}

	t.Fatalf("the program was sent %q, want %q", process.writtenSoFar(), want)
}

// waitLines waits until the session's screen shows want.
func waitLines(t *testing.T, host *pty.Host, sessionID pty.ID, want []string) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(waitLimit))

	var got []string

	for time.Now().Before(deadline) {
		var err error

		got, _, err = host.ScreenLines(sessionID, readScreenLines)
		if err != nil {
			t.Fatal(err)
		}

		if slices.Equal(got, want) {
			return
		}

		time.Sleep(waitPoll)
	}

	t.Fatalf("ScreenLines = %q, want %q", got, want)
}

func TestHostAnswersQueriesWithNoViewer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		answers pty.Answers
		output  string
		want    string
	}{
		{"device attributes", pty.Answers{}, "\x1b[c", deviceAttributesReply},
		{
			"no cursor position, only what follows",
			pty.Answers{},
			"\r\nabc\x1b[6n\x1b[c",
			deviceAttributesReply,
		},
		{
			"the configured background",
			pty.Answers{Background: pty.RGB{Red: 0xfa, Green: 0xfb, Blue: 0xfc}},
			"\x1b]11;?\a", "\x1b]11;rgb:fafa/fbfb/fcfc\x1b\\",
		},
		// The queries Claude Code sends besides device attributes get nothing; the reply to
		// device attributes, which it sends last, is the only thing typed.
		{
			"only device attributes of Claude Code's start-up",
			pty.Answers{},
			"\x1b[>0q\x1b[?u\x1b]7501;?\a\x1b[21t\x1bP$qm\x1b\\\x1b[c",
			deviceAttributesReply,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spawner := &fakes{}
			limits := pty.DefaultLimits()
			host := pty.NewHost(pty.Config{
				Spawner: spawner.spawn, Limits: limits, Answers: tt.answers,
			})

			t.Cleanup(func() { _ = host.StopAll(context.WithoutCancel(t.Context())) })

			_, err := host.Start(t.Context(), testSpec(t))
			if err != nil {
				t.Fatal(err)
			}

			process := spawner.last(t)
			process.feed(t, []byte(tt.output))
			waitWritten(t, process, tt.want)
		})
	}
}

// A program that floods queries while not reading its input never holds up its reader, and
// gets at most a bounded number of answers.
func TestHostAnswersAFloodOfQueriesBoundedly(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	spawner := &fakes{writeGate: gate}
	host := pty.NewHost(pty.Config{
		Spawner: spawner.spawn, Limits: pty.DefaultLimits(), Answers: pty.Answers{},
	})

	t.Cleanup(func() { _ = host.StopAll(context.WithoutCancel(t.Context())) })

	_, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	process := spawner.last(t)
	flood := []byte(strings.Repeat("\x1b[c", pty.ReadBufferBytes/len("\x1b[c")))

	for range 64 {
		// feed fails the test if the reader stops taking output.
		process.feed(t, flood)
	}

	// The reader has taken this chunk only once it has dealt with the last flood.
	process.feed(t, []byte("x"))
	close(gate)

	// The answers already queued arrive; then nothing more does.
	most := (pty.AnswersQueuedMax + 1) * len(deviceAttributesReply)

	time.Sleep(testtime.Scaled(100 * time.Millisecond))

	got := process.writtenSoFar()
	if len(got) > most {
		t.Errorf("the program was sent %d bytes of answers, at most %d", len(got), most)
	}

	if len(got) == 0 {
		t.Error("the program was sent no answer")
	}

	whole := bytes.Repeat([]byte(deviceAttributesReply), len(got)/len(deviceAttributesReply))
	if !bytes.Equal(got, whole) {
		t.Errorf("the program was sent %q, want whole device attribute replies", got)
	}
}

func TestScreenLinesShowTheSessionsScreen(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	process := spawner.last(t)
	process.feed(t, []byte("hello\r\nworld"))
	waitLines(t, host, sessionID, []string{"hello", "world"})

	err = host.Resize(sessionID, 10, 3)
	if err != nil {
		t.Fatal(err)
	}

	process.feed(t, []byte("\x1b[2J\x1b[99;99Hx"))
	waitLines(t, host, sessionID, []string{"", "", "         x"})

	// The screen of an ended session can still be read.
	process.exit(0)
	waitEnded(t, host, sessionID)
	waitLines(t, host, sessionID, []string{"", "", "         x"})
}

func TestScreenLinesRefuseAnUnknownSession(t *testing.T) {
	t.Parallel()

	host, _ := newFakeHost(t, pty.DefaultLimits())

	_, _, err := host.ScreenLines(pty.ID("unknown"), readScreenLines)
	if !errors.Is(err, pty.ErrNoSession) {
		t.Errorf("ScreenLines = %v, want ErrNoSession", err)
	}
}

func TestResizeRefusesMoreCellsThanTheScreenHolds(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	err = host.Resize(sessionID, pty.ColumnsMax, pty.RowsMax)
	if !errors.Is(err, pty.ErrInvalidSize) {
		t.Errorf("Resize = %v, want ErrInvalidSize", err)
	}

	if len(spawner.last(t).sizesSoFar()) != 0 {
		t.Error("the refused size reached the program")
	}
}

// A resize the program's terminal refuses leaves the screen at the size it had.
func TestAFailedResizeLeavesTheScreensSize(t *testing.T) {
	t.Parallel()

	spawner := &fakes{resizeErr: errFakeClosed}
	host := pty.NewHost(pty.Config{
		Spawner: spawner.spawn, Limits: pty.DefaultLimits(), Answers: pty.Answers{},
	})

	t.Cleanup(func() { _ = host.StopAll(context.WithoutCancel(t.Context())) })

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	err = host.Resize(sessionID, 10, 3)
	if !errors.Is(err, errFakeClosed) {
		t.Fatalf("Resize = %v, want the terminal's error", err)
	}

	// At the default 80 by 24, the bottom right corner is row 24, column 80.
	spawner.last(t).feed(t, []byte("\x1b[99;99Hx"))

	want := make([]string, 24)
	want[23] = strings.Repeat(" ", 79) + "x"

	waitLines(t, host, sessionID, want)
}
