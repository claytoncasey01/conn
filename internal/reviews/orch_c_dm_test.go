package reviews_test

// The DM scan: its own schedule and cursor, its own error, and a signed-out Slack shared with the
// channel scan (Bun's reviews.test.ts from "a refresh pressed while GitHub is off").

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

func TestOrchCRefreshWhileGitHubIsOffFinishesAtOnce(t *testing.T) {
	t.Parallel()

	off := settingsOf(settingsOpts{github: new(false)})
	tab, fakes := setup(t, setupOpts{settings: &off, noClient: true})

	tab.Tick()

	bumps := cBumps(fakes)

	cIs(t, "seq", cRefresh(t, tab), 1)
	cPressIs(t, tab, 1, "at once")
	cIs(t, "a snapshot was sent", cBumps(fakes) > bumps, true)

	tab.Settle()
	tab.Tick()

	cIs(t, "enabled", view(t, tab).Enabled, false)
	cPressIs(t, tab, 1, "after a tick")
}

func TestOrchCDMScanRunsOnceTheListIsKnownAndRecordsTheDM(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	fakes.with(func(state *world) {
		state.dmResult = cDMOnly(cDM(7, cTS, "1799999500.000200"))
	})

	tab.Tick()
	tab.Settle()
	cIs(t, "DM scans before the first poll has finished", len(cDMs(fakes)), 0)

	tickAndSettle(tab)

	dms := cDMs(fakes)
	if len(dms) != 1 {
		t.Fatalf("%d DM scans", len(dms))
	}

	cIs(t, "since", dms[0].since, reviews.MsTS(worldT0-cWindowMs))

	entry := cEntry(fakes.file, keyOf(7))
	if entry == nil {
		t.Fatal("no record of 7")
	}

	want := `{"ts":"1799999000.000100","permalink":"` + cDMLink(cTS) + `","postedAt":1799999000000}`
	cIs(t, "dm", cJSON(entry.DM), want)
	cIs(t, "dmLastAt", cNum(entry.DMLastAt), "1799999500000")
	cIs(t, "slack", cJSON(entry.Slack), "null")

	rows := view(t, tab).Rows
	if len(rows) != 1 {
		t.Fatalf("%d rows", len(rows))
	}

	got := []string{cJSON(rows[0].Number), cText(rows[0].DMURL), cText(rows[0].SlackURL)}
	cSame(t, "row", got, []string{"7", cDMLink(cTS), "<null>"})
}

func TestOrchCDMScanIsIndependentOfTheChannelScan(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	fakes.with(func(state *world) { state.scanResult = cPosts(cPost(7)) })
	tickAndSettle(tab)
	cIs(t, "scans", cScans(fakes), 1)

	fakes.advance(cDMScanMs)
	fakes.with(func(state *world) { state.dmResult = cDMOnly() })
	tickAndSettle(tab)

	cIs(t, "PR 7 has its post and no thread to read: no channel scan", cScans(fakes), 1)
	cIs(t, "DM scans", len(cDMs(fakes)), 2)
}

func TestOrchCDMScanRunsOnlyWhenItCan(t *testing.T) {
	t.Parallel()

	dmsOff := settingsOf(settingsOpts{dms: new(false)})
	githubOff := settingsOf(settingsOpts{github: new(false)})

	tests := []struct {
		name      string
		opts      setupOpts
		wantDMs   int
		wantScans int
	}{
		{name: "no dashboard open", opts: setupOpts{noClient: true}, wantDMs: 0, wantScans: -1},
		{name: "the DM switch off", opts: setupOpts{settings: &dmsOff}, wantDMs: 0, wantScans: 1},
		{
			name: "no PR known yet", opts: setupOpts{open: []*reviews.PR{}}, wantDMs: 1,
			wantScans: -1,
		},
		{name: "GitHub off", opts: setupOpts{settings: &githubOff}, wantDMs: 0, wantScans: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tab, fakes := setup(t, tt.opts)
			tickAndSettle(tab)
			tickAndSettle(tab)
			cIs(t, "DM scans", len(cDMs(fakes)), tt.wantDMs)

			if tt.wantScans >= 0 {
				cIs(t, "the channel scan is untouched", cScans(fakes), tt.wantScans)
			}
		})
	}
}

