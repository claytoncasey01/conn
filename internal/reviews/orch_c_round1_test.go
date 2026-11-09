package reviews_test

// Fix round 1: failures that stay local, cost, and settings that change under a running poll or
// scan, to the rate limit's pause.

import (
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/reviews"
)

const cSAML = "Resource protected by organization SAML enforcement"

// cFailsFor is a search error for every query that ends with suffix.
func cFailsFor(suffix, why string) func(query string) string {
	return func(query string) string {
		if strings.HasSuffix(query, suffix) {
			return why
		}

		return ""
	}
}

func TestOrchCPatternGitHubCannotSearchLeavesItsReposStale(t *testing.T) {
	t.Parallel()

	tools := cRequested(func(pr *reviews.PR) { pr.Number, pr.Repo = 3, cJaneTools })
	tab, fakes := setup(t, setupOpts{
		open:     []*reviews.PR{cRequested(nil), tools},
		settings: cSettings(settingsOpts{allow: []string{"acme/*", cJaneTools}}), noClient: true,
	})
	tickAndSettle(tab)
	cSame(t, "rows", cKeysSorted(cRowKeys(t, tab)), []string{keyOf(7), keyOf(3, cJaneTools)})

	// jane/tools cannot be searched now (an org's SAML rule, say); acme/widgets#7 changes title.
	fakes.with(func(state *world) {
		state.searchError = cFailsFor("repo:jane/tools", cSAML)
		retitled := cRequested(func(pr *reviews.PR) { pr.Title = "New title" })
		state.open = []*reviews.PR{retitled, tools}
	})

	reads := len(cFetched(fakes))

	fakes.advance(pollMs)
	tickAndSettle(tab)

	shown := view(t, tab)
	cIs(t, "error", cText(shown.Error), "<null>")
	cIs(t, "failed", cJSON(shown.Failed), `[{"pattern":"jane/tools","error":"`+cSAML+`"}]`)

	fresh, stale := cRow(t, tab, keyOf(7)), cRow(t, tab, keyOf(3, cJaneTools))
	if fresh == nil {
		t.Fatal("no row 7")
	}

	if stale == nil {
		t.Fatal("no row 3")
	}

	cIs(t, "title", fresh.Title, "New title")
	cIs(t, "stale", stale.Stale, true)
	cIs(t, "7 is not stale", fresh.Stale, false)
	cIs(t, "a stale repo is not read by number",
		slices.Contains(cFetched(fakes)[reads:], keyOf(3, cJaneTools)), false)
	cIs(t, "REVIEW of a stale row", cStart(t, tab, cJaneTools, "3").Code, 409)
	cIs(t, "REVIEW of a fresh row", cStart(t, tab, worldRepo, "7").Code, 200)

	// Weeks of failing: the stale record is never pruned.
	for range 3 {
		fakes.advance(7 * cDayMs)
		tickAndSettle(tab)
	}

	cIs(t, "the stale record is kept", cHas(fakes.file, keyOf(3, cJaneTools)), true)

	// Every search failing is an error of the poll, and the rows stay.
	fakes.with(func(state *world) { state.searchError = func(string) string { return "nope" } })
	fakes.advance(pollMs)
	tickAndSettle(tab)
	cIs(t, "error", cText(view(t, tab).Error), "nope")
	cIs(t, "rows", len(view(t, tab).Rows), 2)
}

// cRateLimitedSearch is a search that counts itself and finds the rate limit used up.
func cRateLimitedSearch(fakes *world, resetAt float64) cGh {
	return cGh{
		w: fakes, fetch: nil,
		search: func([]reviews.Query) ([]reviews.SearchResult, *ghbudget.RateInfo, error) {
			fakes.with(func(state *world) { state.calls.list++ })

			return nil, nil, cRateLimited(resetAt)
		},
	}
}

