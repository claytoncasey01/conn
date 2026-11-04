package reviews_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/settings"
)

// The answers in testdata/answers/reviews/pure.json.gz are Bun's: seeded rows, GitHub answers,
// budgets, scan answers and prompts, reviews.json files, launch names, close decisions, the
// GraphQL calls Reviews makes (their argv and what came of them), the scan runs (what the helper
// is asked and what came of it) and the dry view.

const answersT0 = 1_800_000_000_000

func answers(t *testing.T) *js.Object {
	t.Helper()

	file, err := os.Open("../../testdata/answers/reviews/pure.json.gz")
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

func items(value any) []any {
	list, _ := value.([]any)

	return list
}

func section(t *testing.T, data *js.Object, name string) []any {
	t.Helper()

	list := items(get(data, name))
	if len(list) == 0 {
		t.Fatalf("no %s answers", name)
	}

	return list
}

func str(value any) string {
	found, _ := value.(string)

	return found
}

func num(value any) float64 {
	found, isNumber := value.(float64)
	if !isNumber {
		return math.NaN()
	}

	return found
}

func strs(value any) []string {
	list := items(value)

	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, str(item))
	}

	return out
}

// decode reads a recorded value into a Go value through its JSON.
func decode[T any](t *testing.T, value any) T {
	t.Helper()

	var out T

	err := json.Unmarshal([]byte(js.Stringify(value)), &out)
	if err != nil {
		t.Fatalf("decode %s: %v", js.Stringify(value), err)
	}

	return out
}

// check fails when got does not write the JSON Bun recorded.
func check(t *testing.T, label string, got, want any) {
	t.Helper()

	parsed, err := js.Parse(js.Marshal(got))
	if err != nil {
		t.Fatalf("%s: %v", label, err)
	}

	if js.Stringify(parsed) != js.Stringify(want) {
		t.Errorf("%s:\n got  %s\n want %s", label, js.Stringify(parsed), js.Stringify(want))
	}
}

// outOrError is Bun's {out} or {error}.
func outOrError(value any, err error) *js.Object {
	result := js.NewObject()
	if err != nil {
		result.Set("error", err.Error())

		return result
	}

	result.Set("out", value)

	return result
}

type sourcesJSON struct {
	Requested bool      `json:"requested"`
	Channels  *[]string `json:"channels"`
	DMs       bool      `json:"dms"`
	AllOpen   bool      `json:"allOpen"`
}

type ctxJSON struct {
	Me      string              `json:"me"`
	Sources sourcesJSON         `json:"sources"`
	Session *reviews.RowSession `json:"session"`
	Now     float64             `json:"now"`
}

func TestRowsMatchBun(t *testing.T) {
	t.Parallel()

	data := answers(t)

	for index, row := range section(t, data, "deriveRow") {
		pull := decode[reviews.PR](t, get(row, "pr"))
		tracking := decode[reviews.Entry](t, get(row, "tracking"))
		recorded := decode[ctxJSON](t, get(row, "ctx"))

		var channels []string
		if recorded.Sources.Channels != nil {
			channels = *recorded.Sources.Channels
		}

		got := reviews.DeriveRow(&pull, &tracking, &reviews.DeriveContext{
			Me: recorded.Me, Session: recorded.Session, Now: recorded.Now,
			Sources: reviews.Sources{
				Requested: recorded.Sources.Requested, Channels: channels,
				DMs: recorded.Sources.DMs, AllOpen: recorded.Sources.AllOpen,
			},
		}, str(get(row, "key")))
		check(t, "deriveRow "+strconv.Itoa(index), got, get(row, "out"))
	}

	for index, row := range section(t, data, "sortRows") {
		keys := strs(get(row, "keys"))
		states := strs(get(row, "states"))
		rows := make([]*reviews.Row, 0, len(keys))

		for at, key := range keys {
			rows = append(rows, &reviews.Row{
				Key: key, State: states[at], UpdatedAt: num(items(get(row, "updated"))[at]),
			})
		}

		ordered := reviews.SortRows(rows)

		sorted := make([]string, 0, len(ordered))
		for _, item := range ordered {
			sorted = append(sorted, item.Key)
		}

		check(t, "sortRows "+strconv.Itoa(index), sorted, get(row, "out"))
	}
}

