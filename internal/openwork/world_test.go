package openwork_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/argv"
	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/drysession"
	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/openwork"
	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/settings"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// world is a Work over a temp data folder whose every call to the outside is a fake that
// records it, in the shape the recorder wrote: [kind, ...arguments].
type world struct {
	t     *testing.T
	dir   string
	home  string
	store *settings.Store
	work  *openwork.Work
	stop  context.CancelFunc

	mu    sync.Mutex
	calls []any
	clock float64

	// The fakes; nil answers as the recorder's default did.
	sh         func(name string, args []string, dir string) (string, error)
	helper     func(ctx context.Context, opts claude.Opts) (claude.Answer, error)
	transition func(ctx context.Context, key string, move jira.Move) (jira.Moved, error)
	fetch      func(ctx context.Context, hooks openwork.Hooks) (jira.Sprint, error)
	cmuxFails  bool
	// cmuxFailsIn is a folder cmux fails to open a workspace in, "" for none.
	cmuxFailsIn string
	// failReader is the reader (by its run name's prefix) whose helper run fails.
	failReader string
	session    *openwork.Found
	// sessionsIn are the live sessions by folder; a folder not in it finds session.
	sessionsIn map[string]*openwork.Found
	// onReply runs as text is typed into a session, before it is taken; nil does nothing.
	onReply func()
}

type worldOpts struct {
	// base is the folder the world's folders go in; "" is a new temp folder.
	base     string
	settings any
	// runs are run records (values to write as JSON, or text written as is).
	runs  []any
	cache any
	dry   bool
	// seen are the repositories found on this machine.
	seen []string
	// sleep stands in for the dry run's waits; nil waits a twentieth of the time.
	sleep func(ctx context.Context, wait time.Duration)
}

func newWorld(t *testing.T, opts worldOpts) *world {
	t.Helper()

	root := t.TempDir()

	dir := filepath.Join(root, "data")
	settingsDir := filepath.Join(root, "settings")
	home := filepath.Join(root, "home")

	for _, folder := range []string{filepath.Join(dir, "runs"), settingsDir, home} {
		err := os.MkdirAll(folder, 0o755)
		if err != nil {
			t.Fatal(err)
		}
	}

	writeJSON(t, filepath.Join(settingsDir, "settings.json"), opts.settings)

	for _, run := range opts.runs {
		key := js.String(get(run, "key"))
		writeJSON(t, filepath.Join(dir, "runs", key+".json"), run)
	}

	if opts.cache != nil {
		writeJSON(t, filepath.Join(dir, "work-cache.json"), opts.cache)
	}

	store := settings.NewStore(nil)
	store.Reload(settingsDir)

	ctx, stop := context.WithCancel(context.Background())
	made := &world{
		t: t, dir: dir, home: home, store: store, stop: stop, clock: 1_800_000_000_000,
	}
	made.work = openwork.New(ctx, made.deps(opts))

	store.OnChange(func(settings.Settings) { made.work.SettingsChanged() })

	t.Cleanup(func() {
		stop()
		made.work.Settle()
	})

	return made
}

// realTemp is a temp folder as the disk names it (macOS's /var is /private/var).
func realTemp(t *testing.T) string {
	t.Helper()

	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	return path
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()

	data := []byte(js.Stringify(value))
	if text, isText := value.(string); isText {
		data = []byte(text)
	}

	err := os.WriteFile(path, data, 0o644)
	if err != nil {
		t.Fatal(err)
	}
}

func (w *world) deps(opts worldOpts) openwork.Deps {
	sleep := opts.sleep
	if sleep == nil {
		sleep = func(ctx context.Context, wait time.Duration) {
			select {
			case <-ctx.Done():
			case <-time.After(wait / 20):
			}
		}
	}

	reader := origin.NewReader()

	return openwork.Deps{
		DataDir: w.dir, Dry: opts.dry, Settings: w.store.Get,
		CloneFor: func(repo string, current settings.Settings) (string, bool) {
			clone, found := reader.CloneFor(repo, current, w.home)

			return clone.Path, found
		},
		SeenRepos: func() []string { return opts.seen },
		OriginOf:  reader.Of, Sh: w.fakeSh, Helper: w.fakeHelper,
		FetchSprint: w.fakeFetch, Transition: w.fakeTransition, Open: w.fakeOpen,
		Claude: "claude",
		Record: func(name string, ok bool, detail string) {
			w.call("record", name, ok, detail)
		},
		FindSession: func(name, cwd string) *openwork.Found {
			w.call("findSession", name, cwd)

			return w.sessionIn(cwd)
		},
		Reply: func(_ context.Context, sessionID, text string) openwork.Reply {
			w.call("reply", sessionID, text)

			if w.onReply != nil {
				w.onReply()
			}

			return openwork.Reply{Code: 200, Body: js.NewObject()}
		},
		Bump: func() {}, Now: w.now, Sleep: sleep, Logger: nil,
	}
}

