package transcript_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/transcript"
)

func writeFile(t *testing.T, name, text string) {
	t.Helper()

	err := os.WriteFile(name, []byte(text), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, name, text string) {
	t.Helper()

	file, err := os.OpenFile(name, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	_, err = file.WriteString(text)
	if err != nil {
		t.Fatal(err)
	}

	err = file.Close()
	if err != nil {
		t.Fatal(err)
	}
}

// numbered is count lines of {"n": i, ...extra(i)}, each ending in a newline.
func numbered(count int, extra func(index int) string) string {
	var builder strings.Builder

	for index := range count {
		builder.WriteString(`{"n":` + strconv.Itoa(index) + extra(index) + "}\n")
	}

	return builder.String()
}

// drain reads in budgets of maxBytes until the tail is at its end.
func drain(t *testing.T, tail *transcript.Tail, maxBytes int64) ([]any, int) {
	t.Helper()

	got := []any{}
	calls := 0

	for {
		got = append(got, tail.Read(maxBytes)...)
		calls++

		if tail.AtEnd() {
			return got, calls
		}

		if calls >= 100_000 {
			t.Fatal("Read(bytesMax) makes no progress")
		}
	}
}

func TestABudgetGivesTheSameLinesOverSeveralCalls(t *testing.T) {
	t.Parallel()

	name := filepath.Join(t.TempDir(), "many.jsonl")
	writeFile(t, name, numbered(100, func(index int) string {
		return `,"text":"line ` + strconv.Itoa(index) + " é " + strings.Repeat("x", index%17) + `"`
	}))

	all := transcript.NewTail(name).ReadAll()
	if len(all) != 100 {
		t.Fatalf("lines = %d", len(all))
	}

	tail := transcript.NewTail(name)

	got, calls := drain(t, tail, 1000)
	if calls < 2 {
		t.Error("a 1000 byte budget took one call")
	}

	if js.Stringify(got) != js.Stringify(all) {
		t.Error("the budgeted lines differ")
	}

	if len(tail.Read(1000)) != 0 {
		t.Error("a read at the end gave lines")
	}
}

func TestAPartialLastLineIsHeldUntilItIsCompleted(t *testing.T) {
	t.Parallel()

	name := filepath.Join(t.TempDir(), "partial.jsonl")
	writeFile(t, name, `{"a":1}`+"\n"+`{"b":`)

	tail := transcript.NewTail(name)
	if js.Stringify(tail.ReadAll()) != `[{"a":1}]` {
		t.Error("the first read")
	}

	if !tail.AtEnd() {
		t.Error("not at the end with a partial line held")
	}

	appendFile(t, name, "2}\n")

	if !tail.AtEnd() {
		t.Error("AtEnd is not relative to the size seen at the last read")
	}

	if js.Stringify(tail.ReadAll()) != `[{"b":2}]` {
		t.Error("the completed line")
	}
}

func TestAFileDeletedMidReplayEndsTheLoop(t *testing.T) {
	t.Parallel()

	name := filepath.Join(t.TempDir(), "deleted.jsonl")
	writeFile(t, name, numbered(100, func(int) string { return "" }))

	tail := transcript.NewTail(name)
	got := append(tail.Read(100), tail.Read(100)...)

	if tail.AtEnd() {
		t.Fatal("at the end after 200 bytes")
	}

	err := os.Remove(name)
	if err != nil {
		t.Fatal(err)
	}

	more, calls := drain(t, tail, 100)
	if calls > 3 {
		t.Errorf("the loop took %d calls", calls)
	}

	if len(got)+len(more) >= 100 {
		t.Error("read every line of a deleted file")
	}

	// A file that comes back is read again (shorter than the offset, so it restarts).
	writeFile(t, name, `{"back":1}`+"\n")

	if len(tail.ReadAll()) < 1 {
		t.Error("the file that came back was not read")
	}
}

func TestATruncatedFileResetsAndReadsItsNewLines(t *testing.T) {
	t.Parallel()

	name := filepath.Join(t.TempDir(), "truncated.jsonl")
	writeFile(t, name, numbered(50, func(int) string { return "" }))

	tail := transcript.NewTail(name)
	drain(t, tail, 200)

	writeFile(t, name, `{"fresh":1}`+"\n"+`{"fresh":2}`+"\n")

	got, _ := drain(t, tail, 8)
	if js.Stringify(got) != `[{"fresh":1},{"fresh":2}]` {
		t.Errorf("got %s", js.Stringify(got))
	}
}

func TestABudgetEndingInsideACharacterStillParsesEveryLine(t *testing.T) {
	t.Parallel()

	name := filepath.Join(t.TempDir(), "multibyte.jsonl")
	writeFile(t, name, numbered(20, func(index int) string {
		return `,"text":"` + "\u20ac \U0001F600 " + strings.Repeat("\u20ac", index) + `"`
	}))

	all := transcript.NewTail(name).ReadAll()
	if len(all) != 20 {
		t.Fatalf("lines = %d", len(all))
	}

	for _, budget := range []int64{1, 2, 3, 4, 5, 7} {
		got, _ := drain(t, transcript.NewTail(name), budget)
		if js.Stringify(got) != js.Stringify(all) {
			t.Errorf("budget %d: the lines differ", budget)
		}
	}
}

func TestBadLinesAreSkipped(t *testing.T) {
	t.Parallel()

	name := filepath.Join(t.TempDir(), "bad.jsonl")
	writeFile(t, name, "{\"a\":1}\n\nnot json\n\ufeff{\"bom\":1}\n{\"b\":2}\r\n7\n")

	got := js.Stringify(transcript.NewTail(name).ReadAll())
	if got != `[{"a":1},{"b":2},7]` {
		t.Errorf("got %s", got)
	}
}
