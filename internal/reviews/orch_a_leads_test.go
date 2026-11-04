package reviews_test

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

// At most 30 leads are read per scan, the oldest first; the rest wait for the next scan,
// and a lead whose read failed waits too.
func TestAtMostThirtyLeadsReadPerScan(t *testing.T) {
	t.Parallel()

	open := make([]*reviews.PR, 0, 1+reviews.FetchPerScan+5)
	open = append(open, aRequested(7))
	posts := make([]reviews.Post, 0, reviews.FetchPerScan+5)

	for index := range reviews.FetchPerScan + 5 {
		number := int64(100 + index)
		open = append(open, aNumbered(number))
		posts = append(posts, aPost(number, reviews.MsTS(aMinutes(-60)+float64(index)*1000)))
	}

	// The DM scan is off: its own read would take the next 30.
	tab, fakes := setup(t, setupOpts{
		open: open, settings: aSettingsOf(settingsOpts{dms: new(false)}),
	})

	fakes.with(func(fakes *world) {
		fakes.readFails[keyOf(100)] = true
		fakes.scanResult = aPosts(posts...)
	})
	tickAndSettle(tab)

	reads := fakes.snapshotCalls().fetch
	aEqual(t, "reads", len(reads), 1)
	aEqual(t, "read size", len(reads[0]), reviews.FetchPerScan)
	aEqual(t, "five not read yet, and the one whose read failed",
		reviews.LoadFile(fakes.file).Found.Len(), 6)
	aEqual(t, "rows", len(view(t, tab).Rows), 1+reviews.FetchPerScan-1)

	// The next scan reads the rest (no new post needed).
	fakes.with(func(fakes *world) {
		clear(fakes.readFails)
		fakes.scanResult = aEmptyScan
		fakes.now += 10 * minuteMs
	})
	aRefresh(t, tab)
	tickAndSettle(tab)
	aEqual(t, "rows after", len(view(t, tab).Rows), 1+reviews.FetchPerScan+5)
	aEqual(t, "leads after", reviews.LoadFile(fakes.file).Found.Len(), 0)
}

// A lead about a repo that is not allowed is never read, and a lead waits while GitHub
// cannot be read.
func TestLeadOfDisallowedRepoIsNotRead(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{open: []*reviews.PR{aRequested(7), aNumbered(8)}})
	held := newGate()

	fakes.with(func(fakes *world) {
		fakes.scanResult = aPosts(aPost(8, aTS))
		fakes.endedFailures = 1
		fakes.endedGate = held
	})
	aOnce(tab)
	tab.Tick()
	// The DM scan of the same tick finishes while the channel scan's read of its lead
	// is out (so it does not read the lead again): the read is held until the DM scan is done.
	held.reached(t)
	aWaitRecord(t, fakes, "Slack DMs")
	tab.Snapshot()
	held.open()
	tab.Settle()
	aEqual(t, "leads", reviews.LoadFile(fakes.file).Found.Keys(), []string{keyOf(8)})
	aEqual(t, "rows", aNumbers(view(t, tab).Rows), []int64{7})

	// The repo leaves the list: the lead is kept, not read.
	fakes.setSettings(settingsOf(settingsOpts{allow: []string{"jane/*"}}))
	fakes.advance(10 * minuteMs)
	aRefresh(t, tab)
	tickAndSettle(tab)
	aEqual(t, "the lead of a repo no longer allowed is not read",
		len(fakes.snapshotCalls().fetch), 1)
	aEqual(t, "leads after", reviews.LoadFile(fakes.file).Found.Keys(), []string{keyOf(8)})
}

// aReadFrom forces a scan of PR 7 a minute on, and answers where it read #code-review from.
func aReadFrom(t *testing.T, tab *reviews.Reviews, fakes *world) string {
	t.Helper()

	fakes.advance(minuteMs)
	// An unmatched PR with a scan due now.
	tab.WithFile(func(file *reviews.File) {
		entry, _ := file.PRs.Get(keyOf(7))
		entry.Scan = reviews.Scan{Tries: 0, LastAt: 0}
	})
	aRefresh(t, tab)
	tickAndSettle(tab)

	scans := fakes.snapshotCalls().scan

	return aSince(scans[len(scans)-1], aCodeReview)
}