func TestOrchCDMScanRunsOnceADashboardOpens(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})
	tickAndSettle(tab)
	tickAndSettle(tab)
	fakes.with(func(state *world) { state.clients = true })
	tickAndSettle(tab)
	cIs(t, "DM scans", len(cDMs(fakes)), 1)
}

func TestOrchCDMScanWaitsItsIntervalAndReadsFromItsCursor(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	held := cGated(t)

	fakes.with(func(state *world) {
		state.dmResult = func() ([]reviews.DMFind, error) {
			held.pass()

			return []reviews.DMFind{}, nil
		}
	})

	tab.Tick()
	tab.Settle()
	tab.Tick()
	held.reached(t)
	fakes.advance(cDMScanMs - 1)
	tab.Tick()
	tab.Tick()
	held.open()
	tab.Settle()

	cIs(t, "one is running: DM scans", len(cDMs(fakes)), 1)
	cIs(t, "cursor", cText(reviews.LoadFile(fakes.file).DMCursorTS),
		reviews.MsTS(worldT0-cOverlapMs))

	fakes.with(func(state *world) {
		state.dmResult = cDMOnly()
		state.now = worldT0 + cDMScanMs - 1
	})
	tickAndSettle(tab)
	cIs(t, "not due yet: DM scans", len(cDMs(fakes)), 1)

	fakes.with(func(state *world) { state.now = worldT0 + cDMScanMs })
	tickAndSettle(tab)

	dms := cDMs(fakes)
	if len(dms) != 2 {
		t.Fatalf("%d DM scans", len(dms))
	}

	cIs(t, "since", dms[1].since, reviews.MsTS(worldT0-2*cOverlapMs))
	cIs(t, "cursor", cText(reviews.LoadFile(fakes.file).DMCursorTS),
		reviews.MsTS(worldT0+cDMScanMs-cOverlapMs))
}

func TestOrchCFirstDMIsKeptAndItsLastTimeOnlyMovesUp(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	runs := [][2]string{
		{cTS, "1799999100.000100"},
		{"1799999800.000100", "1799999900.000100"},
		{"1799999200.000100", "1799999300.000100"},
	}

	for index, run := range runs {
		if index > 0 {
			fakes.advance(cDMScanMs)
		}

		fakes.with(func(state *world) { state.dmResult = cDMOnly(cDM(7, run[0], run[1])) })
		tickAndSettle(tab)
	}

	entry := cEntry(fakes.file, keyOf(7))
	if entry == nil {
		t.Fatal("no record of 7")
	}

	if entry.DM == nil {
		t.Fatal("no DM")
	}

	cIs(t, "ts", entry.DM.TS, cTS)
	cIs(t, "permalink", entry.DM.Permalink, cDMLink(cTS))
	cIs(t, "dmLastAt", cNum(entry.DMLastAt), "1799999900000")
}

func TestOrchCFailedDMScanSetsDMErrorAndKeepsTheCursor(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	tickAndSettle(tab)
	cIs(t, "slack", view(t, tab).Slack, "ok")
	cIs(t, "dmError", cText(view(t, tab).DMError), "<null>")

	fakes.advance(cDMScanMs)
	fakes.with(func(state *world) {
		state.dmResult = cDMFails(cScanErr("Slack DM scan failed: boom"))
	})
	tickAndSettle(tab)

	shown := view(t, tab)
	cIs(t, "dmError", cText(shown.DMError), "Slack DM scan failed: boom")
	cIs(t, "slack", shown.Slack, "ok")
	cIs(t, "slackError", cText(shown.SlackError), "<null>")
	cIs(t, "the cursor of the earlier success", cText(reviews.LoadFile(fakes.file).DMCursorTS),
		reviews.MsTS(worldT0-cOverlapMs))
	cIs(t, "DM scans", len(cDMs(fakes)), 2)

	tickAndSettle(tab)
	cIs(t, "DM scans before its interval", len(cDMs(fakes)), 2)

	// A plain error is a failure too.
	fakes.advance(cDMScanMs)
	fakes.with(func(state *world) { state.dmResult = cDMFails(errCPlain) })
	tickAndSettle(tab)
	cIs(t, "dmError", cText(view(t, tab).DMError), "plain")

	// The next success clears it and moves the cursor to 15 minutes before that run started.
	fakes.advance(cDMScanMs)
	fakes.with(func(state *world) { state.dmResult = cDMOnly() })
	tickAndSettle(tab)
	cIs(t, "dmError", cText(view(t, tab).DMError), "<null>")
	cIs(t, "cursor", cText(reviews.LoadFile(fakes.file).DMCursorTS),
		reviews.MsTS(fakes.clock()-cOverlapMs))
}

