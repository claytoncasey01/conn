package reviews_test

// Helpers for the last orchestrator tests (the DM scan, several repos, the fix rounds, the
// Settings cards, the poll cadence and the lows). Every name starts with c, so it cannot clash
// with the helpers of the other parts.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
	"github.com/claytoncasey01/conn/internal/testtime"
)

const (
	// cDMScanMs is the DM scan's interval in the world's config.
	cDMScanMs = 600_000
	cDayMs    = 86_400_000
	// cWindowMs is the first-scan window (FirstWindowDays is 2 in the world's config).
	cWindowMs = 2 * cDayMs
	// cOverlapMs is how far back of its cursor a scan reads.
	cOverlapMs = 15 * minuteMs
	cClone     = "/nowhere/clones/widgets"
	cConnector = "claude.ai Slack"
	cTS        = "1799999000.000100"
	cGitLimit  = 30 * time.Second
	cWaitStep  = time.Millisecond
	cSonnet    = "sonnet"
	cGadgets   = "acme/gadgets"
	cJaneTools = "jane/tools"
	cSignedOut = "Slack is signed out"
	cRateUntil = worldT0 + 2*hourMs
)

var (
	errCBoom        = errors.New("boom")
	errCPlain       = errors.New("plain")
	errCTimedOut    = errors.New("timed out")
	errCChannelBoom = errors.New("channel boom")
	errCHost        = errors.New("gh: could not resolve host")
)

// cIs checks one fact.
func cIs[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

// cSame checks one list.
func cSame[T comparable](t *testing.T, what string, got, want []T) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

// cPost is a channel post as the scan reports it (in #code-review, about acme/widgets#number,
// at cTS).
func cPost(number int64) reviews.Post {
	return reviews.Post{
		Repo: worldRepo, Number: number, Key: keyOf(number), Channel: "#code-review", TS: cTS,
		Permalink: "https://acme.slack.com/archives/C1/p" + strconv.FormatInt(number, 10),
	}
}

// cDMLink is the link of a DM whose first message is ts.
func cDMLink(firstTS string) string {
	return "https://acme.slack.com/archives/D000/p" + strings.Replace(firstTS, ".", "", 1)
}

// cDM is a DM as the DM scan reports it (about acme/widgets or repo).
func cDM(number int64, firstTS, lastTS string, repo ...string) reviews.DMFind {
	name := worldRepo
	if len(repo) > 0 {
		name = repo[0]
	}

	return reviews.DMFind{
		Repo: name, Number: number, Key: keyOf(number, name), FirstTS: firstTS,
		Permalink: cDMLink(firstTS), LastTS: lastTS,
	}
}

// cDMOnly is a DM scan that answers finds.
func cDMOnly(finds ...reviews.DMFind) func() ([]reviews.DMFind, error) {
	return func() ([]reviews.DMFind, error) { return append([]reviews.DMFind{}, finds...), nil }
}

// cDMFails is a DM scan that fails with err.
func cDMFails(err error) func() ([]reviews.DMFind, error) {
	return func() ([]reviews.DMFind, error) { return nil, err }
}

// cSlackOut is a scan that found Slack signed out.
func cSlackOut(message string) error {
	return &reviews.ScanError{Message: message, NeedsAuth: []string{cConnector}}
}

// cScanErr is a scan that failed with Slack signed in.
func cScanErr(message string) error {
	return &reviews.ScanError{Message: message, NeedsAuth: []string{}}
}

// cEmptyScan is a scan that found nothing.
func cEmptyScan() reviews.ScanResult {
	return reviews.ScanResult{Posts: []reviews.Post{}, Asks: []reviews.Ask{}, NeedsAuth: []string{}}
}

// cPosts is a channel scan that finds posts.
func cPosts(posts ...reviews.Post) func() (reviews.ScanResult, error) {
	return func() (reviews.ScanResult, error) {
		result := cEmptyScan()
		result.Posts = append(result.Posts, posts...)

		return result, nil
	}
}

// cScanFails is a channel scan that fails with err.
func cScanFails(err error) func() (reviews.ScanResult, error) {
	return func() (reviews.ScanResult, error) { return reviews.ScanResult{}, err }
}

// cRefresh presses REFRESH.
func cRefresh(t *testing.T, tab *reviews.Reviews) int {
	t.Helper()

	seq, err := tab.Refresh()
	if err != nil {
		t.Fatal(err)
	}

	return seq
}

// cPressIs checks the snapshot's refresh count after one press: done is 0 while its work runs.
func cPressIs(t *testing.T, tab *reviews.Reviews, done int, why string) {
	t.Helper()

	shown := view(t, tab).Refresh
	cIs(t, why+": refresh seq", shown.Seq, 1)
	cIs(t, why+": refresh done", shown.Done, done)
}

// cDMs are the DM scans so far.
func cDMs(fakes *world) []dmCall { return fakes.snapshotCalls().dm }

// cScans is how many channel scans ran.
func cScans(fakes *world) int { return len(fakes.snapshotCalls().scan) }

// cBumps is how many snapshots were sent.
func cBumps(fakes *world) int { return fakes.snapshotCalls().bump }

// cText is a nullable text as the checks show it.
func cText(value *string) string {
	if value == nil {
		return "<null>"
	}

	return *value
}

// cNum is a nullable number as the checks show it.
func cNum(value *float64) string {
	if value == nil {
		return "<null>"
	}

	return js.Number(*value)
}

// cEdit changes the saved file: a load, the change, then a save.
func cEdit(t *testing.T, path string, change func(file *reviews.File)) {
	t.Helper()

	file := reviews.LoadFile(path)
	change(file)

	err := reviews.SaveFile(path, file, nil)
	if err != nil {
		t.Fatal(err)
	}
}

// cEntry is the saved record of key, nil when there is none.
func cEntry(path, key string) *reviews.Entry {
	entry, _ := reviews.LoadFile(path).PRs.Get(key)

	return entry
}

// cHas is whether the saved file has a record of key.
func cHas(path, key string) bool { return reviews.LoadFile(path).PRs.Has(key) }

// cWaitFor waits until done holds (it reads what the goroutines Reviews started have done).
func cWaitFor(t *testing.T, what string, done func() bool) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(gateWait))
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("waited too long for %s", what)
		}

		time.Sleep(cWaitStep)
	}
}

