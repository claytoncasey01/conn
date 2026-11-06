package pty_test

import (
	"compress/gzip"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/claytoncasey01/conn/internal/ask"
	"github.com/claytoncasey01/conn/internal/js"
)

// readScreenLines is how many lines the sessions code reads, as `cmux read-screen --lines 80`.
const readScreenLines = 80

// drawnColumns is the width of the screen recorded screens are drawn on, and drawnWidthMax the
// widest line drawn on it, every character counted as two cells.
const (
	drawnColumns  = 500
	drawnWidthMax = drawnColumns / 2
)

// recordedAsks is testdata/answers/ask/ask.json.gz: every screen the ask parser's tests use and
// seeded mutations of them, with what the parser read from each.
func recordedAsks(t *testing.T) []any {
	t.Helper()

	file, err := os.Open("../../testdata/answers/ask/ask.json.gz")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = file.Close() }()

	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := js.Parse(data)
	if err != nil {
		t.Fatal(err)
	}

	object, _ := parsed.(*js.Object)
	cases, _ := object.Get("cases")
	rows, _ := cases.([]any)

	return rows
}

func field(value any, key string) any {
	object, _ := value.(*js.Object)
	got, _ := object.Get(key)

	return got
}

// sameJSON compares a value with a recorded one as JSON text, both read back the same way.
func sameJSON(t *testing.T, got, want any) bool {
	t.Helper()

	parsed, err := js.Parse(js.Marshal(got))
	if err != nil {
		t.Fatal(err)
	}

	return js.Stringify(parsed) == js.Stringify(want)
}

// drawable says whether a terminal can show text exactly: no controls, tabs, line or paragraph
// separators, and no character a terminal draws in no cell of its own (a combining mark, a
// joiner, a variation selector), which a terminal merges into its neighbour.
func drawable(text string) bool {
	for _, char := range text {
		if char == '\n' {
			continue
		}

		if !unicode.IsGraphic(char) {
			return false
		}

		if unicode.In(char, unicode.Mn, unicode.Me, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}

	return true
}

// fits says whether every line fits a drawn screen.
func fits(lines []string) bool {
	if len(lines) > readScreenLines {
		return false
	}

	for _, line := range lines {
		if utf8.RuneCountInString(line) > drawnWidthMax {
			return false
		}
	}

	return true
}

// drawn is the byte stream a full-screen program sends to draw lines on a cleared alternate
// screen, each placed by cursor addressing, in a colour.
func drawn(lines []string) []byte {
	var stream strings.Builder

	stream.WriteString("\x1b[?1049h\x1b[2J\x1b[H")

	for index, line := range lines {
		stream.WriteString("\x1b[" + strconv.Itoa(index+1) + ";1H")
		stream.WriteString("\x1b[38;5;246m" + line + "\x1b[39m")
	}

	return []byte(stream.String())
}

// TestScreenReadsRecordedAsksAlike draws every recorded screen the way a full-screen program
// would and reads it back through the model: Parse, Unread and PromptState read the same from
// the model's lines as they did from the recorded text.
func TestScreenReadsRecordedAsksAlike(t *testing.T) {
	t.Parallel()

	compared, skipped := 0, 0

	for index, row := range recordedAsks(t) {
		text, _ := field(row, "screen").(string)
		stamp, _ := field(row, "stamp").(float64)
		lines := strings.Split(text, "\n")

		if !drawable(text) {
			skipped++

			continue
		}

		if !fits(lines) {
			skipped++

			continue
		}

		// A screen taller than the text shows it at the top with blank rows below, which
		// Lines drops.
		screen := newScreen(t, drawnColumns, readScreenLines)
		screen.Feed(drawn(lines))

		modelled := ask.ScreenText(screen.Lines(readScreenLines))
		compared++

		var parsed any
		if found := ask.Parse(modelled, stamp); found != nil {
			parsed = found
		}

		if !sameJSON(t, parsed, field(row, "parse")) {
			t.Errorf("case %d Parse\n got  %s\n want %s\n text %q\n model %q", index,
				js.Marshal(parsed), js.Stringify(field(row, "parse")), text, modelled)
		}

		if !sameJSON(t, ask.Unread(modelled, stamp), field(row, "unread")) {
			t.Errorf("case %d Unread differs\n text %q\n model %q", index, text, modelled)
		}

		if ask.PromptState(modelled) != field(row, "prompt") {
			t.Errorf("case %d PromptState = %s, want %v", index, ask.PromptState(modelled),
				field(row, "prompt"))
		}
	}

	if compared == 0 {
		t.Fatal("no recorded screen was compared")
	}

	t.Logf("compared %d recorded screens, skipped %d a terminal cannot draw exactly", compared,
		skipped)
}

// TestScreenReadsCapturedClaudePrompts feeds the bytes a real Claude Code wrote while it asked
// for a permission and asked a question (testdata/screen, redacted) and reads the ask back.
func TestScreenReadsCapturedClaudePrompts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		file string
		want ask.Ask
	}{
		{"claude-permission.bin", ask.Ask{
			Kind: ask.KindPermission, ID: "", Title: "Bash command", Text: "touch conn-probe.txt",
			Options: []ask.Option{
				{Key: "1", Label: "Yes", Role: ask.RoleAllow},
				{Key: "4", Label: "No", Role: ask.RoleDeny},
			},
			Since: 0,
		}},
		{"claude-question.bin", ask.Ask{
			Kind: ask.KindQuestion, ID: "", Title: "Red or blue?", Text: "",
			Options: []ask.Option{
				{Key: "1", Label: "Red", Role: ask.RolePick},
				{Key: "2", Label: "Blue", Role: ask.RolePick},
			},
			Since: 0,
		}},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile("testdata/screen/" + tt.file)
			if err != nil {
				t.Fatal(err)
			}

			// The captures ran on a terminal of the host's default size, 80 by 24.
			screen := newScreen(t, 80, 24)
			screen.Feed(data)

			lines := screen.Lines(readScreenLines)

			found := ask.Parse(ask.ScreenText(lines), 0)
			if found == nil {
				t.Fatalf("no ask read from\n%s", strings.Join(lines, "\n"))
			}

			if found.Kind != tt.want.Kind {
				t.Errorf("Kind = %q, want %q", found.Kind, tt.want.Kind)
			}

			if found.Title != tt.want.Title {
				t.Errorf("Title = %q, want %q", found.Title, tt.want.Title)
			}

			if found.Text != tt.want.Text {
				t.Errorf("Text = %q, want %q", found.Text, tt.want.Text)
			}

			if !slices.Equal(found.Options, tt.want.Options) {
				t.Errorf("Options = %+v, want %+v", found.Options, tt.want.Options)
			}
		})
	}
}
