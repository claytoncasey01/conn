package reviews_test

// The DM scan's cursor, REFRESH and the DM scan, and the dry view's DM row.

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/settings"
)

func TestOrchCOldDMCursorReadsBackOnlyTheFirstWindow(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})

	old := reviews.MsTS(worldT0 - 30*cDayMs)

	cEdit(t, fakes.file, func(file *reviews.File) { file.DMCursorTS = &old })
	fakes.with(func(state *world) { state.dmResult = cDMFails(errCTimedOut) })
	tickAndSettle(tab)
	cIs(t, "first since", cDMs(fakes)[0].since, reviews.MsTS(worldT0-cWindowMs))

	fakes.advance(cDMScanMs)
	tickAndSettle(tab)
	cIs(t, "second since", cDMs(fakes)[1].since, reviews.MsTS(fakes.clock()-cWindowMs))
	cIs(t, "a failure leaves the cursor", cText(reviews.LoadFile(fakes.file).DMCursorTS), old)

	// A cursor inside the window reads from the cursor less 15 minutes.
	fakes.with(func(state *world) { state.dmResult = cDMOnly() })
	fakes.advance(cDMScanMs)
	tickAndSettle(tab)
	cIs(t, "cursor", cText(reviews.LoadFile(fakes.file).DMCursorTS),
		reviews.MsTS(fakes.clock()-cOverlapMs))

	fakes.advance(cDMScanMs)
	tickAndSettle(tab)

	dms := cDMs(fakes)
	if len(dms) != 4 {
		t.Fatalf("%d DM scans", len(dms))
	}

	cIs(t, "since", dms[3].since, reviews.MsTS(fakes.clock()-cDMScanMs-2*cOverlapMs))
}

func TestOrchCDMCursorNeverMovesBack(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})

	ahead := reviews.MsTS(worldT0 + 60*minuteMs)

	cEdit(t, fakes.file, func(file *reviews.File) { file.DMCursorTS = &ahead })
	tickAndSettle(tab)

	dms := cDMs(fakes)
	if len(dms) != 1 {
		t.Fatalf("%d DM scans", len(dms))
	}

	cIs(t, "since", dms[0].since, reviews.MsTS(worldT0+45*minuteMs))
	cIs(t, "cursor", cText(reviews.LoadFile(fakes.file).DMCursorTS), ahead)
}

func TestOrchCRefreshMakesTheDMScanDueNow(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	tickAndSettle(tab)
	cIs(t, "DM scans", len(cDMs(fakes)), 1)

	fakes.advance(minuteMs)
	tickAndSettle(tab)
	cIs(t, "DM scans inside the wait", len(cDMs(fakes)), 1)

	cRefresh(t, tab)
	tickAndSettle(tab)
	cIs(t, "DM scans after REFRESH", len(cDMs(fakes)), 2)
}

// cHeldDMs makes every DM scan wait at held, then find nothing.
func cHeldDMs(fakes *world, held *gate) {
	fakes.with(func(state *world) {
		state.dmResult = func() ([]reviews.DMFind, error) {
			held.pass()

			return []reviews.DMFind{}, nil
		}
	})
}

// cPolledAt is whether the last poll finished at now.
func cPolledAt(t *testing.T, tab *reviews.Reviews, now float64) func() bool {
	t.Helper()

	return func() bool {
		at := view(t, tab).LastPollAt
		if at == nil {
			return false
		}

		return *at == now
	}
}

func TestOrchCRefreshWaitsForTheDMScanItStarted(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	tickAndSettle(tab)

	held := cGated(t)
	cHeldDMs(fakes, held)
	cRefresh(t, tab)
	// A millisecond on, so the poll's end shows in lastPollAt.
	fakes.advance(1)
	tab.Tick()
	held.reached(t)
	cWaitFor(t, "the poll", cPolledAt(t, tab, worldT0+1))
	cIs(t, "the DM scan started in the same tick as the poll", len(cDMs(fakes)), 2)
	cPressIs(t, tab, 0, "the poll is over, the DM scan is not")

	bumps := cBumps(fakes)

	held.open()
	tab.Settle()
	cPressIs(t, tab, 1, "the DM scan is over")
	cIs(t, "a snapshot was sent", cBumps(fakes) > bumps, true)
}

