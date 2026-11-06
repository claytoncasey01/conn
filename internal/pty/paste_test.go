package pty_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// pasteSession is a session whose program has asked for bracketed paste, as the host's screen
// model has read it, and whose next write is cut after cut bytes.
func pasteSession(t *testing.T, cut int) (*pty.Host, pty.ID, *fakeProcess) {
	t.Helper()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	process := spawner.last(t)
	process.feed(t, []byte("\x1b[?2004h"))
	waitBracketed(t, host, sessionID, true)
	process.cutNextWrite(cut)

	return host, sessionID, process
}

// waitBracketed waits until the host's screen model reads bracketed paste as want.
func waitBracketed(t *testing.T, host *pty.Host, sessionID pty.ID, want bool) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(waitLimit))
	for time.Now().Before(deadline) {
		_, mark, err := host.ScreenLines(sessionID, 1)
		if err != nil {
			t.Fatal(err)
		}

		if mark.BracketedPaste == want {
			return
		}

		time.Sleep(waitPoll)
	}

	t.Fatalf("the screen never read bracketed paste as %v", want)
}

// A paste cut short by the program not reading (the terminal's write deadline) leaves the
// program inside the paste. The next input does not go in after it: the host closes the paste
// alone, counted as input, and refuses that input, so nothing the person did not see is
// submitted by their next key. A cut inside a marker is finished, never repeated whole.
func TestAPasteCutShortIsClosedAloneAndTheNextInputRefused(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		first   string
		cut     int
		next    string
		refused bool
		program string
	}{
		{
			name: "Enter after a cut paste", first: "\x1b[200~rm -rf build/tmp\r\x1b[201~", cut: 13,
			next: "\r", refused: true, program: "\x1b[200~rm -rf \x1b[201~",
		},
		{
			name: "a key after a cut paste", first: "\x1b[200~abcdef\x1b[201~", cut: 9,
			next: "x", refused: true, program: "\x1b[200~abc\x1b[201~",
		},
		{
			name: "cut inside the end marker", first: "\x1b[200~abc\x1b[201~", cut: 12,
			next: "x", refused: true, program: "\x1b[200~abc\x1b[201~",
		},
		{
			name: "cut inside the start marker", first: "ab\x1b[200~cd\x1b[201~", cut: 4,
			next: "x", refused: true, program: "ab\x1b[200~\x1b[201~",
		},
		{
			name: "cut after the whole paste", first: "\x1b[200~abc\x1b[201~y", cut: 15,
			next: "x", refused: false, program: "\x1b[200~abc\x1b[201~x",
		},
		{
			name: "cut before the paste began", first: "ab\x1b[200~cd\x1b[201~", cut: 2,
			next: "x", refused: false, program: "abx",
		},
		{
			name: "cut keys, no paste", first: "abcdef", cut: 3, next: "x", refused: false,
			program: "abcx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host, sessionID, process := pasteSession(t, tt.cut)

			err := host.Write(sessionID, []byte(tt.first))
			if !errors.Is(err, pty.ErrInputBlocked) {
				t.Fatalf("a write cut short = %v, want ErrInputBlocked", err)
			}

			err = host.Write(sessionID, []byte(tt.next))
			if tt.refused != errors.Is(err, pty.ErrPasteClosed) {
				t.Fatalf("the next input = %v, refused as a closed paste: want %v", err, tt.refused)
			}

			if got := string(process.writtenSoFar()); got != tt.program {
				t.Fatalf("the program read %q, want %q", got, tt.program)
			}

			// Once closed, input is typed again.
			err = host.Write(sessionID, []byte("z"))
			if err != nil {
				t.Fatal(err)
			}

			if got := string(process.writtenSoFar()); got != tt.program+"z" {
				t.Fatalf("then the program read %q, want %q", got, tt.program+"z")
			}
		})
	}
}

