package pty_test

import (
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/pty"
)

// The largest screen the host runs: as many columns as it allows, and as many rows as maxCells
// leaves.
const (
	fullColumns = pty.MaxColumns
	fullRows    = pty.MaxCells / pty.MaxColumns
)

// fullSizeSeeds are sequences that make the model work hardest, the costtest package's among
// them.
func fullSizeSeeds() []string {
	wide := string(rune(0x65e5))

	return []string{
		"\x1bc", "\x1b[2J", "a\x1b[999b\x1b[2J", "\x1b[1;2H\x1b[X", "\x1b[1;2H\x1b[P",
		"\x1b[1;2H\x1b[@", "\x1b[H\x1b[999L", "\x1b[999S", "\x1b[999;1H\n\n", wide + "\x1b[999b",
		"\x1b[?1049h\x1b[?1049l", "\x1b[?47h\x1b[?47l", "\u009b2J\u009d0;t\u009c",
		cursorPositionInOriginMode,
	}
}

// FuzzScreenFeedFullSize feeds anything to the largest screen, after it is filled with wide
// text, under the same checks as FuzzScreenFeed.
func FuzzScreenFeedFullSize(f *testing.F) {
	for _, seed := range fullSizeSeeds() {
		f.Add([]byte(strings.Repeat(seed, 4)))
	}

	grammar := replyPattern()

	f.Fuzz(func(t *testing.T, data []byte) {
		screen, err := pty.NewScreen(pty.Size{Columns: fullColumns, Rows: fullRows})
		if err != nil {
			t.Fatal(err)
		}

		// Full of wide text, which makes edits and clears cost the most.
		screen.Feed([]byte(strings.Repeat(string(rune(0x65e5)), fullColumns*fullRows/2)))
		screen.Feed(data)

		checkScreen(t, screen, pty.Size{Columns: fullColumns, Rows: fullRows}, grammar)
	})
}
