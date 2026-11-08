package ask_test

import (
	"compress/gzip"
	"io"
	"os"
	"strconv"
	"testing"

	"github.com/claytoncasey01/conn/internal/ask"
	"github.com/claytoncasey01/conn/internal/js"
)

// The answers in testdata/answers/ask/ask.json.gz are recorded: every screen the parser tests use,
// and seeded mutations of them (lines dropped, doubled, swapped, re-indented, rules and borders
// added, emoji and line separators inserted), each through parseAsk, unreadAsk and promptState.

func answers(t *testing.T) *js.Object {
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

	return object
}

func get(value any, key string) any {
	object, _ := value.(*js.Object)
	got, _ := object.Get(key)

	return got
}

func list(value any) []any {
	items, _ := value.([]any)

	return items
}

// same compares a value with the recorded one as JSON text, both read back the same way.
func same(t *testing.T, got, want any) bool {
	t.Helper()

	parsed, err := js.Parse(js.Marshal(got))
	if err != nil {
		t.Fatal(err)
	}

	return js.Stringify(parsed) == js.Stringify(want)
}

func TestScreensMatchTheRecordedReads(t *testing.T) {
	t.Parallel()

	for index, row := range list(get(answers(t), "cases")) {
		screen, _ := get(row, "screen").(string)
		stamp, _ := get(row, "stamp").(float64)
		name := "case " + strconv.Itoa(index)

		var parsed any
		if found := ask.Parse(screen, stamp); found != nil {
			parsed = found
		}

		if !same(t, parsed, get(row, "parse")) {
			t.Errorf("%s Parse\n got  %s\n want %s\n screen %q", name, js.Marshal(parsed),
				js.Stringify(get(row, "parse")), screen)
		}

		if !same(t, ask.Unread(screen, stamp), get(row, "unread")) {
			t.Errorf("%s Unread\n got  %s\n want %s", name, js.Marshal(ask.Unread(screen, stamp)),
				js.Stringify(get(row, "unread")))
		}

		if ask.PromptState(screen) != get(row, "prompt") {
			t.Errorf("%s PromptState = %s, want %v", name, ask.PromptState(screen),
				get(row, "prompt"))
		}
	}
}

func TestOwnsTerminalAndIDsMatchTheRecording(t *testing.T) {
	t.Parallel()

	data := answers(t)

	for _, row := range list(get(data, "owns")) {
		pair := list(row)
		stat, _ := pair[0].(string)

		if ask.OwnsTerminal(stat) != pair[1] {
			t.Errorf("OwnsTerminal(%q) = %v", stat, ask.OwnsTerminal(stat))
		}
	}

	for _, row := range list(get(data, "ids")) {
		parts := get(row, "parts")
		options := make([]ask.Option, 0, len(list(get(parts, "options"))))

		for _, raw := range list(get(parts, "options")) {
			key, _ := get(raw, "key").(string)
			label, _ := get(raw, "label").(string)
			role, _ := get(raw, "role").(string)
			options = append(options, ask.Option{Key: key, Label: label, Role: role})
		}

		kind, _ := get(parts, "kind").(string)
		title, _ := get(parts, "title").(string)
		text, _ := get(parts, "text").(string)
		stamp, _ := get(parts, "statusStamp").(float64)

		if ask.ID(kind, title, text, options, stamp) != get(row, "id") {
			t.Errorf("ID(%s) = %s, want %v", kind, ask.ID(kind, title, text, options, stamp),
				get(row, "id"))
		}
	}
}

func TestScreenTextJoinsLinesAsReadScreenDoes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{"none", []string{}, ""},
		{"one", []string{"a"}, "a"},
		{"several, blank ones kept", []string{"a", "", " b"}, "a\n\n b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ask.ScreenText(tt.lines)
			if got != tt.want {
				t.Errorf("ScreenText = %q, want %q", got, tt.want)
			}
		})
	}
}