// Closing a paste counts as input: a screen read from before it no longer stands.
func TestClosingAPasteCountsAsInput(t *testing.T) {
	t.Parallel()

	host, sessionID, _ := pasteSession(t, 9)

	_ = host.Write(sessionID, []byte("\x1b[200~abcdef\x1b[201~"))

	_, before, err := host.ScreenLines(sessionID, 1)
	if err != nil {
		t.Fatal(err)
	}

	_ = host.Write(sessionID, []byte("x"))

	_, after, err := host.ScreenLines(sessionID, 1)
	if err != nil {
		t.Fatal(err)
	}

	if after.Inputs != before.Inputs+1 {
		t.Fatalf("inputs went from %d to %d, want one more for the close", before.Inputs,
			after.Inputs)
	}
}

// A reply typed on a screen check is refused while a paste is open: the program holds input the
// screen does not show, so no screen read can say what the reply would do.
func TestWriteIfStillRefusesWhileAPasteIsOpen(t *testing.T) {
	t.Parallel()

	host, sessionID, process := pasteSession(t, 9)

	_ = host.Write(sessionID, []byte("\x1b[200~rm -rf build\r\x1b[201~"))
	written := string(process.writtenSoFar())

	// Long enough after the cut write that the screen read is settled.
	time.Sleep(testtime.Scaled(400 * time.Millisecond))

	_, mark, err := host.ScreenLines(sessionID, readScreenLines)
	if err != nil {
		t.Fatal(err)
	}

	if !mark.Quiet {
		t.Fatal("the screen read never settled")
	}

	always := func([]string) bool { return true }

	_, err = host.WriteIfStill(sessionID, mark, readScreenLines, always, []byte("1\r"))
	if !errors.Is(err, pty.ErrOtherInput) {
		t.Fatalf("a reply while a paste is open = %v, want ErrOtherInput", err)
	}

	if got := string(process.writtenSoFar()); got != written {
		t.Fatalf("the program read %q, want only %q", got, written)
	}
}

// Answers to the program's queries and focus reports pass while a paste is open, unchanged: they
// never close it. The person's next input does.
func TestAnswersAndFocusReportsDoNotCloseAPaste(t *testing.T) {
	t.Parallel()

	host, sessionID, process := pasteSession(t, 9)

	_ = host.Write(sessionID, []byte("\x1b[200~abcdef\x1b[201~"))

	// The program asks for its device attributes; the host answers.
	process.feed(t, []byte("\x1b[c"))

	deadline := time.Now().Add(testtime.Scaled(waitLimit))
	for !bytes.Contains(process.writtenSoFar(), []byte("\x1b[?62;22c")) {
		if time.Now().After(deadline) {
			t.Fatal("the query was never answered")
		}

		time.Sleep(waitPoll)
	}

	err := host.Write(sessionID, []byte("\x1b[I"))
	if err != nil {
		t.Fatalf("a focus report while a paste is open = %v", err)
	}

	const want = "\x1b[200~abc\x1b[?62;22c\x1b[I"
	if got := string(process.writtenSoFar()); got != want {
		t.Fatalf("the program read %q, want %q", got, want)
	}

	err = host.Write(sessionID, []byte("x"))
	if !errors.Is(err, pty.ErrPasteClosed) {
		t.Fatalf("the next input = %v, want ErrPasteClosed", err)
	}
}

// A program that stops asking for bracketed paste (it exited to a shell, or reset) is never sent
// an end marker it would read as keys.
func TestAPasteIsNotClosedForAProgramThatStoppedBracketing(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, output string }{
		{"bracketed paste turned off", "\x1b[?2004l"},
		{"a full reset", "\x1bc"},
		{"a soft reset", "\x1b[!p"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host, sessionID, process := pasteSession(t, 9)

			_ = host.Write(sessionID, []byte("\x1b[200~abcdef\x1b[201~"))

			process.feed(t, []byte(tt.output))
			waitBracketed(t, host, sessionID, false)

			err := host.Write(sessionID, []byte("x"))
			if err != nil {
				t.Fatal(err)
			}

			if got := string(process.writtenSoFar()); got != "\x1b[200~abcx" {
				t.Fatalf("the program read %q, want no end marker", got)
			}
		})
	}
}