// aSetCursor sets #code-review's cursor ("" deletes it).
func aSetCursor(tab *reviews.Reviews, cursor string) {
	tab.WithFile(func(file *reviews.File) {
		if cursor == "" {
			file.Cursors.Delete(aCodeReview)

			return
		}

		file.Cursors.Set(aCodeReview, cursor)
	})
}

// aSetFirstSeen sets when PR 7 was first seen.
func aSetFirstSeen(tab *reviews.Reviews, at float64) {
	tab.WithFile(func(file *reviews.File) {
		entry, _ := file.PRs.Get(keyOf(7))
		entry.FirstSeenAt = at
	})
}

const aWindowMs = 2 * 24 * 60 * minuteMs

// A scan never reads later than the cursor, and never earlier than the first-scan window.
func TestScanReadsFromCursorWithinWindow(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})

	tickAndSettle(tab)
	fakes.with(func(fakes *world) { fakes.clients = true })

	// An old cursor and a PR first seen just now: 15 minutes before the cursor.
	aSetCursor(tab, reviews.MsTS(aMinutes(-60)))
	aEqual(t, "old cursor", aReadFrom(t, tab, fakes), reviews.MsTS(aMinutes(-75)))

	// A cursor from 5 minutes ago, a PR first seen 3 minutes ago: 15 minutes before the cursor.
	readAt := fakes.clock() + minuteMs
	aSetCursor(tab, reviews.MsTS(readAt-5*minuteMs))
	aSetFirstSeen(tab, readAt-3*minuteMs)
	aEqual(t, "recent cursor", aReadFrom(t, tab, fakes),
		reviews.MsTS(readAt-5*minuteMs-15*minuteMs))

	aScanWindowSteps(t, tab, fakes)
}

// aScanWindowSteps are the window steps of TestScanReadsFromCursorWithinWindow.
func aScanWindowSteps(t *testing.T, tab *reviews.Reviews, fakes *world) {
	t.Helper()

	// A recent cursor and a PR first seen long ago: not before the window (2 days).
	aSetCursor(tab, reviews.MsTS(worldT0))
	aSetFirstSeen(tab, worldT0-5*24*60*minuteMs)

	floor := fakes.clock() - aWindowMs
	got := aReadFrom(t, tab, fakes)
	aEqual(t, "window", got, reviews.MsTS(fakes.clock()-aWindowMs))

	if floor >= reviews.TSMs(aCursor(fakes)) {
		t.Errorf("the window %v is not before the cursor", floor)
	}

	// A cursor older than the window reads the window, not from the old cursor.
	aSetCursor(tab, reviews.MsTS(worldT0-10*24*60*minuteMs))
	got = aReadFrom(t, tab, fakes)
	aEqual(t, "old cursor past the window", got, reviews.MsTS(fakes.clock()-aWindowMs))

	// No cursor yet: the first-scan window, as before.
	aSetCursor(tab, "")
	aEqual(t, "no cursor", aReadFrom(t, tab, fakes), "<nil>")

	// A scan with no unmatched PR (threads only) reads from the cursor itself.
	aSetCursor(tab, reviews.MsTS(worldT0))
	tab.WithFile(func(file *reviews.File) {
		entry, _ := file.PRs.Get(keyOf(7))
		entry.Scan = reviews.Scan{Tries: 4, LastAt: 0}
		entry.Slack = &reviews.SlackPost{
			TS: reviews.MsTS(worldT0), Permalink: aThreadLink, PostedAt: worldT0, Channel: nil,
		}
	})
	aEqual(t, "threads only", aText(tab.ScanSince(fakes.clock(), aCodeReview)),
		reviews.MsTS(worldT0))
}
