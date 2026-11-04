package reviews_test

// Several repos: discovery, clones, the old records (Bun's "Several repos" section).

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

func TestOrchCChannelScanLooksForNewPostsAtMostEveryInterval(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{
		discovery: true, settings: cSettings(settingsOpts{dms: new(false)}),
		channelScanMs: 10 * minuteMs,
	})

	// 7 requests me and is unmatched only for its first scan; after the back-off is spent the
	// scan is for new posts alone.
	tickAndSettle(tab)

	first := cScans(fakes)
	cIs(t, "a first scan", first >= 1, true)

	fakes.advance(5 * minuteMs)
	tickAndSettle(tab)
	cIs(t, "not before the interval: scans", cScans(fakes), first)

	cRefresh(t, tab)
	tickAndSettle(tab)
	cIs(t, "a REFRESH looks now: scans", cScans(fakes), first+1)
}

func TestOrchCDMAboutAPRNotKnownYetIsALead(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{open: []*reviews.PR{cRequested(nil), cPR(8, worldRepo)}})
	fakes.with(func(state *world) {
		state.dmResult = cDMOnly(
			cDM(8, cTS, "1799999500.000100"), cDM(9, cTS, cTS, cGadgets),
		)
	})
	tickAndSettle(tab)

	cIs(t, "reads", cJSON(fakes.snapshotCalls().fetch),
		cJSON([][]string{{keyOf(8), keyOf(9, cGadgets)}}))

	shown := view(t, tab).Rows

	rows := make([]string, 0, len(shown))
	for _, row := range shown {
		rows = append(rows, row.Key+","+strconv.FormatBool(row.DMURL != nil))
	}

	cSame(t, "rows", cKeysSorted(rows), []string{keyOf(7) + ",false", keyOf(8) + ",true"})

	entry := cEntry(fakes.file, keyOf(8))
	if entry == nil {
		t.Fatal("no record of 8")
	}

	if entry.DM == nil {
		t.Fatal("no DM on 8")
	}

	cIs(t, "dm ts", entry.DM.TS, cTS)
	cIs(t, "dmLastAt", cNum(entry.DMLastAt), "1799999500000")
	cSame(t, "allow", cDMs(fakes)[0].allow, []string{"acme/*"})
}

func TestOrchCRepoWithNoLocalCloneRefusesReview(t *testing.T) {
	t.Parallel()

	tab, fakes := cReady(t, setupOpts{})
	fakes.with(func(state *world) {
		state.clone = func(repo string) string {
			if repo == worldRepo {
				return ""
			}

			return "/x"
		}
		// Past the clone lookup's short memory.
		state.now += 20_000
	})

	cIs(t, "noClone", cText(view(t, tab).Rows[0].NoClone),
		"No local clone of acme/widgets: set a folder in Settings")

	reply := cStart(t, tab, worldRepo, "7")
	cIs(t, "code", reply.Code, 409)
	cIs(t, "error", strings.Contains(cReason(reply), "No local clone of acme/widgets"), true)
	cIs(t, "launches", len(cLaunches(fakes)), 0)

	// A clone found later is picked up without a restart.
	fakes.with(func(state *world) {
		state.clone = func(string) string { return cClone }
		state.now += 20_000
	})
	cIs(t, "noClone", cText(view(t, tab).Rows[0].NoClone), "<null>")
}

func TestOrchCSamePRNumberInTwoReposIsTwoRowsRecordsAndLaunches(t *testing.T) {
	t.Parallel()

	gadget := cRequested(func(pr *reviews.PR) {
		pr.Repo, pr.URL = cGadgets, "https://github.com/acme/gadgets/pull/7"
	})
	tab, fakes := cReady(t, setupOpts{open: []*reviews.PR{cRequested(nil), gadget}})

	cSame(t, "rows", cKeysSorted(cRowKeys(t, tab)), []string{keyOf(7, cGadgets), keyOf(7)})
	cIs(t, "widgets", cStart(t, tab, worldRepo, "7").Code, 200)
	cIs(t, "gadgets", cStart(t, tab, cGadgets, "7").Code, 200)

	launched := cLaunches(fakes)

	launches := make([]string, 0, len(launched))
	for _, launch := range launched {
		launches = append(launches, launch.Repo+" "+launch.Name+" "+launch.RepoPath)
	}

	cSame(t, "launches", launches, []string{
		"acme/widgets pr-7-acme--widgets /nowhere/clones/widgets",
		"acme/gadgets pr-7-acme--gadgets /nowhere/clones/gadgets",
	})

	for _, repo := range []string{worldRepo, cGadgets} {
		entry := cEntry(fakes.file, keyOf(7, repo))
		if entry == nil {
			t.Fatalf("no record of %s", repo)
		}

		cIs(t, repo+" worktree", entry.Launches[0].Worktree, cWt(fakes, 7, repo))
	}

	// A repo outside the list cannot be started, whatever its number.
	cIs(t, "other", cStart(t, tab, "other/thing", "7").Code, 404)
}

// cLaunches are the launches so far.
func cLaunches(fakes *world) []reviews.LaunchOpts {
	fakes.mu.Lock()
	defer fakes.mu.Unlock()

	return append([]reviews.LaunchOpts{}, fakes.launches...)
}

