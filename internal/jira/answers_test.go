package jira_test

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/settings"
)

// The recorded answers in testdata/answers/openwork/pure.json.gz: ticket keys, the JQL, the move
// steps, the site file, the sprint reads and the moves with every helper run's options and
// outcome.

func answers(t *testing.T) *js.Object {
	t.Helper()

	file, err := os.Open("../../testdata/answers/openwork/pure.json.gz")
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
	found, _ := object.Get(key)

	return found
}

func text(value any) string {
	found, _ := value.(string)

	return found
}

func cases(t *testing.T, section string) []any {
	t.Helper()

	list, _ := get(answers(t), section).([]any)
	if len(list) == 0 {
		t.Fatalf("no %s answers", section)
	}

	return list
}

func settingsOf(raw any) settings.Settings { return settings.Parse(raw, true) }

// same compares two values as JSON text, key order included.
func same(t *testing.T, label string, got, want any) {
	t.Helper()

	gotText, wantText := js.Stringify(got), js.Stringify(want)
	if gotText != wantText {
		t.Errorf("%s:\n got %s\nwant %s", label, gotText, wantText)
	}
}

func orNil(value string, present bool) any {
	if !present {
		return nil
	}

	return value
}

func TestKeysMatchTheRecordedAnswers(t *testing.T) {
	t.Parallel()

	for index, item := range cases(t, "keys") {
		current := settingsOf(get(item, "settings"))
		key := text(get(item, "key"))
		label := strconv.Itoa(index) + " " + strconv.Quote(key)
		source := jira.PatternSource(current)

		same(t, label+" on", jira.On(current), get(item, "on"))
		same(t, label+" source", orNil(source, source != ""), get(item, "source"))
		same(t, label+" isKey", jira.IsKey(current, key), get(item, "isKey"))

		keyOf := jira.KeyOf(current, key)
		same(t, label+" keyOf", orNil(keyOf, keyOf != ""), get(item, "keyOf"))
		same(t, label+" any", jira.AnyKey(key), get(item, "any"))
	}
}

func TestJQLAndStepsMatchTheRecordedAnswers(t *testing.T) {
	t.Parallel()

	for _, item := range cases(t, "jql") {
		listed, _ := get(item, "projects").([]any)
		projects := make([]string, 0, len(listed))

		for _, project := range listed {
			projects = append(projects, text(project))
		}

		same(t, "jql", jira.SprintJQL(projects), get(item, "out"))
	}

	for _, item := range cases(t, "steps") {
		same(t, "steps", jira.Steps(text(get(item, "key")), moveOf(get(item, "to"))),
			get(item, "out"))
	}
}

func moveOf(value any) jira.Move {
	object, _ := value.(*js.Object)

	if status, present := object.Get("status"); present {
		return jira.MoveTo(text(status))
	}

	if _, present := object.Get("category"); present {
		return jira.MoveStarted()
	}

	return jira.MoveReview()
}