// cGated is a gate that is opened when the test ends, so nothing waits on it for good.
func cGated(t *testing.T) *gate {
	t.Helper()

	held := newGate()
	t.Cleanup(held.open)

	return held
}

// cStart presses REVIEW on PR number of repo.
func cStart(t *testing.T, tab *reviews.Reviews, repo, number string) reviews.Reply {
	t.Helper()

	reply, err := tab.Start(t.Context(), repo, number, cSonnet, nil)
	if err != nil {
		t.Fatal(err)
	}

	return reply
}

// cPlace places old records.
func cPlace(t *testing.T, tab *reviews.Reviews, number, repo any) reviews.Reply {
	t.Helper()

	reply, err := tab.PlaceLegacy(number, repo)
	if err != nil {
		t.Fatal(err)
	}

	return reply
}

// cReason is a reply's error.
func cReason(reply reviews.Reply) string {
	if reply.Body == nil {
		return ""
	}

	why, _ := reply.Body.Get("error")
	text, _ := why.(string)

	return text
}

// cRowKeys are the keys of the rows shown.
func cRowKeys(t *testing.T, tab *reviews.Reviews) []string {
	t.Helper()

	shown := view(t, tab)
	keys := make([]string, 0, len(shown.Rows))

	for _, row := range shown.Rows {
		keys = append(keys, row.Key)
	}

	return keys
}

// cRow is the row of key, nil when none is shown.
func cRow(t *testing.T, tab *reviews.Reviews, key string) *reviews.Row {
	t.Helper()

	for _, row := range view(t, tab).Rows {
		if row.Key == key {
			return row
		}
	}

	return nil
}

// cFetched are every key read by number so far.
func cFetched(fakes *world) []string {
	keys := []string{}

	for _, call := range fakes.snapshotCalls().fetch {
		keys = append(keys, call...)
	}

	return keys
}

// cJSON is a value as JSON.stringify writes it, for comparing shapes.
func cJSON(value any) string { return string(js.Marshal(value)) }

// cDecode reads JSON into out.
func cDecode(t *testing.T, data []byte, out any) {
	t.Helper()

	err := json.Unmarshal(data, out)
	if err != nil {
		t.Fatal(err)
	}
}