const cLegacyV1 = `{"version":1,"cursorTs":null,"prs":{"41":{"slack":null,"slackAskAt":null,` +
	`"firstSeenAt":%FIRST%,"launches":[{"at":%AT%,"model":"sonnet","name":"pr-41",` +
	`"headSha":"a","worktree":"/gone/reviews/pr-41/mystery","sessionId":null,` +
	`"closedAt":%CLOSED%}],"scan":{"tries":0,"lastAt":0},"threadScan":{"tries":0,` +
	`"lastAt":0,"headSha":""}}}}`

// cLegacyFile is a version 1 file holding PR 41, first seen at first, launched at launched and
// closed at closed, its worktree under a folder named mystery.
func cLegacyFile(first, launched, closed float64) string {
	return strings.NewReplacer(
		"%FIRST%", strconv.FormatFloat(first, 'f', -1, 64),
		"%AT%", strconv.FormatFloat(launched, 'f', -1, 64),
		"%CLOSED%", strconv.FormatFloat(closed, 'f', -1, 64),
	).Replace(cLegacyV1)
}

func TestOrchCOldRecordsTheMigrationCouldNotPlaceAreListedAndPlaced(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{settings: cSetupSettings()})

	cWriteV1(t, fakes.file, cLegacyFile(worldT0-1000, worldT0-500, worldT0-400))
	cChanged(tab, fakes, settingsOpts{})
	tickAndSettle(tab)

	shown := view(t, tab)
	cIs(t, "legacy", cJSON(shown.Legacy),
		`[{"number":41,"folder":"mystery","at":`+cJSON(worldT0-500)+`}]`)
	cIs(t, "41 is no row", slices.Contains(cRowKeys(t, tab), keyOf(41)), false)

	tests := []struct {
		number, repo any
		want         int
	}{
		{number: 41.0, repo: "other/thing", want: 400},
		{number: 41.0, repo: "../x", want: 400},
		{number: "4x", repo: worldRepo, want: 400},
		{number: 42.0, repo: worldRepo, want: 409},
	}

	for _, tt := range tests {
		cIs(t, cJSON(tt.number)+" under "+cJSON(tt.repo), cPlace(t, tab, tt.number, tt.repo).Code,
			tt.want)
	}

	cIs(t, "placed", cReply(cPlace(t, tab, "all", "acme/mystery")), `200 {"ok":true,"placed":1}`)
	cIs(t, "nothing left to place", cPlace(t, tab, "all", "acme/mystery").Code, 409)
	cIs(t, "legacy", cJSON(view(t, tab).Legacy), "[]")
	cIs(t, "in the file", cHas(fakes.file, keyOf(41, "acme/mystery")), true)
}

func TestOrchCPlacedOldRecordIsNotForgottenByTheNextPoll(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{settings: cSetupSettings()})

	cWriteV1(t, fakes.file, cLegacyFile(worldT0-40*cDayMs, worldT0-40*cDayMs, worldT0-39*cDayMs))
	cChanged(tab, fakes, settingsOpts{})
	tickAndSettle(tab)

	// GitHub says it merged 30 days ago, and no folder of it is there.
	placed := keyOf(41, "acme/mystery")

	fakes.with(func(state *world) {
		state.exists = func(string) bool { return false }
		state.known[placed] = cPR(41, "acme/mystery")
		state.endedAs = new(reviews.EndedMerged)
		state.endedAt = worldT0 - 30*cDayMs
	})
	cIs(t, "place", cPlace(t, tab, 41.0, "acme/mystery").Code, 200)

	fakes.advance(2 * minuteMs)
	tickAndSettle(tab)
	cIs(t, "the placed record was read by number", slices.Contains(cFetched(fakes), placed), true)
	cIs(t, "still there after the poll that read it as ended", cHas(fakes.file, placed), true)

	// A restart reads it again from the file, and it is still there: the stamp is kept.
	entry := cEntry(fakes.file, placed)
	if entry == nil {
		t.Fatal("no record")
	}

	cIs(t, "placedAt", entry.PlacedAt != nil, true)

	// 14 days after the placing, with nothing since, it goes like any old record.
	fakes.advance(15 * cDayMs)
	cRefresh(t, tab)
	tickAndSettle(tab)
	cIs(t, "gone", cHas(fakes.file, placed), false)
}

func TestOrchCBackupNameTakenDoesNotStopTheMigration(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{settings: cSetupSettings()})

	old := `{"version":1,"cursorTs":null,"prs":{}}`
	cWriteV1(t, fakes.file, old)

	// The backup's place is taken by a folder: it is left as it is.
	err := os.Mkdir(fakes.file+".v1.bak", 0o700)
	if err != nil {
		t.Fatal(err)
	}

	cChanged(tab, fakes, settingsOpts{})
	tickAndSettle(tab)
	cSame(t, "rows", cRowKeys(t, tab), []string{keyOf(7)})
	cIs(t, "storeError", cText(view(t, tab).StoreError), "<null>")

	copied, err := os.ReadFile(fakes.file + ".v1.2.bak")
	if err != nil {
		t.Fatal(err)
	}

	cIs(t, "the copy", string(copied), old)
	cIs(t, "version", reviews.LoadFile(fakes.file).Version, 2)
}
