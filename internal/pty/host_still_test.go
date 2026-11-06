package pty_test

import (
	"context"
	"errors"
	"os"
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

// lateViewerKey waits until the session is older than the settle window, then types a viewer's
// key: the window is counted from the key, not from the session's start.
func lateViewerKey(t *testing.T, host *pty.Host, sessionID pty.ID, process *fakeProcess) {
	t.Helper()

	time.Sleep(settleWait)
	viewerKey(t, host, sessionID, process)
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

func lineMode(_ *testing.T, _ *pty.Host, _ pty.ID, process *fakeProcess) {
	process.lineMode.Store(true)
}

func focusReports(t *testing.T, host *pty.Host, sessionID pty.ID, _ *fakeProcess) {
	t.Helper()

	err := host.Write(sessionID, []byte("\x1b[O\x1b[I"))
	if err != nil {
		t.Fatal(err)
	}
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
		{"a viewer's key just before the read", lateViewerKey, nil, pty.ErrOtherInput, "3"},
		{"a viewer's key settled before the read", settledViewerKey, nil, nil, "31"},
		{"a query answered after the read", nil, queryAnswered, nil, deviceAttributesReply + "1"},
		{"focus reports just before the read", focusReports, nil, nil, "\x1b[O\x1b[I1"},
		{"focus reports after the read", nil, focusReports, nil, "\x1b[O\x1b[I1"},
		{"another program has the terminal", nil, backgrounded, pty.ErrNotForeground, ""},
		{"a program reads a line on the terminal", nil, lineMode, pty.ErrLineMode, ""},
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

			_, err = host.WriteIfStill(sessionID, mark, readScreenLines, promptShown, []byte("1"))
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
	mark := pty.ScreenMark{Inputs: 0, Quiet: true, BracketedPaste: false}

	_, err := host.WriteIfStill(pty.ID("0123456789abcdef0123456789abcdef"), mark, readScreenLines,
		always, []byte("1"))
	if !errors.Is(err, pty.ErrNoSession) {
		t.Errorf("an unknown session: %v, want %v", err, pty.ErrNoSession)
	}

	_, err = host.WriteIfStill(sessionID, mark, readScreenLines, always, make([]byte, 64<<10+1))
	if !errors.Is(err, pty.ErrInputTooLarge) {
		t.Errorf("too much input: %v, want %v", err, pty.ErrInputTooLarge)
	}
}

// A viewer's write still being taken by the terminal (a full input queue) makes a read not
// settled, however long it has been held: the program has not even got the input yet.
func TestAReadWhileInputIsBeingWrittenIsNotSettled(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	spawner := &fakes{writeGate: gate}
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
	viewer := make(chan error, 1)

	go func() { viewer <- host.Write(sessionID, []byte("3")) }()

	waitWriting(t, process)
	time.Sleep(settleWait)

	_, mark, err := host.ScreenLines(sessionID, readScreenLines)
	if err != nil {
		t.Fatal(err)
	}

	if mark.Quiet {
		t.Error("a read while a viewer's input is being written is settled")
	}

	clicked := make(chan error, 1)

	go func() {
		_, err := host.WriteIfStill(sessionID, mark, readScreenLines,
			func([]string) bool { return true }, []byte("1"))
		clicked <- err
	}()

	close(gate)

	err = <-viewer
	if err != nil {
		t.Fatal(err)
	}

	err = <-clicked
	if !errors.Is(err, pty.ErrOtherInput) {
		t.Errorf("WriteIfStill = %v, want %v", err, pty.ErrOtherInput)
	}

	if got := string(process.writtenSoFar()); got != "3" {
		t.Errorf("the program was sent %q, want only the viewer's 3", got)
	}
}

// The mark a WriteIfStill returns stands for its own input: a write checked against it types at
// once, with no settle wait, unless other input came after it.
func TestAWriteMarkCoversOnlyItsOwnInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		between stillStep
		wantErr error
		want    string
	}{
		{"nothing typed after it", nil, nil, "12"},
		{"a viewer's key after it", viewerKey, pty.ErrOtherInput, "13"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host, sessionID, process := promptSession(t)

			_, mark, err := host.ScreenLines(sessionID, readScreenLines)
			if err != nil {
				t.Fatal(err)
			}

			after, err := host.WriteIfStill(sessionID, mark, readScreenLines, promptShown,
				[]byte("1"))
			if err != nil {
				t.Fatal(err)
			}

			if tt.between != nil {
				tt.between(t, host, sessionID, process)
			}

			_, err = host.WriteIfStill(sessionID, after, readScreenLines, promptShown, []byte("2"))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("WriteIfStill = %v, want %v", err, tt.wantErr)
			}

			if got := string(process.writtenSoFar()); got != tt.want {
				t.Errorf("the program was sent %q, want %q", got, tt.want)
			}
		})
	}
}

// A terminal that cannot be looked at because it has closed is the session ending.
func TestAClosedTerminalIsTheSessionEnding(t *testing.T) {
	t.Parallel()

	host, sessionID, process := promptSession(t)
	process.keyboardErr = os.ErrClosed

	_, mark, err := host.ScreenLines(sessionID, readScreenLines)
	if err != nil {
		t.Fatal(err)
	}

	_, err = host.WriteIfStill(sessionID, mark, readScreenLines, promptShown, []byte("1"))
	if !errors.Is(err, pty.ErrEnded) {
		t.Errorf("WriteIfStill = %v, want %v", err, pty.ErrEnded)
	}
}

// WaitScreen returns as soon as the program draws what is waited for, and ends with the wait's
// context or the session.
func TestWaitScreenWakesWhenTheProgramDraws(t *testing.T) {
	t.Parallel()

	drawn := func(lines []string) bool { return slices.Contains(lines, "drawn") }

	host, sessionID, process := promptSession(t)
	waited := make(chan error, 1)

	go func() {
		_, err := host.WaitScreen(t.Context(), sessionID, readScreenLines, drawn)
		waited <- err
	}()

	// The wait has looked once, and waits for a redraw, by the time this is drawn.
	time.Sleep(settleWait)
	process.feed(t, []byte("\r\ndrawn"))

	select {
	case err := <-waited:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(waitLimit):
		t.Fatal("WaitScreen did not wake when the program drew")
	}

	never := func([]string) bool { return false }

	short, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, err := host.WaitScreen(short, sessionID, readScreenLines, never)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a wait past its context = %v", err)
	}

	go func() {
		_, err := host.WaitScreen(t.Context(), sessionID, readScreenLines, never)
		waited <- err
	}()

	process.exit(0)

	err = <-waited
	if !errors.Is(err, pty.ErrEnded) {
		t.Errorf("a wait on an ending session = %v, want %v", err, pty.ErrEnded)
	}
}