// cWt is where a launch of PR number of repo (acme/widgets by default) puts its worktree.
func cWt(fakes *world, number int64, repo ...string) string {
	name := worldRepo
	if len(repo) > 0 {
		name = repo[0]
	}

	return worktreeOf(fakes.dir, name, number)
}

// cOldWt is where a launch of PR number put it before Reviews read several repos.
func cOldWt(fakes *world, number int64) string {
	return filepath.Join(fakes.dir, "reviews", "pr-"+strconv.FormatInt(number, 10), "widgets")
}

// cSeededLaunch is a reviews.json holding one closed launch of PR number of repo at worktree, as
// a run before a restart left it.
func cSeededLaunch(t *testing.T, fakes *world, worktree, repo string, number int64) {
	t.Helper()

	at := float64(worldT0 - hourMs)

	cEdit(t, fakes.file, func(file *reviews.File) {
		file.PRs.Set(keyOf(number, repo), cRecord(at, number, worktree, worldT0-hourMs/2))
	})
}

// cRecord is a requested record first seen at seenAt, with one closed launch at worktree.
func cRecord(seenAt float64, number int64, worktree string, closedAt float64) *reviews.Entry {
	session := "s"

	return &reviews.Entry{
		Slack: nil, SlackAskAt: nil, DM: nil, DMLastAt: nil, FirstSeenAt: seenAt, Requested: true,
		Ended: nil, SeenAt: seenAt, PlacedAt: nil,
		Launches: []*reviews.Launch{{
			At: seenAt, Model: cSonnet, Name: "pr-" + strconv.FormatInt(number, 10), HeadSha: "aaa",
			Worktree: worktree, SessionID: &session, ClosedAt: &closedAt, Clone: "",
		}},
		Scan:       reviews.Scan{Tries: 0, LastAt: 0},
		ThreadScan: reviews.ThreadScan{Tries: 0, LastAt: 0, HeadSha: ""},
	}
}

// cOldRecord is a record of acme/widgets#number, last seen open days ago, requested, with one
// closed launch; change edits it.
func cOldRecord(
	t *testing.T, fakes *world, number int64, days float64, change func(*reviews.Entry),
) {
	t.Helper()

	at := worldT0 - days*cDayMs

	cEdit(t, fakes.file, func(file *reviews.File) {
		entry := cRecord(at, number, cOldWt(fakes, number), at)
		if change != nil {
			change(entry)
		}

		file.PRs.Set(keyOf(number), entry)
	})
}

// cSession is the live session of a review of PR number, idle.
func cSession(fakes *world, number int64) sessions.LiveSession {
	return sessions.LiveSession{
		Name: "pr-" + strconv.FormatInt(number, 10) + "-acme--widgets", Cwd: cWt(fakes, number),
		Status: "idle", SessionID: "s-1", PID: 501,
	}
}

// cAt sets the clock to minutes after T0 and ticks once, waited out.
func cAt(tab *reviews.Reviews, fakes *world, minutes float64) {
	fakes.with(func(state *world) { state.now = worldT0 + minutes*minuteMs })
	tab.Tick()
	tab.Settle()
}

// cReady is Reviews with PR 7 listed (requests me) and its rows built.
func cReady(t *testing.T, opts setupOpts) (*reviews.Reviews, *world) {
	t.Helper()

	tab, fakes := setup(t, opts)
	tickAndSettle(tab)

	return tab, fakes
}

// cClosedLaunch is PR 7 launched, its session gone and the launch closed; the PR is still open.
func cClosedLaunch(t *testing.T) (*reviews.Reviews, *world) {
	t.Helper()

	tab, fakes := cReady(t, setupOpts{})

	if reply := cStart(t, tab, worldRepo, "7"); reply.Code != 200 {
		t.Fatalf("start %d %s", reply.Code, cReason(reply))
	}

	fakes.with(func(state *world) { state.sessions = []sessions.LiveSession{cSession(state, 7)} })
	cAt(tab, fakes, 1)
	fakes.with(func(state *world) { state.sessions = nil })
	cAt(tab, fakes, 2)

	entry := cEntry(fakes.file, keyOf(7))
	if entry == nil {
		t.Fatal("no record of PR 7")
	}

	if entry.Launches[0].ClosedAt == nil {
		t.Fatal("the launch is not closed")
	}

	return tab, fakes
}

