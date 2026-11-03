// Package answers reads testdata/answers/: recorded calls and what the server answered, shared
// with the UI's tests, so the page and the server agree on every case. A test runs each case
// through the Go code and compares the JSON of the result.
package answers

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
)

// Case is one recorded call: its arguments and the answer, as decoded JSON (objects as
// *js.Object).
type Case struct {
	Args []any
	Out  any
}

// Load reads the cases recorded for function in testdata/answers/<file>.
func Load(tb testing.TB, file, function string) []Case {
	tb.Helper()

	recorded := readFile(tb, file)

	list, found := recorded.Get(function)
	if !found {
		tb.Fatalf("%s has no %s", file, function)
	}

	entries, isList := list.([]any)
	if !isList {
		tb.Fatalf("%s: %s is not a list", file, function)
	}

	cases := make([]Case, 0, len(entries))
	for _, entry := range entries {
		cases = append(cases, readCase(tb, file, entry))
	}

	if len(cases) == 0 {
		tb.Fatalf("%s: no cases for %s", file, function)
	}

	return cases
}

func readFile(tb testing.TB, file string) *js.Object {
	tb.Helper()

	_, here, _, found := runtime.Caller(0)
	if !found {
		tb.Fatal("cannot find this file")
	}

	path := filepath.Join(filepath.Dir(here), "..", "..", "testdata", "answers", file)

	text, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}

	parsed, err := js.Parse(text)
	if err != nil {
		tb.Fatalf("%s: %v", file, err)
	}

	recorded, isObject := parsed.(*js.Object)
	if !isObject {
		tb.Fatalf("%s is not an object", file)
	}

	return recorded
}

func readCase(tb testing.TB, file string, entry any) Case {
	tb.Helper()

	object, isObject := entry.(*js.Object)
	if !isObject {
		tb.Fatalf("%s: a case is not an object", file)
	}

	args, _ := object.Get("args")
	out, _ := object.Get("out")

	list, isList := args.([]any)
	if !isList {
		tb.Fatalf("%s: a case's args are not a list", file)
	}

	return Case{Args: list, Out: out}
}

// Check fails t when got, written as JSON, is not the recorded answer.
func Check(tb testing.TB, recorded Case, got any) {
	tb.Helper()

	gotJSON := js.Stringify(got)

	wantJSON := js.Stringify(recorded.Out)
	if gotJSON != wantJSON {
		tb.Errorf("%s:\n got  %s\n want %s", js.Stringify(recorded.Args), gotJSON, wantJSON)
	}
}

// String is a case's argument that must be a string.
func String(tb testing.TB, recorded Case, index int) string {
	tb.Helper()

	text, ok := recorded.Args[index].(string)
	if !ok {
		tb.Fatalf("%s: argument %d is not text", js.Stringify(recorded.Args), index)
	}

	return text
}

// Strings is a case's argument that is a list of strings, or nil when it is not a list.
func Strings(tb testing.TB, recorded Case, index int) []string {
	tb.Helper()

	list, ok := recorded.Args[index].([]any)
	if !ok {
		return nil
	}

	texts := make([]string, 0, len(list))
	for _, element := range list {
		text, isText := element.(string)
		if !isText {
			tb.Fatalf("%s: argument %d holds a non-text", js.Stringify(recorded.Args), index)
		}

		texts = append(texts, text)
	}

	return texts
}
