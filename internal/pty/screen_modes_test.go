package pty_test

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/answers"
	"github.com/claytoncasey01/conn/internal/pty"
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

		// Every byte has left the replay's window: the preamble is all the viewer gets.
		answers.Check(t, recorded, string(screen.ModePreamble(uint64(len(output)))))
	}
}

// The preamble is read whole even from output fed a byte at a time.
func TestScreenModePreambleTakesSequencesSplitAcrossFeeds(t *testing.T) {
	t.Parallel()

	screen := newScreen(t, 80, 24)

	output := []byte("\x1b[?2004;1003h\x1b[?1006h")
	for _, single := range output {
		screen.Feed([]byte{single})
	}

	feeds := uint64(len(output))

	const want = "\x1b[?2004h\x1b[?1003h\x1b[?1006h"
	if got := string(screen.ModePreamble(feeds)); got != want {
		t.Fatalf("ModePreamble = %q, want %q", got, want)
	}
}

// The alternate screen is in the preamble only when the switch to it has left the window: one
// still in it switches the viewer's terminal itself, after the main screen's output before it.
func TestScreenModePreambleSwitchesScreensOnlyForASwitchThatHasLeft(t *testing.T) {
	t.Parallel()

	const before = "MAIN\r\n"

	switchAt := uint64(len(before))

	tests := []struct {
		name        string
		output      string
		windowStart uint64
		want        string
	}{
		{"the switch in the window", before + "\x1b[?1049hVIM", 0, ""},
		{"the window starts at the switch", before + "\x1b[?1049hVIM", switchAt, ""},
		{"the switch cut by the window", before + "\x1b[?1049hVIM", switchAt + 2, "\x1b[?1049h"},
		{"the switch before the window", before + "\x1b[?1049hVIM", switchAt + 9, "\x1b[?1049h"},
		{"a C1 switch before the window", before + "\u009b?47hVIM", switchAt + 1, "\x1b[?1049h"},
		{"a C1 switch inside the window, after an ESC before it", "\x1b[mAB\u009b?47hVIM", 3, ""},
		{"left the alternate screen", "\x1b[?1049hVIM\x1b[?1049l", 1 << 20, ""},
		{"switched again inside the window", "\x1b[?1049hA\x1b[?1049lB\x1b[?1049hC", 12, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			screen := newScreen(t, 80, 24)
			screen.Feed([]byte(tt.output))

			if got := string(screen.ModePreamble(tt.windowStart)); got != tt.want {
				t.Fatalf("ModePreamble(%d) = %q, want %q", tt.windowStart, got, tt.want)
			}
		})
	}
}

// The longest preamble, every mode on at once, is within PreambleBytesMax.
func TestScreenModePreambleIsWithinItsBound(t *testing.T) {
	t.Parallel()

	screen := newScreen(t, 80, 24)
	screen.Feed([]byte("\x1b[?1049;1;66;2004;1004;1003;1016h"))

	got := screen.ModePreamble(1 << 20)
	if len(got) > pty.PreambleBytesMax {
		t.Fatalf("preamble of %d bytes, over %d", len(got), pty.PreambleBytesMax)
	}

	const want = "\x1b[?1049h\x1b[?1h\x1b=\x1b[?2004h\x1b[?1004h\x1b[?1003h\x1b[?1016h"
	if string(got) != want {
		t.Fatalf("preamble %q, want %q", got, want)
	}
}