// cLeaves is the PR ending (merged) before the next poll, which reads it by number.
func cLeaves(t *testing.T, tab *reviews.Reviews, fakes *world, minutes float64) {
	t.Helper()

	fakes.with(func(state *world) { state.open = []*reviews.PR{} })
	cRefresh(t, tab)
	cAt(tab, fakes, minutes)
	tab.Settle()
}

// cGitCalls are the git calls so far.
func cGitCalls(fakes *world) []gitCall {
	fakes.mu.Lock()
	defer fakes.mu.Unlock()

	return append([]gitCall{}, fakes.git...)
}

// cRecords are what Reviews told the Settings cards so far.
func cRecords(fakes *world) []string {
	fakes.mu.Lock()
	defer fakes.mu.Unlock()

	return append([]string{}, fakes.records...)
}

// cLastRecord is the last record pick keeps ("" for none).
func cLastRecord(fakes *world, pick func(line string) bool) string {
	last := ""

	for _, line := range cRecords(fakes) {
		if pick(line) {
			last = line
		}
	}

	return last
}

// cSlackLine is whether a record is the Slack card's.
func cSlackLine(line string) bool { return strings.HasPrefix(line, "slack") }

// cRequested is PR 7 asking me.
func cRequested(change func(pr *reviews.PR)) *reviews.PR {
	return prOf(func(pr *reviews.PR) {
		pr.Requested = []string{worldMe}

		if change != nil {
			change(pr)
		}
	})
}

// cPR is PR number of repo, asking nobody.
func cPR(number int64, repo string) *reviews.PR {
	return prOf(func(pr *reviews.PR) { pr.Number, pr.Repo = number, repo })
}

// cGh is the world's GitHub with a search or a read by number swapped (nil: the world's).
type cGh struct {
	worldGh

	search func(queries []reviews.Query) ([]reviews.SearchResult, *ghbudget.RateInfo, error)
	fetch  func(prs []reviews.KeyParts) (*js.Map[*reviews.PR], error)
}

func (g cGh) Search(ctx context.Context, queries []reviews.Query) (
	[]reviews.SearchResult, *ghbudget.RateInfo, error,
) {
	if g.search == nil {
		return g.worldGh.Search(ctx, queries)
	}

	return g.search(queries)
}

func (g cGh) Fetch(ctx context.Context, prs []reviews.KeyParts,
	onRate func(*ghbudget.RateInfo),
) (*js.Map[*reviews.PR], error) {
	if g.fetch == nil {
		return g.worldGh.Fetch(ctx, prs, onRate)
	}

	return g.fetch(prs)
}

// cSwapGh puts gh in place of the world's GitHub (nothing may be running).
func cSwapGh(tab *reviews.Reviews, gh reviews.GitHub) {
	tab.SetDeps(func(deps *reviews.Deps) { deps.Gh = gh })
}

// cWriteV1 writes a version 1 reviews.json.
func cWriteV1(t *testing.T, path, body string) {
	t.Helper()

	err := os.WriteFile(path, []byte(body), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

// cSetupSettings are the settings while Conn is in setup (no allowlist yet).
func cSetupSettings() *settings.Settings { return cSettings(settingsOpts{setup: true}) }

// cSettings are the settings opts make.
func cSettings(opts settingsOpts) *settings.Settings {
	current := settingsOf(opts)

	return &current
}

// cChanged saves new settings and tells Reviews, as a saved update does.
func cChanged(tab *reviews.Reviews, fakes *world, opts settingsOpts) {
	fakes.setSettings(settingsOf(opts))
	tab.SettingsChanged()
}

// cRateLimited is GitHub's rate limit used up until resetAt.
func cRateLimited(resetAt float64) error { return &ghbudget.RateLimitError{ResetAt: &resetAt} }

// cReply is a reply as text for comparing: the code and the body.
func cReply(reply reviews.Reply) string {
	return strconv.Itoa(reply.Code) + " " + js.Stringify(reply.Body)
}

// cKeysSorted are keys sorted as JavaScript's sort() sorts strings.
func cKeysSorted(keys []string) []string {
	sorted := append([]string{}, keys...)
	slices.Sort(sorted)

	return sorted
}
