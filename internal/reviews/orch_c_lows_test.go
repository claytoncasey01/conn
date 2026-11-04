package reviews_test

// The re-review round's lows: an old worktree's own origin, reviews an earlier conn started,
// settings that change under a search, partly signed-out channel runs, and leads whose source is
// off or whose reads are paused.

import (
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
)

var cEarlierConn = regexp.MustCompile(`started by an earlier conn is still running`)

func TestOrchCOldWorktreeWhoseOriginNamesAnotherRepoIsNotSwept(t *testing.T) {
	t.Parallel()

	tests := []struct {
		origin string
		swept  bool
	}{
		{origin: worldRepo, swept: false},
		{origin: cJaneTools, swept: true},
		{origin: "JANE/Tools", swept: true},
		{origin: "", swept: true},
	}

	for _, tt := range tests {
		t.Run("origin "+tt.origin, func(t *testing.T) {
			t.Parallel()

			tab, fakes := setup(t, setupOpts{
				open:     []*reviews.PR{},
				settings: cSettings(settingsOpts{allow: []string{cJaneTools}}),
			})
			old := cOldWt(fakes, 41)

			cSeededLaunch(t, fakes, old, cJaneTools, 41)
			fakes.with(func(state *world) {
				state.gone[keyOf(41, cJaneTools)] = true
				state.originOf = func(path string) string {
					if path == old {
						return tt.origin
					}

					return ""
				}
			})
			tickAndSettle(tab)

			swept := slices.ContainsFunc(cGitCalls(fakes), func(call gitCall) bool {
				return call.args[0] == "worktree"
			})
			cIs(t, "swept", swept, tt.swept)
		})
	}
}

func TestOrchCReviewAnEarlierConnStartedBlocksReviewOfThatNumber(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cwd  func(fakes *world) string
		want int
	}{
		{name: "pr-7", cwd: func(fakes *world) string { return cOldWt(fakes, 7) }, want: 409},
		{name: "pr-7-2", cwd: func(fakes *world) string { return cOldWt(fakes, 7) }, want: 409},
		{name: "pr-7", cwd: func(fakes *world) string {
			return filepath.Join(fakes.dir, "mine", "widgets")
		}, want: 200},
		{name: "pr-70", cwd: func(fakes *world) string {
			return filepath.Join(fakes.dir, "reviews", "pr-70", "widgets")
		}, want: 200},
	}

	for _, tt := range tests {
		t.Run(tt.name+" "+cJSON(tt.want), func(t *testing.T) {
			t.Parallel()

			tab, fakes := cReady(t, setupOpts{})
			fakes.with(func(state *world) {
				state.sessions = []sessions.LiveSession{{
					Name: tt.name, Cwd: tt.cwd(state), Status: "busy", SessionID: "old-session",
					PID: 4242,
				}}
			})

			reply := cStart(t, tab, worldRepo, "7")
			cIs(t, "code", reply.Code, tt.want)

			if tt.want == 409 {
				cIs(t, "error "+cReason(reply), cEarlierConn.MatchString(cReason(reply)), true)
			}
		})
	}
}

func TestOrchCRepoTakenOffTheListWhileTheSearchRunsGetsNoRecord(t *testing.T) {
	t.Parallel()

	tools := cRequested(func(pr *reviews.PR) { pr.Number, pr.Repo = 3, cJaneTools })
	tab, fakes := setup(t, setupOpts{
		open: []*reviews.PR{cRequested(nil), tools},
		settings: cSettings(settingsOpts{
			allow: []string{"acme/*", "jane/*"}, slack: new(false),
		}),
	})
	searches := cGated(t)

	fakes.with(func(state *world) { state.listGate = searches })
	tab.Tick()
	searches.reached(t)
	fakes.setSettings(settingsOf(settingsOpts{allow: []string{"acme/*"}, slack: new(false)}))
	searches.open()
	tab.Settle()
	cSame(t, "records", reviews.LoadFile(fakes.file).PRs.Keys(), []string{keyOf(7)})
	cSame(t, "rows", cRowKeys(t, tab), []string{keyOf(7)})
}

