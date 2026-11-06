package pty_test

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/answers"
)

// The modes a viewer's terminal needs before the replay, read from what the program sent: the
// cases are shared with the page's tests (testdata/answers/terminal/modes.json), which check
// that xterm.js given the preamble is in the modes it would be in given the output.
func TestScreenModePreambleFollowsTheProgram(t *testing.T) {
	t.Parallel()

	for _, recorded := range answers.Load(t, "terminal/modes.json", "preamble") {
		output := answers.String(t, recorded, 0)

		screen := newScreen(t, 80, 24)
		screen.Feed([]byte(output))

		answers.Check(t, recorded, string(screen.ModePreamble()))
	}
}

// The preamble is read whole even from output fed a byte at a time.
func TestScreenModePreambleTakesSequencesSplitAcrossFeeds(t *testing.T) {
	t.Parallel()

	screen := newScreen(t, 80, 24)
	for _, single := range []byte("\x1b[?2004;1003h\x1b[?1006h") {
		screen.Feed([]byte{single})
	}

	const want = "\x1b[?2004h\x1b[?1003h\x1b[?1006h"
	if got := string(screen.ModePreamble()); got != want {
		t.Fatalf("ModePreamble = %q, want %q", got, want)
	}
}