func TestOrchCRateLimitUsedUpPausesGitHubUntilItResets(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})
	tickAndSettle(tab)
	cIs(t, "rows", len(view(t, tab).Rows), 1)

	cSwapGh(tab, cRateLimitedSearch(fakes, cRateUntil))
	fakes.advance(pollMs)
	tickAndSettle(tab)

	shown := view(t, tab)
	cIs(t, "pausedUntil", cNum(shown.PausedUntil), cJSON(cRateUntil))
	cIs(t, "error", cText(shown.Error), "<null>")
	cIs(t, "the list is kept", len(shown.Rows), 1)

	calls := fakes.snapshotCalls().list

	cRefresh(t, tab)
	cIs(t, "seq", view(t, tab).Refresh.Seq, 1)

	for range 5 {
		fakes.advance(pollMs)
		tickAndSettle(tab)
	}

	cIs(t, "nothing asked before the reset", fakes.snapshotCalls().list, calls)
	cPressIs(t, tab, 1, "a press has nothing to wait for")

	cSwapGh(tab, worldGh{w: fakes})
	fakes.with(func(state *world) { state.now = cRateUntil })
	tickAndSettle(tab)
	cIs(t, "polls after the reset", fakes.snapshotCalls().list, calls+1)
	cIs(t, "pausedUntil", cNum(view(t, tab).PausedUntil), "<null>")
}

// cGone makes nothing GitHub has a folder of on disk.
func cGone(fakes *world) {
	fakes.with(func(state *world) { state.exists = func(string) bool { return false } })
}

// cEndsAs makes a PR read by number after it left read as ended how, at.
func cEndsAs(fakes *world, how string, at float64) {
	fakes.with(func(state *world) {
		state.endedAs = &how
		state.endedAt = at
	})
}

// cKnown makes GitHub know PR number of acme/widgets (read by number once it has ended).
func cKnown(fakes *world, number int64) {
	fakes.with(func(state *world) { state.known[keyOf(number)] = cPR(number, worldRepo) })
}

func TestOrchCOnlyAReadThatAnsweredLetsARecordGo(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{open: []*reviews.PR{}})
	cGone(fakes)
	cOldRecord(t, fakes, 20, 15, nil)
	cOldRecord(t, fakes, 21, 15, nil)
	cKnown(fakes, 21)
	cEndsAs(fakes, reviews.EndedClosed, worldT0-15*cDayMs)
	fakes.with(func(state *world) { state.readFails[keyOf(20)] = true })
	tickAndSettle(tab)
	cIs(t, "not answered: not decided, not pruned", cHas(fakes.file, keyOf(20)), true)
	cIs(t, "answered closed, untouched for 14 days: pruned", cHas(fakes.file, keyOf(21)), false)
}

func TestOrchCRecordWhoseWorktreeIsOnDiskIsNotPruned(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{open: []*reviews.PR{}})
	cOldRecord(t, fakes, 22, 15, func(entry *reviews.Entry) {
		entry.Ended = &reviews.Ended{How: reviews.EndedMerged, At: worldT0 - 15*cDayMs}
	})
	fakes.with(func(state *world) { state.porcelain = " M dirty\n" })
	tickAndSettle(tab)
	cIs(t, "kept while its worktree is there", cHas(fakes.file, keyOf(22)), true)

	cGone(fakes)
	fakes.advance(pollMs)
	tickAndSettle(tab)
	cIs(t, "its folder gone: it can go", cHas(fakes.file, keyOf(22)), false)
}

func TestOrchCPollAcrossASettingsChangeReadsPrunesAndSweepsNothing(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{open: []*reviews.PR{}, noClient: true})
	cGone(fakes)
	cOldRecord(t, fakes, 23, 15, nil)
	cKnown(fakes, 23)
	cEndsAs(fakes, reviews.EndedClosed, worldT0-15*cDayMs)

	searches := cGated(t)

	fakes.with(func(state *world) { state.listGate = searches })
	tab.Tick()
	searches.reached(t)
	// The repo leaves the list while the search is out.
	fakes.setSettings(settingsOf(settingsOpts{allow: []string{"jane/*"}}))
	searches.open()
	tab.Settle()
	cIs(t, "nothing of the removed repo is read", len(fakes.snapshotCalls().fetch), 0)
	cIs(t, "kept", cHas(fakes.file, keyOf(23)), true)
	cIs(t, "git calls", len(cGitCalls(fakes)), 0)

	// A change while the reads are out: the answer is kept, but nothing is pruned or swept with
	// the old list.
	fakes.setSettings(settingsOf(settingsOpts{}))

	reads := cGated(t)

	fakes.with(func(state *world) { state.endedGate = reads })
	cRefresh(t, tab)
	tab.Tick()
	reads.reached(t)
	cIs(t, "reads", cJSON(fakes.snapshotCalls().fetch), cJSON([][]string{{keyOf(23)}}))
	fakes.setSettings(settingsOf(settingsOpts{allow: []string{"acme/*", "jane/*"}}))
	reads.open()
	tab.Settle()
	cIs(t, "read as closed long ago, but the list changed meanwhile: not pruned",
		cHas(fakes.file, keyOf(23)), true)
}