// sessionIn is the live session the fake session list finds in cwd.
func (w *world) sessionIn(cwd string) *openwork.Found {
	w.mu.Lock()
	defer w.mu.Unlock()

	if found, present := w.sessionsIn[cwd]; present {
		return found
	}

	return w.session
}

// setSessionIn makes found the live session in cwd (nil: none there).
func (w *world) setSessionIn(cwd string, found *openwork.Found) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.sessionsIn == nil {
		w.sessionsIn = map[string]*openwork.Found{}
	}

	w.sessionsIn[cwd] = found
}

func (w *world) now() float64 {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.clock
}

func (w *world) setClock(clock float64) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.clock = clock
}

func (w *world) call(parts ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.calls = append(w.calls, parts)
}

// recorded are the calls so far.
func (w *world) recorded() []any {
	w.mu.Lock()
	defer w.mu.Unlock()

	return slices.Clone(w.calls)
}

// lines are the calls as text, one per call.
func (w *world) lines() []string {
	recorded := w.recorded()
	out := make([]string, 0, len(recorded))

	for _, call := range recorded {
		out = append(out, callLine(call))
	}

	return out
}

func callLine(call any) string {
	parts := listOf(call)

	words := []string{}

	for _, part := range parts {
		if text, isText := part.(string); isText {
			words = append(words, text)

			continue
		}

		if list, isList := part.([]any); isList {
			for _, item := range list {
				words = append(words, js.String(item))
			}

			continue
		}

		words = append(words, js.Stringify(part))
	}

	return strings.Join(words, " ")
}

func anyList(items []string) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}

	return out
}

func orNil(value string) any {
	if value == "" {
		return nil
	}

	return value
}

func (w *world) fakeSh(_ context.Context, name string, args []string, dir string,
	timeout time.Duration,
) (string, error) {
	var limit any
	if timeout != 120*time.Second {
		limit = float64(timeout / time.Millisecond)
	}

	w.call("sh", name, anyList(args), orNil(dir), limit)

	if w.sh != nil {
		return w.sh(name, args, dir)
	}

	if name == "git" {
		if slices.Equal(args[:min(2, len(args))], []string{"rev-parse", "--verify"}) {
			return "", fakeError("no such branch")
		}
	}

	return "", nil
}

type fakeError string

func (e fakeError) Error() string { return string(e) }