func TestGitHubParsingMatchesBun(t *testing.T) {
	t.Parallel()

	data := answers(t)

	for index, row := range section(t, data, "parsePrList") {
		got := reviews.ParsePRList(get(row, "json"), str(get(row, "repo")))
		check(t, "parsePrList "+strconv.Itoa(index), got, get(row, "out"))
	}

	for index, row := range section(t, data, "parseEnded") {
		check(t, "parseEnded "+strconv.Itoa(index), reviews.ParseEnded(get(row, "json")),
			get(row, "out"))
	}

	for index, row := range section(t, data, "parsePrNode") {
		check(t, "parsePrNode "+strconv.Itoa(index), reviews.ParsePRNode(get(row, "v")),
			get(row, "out"))
	}

	for _, row := range section(t, data, "searchFor") {
		var got any

		if found := reviews.SearchFor(str(get(row, "kind")), str(get(row, "p"))); found != "" {
			got = found
		}

		check(t, "searchFor "+str(get(row, "p")), got, get(row, "out"))
	}

	check(t, "PRFragment", reviews.PRFragment, get(data, "prFragment"))
	check(t, "PRListFields", reviews.PRListFields, get(data, "prListFields"))
}

func TestBudgetMatchesBun(t *testing.T) {
	t.Parallel()

	data := answers(t)

	for index, row := range section(t, data, "parseRate") {
		check(t, "parseRate "+strconv.Itoa(index), ghbudget.ParseRate(get(row, "json")),
			get(row, "out"))
	}

	for index, row := range section(t, data, "pollInterval") {
		got := ghbudget.PollInterval(decode[*ghbudget.RateInfo](t, get(row, "rate")),
			num(get(row, "now")), num(get(row, "base")), int(num(get(row, "parties"))))
		check(t, "pollInterval "+strconv.Itoa(index), got, get(row, "out"))
	}

	for index, row := range section(t, data, "addRate") {
		got := ghbudget.AddRate(decode[*ghbudget.RateInfo](t, get(row, "a")),
			decode[*ghbudget.RateInfo](t, get(row, "b")))
		check(t, "addRate "+strconv.Itoa(index), got, get(row, "out"))
	}

	for index, steps := range section(t, data, "budget") {
		budget := ghbudget.New()

		for at, step := range items(steps) {
			label := "budget " + strconv.Itoa(index) + " step " + strconv.Itoa(at)
			budgetStep(t, label, budget, step)
		}
	}
}

func budgetStep(t *testing.T, label string, budget *ghbudget.Budget, step any) {
	t.Helper()

	who := str(get(step, "who"))
	now := num(get(step, "now"))

	switch str(get(step, "op")) {
	case "note":
		budget.Note(who, now)
	case "leave":
		budget.Leave(who)
	case "parties":
		check(t, label, budget.Parties(who, now), get(step, "out"))
	case "every":
		rate := decode[*ghbudget.RateInfo](t, get(step, "rate"))
		check(t, label, budget.Every(who, rate, now, 180_000), get(step, "out"))
	case "pause":
		budget.Pause(num(get(step, "until")))
	case "pausedUntil":
		check(t, label, budget.PausedUntil(now), get(step, "out"))
	default:
		budget.Resume()
	}
}

