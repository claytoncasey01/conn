package pty_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/claytoncasey01/conn/internal/pty"
)

// replyGrammar is every reply the fixed table can give: digits and fixed bytes only.
const replyGrammar = `^(\x1b\[\?62;22c` +
	`|\x1b\[[1-9][0-9]{0,3};[1-9][0-9]{0,3}R` +
	`|\x1b\]11;rgb:[0-9a-f]{4}/[0-9a-f]{4}/[0-9a-f]{4}\x1b\\)$`

func TestScreenRecordsOnlyTheQueriesTheHostAnswers(t *testing.T) {
	t.Parallel()

	attributes := pty.Query{Kind: pty.QueryDeviceAttributes, Row: 0, Column: 0}
	background := pty.Query{Kind: pty.QueryBackground, Row: 0, Column: 0}

	tests := []struct {
		name  string
		input string
		want  []pty.Query
	}{
		{"device attributes", "\x1b[c", []pty.Query{attributes}},
		{"device attributes with a zero", "\x1b[0c", []pty.Query{attributes}},
		{"cursor position, where it was asked", "abc\x1b[6n\r\nmore", []pty.Query{
			{Kind: pty.QueryCursorPosition, Row: 0, Column: 3},
		}},
		{"background, ended by BEL", "\x1b]11;?\a", []pty.Query{background}},
		{"background, ended by ST", "\x1b]11;?\x1b\\", []pty.Query{background}},
		{"in order", "\x1b]11;?\a\x1b[c", []pty.Query{background, attributes}},

		// Everything else gets no answer.
		{"secondary device attributes", "\x1b[>c", nil},
		{"tertiary device attributes", "\x1b[=c", nil},
		{"device attributes with more parameters", "\x1b[0;1c", nil},
		{"device attributes with an intermediate", "\x1b[ c", nil},
		{"terminal version", "\x1b[>0q", nil},
		{"kitty keyboard flags", "\x1b[?u", nil},
		{"device status", "\x1b[5n", nil},
		{"extended cursor position", "\x1b[?6n", nil},
		{"cursor position with more parameters", "\x1b[6;1n", nil},
		{"title report", "\x1b[21t", nil},
		{"icon label report", "\x1b[20t", nil},
		{"window size reports", "\x1b[14t\x1b[18t\x1b[19t\x1b[16t", nil},
		{"window position and state reports", "\x1b[13t\x1b[11t", nil},
		{"setting report (DECRQSS)", "\x1bP$qm\x1b\\\x1bP$q\"p\x1b\\", nil},
		{"mode reports (DECRQM)", "\x1b[?2026$p\x1b[4$p", nil},
		{"foreground colour", "\x1b]10;?\a", nil},
		{"cursor colour", "\x1b]12;?\a", nil},
		{"palette colour", "\x1b]4;1;?\a", nil},
		{"clipboard read (OSC 52)", "\x1b]52;c;?\a", nil},
		{"Claude Code's private OSC 7501", "\x1b]7501;?\a", nil},
		{"setting the background", "\x1b]11;rgb:00/00/00\a", nil},
		{"background with more after the ?", "\x1b]11;?x\a", nil},
		{"a query cut short by CAN", "\x1b[\x18c", nil},
		// An escape ends a title, as in xterm and xterm.js, so a query after it is one.
		{"an escape ends a title", "\x1b]0;title\x1b[c\a", []pty.Query{attributes}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			screen := newScreen(t, 80, 24)
			screen.Feed([]byte(tt.input))

			got := screen.Queries()
			if !slices.Equal(got, tt.want) {
				t.Errorf("Queries = %+v, want %+v", got, tt.want)
			}

			if len(screen.Queries()) != 0 {
				t.Error("Queries gave the same queries twice")
			}
		})
	}
}

