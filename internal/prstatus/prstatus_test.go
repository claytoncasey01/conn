package prstatus_test

import (
	"compress/gzip"
	"io"
	"os"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/prstatus"
)

// The answers in testdata/answers/prstatus/prstatus.json.gz were recorded for seeded check
// rollups, PR views and fix messages.

func answers(t *testing.T) any {
	t.Helper()

	file, err := os.Open("../../testdata/answers/prstatus/prstatus.json.gz")
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

	return parsed
}

func get(value any, key string) any {
	object, _ := value.(*js.Object)
	found, _ := object.Get(key)

	return found
}

func items(value any) []any {
	list, _ := value.([]any)

	return list
}

func same(got, want any) bool {
	parsed, _ := js.Parse(js.Marshal(got))

	return js.Stringify(parsed) == js.Stringify(want)
}

func TestPRStatusMatchesTheRecording(t *testing.T) {
	t.Parallel()

	data := answers(t)

	for index, row := range items(get(data, "checks")) {
		got := prstatus.SummarizeChecks(get(row, "rollup"))
		if !same(got, get(row, "out")) {
			t.Errorf("checks %d: got %s, want %s", index, js.Marshal(got),
				js.Stringify(get(row, "out")))
		}
	}

	for index, row := range items(get(data, "views")) {
		view, _ := get(row, "view").(*js.Object)
		at, _ := get(get(row, "out"), "at").(float64)

		got := prstatus.From(view, at)
		if !same(got, get(row, "out")) {
			t.Errorf("view %d: got %s, want %s", index, js.Marshal(got),
				js.Stringify(get(row, "out")))
		}
	}

	for index, row := range items(get(data, "messages")) {
		checkMessage(t, index, row)
	}

	for _, row := range items(get(data, "clean")) {
		pair := items(row)
		name, _ := pair[0].(string)

		if prstatus.CleanName(name) != pair[1] {
			t.Errorf("CleanName(%q) = %q, want %v", name, prstatus.CleanName(name), pair[1])
		}
	}
}

func checkMessage(t *testing.T, index int, row any) {
	t.Helper()

	number, _ := get(row, "n").(float64)
	repo, _ := get(row, "repo").(string)
	failed := make([]string, 0, len(items(get(row, "failed"))))

	for _, name := range items(get(row, "failed")) {
		text, _ := name.(string)
		failed = append(failed, text)
	}

	got, err := prstatus.FixChecksMessage(number, failed, repo)

	want, isMessage := get(row, "out").(string)
	if isMessage {
		if got != want {
			t.Errorf("message %d:\n got  %q\n want %q", index, got, want)
		}

		return
	}

	if err == nil {
		t.Errorf("message %d: got %q, want %v", index, got, get(row, "error"))

		return
	}

	if err.Error() != get(row, "error") {
		t.Errorf("message %d: error %q, want %v", index, err, get(row, "error"))
	}
}