func writeSite(t *testing.T, dir string, file any) {
	t.Helper()

	path := filepath.Join(dir, "jira-site.json")
	if file == nil {
		_ = os.Remove(path)

		return
	}

	err := os.WriteFile(path, []byte(text(file)), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSiteMatchesTheRecordedAnswers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for index, item := range cases(t, "site") {
		writeSite(t, dir, get(item, "file"))

		var got any

		if site := jira.ReadSite(dir); site != nil {
			got = site.Value
		}

		same(t, strconv.Itoa(index), got, get(item, "out"))
	}
}

// optsOf is a helper run's options as the recorder wrote them.
func optsOf(opts claude.Opts) *js.Object {
	out := js.NewObject()
	out.Set("name", opts.Name)
	out.Set("model", opts.Model)
	out.Set("cwd", orNil(opts.Dir, opts.Dir != ""))

	tools := make([]any, 0, len(opts.AllowedTools))
	for _, tool := range opts.AllowedTools {
		tools = append(tools, tool)
	}

	out.Set("allowedTools", tools)
	out.Set("schema", orNil(opts.Schema, opts.Schema != ""))

	var strict any
	if opts.Lenient {
		strict = false
	}

	out.Set("strict", strict)
	out.Set("maxBudgetUsd", opts.BudgetUSD)
	out.Set("timeoutMs", float64(opts.Timeout/time.Millisecond))
	out.Set("prompt", opts.Prompt)

	return out
}

type fakeError string

func (e fakeError) Error() string { return string(e) }

func fakeRun(item any, asked *[]any) jira.Run {
	return func(_ context.Context, opts claude.Opts) (claude.Answer, error) {
		*asked = append(*asked, optsOf(opts))

		if auth, isList := get(item, "auth").([]any); isList {
			names := make([]string, 0, len(auth))
			for _, name := range auth {
				names = append(names, text(name))
			}

			opts.OnAuth(names)
		}

		if fail := get(item, "fail"); fail != nil {
			return claude.Answer{}, fakeError(text(fail))
		}

		return claude.Answer{Text: "", Structured: get(item, "answer")}, nil
	}
}

func outcome(out any, err error) *js.Object {
	result := js.NewObject()
	if err != nil {
		result.Set("error", err.Error())

		return result
	}

	result.Set("out", out)

	return result
}

func TestSprintReadsMatchTheRecordedAnswers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for index, item := range cases(t, "sprint") {
		writeSite(t, dir, get(item, "site"))

		asked, saved := []any{}, []any{}
		sprint, err := jira.FetchSprint(context.Background(), jira.SprintDeps{
			Settings: settingsOf(get(item, "settings")), DataDir: dir,
			Run: fakeRun(item, &asked),
			Save: func(cloudID, user any) error {
				site := js.NewObject()
				site.Set("cloudId", cloudID)
				site.Set("me", user)
				saved = append(saved, site)

				return nil
			},
			OnProgress: nil, OnAuth: nil,
		})

		label := strconv.Itoa(index)
		same(t, label+" opts", asked, get(item, "opts"))
		same(t, label+" saved", saved, get(item, "saved"))

		var out any

		if err == nil {
			list := js.NewObject()
			tickets := make([]any, 0, len(sprint.Tickets))

			for _, ticket := range sprint.Tickets {
				tickets = append(tickets, ticket)
			}

			list.Set("tickets", tickets)
			list.Set("truncated", sprint.Truncated)
			out = list
		}

		want := js.NewObject()

		for _, key := range []string{"out", "error"} {
			if value, present := asObject(item).Get(key); present {
				want.Set(key, value)
			}
		}

		same(t, label+" outcome", outcome(out, err), want)
	}
}

func TestMovesMatchTheRecordedAnswers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for index, item := range cases(t, "moves") {
		writeSite(t, dir, get(item, "site"))

		asked := []any{}
		moved, err := jira.Transition(context.Background(), jira.MoveDeps{
			Settings: settingsOf(get(item, "settings")), DataDir: dir, Run: fakeRun(item, &asked),
		}, text(get(item, "key")), moveOf(get(item, "to")))

		label := strconv.Itoa(index)
		same(t, label+" opts", asked, get(item, "opts"))

		got := js.NewObject()

		if err == nil {
			out := js.NewObject()
			out.Set("to", moved.To)
			out.Set("moved", moved.Moved)
			got.Set("out", out)
		} else {
			got.Set("error", err.Error())
			kind, called := errorKind(err)
			got.Set("kind", kind)
			got.Set("called", called)
		}

		want := js.NewObject()

		for _, key := range []string{"out", "error", "kind", "called"} {
			if value, present := asObject(item).Get(key); present {
				want.Set(key, value)
			}
		}

		same(t, label+" outcome", got, want)
	}
}

func errorKind(err error) (string, any) {
	if refused, isRefused := errors.AsType[*jira.MoveRefusedError](err); isRefused {
		return "refused", refused.Called
	}

	if _, isSignedOut := errors.AsType[*jira.SignedOutError](err); isSignedOut {
		return "signedOut", nil
	}

	return "error", nil
}

func asObject(value any) *js.Object {
	object, _ := value.(*js.Object)

	return object
}