func TestOrchCFirstDMScanFailingLeavesNoCursor(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	fakes.with(func(state *world) { state.dmResult = cDMFails(errCBoom) })
	tickAndSettle(tab)
	cIs(t, "cursor", cText(reviews.LoadFile(fakes.file).DMCursorTS), "<null>")

	fakes.advance(cDMScanMs)
	fakes.with(func(state *world) { state.dmResult = cDMOnly() })
	tickAndSettle(tab)

	dms := cDMs(fakes)
	if len(dms) != 2 {
		t.Fatalf("%d DM scans", len(dms))
	}

	cIs(t, "since", dms[1].since, reviews.MsTS(fakes.clock()-cWindowMs))
}

func TestOrchCDMSuccessWhileSignedOutMakesSlackOK(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	fakes.with(func(state *world) {
		state.scanResult = func() (reviews.ScanResult, error) {
			result := cEmptyScan()
			result.NeedsAuth = []string{cConnector}

			return result, nil
		}
		state.dmResult = cDMFails(cSlackOut(cSignedOut))
	})
	tickAndSettle(tab)
	cIs(t, "slack", view(t, tab).Slack, "needs-auth")
	cIs(t, "dmError", cText(view(t, tab).DMError), "<null>")

	// A failure changes nothing.
	fakes.advance(cDMScanMs)
	fakes.with(func(state *world) { state.dmResult = cDMFails(errCBoom) })
	tickAndSettle(tab)
	cIs(t, "slack after a failure", view(t, tab).Slack, "needs-auth")
	cIs(t, "dmError", cText(view(t, tab).DMError), "boom")

	fakes.advance(cDMScanMs)
	fakes.with(func(state *world) { state.dmResult = cDMOnly(cDM(7, cTS, cTS)) })
	tickAndSettle(tab)

	shown := view(t, tab)
	cIs(t, "slack after a success", shown.Slack, "ok")
	cIs(t, "slackError", cText(shown.SlackError), "<null>")
	cIs(t, "dmError", cText(shown.DMError), "<null>")

	if len(shown.Rows) == 0 {
		t.Fatal("no rows")
	}

	cIs(t, "inChannel", shown.Rows[0].InChannel, true)
}

func TestOrchCDMSuccessNeverTouchesAChannelScanError(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	fakes.with(func(state *world) { state.scanResult = cScanFails(errCChannelBoom) })
	tickAndSettle(tab)
	cIs(t, "slack", view(t, tab).Slack, "error")

	fakes.advance(cDMScanMs)
	fakes.with(func(state *world) { state.dmResult = cDMOnly(cDM(7, cTS, cTS)) })
	tickAndSettle(tab)
	cIs(t, "slack", view(t, tab).Slack, "error")
	cIs(t, "slackError", cText(view(t, tab).SlackError), "channel boom")
}

func TestOrchCDMSuccessResetsTheSignedOutWait(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	fakes.with(func(state *world) { state.scanResult = cPosts(cPost(7)) })
	tickAndSettle(tab)

	// Three signed-out DM runs in a row: the waits double (3, 6, 12 minutes).
	fakes.with(func(state *world) { state.dmResult = cDMFails(cSlackOut(cSignedOut)) })

	for range 3 {
		fakes.advance(cDMScanMs)
		tickAndSettle(tab)
	}

	cIs(t, "slack", view(t, tab).Slack, "needs-auth")

	fakes.advance(12 * minuteMs)
	fakes.with(func(state *world) { state.dmResult = cDMOnly() })
	tickAndSettle(tab)
	cIs(t, "slack", view(t, tab).Slack, "ok")

	// Signed out again: the first wait is a poll interval again, so the DM scan is not pushed
	// past its own 10 minutes.
	fakes.with(func(state *world) { state.dmResult = cDMFails(cSlackOut(cSignedOut)) })
	fakes.advance(cDMScanMs)
	tickAndSettle(tab)

	runs := len(cDMs(fakes))

	fakes.advance(cDMScanMs)
	tickAndSettle(tab)
	cIs(t, "DM scans", len(cDMs(fakes)), runs+1)
}