func TestScanParsingMatchesBun(t *testing.T) {
	t.Parallel()

	data := answers(t)

	for _, row := range section(t, data, "slackNames") {
		check(t, "slackNames", reviews.SlackNames(strs(get(row, "names"))), get(row, "out"))
	}

	for index, row := range section(t, data, "scanDue") {
		scan := reviews.Scan{Tries: num(get(get(row, "scan"), "tries")), LastAt: 0}
		got := reviews.ScanDue(scan, num(get(row, "first")), num(get(row, "now")))
		check(t, "scanDue "+strconv.Itoa(index), got, get(row, "out"))
	}

	for _, row := range section(t, data, "reposInWords") {
		check(t, "reposInWords", reviews.ReposInWords(strs(get(row, "allow"))), get(row, "out"))
	}

	for _, row := range section(t, data, "tsMs") {
		check(t, "tsMs "+str(get(row, "ts")), reviews.TSMs(str(get(row, "ts"))), get(row, "out"))
	}

	for _, row := range section(t, data, "msTs") {
		check(t, "msTs", reviews.MsTS(num(get(row, "ms"))), get(row, "out"))
	}

	for _, row := range section(t, data, "compareTs") {
		got := reviews.CompareTS(str(get(row, "a")), str(get(row, "b")))
		check(t, "compareTs "+str(get(row, "a"))+" "+str(get(row, "b")), got, get(row, "out"))
	}

	for index, row := range section(t, data, "acceptPosts") {
		got := reviews.AcceptPosts(get(row, "raw"), strs(get(row, "allow")),
			strs(get(row, "channels")))
		check(t, "acceptPosts "+strconv.Itoa(index), got, get(row, "out"))
	}

	for index, row := range section(t, data, "acceptAsks") {
		threads := decode[[]reviews.Thread](t, get(row, "threads"))
		got := reviews.AcceptAsks(get(row, "raw"), threads, num(get(row, "now")))
		check(t, "acceptAsks "+strconv.Itoa(index), got, get(row, "out"))
	}

	for index, row := range section(t, data, "acceptDmPosts") {
		got := reviews.AcceptDMPosts(get(row, "raw"), strs(get(row, "allow")), num(get(row, "now")))
		check(t, "acceptDmPosts "+strconv.Itoa(index), got, get(row, "out"))
	}
}

// scanInputOf reads a recorded prompt input, its NaN times written as null.
func scanInputOf(value any) reviews.ScanInput {
	input := reviews.ScanInput{
		Allow: strs(get(value, "allow")), Unmatched: strs(get(value, "unmatched")),
	}

	for _, channel := range items(get(value, "channels")) {
		var since *string

		if found, isText := get(channel, "sinceTs").(string); isText {
			since = &found
		}

		input.Channels = append(input.Channels, reviews.ChannelRead{
			Name: str(get(channel, "name")), SinceTS: since,
		})
	}

	for _, thread := range items(get(value, "threads")) {
		input.Threads = append(input.Threads, reviews.Thread{
			PR: str(get(thread, "pr")), Channel: str(get(thread, "channel")),
			TS: str(get(thread, "ts")), SinceMs: num(get(thread, "sinceMs")),
		})
	}

	return input
}

func TestPromptsMatchBun(t *testing.T) {
	t.Parallel()

	data := answers(t)

	for index, row := range section(t, data, "scanPrompt") {
		got := reviews.ScanPrompt(scanInputOf(get(row, "o")), 2)
		check(t, "scanPrompt "+strconv.Itoa(index), got, get(row, "out"))
	}

	for _, row := range section(t, data, "dmPrompt") {
		got := reviews.DMPrompt(strs(get(row, "allow")), str(get(row, "sinceTs")))
		check(t, "dmPrompt", got, get(row, "out"))
	}
}

// fakeRun answers a scan run as the recorded case did, and keeps what it was asked.
func fakeRun(row any, asked *claude.Opts) reviews.Run {
	return func(_ context.Context, opts claude.Opts) (claude.Answer, error) {
		*asked = opts

		if auth := strs(get(row, "auth")); len(auth) > 0 {
			opts.OnAuth(auth)
		}

		if fail, failed := get(row, "fail").(string); failed {
			return claude.Answer{}, textError(fail)
		}

		return claude.Answer{Text: "", Structured: get(row, "answer")}, nil
	}
}

// textError is an error that is only its text, as a recorded failure is.
type textError string

func (f textError) Error() string { return string(f) }

