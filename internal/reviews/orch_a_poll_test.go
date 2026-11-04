package reviews_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// The view carries the chosen channels and the sources, in every shape (live, off, dry).
func TestViewCarriesChannelsAndSources(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{
		settings: aSettingsOf(settingsOpts{channels: []string{aCodeReview, aShipIt}}),
	})

	aEqual(t, "channel", view(t, tab).Channel, "#code-review, #ship-it")
	tickAndSettle(tab)
	aEqual(t, "sources", string(js.Marshal(view(t, tab).Sources)),
		`{"requested":true,"channels":["#code-review","#ship-it"],"dms":true,"allOpen":false}`)

	fakes.setSettings(settingsOf(settingsOpts{slack: new(false)}))

	off := view(t, tab)
	aEqual(t, "channel off", off.Channel, "")
	aEqual(t, "slack off", off.Slack, "unused")
	aEqual(t, "sources off", string(js.Marshal(off.Sources)),
		`{"requested":true,"channels":[],"dms":false,"allOpen":false}`)

	dry := aDryReviews(t, filepath.Join(t.TempDir(), "dry.json"), aFixedNow)
	aEqual(t, "dry channel", aDrySnapshot(t, dry).Channel, aCodeReview)
}

// A poll runs every reviewPollMs, and login is read once.
func TestPollRunsEveryPollMsAndLoginOnce(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})

	tickAndSettle(tab)
	fakes.advance(pollMs - 1)
	tickAndSettle(tab)
	aEqual(t, "polls before the interval", fakes.snapshotCalls().list, 1)

	fakes.advance(1)
	tickAndSettle(tab)
	aEqual(t, "polls after it", fakes.snapshotCalls().list, 2)
	aEqual(t, "logins", fakes.snapshotCalls().login, 1)
}

// Refresh polls now.
func TestRefreshPollsNow(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})

	tickAndSettle(tab)
	aRefresh(t, tab)
	tickAndSettle(tab)
	aEqual(t, "polls", fakes.snapshotCalls().list, 2)
}

// A tick that finds a poll still running starts no second one.
func TestTickWithPollRunningStartsNoSecond(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})
	held := newGate()

	fakes.with(func(fakes *world) { fakes.listGate = held })
	tab.Tick()
	held.reached(t)
	fakes.advance(pollMs * 3)
	tab.Tick()
	tab.Tick()
	aEqual(t, "polls", fakes.snapshotCalls().list, 1)

	held.open()
	tab.Settle()

	shown := view(t, tab)
	aEqual(t, "rows", len(shown.Rows), 1)

	if shown.LastPollAt == nil {
		t.Fatal("no poll finished")
	}

	aEqual(t, "last poll", *shown.LastPollAt, fakes.clock())
}

// A gh failure keeps the previous rows, sets the error, and never throws out of tick.
func TestGhFailureKeepsRowsAndSetsError(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})

	tickAndSettle(tab)
	aEqual(t, "rows", len(view(t, tab).Rows), 1)

	fakes.with(func(fakes *world) {
		fakes.listErr = errANotLoggedIn
		fakes.now += pollMs
	})
	aNoPanic(t, "tick", tab.Tick)
	tab.Settle()

	shown := view(t, tab)
	aEqual(t, "error", aText(shown.Error), "gh: not logged in")
	aEqual(t, "rows kept", len(shown.Rows), 1)
	aEqual(t, "enabled", shown.Enabled, true)

	// And recovers.
	fakes.with(func(fakes *world) {
		fakes.listErr = nil
		fakes.now += pollMs
	})
	tickAndSettle(tab)
	aEqual(t, "error after", aText(view(t, tab).Error), "<nil>")
}

// A gh that fails from the start gives an error and an empty list, and the tab is off with
// that reason.
func TestGhFailingFromStartTurnsTheTabOff(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})

	fakes.with(func(fakes *world) { fakes.listErr = errANoGh })
	tickAndSettle(tab)

	shown := view(t, tab)
	aEqual(t, "error", aText(shown.Error), "gh: command not found")
	aEqual(t, "enabled", shown.Enabled, false)

	if !strings.Contains(aText(shown.DisabledReason), "command not found") {
		t.Errorf("reason %q", aText(shown.DisabledReason))
	}

	aEqual(t, "rows", len(shown.Rows), 0)

	fakes.with(func(fakes *world) {
		fakes.listErr = nil
		fakes.now += pollMs
	})
	tickAndSettle(tab)
	aEqual(t, "enabled after", view(t, tab).Enabled, true)
	aEqual(t, "reason after", aText(view(t, tab).DisabledReason), "<nil>")
}

// aWideGh is a GitHub whose searches answer every open PR, whatever they were asked for.
type aWideGh struct {
	worldGh
}

func (g aWideGh) Search(_ context.Context, _ []reviews.Query) (
	[]reviews.SearchResult, *ghbudget.RateInfo, error,
) {
	g.w.mu.Lock()
	defer g.w.mu.Unlock()

	open := append([]*reviews.PR{}, g.w.open...)

	return []reviews.SearchResult{{PRs: open, Capped: false, Error: nil}}, nil, nil
}

// Every PR a search returns is checked against the list again.
func TestEverySearchedPRIsCheckedAgainstTheList(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true, open: []*reviews.PR{
		aRequested(7), aRequested(5, "acme-labs/x"),
	}})

	tab.SetDeps(func(deps *reviews.Deps) { deps.Gh = aWideGh{worldGh{w: fakes}} })
	tickAndSettle(tab)
	aEqual(t, "rows", aKeys(view(t, tab).Rows), []string{keyOf(7)})
}

// The saved file is plain JSON that loads back.
func TestSavedFileIsPlainJSON(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})

	tickAndSettle(tab)

	data, err := os.ReadFile(fakes.file)
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
	aEqual(t, "version", version, float64(2))
}