// cEndedLaunch is reviews.json holding PR 9's closed launch, the PR merged a second ago.
func cEndedLaunch(t *testing.T, fakes *world) {
	t.Helper()

	cSeededLaunch(t, fakes, cWt(fakes, 9), worldRepo, 9)
	cEdit(t, fakes.file, func(file *reviews.File) {
		entry, _ := file.PRs.Get(keyOf(9))
		entry.Ended = &reviews.Ended{How: reviews.EndedMerged, At: worldT0 - 1000}
	})
}

func TestOrchCStaleReposEndedPRKeepsItsWorktree(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{
		open: []*reviews.PR{}, settings: cSettings(settingsOpts{allow: []string{"acme/*"}}),
	})
	cEndedLaunch(t, fakes)
	// acme/* cannot be searched; a second pattern can (with every search failing, the poll
	// fails and sweeps nothing anyway).
	fakes.setSettings(settingsOf(settingsOpts{allow: []string{"acme/*", cJaneTools}}))
	fakes.with(func(state *world) { state.searchError = cFailsFor("user:acme", "cannot search") })
	tickAndSettle(tab)

	failed := view(t, tab).Failed

	patterns := make([]string, 0, len(failed))
	for _, pattern := range failed {
		patterns = append(patterns, pattern.Pattern)
	}

	cSame(t, "failed", patterns, []string{"acme/*"})
	cIs(t, "git calls", len(cGitCalls(fakes)), 0)
}

func TestOrchCEveryPRASearchReturnsIsCheckedAgainstTheList(t *testing.T) {
	t.Parallel()

	labs := cRequested(func(pr *reviews.PR) { pr.Number, pr.Repo = 5, "acme-labs/x" })
	tab, fakes := setup(t, setupOpts{open: []*reviews.PR{cRequested(nil), labs}, noClient: true})
	cSwapGh(tab, cGh{
		w: fakes, fetch: nil,
		search: func([]reviews.Query) ([]reviews.SearchResult, *ghbudget.RateInfo, error) {
			var open []*reviews.PR

			fakes.with(func(state *world) { open = append(open, state.open...) })

			return []reviews.SearchResult{{PRs: open, Capped: false, Error: nil}}, nil, nil
		},
	})
	tickAndSettle(tab)
	cSame(t, "records", reviews.LoadFile(fakes.file).PRs.Keys(), []string{keyOf(7)})
}

// cResults are searches that answer nothing, each capped or not.
func cResults(capped ...bool) func([]reviews.Query) (
	[]reviews.SearchResult, *ghbudget.RateInfo, error,
) {
	return func([]reviews.Query) ([]reviews.SearchResult, *ghbudget.RateInfo, error) {
		results := make([]reviews.SearchResult, 0, len(capped))
		for _, cut := range capped {
			results = append(results, reviews.SearchResult{
				PRs: []*reviews.PR{}, Capped: cut, Error: nil,
			})
		}

		return results, nil, nil
	}
}

func TestOrchCSettingsDuringSetupMigrateNothingAndCappedIsTheOpenSearch(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{settings: cSetupSettings(), noClient: true})
	cWriteV1(t, fakes.file, `{"version":1,"cursorTs":null,"prs":{}}`)
	cChanged(tab, fakes, settingsOpts{setup: true, slack: new(false)})
	cIs(t, "version", reviews.ReadFile(fakes.file).Kind, reviews.ReadV1)

	allow := []string{"acme/*", worldRepo, cJaneTools}

	fakes.with(func(state *world) { state.openCapped = true })
	cChanged(tab, fakes, settingsOpts{allow: allow})
	tickAndSettle(tab)
	cSame(t, "searches", fakes.snapshotCalls().search[0], []string{
		"is:pr is:open archived:false user-review-requested:@me user:acme",
		"is:pr is:open archived:false user-review-requested:@me repo:jane/tools",
	})

	// A capped "Requests me" search is no notice: that notice is about the every-open list.
	fakes.setSettings(settingsOf(settingsOpts{allow: allow, allOpen: true}))
	cSwapGh(tab, cGh{
		w: fakes, fetch: nil,
		search: cResults(true, true, false, true),
	})
	cRefresh(t, tab)
	tickAndSettle(tab)
	cSame(t, "capped", view(t, tab).Capped, []string{cJaneTools})
}