func askedJSON(opts claude.Opts) *js.Object {
	asked := js.NewObject()
	asked.Set("name", opts.Name)
	asked.Set("prompt", opts.Prompt)
	asked.Set("model", opts.Model)
	asked.Set("allowedTools", opts.AllowedTools)
	asked.Set("disallowedTools", opts.DisallowedTools)
	asked.Set("schema", opts.Schema)
	asked.Set("maxBudgetUsd", opts.BudgetUSD)
	asked.Set("timeoutMs", float64(opts.Timeout.Milliseconds()))

	return asked
}

func TestScanRunsMatchBun(t *testing.T) {
	t.Parallel()

	cfg := config.FromEnv(func(string) (string, bool) { return "", false })
	scanConfig := reviews.ScanConfig{
		Model: cfg.ReviewScanModel, WindowDays: cfg.ReviewFirstWindowDays,
		BudgetUSD: cfg.ReviewScanCostUSDMax, Timeout: cfg.ReviewScanTimeout,
	}
	input := reviews.ScanInput{
		Channels: []reviews.ChannelRead{{Name: "#code-review", SinceTS: nil}},
		Allow:    []string{"acme/*"}, Unmatched: []string{"acme/widgets#7"}, Threads: nil,
	}

	for index, row := range section(t, answers(t), "scanRuns") {
		var asked claude.Opts

		label := "scanRuns " + strconv.Itoa(index)
		got := js.NewObject()

		if str(get(row, "kind")) == "scan" {
			result, err := reviews.RunScan(t.Context(), input, fakeRun(row, &asked), scanConfig,
				answersT0)
			scanOutcome(got, result, err)
		} else {
			posts, err := reviews.RunDMScan(t.Context(), []string{"acme/*"}, "1799999000.000100",
				fakeRun(row, &asked), scanConfig, answersT0)
			body := js.NewObject()
			body.Set("posts", posts)
			scanOutcome(got, body, err)
		}

		check(t, label+" run", askedJSON(asked), get(row, "run"))

		for _, key := range []string{"out", "error", "needsAuth"} {
			found, _ := got.Get(key)
			check(t, label+" "+key, found, get(row, key))
		}
	}
}

func scanOutcome(got *js.Object, result any, err error) {
	if scanErr, isScan := errors.AsType[*reviews.ScanError](err); isScan {
		got.Set("error", scanErr.Message)
		got.Set("needsAuth", scanErr.NeedsAuth)

		return
	}

	got.Set("out", result)
}

func TestLaunchNamesMatchBun(t *testing.T) {
	t.Parallel()

	data := answers(t)
	cfg := config.FromEnv(func(string) (string, bool) { return "", false })
	names := reviews.Names{Pattern: cfg.ReviewNamePattern}

	for _, row := range section(t, data, "reviewName") {
		number, previous := asNumber(get(row, "n")), asCount(get(row, "prev"))
		got, err := names.ReviewName(str(get(row, "repo")), number, previous)
		check(t, "reviewName "+js.Stringify(row), outOrError(got, err), get(row, "out"))
	}

	for _, row := range section(t, data, "isReviewNameOf") {
		name, isText := get(row, "name").(string)
		got := false

		if isText {
			got = names.IsReviewNameOf(name, str(get(row, "repo")), int64(num(get(row, "n"))))
		}

		check(t, "isReviewNameOf "+js.Stringify(row), got, get(row, "out"))
	}

	for _, row := range section(t, data, "reviewWorktree") {
		got, err := names.ReviewWorktree("/data", str(get(row, "repo")), 7)
		check(t, "reviewWorktree "+str(get(row, "repo")), outOrError(got, err), get(row, "out"))
	}

	prompts := reviews.Prompts{Review: cfg.ReviewPrompt, ReReview: cfg.ReReviewLine}

	for _, row := range section(t, data, "buildPrompt") {
		var sha *string

		if found, isText := get(get(row, "reReview"), "sha").(string); isText {
			sha = &found
		}

		got, err := prompts.BuildPrompt(asNumber(get(row, "n")), sha, str(get(row, "repo")))
		check(t, "buildPrompt "+js.Stringify(row), outOrError(got, err), get(row, "out"))
	}

	for _, row := range section(t, data, "claudeCommand") {
		got, err := reviews.ClaudeCommand(str(get(row, "name")), str(get(row, "model")),
			cfg.ReviewNamePattern, strs(get(row, "disallowed")))
		check(t, "claudeCommand "+js.Stringify(row), outOrError(got, err), get(row, "out"))
	}

	launchChecks(t, data)
}

