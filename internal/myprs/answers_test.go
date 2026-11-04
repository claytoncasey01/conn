package myprs_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/settings"
)

// The recorded answers in testdata/answers/myprs/pure.json.gz cover text cleaning, GitHub answers
// parsed, the searches and thread reads (argv and results), the rules, the replies file, the
// writes, the Slack draft runs, the launches (every git, write and cmux call), myprs.json files
// read, saved and pruned, the scope and the order of repo names.

const trigger = "@greptile review"

var (
	errNoMoreAnswers = errors.New("no more answers")
	errExited        = errors.New("claude exited 1\x07")
	errEmpty         = errors.New("")
)

func answers(t *testing.T) *js.Object {
	t.Helper()

	file, err := os.Open("../../testdata/answers/myprs/pure.json.gz")
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

func number(value any) float64 {
	found, _ := value.(float64)

	return found
}

func texts(value any) []string {
	list, _ := value.([]any)
	out := make([]string, 0, len(list))

	for _, item := range list {
		out = append(out, text(item))
	}

	return out
}

func cases(t *testing.T, data *js.Object, name string) []any {
	t.Helper()

	list, _ := get(data, name).([]any)
	if len(list) == 0 {
		t.Fatalf("no %s answers", name)
	}

	return list
}

// decode reads a recorded value into a Go value through JSON.
func decode[T any](t *testing.T, value any) T {
	t.Helper()

	var out T

	err := json.Unmarshal(js.Marshal(value), &out)
	if err != nil {
		t.Fatalf("decoding %s: %v", js.Stringify(value), err)
	}

	return out
}

// same compares got, as JSON.stringify would write it, with the recorded answer.
func same(t *testing.T, what string, index int, got, want any) {
	t.Helper()

	parsed, err := js.Parse(js.Marshal(got))
	if err != nil {
		t.Fatalf("%s %d: %v", what, index, err)
	}

	if js.Stringify(parsed) != js.Stringify(want) {
		t.Errorf("%s %d:\n got  %s\n want %s", what, index, js.Stringify(parsed),
			js.Stringify(want))
	}
}

func defaults() config.Config {
	return config.FromEnv(func(string) (string, bool) { return "", false })
}

func TestTextMatchesTheRecording(t *testing.T) {
	t.Parallel()

	data := answers(t)

	for index, one := range cases(t, data, "clean") {
		got := myprs.Clean(text(get(one, "s")), int(number(get(one, "max"))))
		same(t, "clean", index, got, get(one, "out"))
	}

	for index, one := range cases(t, data, "cleanMultiline") {
		got := myprs.CleanMultiline(text(get(one, "s")), int(number(get(one, "max"))))
		same(t, "cleanMultiline", index, got, get(one, "out"))
	}
}

func TestParsingMatchesTheRecording(t *testing.T) {
	t.Parallel()

	data := answers(t)

	for index, one := range cases(t, data, "parse") {
		got := myprs.ParsePRs(get(one, "answer"), text(get(one, "me")), trigger)
		same(t, "parse", index, got, get(one, "out"))
	}

	for index, one := range cases(t, data, "searchFor") {
		found, ok := myprs.SearchFor(text(get(one, "me")), text(get(one, "p")))

		var got any
		if ok {
			got = found
		}

		same(t, "searchFor", index, got, get(one, "out"))
	}

	for index, one := range cases(t, data, "searchPatterns") {
		same(t, "searchPatterns", index, myprs.SearchPatterns(texts(get(one, "ps"))),
			get(one, "out"))
	}
}

// script answers gh calls as the recorder's fake did, and logs them.
type script struct {
	steps []any
	calls [][]string
}

func (s *script) exec(_ context.Context, args []string) (string, error) {
	s.calls = append(s.calls, args)

	if len(s.steps) == 0 {
		return "", errNoMoreAnswers
	}

	step := s.steps[0]
	s.steps = s.steps[1:]

	if failed, isFail := get(step, "fail").(string); isFail {
		return text(get(step, "stdout")), &myprs.FailureError{Message: failed}
	}

	return text(get(step, "out")), nil
}

func newScript(value any) *script {
	steps, _ := value.([]any)

	return &script{steps: steps, calls: [][]string{}}
}

func failureOf(err error) any {
	out := js.NewObject()
	out.Set("error", err.Error())

	if limited, ok := errors.AsType[*ghbudget.RateLimitError](err); ok {
		out.Set("rateLimited", true)

		var reset any
		if limited.ResetAt != nil {
			reset = *limited.ResetAt
		}

		out.Set("resetAt", reset)
	}

	return out
}

type searchOut struct {
	PRs    []*myprs.PR           `json:"prs"`
	Capped bool                  `json:"capped"`
	Failed []myprs.SearchFailure `json:"failed"`
	Rate   *ghbudget.RateInfo    `json:"rate"`
}

func TestSearchesMatchTheRecording(t *testing.T) {
	t.Parallel()

	data := answers(t)

	for index, one := range cases(t, data, "prs") {
		calls := newScript(get(one, "script"))
		source := myprs.Gh{Exec: calls.exec, Trigger: trigger}
		found, err := source.PRs(context.Background(), text(get(one, "me")),
			texts(get(one, "patterns")))

		var got any
		if err != nil {
			got = failureOf(err)
		} else {
			wrapped := js.NewObject()
			wrapped.Set("out", searchOut{
				PRs: found.PRs, Capped: found.Capped, Failed: found.Failed, Rate: found.Rate,
			})
			got = wrapped
		}

		same(t, "prs calls", index, calls.calls, get(one, "calls"))
		same(t, "prs", index, got, get(one, "out"))
	}

	for index, one := range cases(t, data, "threadInfo") {
		calls := newScript(get(one, "script"))
		source := myprs.Gh{Exec: calls.exec, Trigger: trigger}
		info, err := source.ThreadInfo(context.Background(), text(get(one, "id")))

		got := js.NewObject()
		if err != nil {
			got.Set("error", err.Error())
		} else {
			got.Set("out", info)
		}

		same(t, "threadInfo calls", index, calls.calls, get(one, "calls"))
		same(t, "threadInfo", index, got, get(one, "out"))
	}
}

func TestRulesMatchTheRecording(t *testing.T) {
	t.Parallel()

	data := answers(t)
	bots := defaults().FindingBots

	for index, one := range cases(t, data, "rowThreads") {
		pull := decode[*myprs.PR](t, get(one, "p"))
		proposals := decode[[]*myprs.Proposal](t, get(one, "ps"))
		same(t, "rowThreads", index, myprs.RowThreads(pull, proposals), get(one, "out"))
	}

	for index, one := range cases(t, data, "threadCounts") {
		pull := decode[*myprs.PR](t, get(one, "p"))
		same(t, "threadCounts", index, myprs.ThreadCounts(pull, bots), get(one, "out"))
	}

	for index, one := range cases(t, data, "botReviewed") {
		pull := decode[*myprs.PR](t, get(one, "p"))
		got := myprs.BotReviewed(pull, text(get(one, "bot")), number(get(one, "since")))
		same(t, "botReviewed", index, got, get(one, "out"))
	}

	for index, one := range cases(t, data, "protectedHead") {
		pull := decode[*myprs.PR](t, get(one, "p"))

		var got any
		if why := myprs.ProtectedHead(pull, texts(get(one, "heads"))); why != "" {
			got = why
		}

		same(t, "protectedHead", index, got, get(one, "out"))
	}
}

func factsOf(t *testing.T, value any) myprs.RoundFacts {
	t.Helper()

	facts := myprs.RoundFacts{
		Launch:         decode[*myprs.Launch](t, get(value, "launch")),
		SessionGone:    get(value, "sessionGone") == true,
		SessionIdle:    get(value, "sessionIdle") == true,
		SessionWaiting: get(value, "sessionWaiting") == true,
		QuietMs:        number(get(value, "quietMs")), RepliesAt: nil,
	}

	if at, isNumber := get(value, "repliesAt").(float64); isNumber {
		facts.RepliesAt = &at
	}

	return facts
}

func TestFlowMatchesTheRecording(t *testing.T) {
	t.Parallel()

	data := answers(t)
	bots := defaults().FindingBots

	for index, one := range cases(t, data, "roundOver") {
		same(t, "roundOver", index, myprs.RoundOver(factsOf(t, get(one, "f"))), get(one, "out"))
	}

	for index, one := range cases(t, data, "askDue") {
		facts := get(one, "f")
		last := math.Inf(-1)

		if at, isNumber := get(facts, "lastAskAt").(float64); isNumber {
			last = at
		}

		got := myprs.AskDue(myprs.AskFacts{
			RoundFacts: factsOf(t, facts), PR: decode[*myprs.PR](t, get(facts, "pr")),
			LastAskAt: last, Now: number(get(facts, "now")),
		})
		same(t, "askDue", index, got, get(one, "out"))
	}

	for index, one := range cases(t, data, "askedLaunch") {
		launches := decode[[]*myprs.Launch](t, get(one, "ls"))
		found := myprs.AskedLaunch(launches, text(get(one, "head")))

		got := -1

		for at, launch := range launches {
			if launch == found {
				got = at
			}
		}

		same(t, "askedLaunch", index, got, get(one, "out"))
	}

	for index, one := range cases(t, data, "readyFor") {
		pull := decode[*myprs.PR](t, get(one, "p"))
		launch := decode[*myprs.Launch](t, get(one, "l"))
		same(t, "readyFor", index, myprs.ReadyFor(pull, launch, bots, "greptile-apps[bot]"),
			get(one, "out"))
	}
}

func setOf(value any) map[string]bool {
	out := map[string]bool{}
	for _, item := range texts(value) {
		out[item] = true
	}

	return out
}

func TestRepliesMatchTheRecording(t *testing.T) {
	t.Parallel()

	for index, one := range cases(t, answers(t), "replies") {
		got := myprs.AcceptReplies(get(one, "raw"), setOf(get(one, "open")),
			setOf(get(one, "settled")))
		same(t, "replies", index, got, get(one, "out"))
	}
}

func TestWritesMatchTheRecording(t *testing.T) {
	t.Parallel()

	for index, one := range cases(t, answers(t), "writes") {
		calls := newScript(get(one, "script"))
		writes := myprs.Writes{Exec: calls.exec, Trigger: trigger}
		ctx := context.Background()

		var err error

		switch text(get(one, "kind")) {
		case "trigger":
			err = writes.TriggerReview(ctx, text(get(one, "repo")), number(get(one, "n")))
		case "reply":
			err = writes.ReplyToThread(ctx, text(get(one, "id")), text(get(one, "text")))
		default:
			err = writes.ResolveThread(ctx, text(get(one, "id")))
		}

		got := js.NewObject()
		if err != nil {
			got.Set("error", err.Error())
		}

		same(t, "write calls", index, calls.calls, get(one, "calls"))
		same(t, "write", index, got, get(one, "out"))
	}
}

func outOrError(found string, err error) any {
	out := js.NewObject()
	if err != nil {
		out.Set("error", err.Error())
	} else {
		out.Set("out", found)
	}

	return out
}

func TestDraftsMatchTheRecording(t *testing.T) {
	t.Parallel()

	data := answers(t)
	cfg := defaults()

	for index, one := range cases(t, data, "draftText") {
		o := get(one, "o")
		found, err := myprs.DraftText(text(get(o, "title")), text(get(o, "url")),
			number(get(o, "round")))
		same(t, "draftText", index, outOrError(found, err), get(one, "out"))
	}

	for index, one := range cases(t, data, "drafts") {
		var asked any

		run := draftRun(text(get(one, "mode")), &asked)
		result := myprs.RunDraft(context.Background(), text(get(one, "channel")),
			text(get(one, "text")), run, myprs.DraftConfig{
				Model: cfg.ReviewScanModel, BudgetUSD: cfg.ReviewScanCostUSDMax,
				Timeout: cfg.ReviewScanTimeout,
			})

		got := js.NewObject()
		got.Set("ok", result.OK)

		if !result.OK {
			got.Set("error", result.Error)

			if result.Final {
				got.Set("final", true)
			}
		}

		same(t, "draft opts", index, asked, get(one, "opts"))
		same(t, "draft", index, got, get(one, "out"))
	}
}

// draftRun is the recorder's fake helper run for mode; asked gets the options as the recorder's
// fake kept them.
func draftRun(mode string, asked *any) reviews.Run {
	answers := map[string]any{"ok": okAnswer(true), "notok": okAnswer(false)}

	return func(_ context.Context, opts claude.Opts) (claude.Answer, error) {
		schema, _ := js.Parse([]byte(opts.Schema))
		kept := js.NewObject()

		for _, field := range []struct {
			key   string
			value any
		}{
			{"name", opts.Name},
			{"prompt", opts.Prompt},
			{"model", opts.Model},
			{"allowedTools", opts.AllowedTools},
			{"disallowedTools", opts.DisallowedTools},
			{"schema", schema},
			{"maxBudgetUsd", opts.BudgetUSD},
			{"timeoutMs", float64(opts.Timeout / time.Millisecond)},
			{"onAuth", "function"},
		} {
			kept.Set(field.key, field.value)
		}

		*asked = kept

		switch mode {
		case "auth", "authThrow":
			opts.OnAuth([]string{"claude.ai Slack", "Atlassian"})
		}

		switch mode {
		case "throw", "authThrow":
			return claude.Answer{Text: "", Structured: nil}, errExited
		case "throwEmpty":
			return claude.Answer{Text: "", Structured: nil}, errEmpty
		}

		structured := map[string]any{
			"code": codeAnswer("channel_not_found"), "exists": codeAnswer("attached_draft_exists"),
			"badcode": codeAnswer("Bad Code!"), "auth": okAnswer(true), "nonobj": "yes",
		}[mode]
		if found, ok := answers[mode]; ok {
			structured = found
		}

		return claude.Answer{Text: "", Structured: structured}, nil
	}
}

func okAnswer(ok bool) *js.Object {
	out := js.NewObject()
	out.Set("ok", ok)

	return out
}

func codeAnswer(code string) *js.Object {
	out := okAnswer(false)
	out.Set("error", code)

	return out
}

func selfOf() myprs.Self {
	cfg := defaults()

	return myprs.Self{Pattern: cfg.SelfNamePattern, Prompt: cfg.SelfReviewPrompt}
}

func TestLaunchNamesMatchTheRecording(t *testing.T) {
	t.Parallel()

	data := answers(t)
	self := selfOf()

	for index, one := range cases(t, data, "selfName") {
		found, err := self.Name(text(get(one, "repo")), number(get(one, "n")),
			number(get(one, "round")))
		same(t, "selfName", index, outOrError(found, err), get(one, "out"))
	}

	for index, one := range cases(t, data, "selfFolder") {
		found, err := myprs.Folder(text(get(one, "dir")), text(get(one, "repo")),
			number(get(one, "n")))
		same(t, "selfFolder", index, outOrError(found, err), get(one, "out"))
	}

	for index, one := range cases(t, data, "selfPrompt") {
		o := get(one, "o")
		found, err := self.BuildPrompt(myprs.PromptOpts{
			Repo: text(get(o, "repo")), Number: number(get(o, "n")),
			Round: number(get(o, "round")), Worktree: text(get(o, "worktree")),
			HeadRef: text(get(o, "headRef")),
		})
		same(t, "selfPrompt", index, outOrError(found, err), get(one, "out"))
	}
}

// launchWorld is the recorder's fake launch deps for one case.
type launchWorld struct {
	answers *js.Object
	present map[string]bool
	opened  any
	fails   bool
	calls   []any
	writes  []any
}

func (w *launchWorld) git(_ context.Context, args []string, dir string) (string, error) {
	call := js.NewObject()
	call.Set("args", args)

	var cwd any
	if dir != "" {
		cwd = dir
	}

	call.Set("cwd", cwd)
	w.calls = append(w.calls, call)

	rest := args
	if len(args) > 1 {
		if args[0] == "-C" {
			rest = args[2:]
		}
	}

	joined := strings.Join(rest, " ")

	for _, key := range w.answers.Keys() {
		if !strings.HasPrefix(joined, key) {
			continue
		}

		answer, _ := w.answers.Get(key)
		if why, isError := get(answer, "error").(string); isError {
			return "", &myprs.FailureError{Message: why}
		}

		return text(answer), nil
	}

	return "", nil
}

func (w *launchWorld) open(_ context.Context, name, cwd, command string) cmux.Opened {
	asked := js.NewObject()
	asked.Set("name", name)
	asked.Set("cwd", cwd)
	asked.Set("command", command)

	call := js.NewObject()
	call.Set("open", asked)
	w.calls = append(w.calls, call)

	return cmux.Opened{
		OK: get(w.opened, "ok") == true, Error: text(get(w.opened, "error")),
		ManualCommand: text(get(w.opened, "manualCommand")),
	}
}

func (w *launchWorld) write(path, content string, shared bool) error {
	write := js.NewObject()
	write.Set("path", path)
	write.Set("text", content)

	var how any

	if shared {
		object := js.NewObject()
		object.Set("shared", true)
		how = object
	}

	write.Set("w", how)
	w.writes = append(w.writes, write)

	if w.fails {
		return &myprs.FailureError{Message: "EACCES: permission denied, open '" + path + "'"}
	}

	return nil
}

func launchResultOf(result myprs.LaunchResult) *js.Object {
	out := js.NewObject()
	out.Set("ok", result.OK)

	if result.OK {
		out.Set("worktree", result.Worktree)
		out.Set("headSha", result.HeadSha)

		return out
	}

	out.Set("error", result.Error)

	if result.ManualCommand != "" {
		out.Set("manualCommand", result.ManualCommand)
	}

	if result.Worktree != "" {
		out.Set("worktree", result.Worktree)
		out.Set("headSha", result.HeadSha)
	}

	return out
}

func TestLaunchesMatchTheRecording(t *testing.T) {
	t.Parallel()

	self := selfOf()

	for index, one := range cases(t, answers(t), "launches") {
		answersOf, _ := get(one, "answers").(*js.Object)
		world := &launchWorld{
			answers: answersOf, present: setOf(get(one, "present")), opened: get(one, "opened"),
			fails: get(one, "writeFails") == true, calls: []any{}, writes: []any{},
		}
		deps := reviews.LaunchDeps{
			Git: world.git, Open: world.open, Write: world.write,
			Exists: func(path string) bool { return world.present[path] },
			Read:   func(string) (string, error) { return "existing\n", nil },
			Origin: nil, Names: reviews.Names{Pattern: nil},
			Prompts: reviews.Prompts{Review: "", ReReview: ""},
		}
		o := get(one, "o")
		result := self.Launch(context.Background(), deps, myprs.LaunchOpts{
			Repo: text(get(o, "repo")), Number: number(get(o, "number")),
			HeadRef: text(get(o, "headRef")), Model: text(get(o, "model")),
			Name: text(get(o, "name")), RepoPath: text(get(o, "repoPath")),
			DataDir: text(get(o, "dataDir")), Round: number(get(o, "round")),
		})

		same(t, "launch calls", index, world.calls, get(one, "calls"))
		same(t, "launch writes", index, world.writes, get(one, "writes"))
		same(t, "launch", index, launchResultOf(result), get(one, "out"))
	}
}

func TestStoreFilesMatchTheRecording(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for index, one := range cases(t, answers(t), "files") {
		path := filepath.Join(dir, "myprs.json")

		err := os.WriteFile(path, []byte(text(get(one, "text"))), 0o600)
		if err != nil {
			t.Fatal(err)
		}

		loaded := myprs.LoadFile(path)
		same(t, "loaded", index, loaded, get(one, "loaded"))

		if string(loaded.Marshal()) != text(get(one, "saved")) {
			t.Errorf("saved %d:\n got  %s\n want %s", index, loaded.Marshal(),
				text(get(one, "saved")))
		}

		covers := func(string) bool { return true }
		if get(one, "cover") != true {
			covers = func(key string) bool { return strings.HasPrefix(key, "acme/") }
		}

		myprs.Prune(loaded, setOf(get(one, "open")), 1_800_000_000_000, covers)
		same(t, "pruned", index, loaded, get(one, "pruned"))
	}
}

func TestScopeAndOrderMatchTheRecording(t *testing.T) {
	t.Parallel()

	data := answers(t)

	for index, one := range cases(t, data, "scope") {
		holder := settings.Defaults()
		holder.Repos.Allow = nil

		if allow, isList := get(get(get(one, "s"), "repos"), "allow").([]any); isList {
			holder.Repos.Allow = texts(allow)
		}

		slack := get(get(one, "s"), "slack")
		if channel, isText := get(slack, "draftChannel").(string); isText {
			holder.Slack.DraftChannel = &channel
		}

		holder.Slack.Channels = texts(get(slack, "channels"))
		on := setOf(get(get(one, "o"), "on"))
		got := myprs.ScopeOf(holder, get(get(one, "o"), "setup") == true,
			func(name string) bool { return on[name] })

		same(t, "scope", index, scopeOut{
			Setup: got.Setup, Allow: got.Allow, GitHub: got.GitHub, Slack: got.Slack,
			DraftChannel: got.DraftChannel,
		}, get(one, "out"))
	}

	for index, one := range cases(t, data, "collate") {
		got := myprs.Collate(text(get(one, "a")), text(get(one, "b")))
		same(t, "collate "+text(get(one, "a"))+" "+text(get(one, "b")), index, got,
			get(one, "out"))
	}
}

type scopeOut struct {
	Setup        bool     `json:"setup"`
	Allow        []string `json:"allow"`
	GitHub       bool     `json:"github"`
	Slack        bool     `json:"slack"`
	DraftChannel *string  `json:"draftChannel"`
}
