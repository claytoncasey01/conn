package openwork_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/openwork"
)

// makeClone is the recorder's stand-in clone of acme/widgets under base.
func makeClone(t *testing.T, base string) string {
	t.Helper()

	clone := filepath.Join(base, "clones", "widgets")
	heads := filepath.Join(clone, ".git", "refs", "remotes", "origin")

	err := os.MkdirAll(heads, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	writeJSON(t, filepath.Join(clone, ".git", "config"),
		"[remote \"origin\"]\n\turl = https://github.com/acme/widgets.git\n")
	writeJSON(t, filepath.Join(heads, "HEAD"), "ref: refs/remotes/origin/main\n")

	return clone
}

// sequenced answers the helper runs as the recorder's fakes did, in the order they were made:
// every reader's call (and the progress and denials it reports) in turn, then their answers in
// the same order, each once the one before has been taken in.
func (w *world) sequenced(order []string, given map[string]any) {
	readers := 0

	for _, name := range order {
		if !strings.HasPrefix(name, "synth-") {
			readers++
		}
	}

	reported := 0

	w.helper = func(_ context.Context, opts claude.Opts) (claude.Answer, error) {
		turn := indexOf(order, opts.Name)
		until(w.t, "the turn of "+opts.Name, func() bool { return w.count("claude") == turn })

		w.call("claude", optsOf(opts))
		answer := given[opts.Name]

		for _, line := range listOf(get(answer, "progress")) {
			opts.OnProgress(text(line))
		}

		if denied, isList := get(answer, "denied").([]any); isList {
			opts.OnDenied(stringsOf(denied))
		}

		w.mu.Lock()
		reported++
		w.mu.Unlock()

		if turn < readers {
			until(w.t, "the readers' answers before "+opts.Name, func() bool {
				w.mu.Lock()
				defer w.mu.Unlock()

				return reported == readers
			})
			until(w.t, "the answers before "+opts.Name, func() bool {
				return w.count("record") >= turn
			})
		}

		if failure, failed := get(answer, "error").(string); failed {
			return claude.Answer{}, fakeError(failure)
		}

		return claude.Answer{Text: "", Structured: get(answer, "value")}, nil
	}
}

func indexOf(list []string, item string) int {
	for index, entry := range list {
		if entry == item {
			return index
		}
	}

	return -1
}

func stringsOf(list []any) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, text(item))
	}

	return out
}

// count is the number of calls of a kind so far.
func (w *world) count(kind string) int {
	count := 0

	for _, call := range w.recorded() {
		if listOf(call)[0] == kind {
			count++
		}
	}

	return count
}

func TestResearchMatchesTheRecordedAnswers(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	clone := makeClone(t, base)

	for index, item := range section(t, base, "research") {
		label := strconv.Itoa(index)

		t.Run(label, func(t *testing.T) {
			t.Parallel()

			research(t, base, clone, item)
		})
	}
}

func research(t *testing.T, base, clone string, item any) {
	t.Helper()

	worktree := text(get(item, "wt"))

	err := os.MkdirAll(filepath.Join(worktree, ".git", "info"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	if before, isText := get(item, "excludeBefore").(string); isText {
		writeJSON(t, filepath.Join(worktree, ".git", "info", "exclude"), before)
	}

	slack := `,"slack":{"enabled":` + js.Stringify(get(item, "slackOn")) +
		`,"channels":["#code-review"],"dms":false,"draftChannel":null}`
	made := newWorld(t, worldOpts{
		base: base, settings: workSettings(clone, slack), runs: []any{get(item, "record")},
	})
	made.cmuxFails = !openedOK(item)
	made.sh = func(_ string, args []string, _ string) (string, error) {
		if args[0] == "rev-parse" {
			return ".git/info/exclude", nil
		}

		return "", nil
	}

	writeSite(t, made.dir, get(item, "site"))
	made.setClock(clock0 + float64(indexIn(item)*1000))

	order, given := helperOrder(item)
	made.sequenced(order, given)

	err = openwork.Research(made.work, "PROJ-7")
	made.work.Settle()

	thrown := any(nil)
	if err != nil {
		thrown = err.Error()
	}

	same(t, "thrown", thrown, get(item, "thrown"))
	same(t, "calls", made.recorded(), get(item, "calls"))
	same(t, "files", made.files(), loneSurrogates(get(item, "files")))
	same(t, "brief", fileOrNil(filepath.Join(worktree, ".conn", "brief.md")), get(item, "brief"))
	same(t, "exclude", fileOrNil(filepath.Join(worktree, ".git", "info", "exclude")),
		get(item, "exclude"))
	same(t, "snapshot", runsOf(t, made.work), get(item, "snapshot"))
}

// openedOK is whether cmux opened the session in this case: the recorder's choice shows in the
// calls only through the log, so it is read from there.
func openedOK(item any) bool {
	return !strings.Contains(js.Stringify(get(item, "files")), "Could not open a cmux workspace")
}

// indexIn is the case's number, from its worktree's name.
func indexIn(item any) int {
	worktree := text(get(item, "wt"))
	number, _ := strconv.Atoi(worktree[strings.LastIndex(worktree, "-")+1:])

	return number
}

func helperOrder(item any) ([]string, map[string]any) {
	order := []string{}

	for _, call := range listOf(get(item, "calls")) {
		parts := listOf(call)
		if parts[0] == "claude" {
			order = append(order, text(get(parts[1], "name")))
		}
	}

	given := map[string]any{}

	for _, answer := range listOf(get(item, "given")) {
		object := objectOf(answer)
		if _, present := object.Get("progress"); !present {
			object.Set("progress", []any{})
		}

		given[text(get(answer, "name"))] = answer
	}

	return order, given
}

func writeSite(t *testing.T, dir string, site any) {
	t.Helper()

	path := filepath.Join(dir, "jira-site.json")
	_ = os.Remove(path)

	if file, isText := site.(string); isText {
		writeJSON(t, path, file)
	}
}

func fileOrNil(path string) any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	return string(data)
}

func TestListMatchesTheRecordedAnswers(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	clone := makeClone(t, base)

	for index, item := range section(t, base, "list") {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			t.Parallel()

			listCase(t, base, clone, item)
		})
	}
}