// The queries a real Claude Code start-up sends (testdata/screen/claude-startup.bin): terminal
// version, kitty keyboard flags, its own OSC 7501 and device attributes. Only the last is
// answered; Claude Code takes it as the sign that the others are not supported.
func TestClaudeStartupAsksForDeviceAttributesOnly(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/screen/claude-startup.bin")
	if err != nil {
		t.Fatal(err)
	}

	screen := newScreen(t, 80, 24)
	screen.Feed(data)

	got := screen.Queries()
	want := []pty.Query{{Kind: pty.QueryDeviceAttributes, Row: 0, Column: 0}}

	if len(got) != len(want) {
		t.Fatalf("Queries = %+v, want %+v", got, want)
	}

	if got[0].Kind != want[0].Kind {
		t.Errorf("Kind = %v, want %v", got[0].Kind, want[0].Kind)
	}
}

func TestRepliesComeFromTheFixedTable(t *testing.T) {
	t.Parallel()

	themed := pty.Answers{Background: pty.RGB{Red: 0x1e, Green: 0x2f, Blue: 0x3a}}

	tests := []struct {
		name    string
		answers pty.Answers
		query   pty.Query
		want    string
	}{
		{
			"device attributes", themed,
			pty.Query{Kind: pty.QueryDeviceAttributes, Row: 3, Column: 4},
			"\x1b[?62;22c",
		},
		{
			"cursor position is one-based", themed,
			pty.Query{Kind: pty.QueryCursorPosition, Row: 4, Column: 9},
			"\x1b[5;10R",
		},
		{
			"the theme's background", themed,
			pty.Query{Kind: pty.QueryBackground, Row: 0, Column: 0},
			"\x1b]11;rgb:1e1e/2f2f/3a3a\x1b\\",
		},
		{
			"the default background",
			pty.Answers{Background: pty.RGB{Red: 0, Green: 0, Blue: 0}},
			pty.Query{Kind: pty.QueryBackground, Row: 0, Column: 0},
			"\x1b]11;rgb:0000/0000/0000\x1b\\",
		},
	}

	grammar := regexp.MustCompile(replyGrammar)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := string(pty.Reply(tt.answers, tt.query))
			if got != tt.want {
				t.Errorf("Reply = %q, want %q", got, tt.want)
			}

			if !grammar.MatchString(got) {
				t.Errorf("Reply %q is outside the table's grammar", got)
			}
		})
	}
}

func TestReplyPanicsOnAnUnknownQuery(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("Reply did not panic on a query kind the table does not have")
		}
	}()

	pty.Reply(pty.Answers{}, pty.Query{Kind: pty.QueryKind(99), Row: 0, Column: 0})
}

// fuzzSplit is where the fuzz seeds split a capture in two.
const fuzzSplit = 1000

// marker is text that must never come back in a reply.
const marker = "MARKER"

func TestNoReplyHoldsBytesFromTheOutput(t *testing.T) {
	t.Parallel()

	inputs := []string{
		"\x1b]11;?" + marker + "\a\x1b[c",
		marker + "\x1b[6n",
		"\x1b]0;" + marker + "\a\x1b[c\x1b]11;?\a",
		"\x1b[" + marker + "c\x1b[6" + marker + "n\x1b[c",
		"\x1bP$q" + marker + "\x1b\\\x1b[21t\x1b[c",
		"\x1b]11;" + marker + "?\a\x1b]11;?\x1b\\",
		strings.Repeat(marker, 30) + "\x1b[6n",
	}

	grammar := regexp.MustCompile(replyGrammar)

	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			screen := newScreen(t, 80, 24)
			screen.Feed([]byte(input))

			queries := screen.Queries()
			if len(queries) == 0 {
				t.Fatal("no query was recorded, so nothing was checked")
			}

			for _, query := range queries {
				reply := string(pty.Reply(pty.Answers{}, query))
				if strings.Contains(reply, marker) {
					t.Errorf("reply %q holds text from the output", reply)
				}

				if !grammar.MatchString(reply) {
					t.Errorf("reply %q is outside the table's grammar", reply)
				}
			}
		})
	}
}