func TestOrchCPartlySignedOutChannelRunLeavesEveryCursor(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{settings: cSettings(settingsOpts{
		channels: []string{"#code-review", "#ship-it"}, dms: new(false),
	})})
	fakes.with(func(state *world) {
		state.scanResult = func() (reviews.ScanResult, error) {
			result, err := cPosts(cPost(7))()
			result.NeedsAuth = []string{cConnector}

			return result, err
		}
	})
	tickAndSettle(tab)
	cIs(t, "scans", cScans(fakes), 1)
	// Only the post it did read moves its channel's cursor; nothing moves to the run's start.
	cIs(t, "cursors", cJSON(reviews.LoadFile(fakes.file).Cursors), `{"#code-review":"`+cTS+`"}`)

	fakes.advance(10 * minuteMs)
	fakes.with(func(state *world) { state.scanResult = cPosts(cPost(7)) })
	cRefresh(t, tab)
	tickAndSettle(tab)
	cIs(t, "scans", cScans(fakes), 2)
	cSame(t, "cursors", cKeysSorted(reviews.LoadFile(fakes.file).Cursors.Keys()),
		[]string{"#code-review", "#ship-it"})
}

// cLead is a find of PR 8 by a DM or by a post in #code-review.
func cLead(byDM bool) *reviews.Found {
	if byDM {
		return &reviews.Found{At: worldT0 - minuteMs, Slack: nil, DM: &reviews.FoundDM{
			FirstTS: cTS, Permalink: "https://acme.slack.com/archives/D000/p8", LastTS: cTS,
		}}
	}

	return &reviews.Found{At: worldT0 - minuteMs, DM: nil, Slack: &reviews.FoundSlack{
		TS: cTS, Permalink: "https://acme.slack.com/archives/C1/p8", Channel: "#code-review",
	}}
}

func TestOrchCLeadIsReadOnlyWhileTheSourceThatFoundItIsOn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		byDM   bool
		change settingsOpts
	}{
		{name: "dm", byDM: true, change: settingsOpts{dms: new(false)}},
		{name: "post", byDM: false, change: settingsOpts{channels: []string{"#ship-it"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tab, fakes := setup(t, setupOpts{
				open: []*reviews.PR{cRequested(nil), cPR(8, worldRepo)},
			})
			cEdit(t, fakes.file, func(file *reviews.File) {
				file.Found.Set(keyOf(8), cLead(tt.byDM))
			})
			fakes.setSettings(settingsOf(tt.change))
			fakes.with(func(state *world) {
				state.scanResult = cPosts()
				state.dmResult = cDMOnly()
			})
			cRefresh(t, tab)
			tickAndSettle(tab)
			cIs(t, "8 read", slices.Contains(cFetched(fakes), keyOf(8)), false)
			cIs(t, "the lead waits", reviews.LoadFile(fakes.file).Found.Has(keyOf(8)), true)
		})
	}
}

func TestOrchCScanLeadIsNotReadWhileTheRateLimitPauseIsOn(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{
		open:     []*reviews.PR{cRequested(nil), cPR(8, worldRepo)},
		settings: cSettings(settingsOpts{dms: new(false)}),
	})
	fakes.with(func(state *world) {
		state.scanResult = cScanFails(cScanErr("Slack scan failed: timed out"))
	})
	tickAndSettle(tab)
	tab.SetPausedUntil(worldT0 + hourMs)
	fakes.with(func(state *world) { state.scanResult = cPosts(cPost(8)) })
	fakes.advance(10 * minuteMs)
	cRefresh(t, tab)
	tickAndSettle(tab)
	cIs(t, "Slack scans go on while GitHub is paused", cScans(fakes), 2)
	cIs(t, "the lead waits for the reset", slices.Contains(cFetched(fakes), keyOf(8)), false)
	cIs(t, "the lead is kept", reviews.LoadFile(fakes.file).Found.Has(keyOf(8)), true)
}