func TestOrchCSignedOutDMScanBacksOffWithTheChannelScan(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	fakes.with(func(state *world) { state.scanResult = cPosts(cPost(7)) })
	tickAndSettle(tab)
	fakes.with(func(state *world) { state.dmResult = cDMFails(cSlackOut(cSignedOut)) })

	// The channel scan's wait after each signed-out outcome is 3, 6, 12, 24 and 48 minutes; the
	// DM scan is never due sooner than its own 10 (the first run is the success above, the next
	// five are signed out).
	for _, gap := range []float64{10, 10, 10, 12, 24} {
		fakes.advance(gap*minuteMs - 1)

		before := len(cDMs(fakes))

		tickAndSettle(tab)
		cIs(t, "DM scans a millisecond before the wait is over", len(cDMs(fakes)), before)

		fakes.advance(1)
		tickAndSettle(tab)
		cIs(t, "DM scans once the wait is over", len(cDMs(fakes)), before+1)
	}
}

func TestOrchCSignedOutDMScanTakesTheSharedPath(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	fakes.with(func(state *world) { state.scanResult = cPosts(cPost(7)) })
	tickAndSettle(tab)
	cIs(t, "slack", view(t, tab).Slack, "ok")

	cursor := reviews.MsTS(worldT0 - cOverlapMs)
	cIs(t, "cursor", cText(reviews.LoadFile(fakes.file).DMCursorTS), cursor)

	fakes.advance(cDMScanMs)
	fakes.with(func(state *world) { state.dmResult = cDMFails(cSlackOut(cSignedOut)) })
	tickAndSettle(tab)

	shown := view(t, tab)
	cIs(t, "slack", shown.Slack, "needs-auth")
	cIs(t, "dmError", cText(shown.DMError), "<null>")
	cIs(t, "the cursor stays", cText(reviews.LoadFile(fakes.file).DMCursorTS), cursor)
	cIs(t, "DM scans", len(cDMs(fakes)), 2)

	// No fallback: the row that requests me is listed as before, and nothing else is.
	if len(shown.Rows) == 0 {
		t.Fatal("no rows")
	}

	cIs(t, "inChannel", shown.Rows[0].InChannel, true)
}

func TestOrchCSignedOutDMScanClearsDMErrorAndHoldsTheChannelScan(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	fakes.with(func(state *world) { state.dmResult = cDMFails(errCBoom) })
	tickAndSettle(tab)
	cIs(t, "dmError", cText(view(t, tab).DMError), "boom")

	scans := cScans(fakes)
	held := cGated(t)

	fakes.advance(cDMScanMs)
	fakes.with(func(state *world) {
		state.dmResult = func() ([]reviews.DMFind, error) {
			held.pass()

			return nil, cSlackOut(cSignedOut)
		}
	})

	// The channel scan due in the same tick finishes before the DM scan, as in Bun.
	tab.Tick()
	held.reached(t)
	cWaitFor(t, "the channel scan", cScannedAt(t, tab, fakes.clock()))
	held.open()
	tab.Settle()
	tab.Tick()
	tab.Settle()

	cIs(t, "dmError", cText(view(t, tab).DMError), "<null>")
	cIs(t, "slack", view(t, tab).Slack, "needs-auth")

	afterSignedOut := cScans(fakes)
	cIs(t, "scans did not go down", afterSignedOut >= scans, true)

	tickAndSettle(tab)
	cIs(t, "no channel scan until the wait is over", cScans(fakes), afterSignedOut)
}

// cScannedAt is whether the last channel scan finished at now.
func cScannedAt(t *testing.T, tab *reviews.Reviews, now float64) func() bool {
	t.Helper()

	return func() bool {
		at := view(t, tab).LastScanAt
		if at == nil {
			return false
		}

		return *at == now
	}
}