func TestQueriesWaitingAreBounded(t *testing.T) {
	t.Parallel()

	screen := newScreen(t, 80, 24)
	screen.Feed([]byte(strings.Repeat("\x1b[c", 10*pty.QueriesPendingMax)))

	got := len(screen.Queries())
	if got != pty.QueriesPendingMax {
		t.Errorf("%d queries waiting, want %d", got, pty.QueriesPendingMax)
	}
}

// FuzzScreenFeed feeds anything, split anywhere, to a screen of any size: it never panics, the
// cursor stays on the screen, no line is wider than the screen, and every reply is from the
// fixed table.
func FuzzScreenFeed(f *testing.F) {
	captures := []string{"claude-startup.bin", "claude-permission.bin", "claude-question.bin"}

	for _, file := range captures {
		data, err := os.ReadFile("testdata/screen/" + file)
		if err != nil {
			f.Fatal(err)
		}

		f.Add(data, uint16(80), uint16(24), uint16(fuzzSplit))
	}

	seeds := []string{
		"\x1b[65535;65535H\x1b[65535b\x1b[65535@\x1b[65535L\x1b[65535S",
		"\x1b[?1049h\x1b[2;1r\x1b[?6h\x1b[99B\x1bM\x1bD\x1b[?1049l",
		"\x1b]11;?\a\x1b[c\x1b[6n\x1b]52;c;?\a\x1bP$qm\x1b\\",
		"\u65e5\u672c\x1b[2G\xe2\x94\x1b[1;1H\x1b(0qqq\x0e\x0f",
		strings.Repeat("\x1b[", 100) + strings.Repeat("9;", 100),
	}

	for _, seed := range seeds {
		f.Add([]byte(seed), uint16(5), uint16(3), uint16(7))
	}

	grammar := regexp.MustCompile(replyGrammar)

	f.Fuzz(func(t *testing.T, data []byte, columns, rows, split uint16) {
		size := pty.Size{Columns: columns%200 + 1, Rows: rows%60 + 1}

		screen, err := pty.NewScreen(size)
		if err != nil {
			t.Fatal(err)
		}

		cut := min(int(split), len(data))
		screen.Feed(data[:cut])
		screen.Feed(data[cut:])

		checkScreen(t, screen, size, grammar)
	})
}

func checkScreen(t *testing.T, screen *pty.Screen, size pty.Size, grammar *regexp.Regexp) {
	t.Helper()

	checkCursor(t, screen, size)
	checkLines(t, screen, size)

	queries := screen.Queries()
	if len(queries) > pty.QueriesPendingMax {
		t.Fatalf("%d queries waiting", len(queries))
	}

	for _, query := range queries {
		reply := string(pty.Reply(pty.Answers{}, query))
		if !grammar.MatchString(reply) {
			t.Fatalf("reply %q is outside the table's grammar", reply)
		}
	}
}

func checkCursor(t *testing.T, screen *pty.Screen, size pty.Size) {
	t.Helper()

	row, column := screen.Cursor()
	if row < 0 {
		t.Fatalf("cursor row %d", row)
	}

	if row >= int(size.Rows) {
		t.Fatalf("cursor row %d on %d rows", row, size.Rows)
	}

	if column < 0 {
		t.Fatalf("cursor column %d", column)
	}

	if column >= int(size.Columns) {
		t.Fatalf("cursor column %d on %d columns", column, size.Columns)
	}
}

func checkLines(t *testing.T, screen *pty.Screen, size pty.Size) {
	t.Helper()

	lines := screen.Lines(int(size.Rows) + 10)
	if len(lines) > int(size.Rows) {
		t.Fatalf("%d lines from %d rows", len(lines), size.Rows)
	}

	for _, line := range lines {
		if utf8.RuneCountInString(line) > int(size.Columns) {
			t.Fatalf("line %q is wider than %d columns", line, size.Columns)
		}
	}
}