// optsOf is a helper run's options as the recorder wrote them.
func optsOf(opts claude.Opts) *js.Object {
	out := js.NewObject()
	out.Set("name", opts.Name)
	out.Set("model", opts.Model)
	out.Set("cwd", orNil(opts.Dir))
	out.Set("allowedTools", anyList(opts.AllowedTools))
	out.Set("schema", orNil(opts.Schema))

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

func (w *world) fakeHelper(ctx context.Context, opts claude.Opts) (claude.Answer, error) {
	if w.helper != nil {
		return w.helper(ctx, opts)
	}

	w.call("claude", optsOf(opts))

	if w.failReader != "" {
		if strings.HasPrefix(opts.Name, w.failReader+"-") {
			return claude.Answer{}, fakeError("the connector said no")
		}
	}

	answer := js.NewObject()
	if strings.HasPrefix(opts.Name, "synth-") {
		answer.Set("brief", "b")
		answer.Set("answered", []any{})

		question := js.NewObject()
		question.Set("text", "q?")
		question.Set("why", "w")
		question.Set("choices", []any{"a", "b"})
		answer.Set("open", []any{question})
	} else {
		answer.Set("summary", "s")
		answer.Set("facts", []any{})
		answer.Set("questions", []any{})
	}

	return claude.Answer{Text: "", Structured: answer}, nil
}

func (w *world) fakeTransition(ctx context.Context, key string, move jira.Move) (jira.Moved,
	error,
) {
	parsed, _ := js.Parse([]byte(move.String()))
	w.call("transition", key, parsed)

	if w.transition != nil {
		return w.transition(ctx, key, move)
	}

	return jira.Moved{To: "Doing", Moved: true}, nil
}

func (w *world) fakeFetch(ctx context.Context, hooks openwork.Hooks) (jira.Sprint, error) {
	w.call("fetchSprint")

	if w.fetch != nil {
		return w.fetch(ctx, hooks)
	}

	<-ctx.Done()

	return jira.Sprint{}, fakeError("cancelled")
}

// fakeOpen opens sessions as cmux does, where the recorded runs opened them: it records the
// session's name, folder and one shell line, and fails as cmux does when cmuxFails is set.
func (w *world) fakeOpen(_ context.Context, spec pty.Spec) launch.Opened {
	if spec.Argv[0] == drysession.Program {
		w.call("dry-session", spec.Name, spec.Dir, argv.ShellLine(spec.Argv))

		return launch.Opened{
			OK: true, Where: settings.WhereInside, HostedID: "0123456789abcdef0123456789abcdef",
			Place: "inside Conn", Tried: "", Error: "", ManualCommand: "",
		}
	}

	w.call("cmux", spec.Name, spec.Dir, argv.ShellLine(spec.Argv))

	failing := w.cmuxFails
	if w.cmuxFailsIn != "" {
		failing = w.cmuxFailsIn == spec.Dir
	}

	if failing {
		return launch.Opened{
			OK: false, Where: settings.WhereTerminal, HostedID: "",
			Place: "in a new cmux workspace", Tried: "a cmux workspace",
			Error: "cmux is not running", ManualCommand: launch.PasteLine(spec),
		}
	}

	return launch.Opened{
		OK: true, Where: settings.WhereTerminal, HostedID: "", Place: "in a new cmux workspace",
		Tried: "a cmux workspace", Error: "", ManualCommand: "",
	}
}

// files are the run files, by name.
func (w *world) files() *js.Object {
	entries, err := os.ReadDir(filepath.Join(w.dir, "runs"))
	if err != nil {
		w.t.Fatal(err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	sort.Strings(names)

	out := js.NewObject()

	for _, name := range names {
		data, readErr := os.ReadFile(filepath.Join(w.dir, "runs", name))
		if readErr != nil {
			w.t.Fatal(readErr)
		}

		out.Set(name, string(data))
	}

	return out
}

// state is a run's state in the snapshot, "" when there is none.
func (w *world) state(key string) string {
	parsed, _ := js.Parse(openwork.Snapshot(w.work))
	for _, run := range listOf(parsed) {
		if get(run, "key") == key {
			return text(get(run, "state"))
		}
	}

	return ""
}

func (w *world) record(key string) openwork.Record { return openwork.RecordOf(w.work, key) }

// until waits for cond, failing the test after a scaled four seconds.
func until(t *testing.T, what string, cond func() bool) {
	t.Helper()

	end := time.Now().Add(testtime.Scaled(4 * time.Second))
	for time.Now().Before(end) {
		if cond() {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s", what)
}

func (w *world) update(name, body string) {
	w.t.Helper()

	parsed, err := js.Parse([]byte(body))
	if err != nil {
		w.t.Fatal(err)
	}

	result := w.store.Update(name, parsed)
	if !result.OK {
		w.t.Fatalf("update %s: %s", name, result.Error)
	}
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

// same compares two values as JSON text, key order included.
func same(t *testing.T, label string, got, want any) {
	t.Helper()

	gotText, wantText := js.Stringify(got), js.Stringify(want)
	if gotText != wantText {
		t.Errorf("%s:\n got %s\nwant %s", label, gotText, wantText)
	}
}

// byKey is a copy of a list of runs ordered by their keys, so a comparison does not depend on the
// order the runs were listed in.
func byKey(runs any) any {
	list := slices.Clone(listOf(runs))

	slices.SortFunc(list, func(left, right any) int {
		return strings.Compare(js.String(get(left, "key")), js.String(get(right, "key")))
	})

	return list
}

// runsOf are the snapshot's runs as parsed JSON.
func runsOf(t *testing.T, w *openwork.Work) any {
	t.Helper()

	parsed, err := js.Parse(openwork.Snapshot(w))
	if err != nil {
		t.Fatal(err)
	}

	return parsed
}

// listOf is a parsed JSON array, nil for anything else.
func listOf(value any) []any {
	list, _ := value.([]any)

	return list
}

// objectOf is a parsed JSON object, nil for anything else.
func objectOf(value any) *js.Object {
	object, _ := value.(*js.Object)

	return object
}