// cPostOf is a post kept on a record, from before channels were chosen.
func cPostOf() *reviews.SlackPost {
	return &reviews.SlackPost{
		TS: "1700000000.000100", Permalink: "https://acme.slack.com/archives/C1/p1",
		PostedAt: worldT0 - 20*cDayMs, Channel: nil,
	}
}

func TestOrchCRecordNothingOnCouldListIsNotReadOrPruned(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{
		open: []*reviews.PR{}, settings: cSettings(settingsOpts{slack: new(false)}), noClient: true,
	})
	cOldRecord(t, fakes, 24, 20, func(entry *reviews.Entry) {
		entry.Requested, entry.Launches, entry.Slack = false, []*reviews.Launch{}, cPostOf()
	})
	tickAndSettle(tab)
	cIs(t, "reads", len(fakes.snapshotCalls().fetch), 0)
	cIs(t, "kept", cHas(fakes.file, keyOf(24)), true)
}

func TestOrchCLeadIsReadWithTheSettingsOfTheMoment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change settingsOpts
		scanOn bool
	}{
		{name: "GitHub off", change: settingsOpts{github: new(false), dms: new(false)}},
		{name: "repo off the list", change: settingsOpts{
			allow: []string{"jane/*"}, dms: new(false),
		}, scanOn: true},
		{name: "Slack off", change: settingsOpts{slack: new(false), dms: new(false)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tab, fakes := setup(t, setupOpts{
				open:     []*reviews.PR{cRequested(nil), cPR(8, worldRepo)},
				settings: cSettings(settingsOpts{dms: new(false)}),
			})
			held := cGated(t)

			fakes.with(func(state *world) {
				state.scanResult = func() (reviews.ScanResult, error) {
					held.pass()

					return cPosts(cPost(8))()
				}
			})
			tab.Tick()
			tab.Settle()
			tab.Tick()
			held.reached(t)

			reads := len(fakes.snapshotCalls().fetch)

			fakes.setSettings(settingsOf(tt.change))
			held.open()
			tab.Settle()
			cIs(t, "reads", len(fakes.snapshotCalls().fetch), reads)
			// A repo off the list: the lead waits, for when it is allowed again; GitHub or Slack
			// off: the scan's result is dropped whole.
			cIs(t, "the lead waits", reviews.LoadFile(fakes.file).Found.Has(keyOf(8)), tt.scanOn)
		})
	}
}

func TestOrchCDraftOrMyOwnPRAPostNamesIsDropped(t *testing.T) {
	t.Parallel()

	mine := prOf(func(pr *reviews.PR) { pr.Number, pr.Author = 9, worldMe })
	draft := prOf(func(pr *reviews.PR) { pr.Number, pr.IsDraft = 10, true })
	tab, fakes := setup(t, setupOpts{open: []*reviews.PR{cRequested(nil), mine, draft}})
	fakes.with(func(state *world) { state.scanResult = cPosts(cPost(9), cPost(10)) })
	tickAndSettle(tab)

	file := reviews.LoadFile(fakes.file)
	cSame(t, "records", file.PRs.Keys(), []string{keyOf(7)})
	cIs(t, "found", file.Found.Len(), 0)
}

func TestOrchCAtMostFoundMaxLeadsWait(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{settings: cSettings(settingsOpts{dms: new(false)})})

	posts := make([]reviews.Post, 0, reviews.MaxFound+10)
	for index := range reviews.MaxFound + 10 {
		posts = append(posts, cPost(int64(1000+index)))
	}

	fakes.with(func(state *world) {
		state.endedFailures = 1_000_000
		state.scanResult = cPosts(posts...)
	})
	tickAndSettle(tab)

	held := 0

	tab.WithFile(func(file *reviews.File) { held = file.Found.Len() })
	cIs(t, "leads held", held, reviews.MaxFound)
	cIs(t, "leads saved", reviews.LoadFile(fakes.file).Found.Len(), reviews.MaxFound)
}