func launchChecks(t *testing.T, data *js.Object) {
	t.Helper()

	for _, row := range section(t, data, "isSafeRef") {
		check(t, "isSafeRef "+str(get(row, "ref")), reviews.IsSafeRef(str(get(row, "ref"))),
			get(row, "out"))
	}

	for _, row := range section(t, data, "remoteMatches") {
		got := reviews.RemoteMatches(str(get(row, "url")), str(get(row, "repo")))
		check(t, "remoteMatches "+js.Stringify(row), got, get(row, "out"))
	}

	for _, row := range section(t, data, "firstLine") {
		got := reviews.FirstLine(textError(str(get(row, "m"))))
		check(t, "firstLine "+js.Stringify(row), got, get(row, "out"))
	}

	for index, row := range section(t, data, "closeDecision") {
		facts := get(row, "o")

		idle := num(get(facts, "idleForMs"))
		switch get(facts, "idleForMs") {
		case "NaN":
			idle = math.NaN()
		case "Infinity":
			idle = math.Inf(1)
		}

		got := reviews.CloseDecision(reviews.CloseFacts{
			IdleForMs: idle, InputEmpty: get(facts, "inputEmpty") == true,
			ReviewedSinceLaunch: get(facts, "reviewedSinceLaunch") == true,
			AfterMs:             num(get(facts, "afterMs")), CapMs: num(get(facts, "capMs")),
		})
		check(t, "closeDecision "+strconv.Itoa(index), got, get(row, "out"))
	}
}

// asNumber is a recorded PR number as Go takes it: one Bun refuses is 0, which Go refuses too.
func asNumber(value any) int64 {
	found := num(value)
	if found != math.Trunc(found) {
		return 0
	}

	if found > 1<<53-1 {
		return 0
	}

	return int64(found)
}

// asCount is a recorded launch count as Go takes it: one Bun refuses is -1.
func asCount(value any) int {
	found := num(value)
	if found != math.Trunc(found) {
		return -1
	}

	return int(found)
}

func TestDryViewMatchesBun(t *testing.T) {
	t.Parallel()

	dry := get(answers(t), "dry")
	cfg := config.FromEnv(func(string) (string, bool) { return "", false })
	tab := reviews.New(t.Context(), reviews.Deps{
		Settings: settings.NewStore(nil), Now: func() float64 { return answersT0 + 123 },
		Config: reviews.Config{Dry: true, NamePattern: cfg.ReviewNamePattern},
	})

	check(t, "snapshot", tab.Snapshot(), get(dry, "snapshot"))

	seq, err := tab.Refresh()
	if err != nil {
		t.Fatal(err)
	}

	check(t, "refresh", seq, get(dry, "seq"))
	check(t, "after", tab.Snapshot(), get(dry, "after"))

	for _, row := range items(get(dry, "starts")) {
		reply, err := tab.Start(t.Context(), get(row, "repo"), get(row, "n"), get(row, "model"))
		if err != nil {
			t.Fatal(err)
		}

		got := js.NewObject()
		got.Set("code", float64(reply.Code))
		got.Set("body", reply.Body)
		check(t, "start "+js.Stringify(row), got, get(row, "out"))
	}

	reply, err := tab.PlaceLegacy(float64(1), "acme/widgets")
	if err != nil {
		t.Fatal(err)
	}

	got := js.NewObject()
	got.Set("code", float64(reply.Code))
	got.Set("body", reply.Body)
	check(t, "legacy", got, get(dry, "legacy"))
}

// writeText writes a recorded file's text where ReadFile can find it.
func writeText(t *testing.T, text string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "reviews.json")

	err := os.WriteFile(path, []byte(text), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return path
}
