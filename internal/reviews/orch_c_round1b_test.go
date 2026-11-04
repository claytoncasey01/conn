package reviews_test

// Fix round 1, continued: saves that change nothing, the sweep's clone, a file that cannot be
// written over, a scan's reads hitting the rate limit, and records that age out.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/settings"
)

// cCounts are how many polls and channel scans ran.
func cCounts(fakes *world) [2]int {
	return [2]int{fakes.snapshotCalls().list, cScans(fakes)}
}

func TestOrchCSaveThatChangesNothingReadStartsNeitherPollNorScan(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{settings: cSettings(settingsOpts{dms: new(false)})})
	tickAndSettle(tab)

	before := cCounts(fakes)
	cloned := settingsOf(settingsOpts{dms: new(false)})
	cloned.Repos = settings.Repos{
		Allow: []string{"acme/*"}, Clones: settings.NewClones(worldRepo, "/x"), CloneRoot: nil,
	}

	fakes.setSettings(cloned)
	tab.SettingsChanged()
	tickAndSettle(tab)
	cIs(t, "a clone folder starts nothing", cCounts(fakes), before)

	cChanged(tab, fakes, settingsOpts{
		dms: new(false), channels: []string{"#code-review", "#ship-it"},
	})
	tickAndSettle(tab)
	cIs(t, "a channel starts both", cCounts(fakes), [2]int{before[0] + 1, before[1] + 1})
}

// cStatusCall and cRemoveCall are the sweep's git calls for a worktree made from clone.
func cStatusCall(worktree string) gitCall {
	return gitCall{args: []string{"status", "--porcelain"}, dir: worktree, timeout: cGitLimit}
}

func cRemoveCall(worktree string) gitCall {
	return gitCall{args: []string{"worktree", "remove", worktree}, dir: cClone, timeout: cGitLimit}
}

// cGitLines are git calls as text, to compare.
func cGitLines(calls []gitCall) []string {
	lines := make([]string, 0, len(calls))
	for _, call := range calls {
		lines = append(lines, strings.Join(call.args, " ")+" in "+call.dir+" for "+
			call.timeout.String())
	}

	return lines
}

func TestOrchCSweepRemovesAWorktreeWithTheCloneItWasMadeFrom(t *testing.T) {
	t.Parallel()

	tab, fakes := cClosedLaunch(t)
	fakes.with(func(state *world) {
		state.clone = func(string) string { return "/somewhere/else/widgets" }
		state.now += 20_000
	})
	cLeaves(t, tab, fakes, 3)

	worktree := cWt(fakes, 7)
	cSame(t, "git", cGitLines(cGitCalls(fakes)),
		cGitLines([]gitCall{cStatusCall(worktree), cRemoveCall(worktree)}))
}

func TestOrchCPREndedWhileAllowedKeepsItsWorktreeOnceTheRepoLeaves(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{
		open: []*reviews.PR{}, settings: cSettings(settingsOpts{allow: []string{"jane/*"}}),
	})
	cEndedLaunch(t, fakes)
	tickAndSettle(tab)
	cIs(t, "git calls", len(cGitCalls(fakes)), 0)
}

func TestOrchCChannelScanErrorIsNotShownOnceNoChannelIsChosen(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	fakes.with(func(state *world) { state.scanResult = cScanFails(errCBoom) })
	tickAndSettle(tab)
	cIs(t, "slack", view(t, tab).Slack, "error")

	fakes.setSettings(settingsOf(settingsOpts{channels: []string{}}))
	cIs(t, "slack", view(t, tab).Slack, "unused")
	cIs(t, "slackError", cText(view(t, tab).SlackError), "<null>")
}

// cChmod sets a path's mode, and the mode back when the test ends.
func cChmod(t *testing.T, path string, mode, back os.FileMode) {
	t.Helper()
	t.Cleanup(func() { _ = os.Chmod(path, back) })

	err := os.Chmod(path, mode)
	if err != nil {
		t.Fatal(err)
	}
}

// cVersion is the version field of the file at path as it is on disk.
func cVersion(t *testing.T, path string) any {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := js.Parse(data)
	if err != nil {
		t.Fatal(err)
	}

	object, isObject := parsed.(*js.Object)
	if !isObject {
		t.Fatal("not an object")
	}

	version, _ := object.Get("version")

	return version
}

func TestOrchCFileThatCannotBeWrittenOverSaysSoAndStillLists(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{settings: cSetupSettings(), noClient: true})
	cWriteV1(t, fakes.file, `{"version":1,"cursorTs":null,"prs":{}}`)
	cChmod(t, fakes.file, 0, 0o600)
	cChanged(tab, fakes, settingsOpts{})
	tickAndSettle(tab)

	shown := view(t, tab)
	cIs(t, "storeError "+cText(shown.StoreError),
		strings.Contains(cText(shown.StoreError), "could not be kept aside"), true)
	cSame(t, "rows", cRowKeys(t, tab), []string{keyOf(7)})

	err := os.Chmod(fakes.file, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	cIs(t, "never written over", cVersion(t, fakes.file), any(1.0))
}

func TestOrchCScanReadsThatHitTheRateLimitPauseGitHubForMyPRsToo(t *testing.T) {
	t.Parallel()

	budget := ghbudget.New()
	tab, fakes := setup(t, setupOpts{
		open:     []*reviews.PR{cRequested(nil), cPR(8, worldRepo)},
		settings: cSettings(settingsOpts{dms: new(false)}), budget: budget,
	})
	tickAndSettle(tab)

	until := float64(worldT0 + hourMs)
	held := cGated(t)

	cSwapGh(tab, cGh{
		w: fakes, search: nil,
		fetch: func([]reviews.KeyParts) (*js.Map[*reviews.PR], error) {
			held.pass()

			return nil, cRateLimited(until)
		},
	})
	fakes.with(func(state *world) { state.scanResult = cPosts(cPost(8)) })
	fakes.advance(10 * minuteMs)
	cRefresh(t, tab)
	// The poll and the scan start in the same tick; the poll ends before the scan's read
	// answers (a poll that ends later clears the pause all the same).
	tab.Tick()
	held.reached(t)
	cWaitFor(t, "the poll", cPolledAt(t, tab, fakes.clock()))
	held.open()
	tab.Settle()
	tab.Tick()
	tab.Settle()
	cIs(t, "pausedUntil", cNum(view(t, tab).PausedUntil), cJSON(until))
	cIs(t, "the budget's pause", cNum(budget.PausedUntil(fakes.clock())), cJSON(until))
}

func TestOrchCRecordThatHoldsNothingAgesOutOneWithAPostIsKept(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{
		open: []*reviews.PR{}, settings: cSettings(settingsOpts{slack: new(false)}), noClient: true,
	})
	cOldRecord(t, fakes, 25, 20, func(entry *reviews.Entry) {
		entry.Requested, entry.Launches = false, []*reviews.Launch{}
	})
	cOldRecord(t, fakes, 26, 20, func(entry *reviews.Entry) {
		entry.Requested, entry.Launches, entry.Slack = false, []*reviews.Launch{}, cPostOf()
	})
	tickAndSettle(tab)
	cIs(t, "25 aged out", cHas(fakes.file, keyOf(25)), false)
	cIs(t, "26 kept", cHas(fakes.file, keyOf(26)), true)
	cIs(t, "reads", len(fakes.snapshotCalls().fetch), 0)
}

// cDataDir is the folder reviews.json is in.
func cDataDir(fakes *world) string { return filepath.Dir(fakes.file) }
