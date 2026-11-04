package reviews_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// aCallCounts are how many logins, searches, reads, scans and DM scans there were.
func aCallCounts(fakes *world) []int {
	got := fakes.snapshotCalls()

	return []int{got.login, got.list, len(got.fetch), len(got.scan), len(got.dm)}
}

// aFileVersion is the version reviews.json has on disk.
func aFileVersion(t *testing.T, path string) any {
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

// In setup nothing is called at all: no gh, no scan, no DM scan, the file is not read, and
// the tab says why.
func TestInSetupNothingIsCalled(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{settings: aSettingsOf(settingsOpts{setup: true})})

	err := os.WriteFile(fakes.file, []byte(`{"version":1,"cursorTs":null,"prs":{}}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	tickAndSettle(tab)
	fakes.advance(pollMs * 5)
	tickAndSettle(tab)

	shown := view(t, tab)
	aEqual(t, "enabled", shown.Enabled, false)

	if !strings.Contains(aText(shown.DisabledReason), "Choose them in Settings") {
		t.Errorf("reason %q", aText(shown.DisabledReason))
	}

	aEqual(t, "rows", len(shown.Rows), 0)
	aEqual(t, "calls", aCallCounts(fakes), []int{0, 0, 0, 0, 0})
	aEqual(t, "an old file is not migrated before there is an allowlist",
		aFileVersion(t, fakes.file), float64(1))
	aEqual(t, "start", aStart(t, tab, worldRepo, "7", aSonnet).Code, 409)

	// The end of setup: the old file is migrated at once, and the next tick polls.
	fakes.setSettings(settingsOf(settingsOpts{}))
	tab.SettingsChanged()
	aEqual(t, "version after", aFileVersion(t, fakes.file), float64(2))

	_, err = os.Stat(reviews.V1Backup(fakes.file))
	if err != nil {
		t.Errorf("no backup: %v", err)
	}

	tickAndSettle(tab)
	aEqual(t, "polls", fakes.snapshotCalls().list, 1)
	aEqual(t, "rows after", aNumbers(view(t, tab).Rows), []int64{7})
}

// GitHub off: no gh call and no scan of any kind, the tab says so, and turning it on polls
// at once without a restart.
func TestGitHubOffCallsNothing(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{settings: aSettingsOf(settingsOpts{github: new(false)})})

	tickAndSettle(tab)

	shown := view(t, tab)
	aEqual(t, "enabled", shown.Enabled, false)

	if !strings.Contains(aText(shown.DisabledReason), "GitHub is off") {
		t.Errorf("reason %q", aText(shown.DisabledReason))
	}

	counts := aCallCounts(fakes)
	aEqual(t, "logins, searches, scans and DM scans",
		[]int{counts[0], counts[1], counts[3], counts[4]}, []int{0, 0, 0, 0})

	// A REFRESH has nothing to wait for.
	seq := aRefresh(t, tab)
	aEqual(t, "refresh done", view(t, tab).Refresh.Done, seq)

	fakes.setSettings(settingsOf(settingsOpts{}))
	tab.SettingsChanged()
	tickAndSettle(tab)
	aEqual(t, "polls", fakes.snapshotCalls().list, 1)
	aEqual(t, "enabled after", view(t, tab).Enabled, true)

	// And off again: the rows go at once, and nothing more is called.
	fakes.setSettings(settingsOf(settingsOpts{github: new(false)}))
	aEqual(t, "rows off", len(view(t, tab).Rows), 0)
	fakes.advance(pollMs * 3)
	tickAndSettle(tab)
	aEqual(t, "polls off", fakes.snapshotCalls().list, 1)
}

// Slack off: no channel scan and no DM scan, "Requests me" still lists; Slack on with no
// channel scans no channel; the DM switch alone runs only the DM scan.
func TestSlackSwitchesDecideTheScans(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings settingsOpts
		scans    int
		dms      int
	}{
		{name: "slack off", settings: settingsOpts{slack: new(false)}, scans: 0, dms: 0},
		{name: "no channel", settings: settingsOpts{channels: []string{}}, scans: 0, dms: 1},
		{name: "no DM", settings: settingsOpts{dms: new(false)}, scans: 1, dms: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tab, fakes := setup(t, setupOpts{settings: aSettingsOf(tt.settings)})

			tickAndSettle(tab)
			tickAndSettle(tab)
			aEqual(t, "scans", aScans(fakes), tt.scans)
			aEqual(t, "DM scans", len(fakes.snapshotCalls().dm), tt.dms)

			if tt.name == "slack off" {
				aEqual(t, "rows", aNumbers(view(t, tab).Rows), []int64{7})
				aEqual(t, "slack", view(t, tab).Slack, "unused")
			}
		})
	}
}

// One scan run reads every chosen channel, each from its own cursor, and the prompt is
// given only the allowed repos.
func TestOneScanReadsEveryChannelFromItsCursor(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{settings: aSettingsOf(settingsOpts{
		channels: []string{aCodeReview, aShipIt}, dms: new(false),
	})})

	shipped := aPost(7, "1799999100.000100")
	shipped.Channel = aShipIt

	fakes.with(func(fakes *world) { fakes.scanResult = aPosts(aPost(7, aTS), shipped) })
	tickAndSettle(tab)

	scans := fakes.snapshotCalls().scan
	aEqual(t, "scans", len(scans), 1)
	aEqual(t, "channels", scans[0].Channels, []reviews.ChannelRead{
		{Name: aCodeReview, SinceTS: nil}, {Name: aShipIt, SinceTS: nil},
	})
	aEqual(t, "allow", scans[0].Allow, []string{"acme/*"})
	// Each cursor is the later of its newest post and the run's start less the overlap.
	aEqual(t, "cursors", js.Stringify(reviews.LoadFile(fakes.file).Cursors),
		`{"#code-review":"`+reviews.MsTS(aMinutes(-15))+`","#ship-it":"1799999100.000100"}`)
	// The earliest post is the PR's link, and it remembers its channel.
	aEqual(t, "channel", aText(aEntry(t, fakes, keyOf(7)).Slack.Channel), aCodeReview)

	fakes.advance(10 * minuteMs)
	aRefresh(t, tab)
	tickAndSettle(tab)

	second := fakes.snapshotCalls().scan[1]
	aEqual(t, "code-review since", aSince(second, aCodeReview), reviews.MsTS(aMinutes(-15)))
	aEqual(t, "ship-it since", aSince(second, aShipIt), "1799999100.000100")
}

// A channel taken off the list no longer counts: its post stops listing the PR, and no
// thread of it is read.
func TestChannelOffTheListNoLongerCounts(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{open: []*reviews.PR{aNumbered(7)}, discovery: true})

	// No request: the post is what lists it.
	fakes.with(func(fakes *world) { fakes.scanResult = aPosts(aPost(7, aTS)) })
	tickAndSettle(tab)
	aEqual(t, "rows", aNumbers(view(t, tab).Rows), []int64{7})

	fakes.setSettings(settingsOf(settingsOpts{channels: []string{aShipIt}}))
	aEqual(t, "rows off", len(view(t, tab).Rows), 0)
	fakes.setSettings(settingsOf(settingsOpts{}))
	aEqual(t, "rows back", aNumbers(view(t, tab).Rows), []int64{7})
}

// A repo taken off the list: its rows go at once, its records are neither read nor pruned,
// and adding it back shows them again.
func TestRepoOffTheListIsNeitherReadNorPruned(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{
		open:     []*reviews.PR{aRequested(7), aRequested(3, aTools)},
		settings: aSettingsOf(settingsOpts{allow: []string{"acme/*", aTools}}),
	})

	tickAndSettle(tab)
	aEqual(t, "rows", aSortedKeys(view(t, tab).Rows), []string{keyOf(7), keyOf(3, aTools)})

	fakes.setSettings(settingsOf(settingsOpts{allow: []string{aTools}}))
	aEqual(t, "gone from the page before any poll", aKeys(view(t, tab).Rows),
		[]string{keyOf(3, aTools)})
	tab.SettingsChanged()

	before := fakes.snapshotCalls()
	fakes.with(func(fakes *world) { fakes.open = []*reviews.PR{aRequested(3, aTools)} })

	// Weeks pass: acme/widgets#7 is neither read nor forgotten.
	for range 3 {
		fakes.advance(7 * 86_400_000)
		tickAndSettle(tab)
	}

	after := fakes.snapshotCalls()

	for _, keys := range after.fetch[len(before.fetch):] {
		if slices.Contains(keys, keyOf(7)) {
			t.Error("a record of a repo that is not allowed is read")
		}
	}

	for _, queries := range after.search[len(before.search):] {
		for _, query := range queries {
			if strings.Contains(query, "acme") {
				t.Errorf("searched %q", query)
			}
		}
	}

	if !slices.Contains(reviews.LoadFile(fakes.file).PRs.Keys(), keyOf(7)) {
		t.Error("the record of 7 was pruned")
	}

	aRepoBack(t, tab, fakes)
}

// aRepoBack is the end of TestRepoOffTheListIsNeitherReadNorPruned: back on the list, the PR is
// read by number again (GitHub no longer asks me), and listed again.
func aRepoBack(t *testing.T, tab *reviews.Reviews, fakes *world) {
	t.Helper()

	fakes.with(func(fakes *world) {
		fakes.open = []*reviews.PR{aNumbered(7), aRequested(3, aTools)}
	})
	fakes.setSettings(settingsOf(settingsOpts{allow: []string{worldRepo, aTools}}))
	tab.SettingsChanged()
	tickAndSettle(tab)

	read := slices.ContainsFunc(fakes.snapshotCalls().fetch, func(keys []string) bool {
		return slices.Contains(keys, keyOf(7))
	})
	aEqual(t, "read by number", read, true)
	aEqual(t, "rows back", aSortedKeys(view(t, tab).Rows), []string{keyOf(7), keyOf(3, aTools)})
}