func TestOrchCRefreshOfTheFirstPollWaitsForTheDMScanThatFollows(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	held := cGated(t)

	cHeldDMs(fakes, held)
	cRefresh(t, tab)
	// The poll starts: no PR is known yet, so no DM scan starts in this tick.
	tab.Tick()
	tab.Settle()
	cIs(t, "DM scans", len(cDMs(fakes)), 0)
	cPressIs(t, tab, 0, "the DM scan the press asked for is about to start")

	tab.Tick()
	held.reached(t)
	cIs(t, "DM scans", len(cDMs(fakes)), 1)
	cPressIs(t, tab, 0, "the DM scan runs")

	held.open()
	tab.Settle()
	cPressIs(t, tab, 1, "the DM scan is over")
}

func TestOrchCRefreshDoesNotWaitForADMScanThatCannotStart(t *testing.T) {
	t.Parallel()

	dmsOff := settingsOf(settingsOpts{dms: new(false)})

	tests := []struct {
		name string
		opts setupOpts
	}{
		{name: "no dashboard", opts: setupOpts{noClient: true}},
		{name: "switched off", opts: setupOpts{settings: &dmsOff}},
		{name: "no open PR", opts: setupOpts{open: []*reviews.PR{}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tab, fakes := setup(t, tt.opts)
			tickAndSettle(tab)
			cRefresh(t, tab)
			tickAndSettle(tab)
			cPressIs(t, tab, 1, tt.name)

			if tt.opts.noClient {
				cIs(t, "DM scans", len(cDMs(fakes)), 0)
			}
		})
	}
}

func TestOrchCRefreshWhoseDMScanCanNoLongerStartIsLetGoNextTick(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	cRefresh(t, tab)
	// The poll starts: no PR is known yet.
	tab.Tick()
	tab.Settle()
	cPressIs(t, tab, 0, "the DM scan is ready to start on the next tick")

	// The dashboard closes between the poll and that tick.
	fakes.with(func(state *world) { state.clients = false })

	bumps := cBumps(fakes)

	tab.Tick()
	cPressIs(t, tab, 1, "let go")
	cIs(t, "a snapshot was sent", cBumps(fakes) > bumps, true)

	tab.Settle()
	cIs(t, "DM scans", len(cDMs(fakes)), 0)
}

func TestOrchCFailingDMScanStillEndsTheRefresh(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	tickAndSettle(tab)
	fakes.with(func(state *world) { state.dmResult = cDMFails(errCBoom) })
	cRefresh(t, tab)
	tickAndSettle(tab)
	cIs(t, "dmError", cText(view(t, tab).DMError), "boom")
	cPressIs(t, tab, 1, "after a failed DM scan")
}

// cDryView is what the dry view's snapshot says, as the page reads it.
type cDryView struct {
	DMError *string       `json:"dmError"`
	Rows    []reviews.Row `json:"rows"`
}

func TestOrchCDryViewCarriesADMOnlyRowAndNoDMError(t *testing.T) {
	t.Parallel()

	cfg := config.FromEnv(func(string) (string, bool) { return "", false })
	tab := reviews.New(t.Context(), reviews.Deps{
		Settings: settings.NewStore(nil), Now: func() float64 { return worldT0 },
		Config: reviews.Config{Dry: true, NamePattern: cfg.ReviewNamePattern},
	})

	var shown cDryView

	cDecode(t, js.Marshal(tab.Snapshot()), &shown)
	cIs(t, "dmError", cText(shown.DMError), "<null>")

	var dms []reviews.Row

	for _, row := range shown.Rows {
		if row.DMURL != nil {
			dms = append(dms, row)
		}
	}

	if len(dms) != 1 {
		t.Fatalf("%d DM rows", len(dms))
	}

	cIs(t, "slackUrl", cText(dms[0].SlackURL), "<null>")
	cIs(t, "inChannel", dms[0].InChannel, true)
}
