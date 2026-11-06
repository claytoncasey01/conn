package pty_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
)

// settleWait is longer than the host's settle window for input (250 ms), so input written this
// long before a screen read counts as drawn.
const settleWait = 400 * time.Millisecond

// stillStep is something the program or a viewer does before or after the screen read.
type stillStep func(t *testing.T, host *pty.Host, sessionID pty.ID, process *fakeProcess)

func viewerKey(t *testing.T, host *pty.Host, sessionID pty.ID, _ *fakeProcess) {
	t.Helper()

	err := host.Write(sessionID, []byte("3"))
	if err != nil {
		t.Fatal(err)
	}
}

func settledViewerKey(t *testing.T, host *pty.Host, sessionID pty.ID, process *fakeProcess) {
	t.Helper()

	viewerKey(t, host, sessionID, process)
	time.Sleep(settleWait)
}

func printed(output string) stillStep {
	return func(t *testing.T, host *pty.Host, sessionID pty.ID, process *fakeProcess) {
		t.Helper()

		process.feed(t, []byte(output))
		waitChanged(t, host, sessionID)
	}
}

func queryAnswered(t *testing.T, _ *pty.Host, _ pty.ID, process *fakeProcess) {
	t.Helper()

	process.feed(t, []byte("\x1b[c"))
	waitWritten(t, process, deviceAttributesReply)
}

func ended(t *testing.T, host *pty.Host, sessionID pty.ID, _ *fakeProcess) {
	t.Helper()

	err := host.Stop(t.Context(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
}

func backgrounded(_ *testing.T, _ *pty.Host, _ pty.ID, process *fakeProcess) {
	process.background.Store(true)
}

// WriteIfStill types only while what the caller read still stands once the write's turn comes:
// the screen still passes the caller's check (a change it does not care about is fine), no
// other input reached the program since the read or just before it (the program may not have
// drawn it: a key that is never echoed still refuses), the program still has the terminal, and
// the session still runs. An answer to the program's own query is not input.
func TestWriteIfStillTypesOnlyWhileTheReadStands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		before, between stillStep
		wantErr         error
		want            string
	}{
		{"the same screen", nil, nil, nil, "1"},
		{"the prompt is gone", nil, printed("\x1b[2J\x1b[Hmoved on"), pty.ErrScreenChanged, ""},
		{"another line changed", nil, printed("\r\nblink"), nil, "1"},
		{"a viewer's key after the read", nil, viewerKey, pty.ErrOtherInput, "3"},
		{"a viewer's key just before the read", viewerKey, nil, pty.ErrOtherInput, "3"},
		{"a viewer's key settled before the read", settledViewerKey, nil, nil, "31"},
		{"a query answered after the read", nil, queryAnswered, nil, deviceAttributesReply + "1"},
		{"another program has the terminal", nil, backgrounded, pty.ErrNotForeground, ""},
		{"the session ended", nil, ended, pty.ErrEnded, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host, sessionID, process := promptSession(t)

			if tt.before != nil {
				tt.before(t, host, sessionID, process)
			}

			_, mark, err := host.ScreenLines(sessionID, readScreenLines)
			if err != nil {
				t.Fatal(err)
			}

			if tt.between != nil {
				tt.between(t, host, sessionID, process)
			}

			err = host.WriteIfStill(sessionID, mark, readScreenLines, promptShown, []byte("1"))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("WriteIfStill = %v, want %v", err, tt.wantErr)
			}

			if got := string(process.writtenSoFar()); got != tt.want {
				t.Errorf("the program was sent %q, want %q", got, tt.want)
			}
		})
	}
}

func promptShown(lines []string) bool { return slices.Contains(lines, "a prompt") }

// promptSession is a host with one session whose screen shows "a prompt".
func promptSession(t *testing.T) (*pty.Host, pty.ID, *fakeProcess) {
	t.Helper()

	spawner := &fakes{}
	host := pty.NewHost(pty.Config{
		Spawner: spawner.spawn, Limits: pty.DefaultLimits(), Answers: pty.Answers{},
		TurnWaited: nil,
	})

	t.Cleanup(func() { _ = host.StopAll(context.WithoutCancel(t.Context())) })

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	process := spawner.last(t)
	process.feed(t, []byte("a prompt"))
	waitLines(t, host, sessionID, []string{"a prompt"})

	return host, sessionID, process
}

// A screen read says whether the program asks for bracketed pastes.
func TestScreenLinesSayWhetherPastesAreBracketed(t *testing.T) {
	t.Parallel()

	host, sessionID, process := promptSession(t)

	_, mark, err := host.ScreenLines(sessionID, readScreenLines)
	if err != nil {
		t.Fatal(err)
	}

	if mark.BracketedPaste {
		t.Error("pastes are bracketed before the program asked")
	}

	process.feed(t, []byte("\x1b[?2004h!"))
	waitLines(t, host, sessionID, []string{"a prompt!"})

	_, mark, err = host.ScreenLines(sessionID, readScreenLines)
	if err != nil {
		t.Fatal(err)
	}

	if !mark.BracketedPaste {
		t.Error("pastes are not bracketed after the program asked")
	}
}

// TurnWaited is told when a write waits behind another.
func TestTurnWaitedIsToldOfAParkedWrite(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	parked := make(chan pty.ID, 1)
	spawner := &fakes{writeGate: gate}
	host := pty.NewHost(pty.Config{
		Spawner: spawner.spawn, Limits: pty.DefaultLimits(), Answers: pty.Answers{},
		TurnWaited: func(sessionID pty.ID) { parked <- sessionID },
	})

	t.Cleanup(func() { _ = host.StopAll(context.WithoutCancel(t.Context())) })

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	first := make(chan error, 1)
	second := make(chan error, 1)

	go func() { first <- host.Write(sessionID, []byte("a")) }()

	waitWriting(t, spawner.last(t))

	go func() { second <- host.Write(sessionID, []byte("b")) }()

	select {
	case got := <-parked:
		if got != sessionID {
			t.Errorf("told of %s, want %s", got, sessionID)
		}
	case <-time.After(waitLimit):
		t.Fatal("the parked write was never told of")
	}

	close(gate)

	for _, done := range []chan error{first, second} {
		err := <-done
		if err != nil {
			t.Fatal(err)
		}
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

	host, sessionID, _ := promptSession(t)
	always := func([]string) bool { return true }
	mark := pty.ScreenMark{Generation: 0, Inputs: 0, Quiet: true, BracketedPaste: false}

	err := host.WriteIfStill(pty.ID("0123456789abcdef0123456789abcdef"), mark, readScreenLines,
		always, []byte("1"))
	if !errors.Is(err, pty.ErrNoSession) {
		t.Errorf("an unknown session: %v, want %v", err, pty.ErrNoSession)
	}

	err = host.WriteIfStill(sessionID, mark, readScreenLines, always, make([]byte, 64<<10+1))
	if !errors.Is(err, pty.ErrInputTooLarge) {
		t.Errorf("too much input: %v, want %v", err, pty.ErrInputTooLarge)
	}
}