func listCase(t *testing.T, base, clone string, item any) {
	t.Helper()

	recorded := strings.ReplaceAll(js.Stringify(get(item, "settings")),
		filepath.Join(base, "clones", "widgets"), clone)
	current, _ := js.Parse([]byte(recorded))

	made := newWorld(t, worldOpts{base: base, settings: current, cache: get(item, "cache")})

	first := parsed(t, made.work.Tickets())
	second := parsed(t, made.work.Tickets())
	want := len(listOf(get(item, "calls")))

	until(t, "the calls", func() bool { return len(made.recorded()) >= want })
	same(t, "first", first, get(item, "first"))
	same(t, "second", second, get(item, "second"))
	same(t, "calls", made.recorded(), get(item, "calls"))
}

func TestStartMatchesTheRecordedAnswers(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	clone := makeClone(t, base)

	for index, item := range section(t, base, "start") {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			t.Parallel()

			startCase(t, base, clone, index, item)
		})
	}
}

func startCase(t *testing.T, base, clone string, index int, item any) {
	t.Helper()

	cache, _ := js.Parse([]byte(`{"fetchedAt":1800000000000,"truncated":false,"projects":"PROJ"}`))
	objectOf(cache).Set("tickets", get(item, "tickets"))

	made := newWorld(t, worldOpts{
		base: base, settings: get(item, "settings"), runs: listOf(get(item, "runs")),
		cache: reordered(objectOf(cache), "tickets"),
	})
	made.setClock(clock0 + float64(index))
	made.sh = func(string, []string, string) (string, error) { return "", fakeError("stop here") }

	body, _ := get(item, "body").(*js.Object)
	reply := made.work.Start(text(get(item, "key")), body)
	made.work.Settle()

	got := js.NewObject()
	got.Set("code", float64(reply.Code))
	got.Set("body", reply.Body)

	same(t, "reply", got, get(item, "reply"))
	same(t, "calls", made.recorded(), get(item, "calls"))
	same(t, "snapshot", runsOf(t, made.work), get(item, "snapshot"))
	same(t, "files", made.files(), get(item, "files"))

	_ = clone
}

// reordered is object with key first.
func reordered(object *js.Object, key string) *js.Object {
	out := js.NewObject()
	value, _ := object.Get(key)
	out.Set(key, value)

	for _, name := range object.Keys() {
		if name != key {
			entry, _ := object.Get(name)
			out.Set(name, entry)
		}
	}

	return out
}

// loneSurrogates are the run files with the half of an emoji a cut left (the recorded file
// writes it as \ud83d) as U+FFFD, which is what Go's strings hold in its place: the one known
// difference (see js.Cut).
func loneSurrogates(files any) any {
	out := js.NewObject()

	for _, name := range objectOf(files).Keys() {
		content, _ := objectOf(files).Get(name)
		out.Set(name, strings.ReplaceAll(text(content), `\ud83d`, "\uFFFD"))
	}

	return out
}
