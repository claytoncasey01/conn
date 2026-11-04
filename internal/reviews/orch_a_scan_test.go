package reviews_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// Bun: 'an unmatched PR starts one scan, only with a client, then waits per the back-off and stops
// after four'.
func TestUnmatchedPRScansOnBackoffAndStopsAfterFour(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})

	tickAndSettle(tab)
	tickAndSettle(tab)
	aEqual(t, "scans without a client", aScans(fakes), 0)

	fakes.with(func(fakes *world) { fakes.clients = true })
	tickAndSettle(tab)

	first := fakes.snapshotCalls().scan
	aEqual(t, "scans", len(first), 1)
	aEqual(t, "unmatched", first[0].Unmatched, []string{keyOf(7)})
	aEqual(t, "since", aSince(first[0], aCodeReview), "<nil>")

	tickAndSettle(tab)
	aEqual(t, "scans again", aScans(fakes), 1)

	backoff := reviews.ScanBackoffMin()
	for index := 1; index < len(backoff); index++ {
		fakes.with(func(fakes *world) { fakes.now = aMinutes(backoff[index]) - 1 })
		tickAndSettle(tab)
		aEqual(t, fmt.Sprintf("not yet due at try %d", index), aScans(fakes), index)

		fakes.with(func(fakes *world) { fakes.now = aMinutes(backoff[index]) })
		tickAndSettle(tab)
		aEqual(t, fmt.Sprintf("due at try %d", index), aScans(fakes), index+1)
	}

	fakes.with(func(fakes *world) { fakes.now = aMinutes(1000) })
	tickAndSettle(tab)
	aEqual(t, "scans at the end", aScans(fakes), 4)

	if view(t, tab).LastScanAt == nil {
		t.Error("no last scan")
	}
}

// Bun: 'a scan does not start while one is running'.
func TestScanDoesNotStartWhileOneRuns(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	held := newGate()

	fakes.with(func(fakes *world) {
		fakes.scanResult = func() (reviews.ScanResult, error) {
			held.pass()

			return aEmpty(), nil
		}
	})

	// The scan is held, so its goroutine cannot be waited for: the ticks are not settled.
	aOnce(tab)
	tab.Tick()
	held.reached(t)
	tab.Tick()
	fakes.advance(20 * minuteMs)
	tab.Tick()
	tab.Tick()
	aEqual(t, "scans", aScans(fakes), 1)

	held.open()
	tab.Settle()
}

// Bun: 'a channel scan that finishes after Slack was switched off is dropped: no post, cursor,
// try or "Slack ok"'.
func TestChannelScanFinishingAfterSlackOffIsDropped(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{settings: aSettingsOf(settingsOpts{dms: new(false)})})
	held := newGate()

	fakes.with(func(fakes *world) {
		fakes.scanResult = func() (reviews.ScanResult, error) {
			held.pass()

			return aPosts(aPost(7, aTS))()
		}
	})
	aOnce(tab)
	tab.Tick()
	held.reached(t)
	aEqual(t, "scans", aScans(fakes), 1)

	before := string(js.Marshal(reviews.LoadFile(fakes.file)))

	fakes.setSettings(settingsOf(settingsOpts{slack: new(false), dms: new(false)}))
	tab.SettingsChanged()
	held.open()
	tab.Settle()

	fakes.with(func(fakes *world) {
		for _, record := range fakes.records {
			if strings.HasPrefix(record, "slack ") {
				t.Errorf("record %q", record)
			}
		}
	})
	aEqual(t, "file", string(js.Marshal(reviews.LoadFile(fakes.file))), before)
	aEqual(t, "last scan", view(t, tab).LastScanAt, (*float64)(nil))
}

// Bun: 'a DM scan that finishes after the DM switch was turned off is dropped: no DM, cursor or
// "Slack ok"'.
func TestDMScanFinishingAfterDMsOffIsDropped(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	held := newGate()

	fakes.with(func(fakes *world) {
		fakes.dmResult = func() ([]reviews.DMFind, error) {
			held.pass()

			return aDMs(aDM(7, aTS, "1799999500.000200"))()
		}
	})
	aOnce(tab)
	tab.Tick()
	held.reached(t)
	aEqual(t, "DM scans", len(fakes.snapshotCalls().dm), 1)

	before := reviews.LoadFile(fakes.file)

	fakes.setSettings(settingsOf(settingsOpts{dms: new(false)}))
	tab.SettingsChanged()
	held.open()
	tab.Settle()

	fakes.with(func(fakes *world) {
		for _, record := range fakes.records {
			if strings.Contains(record, "Slack DMs") {
				t.Errorf("record %q", record)
			}
		}
	})

	after := reviews.LoadFile(fakes.file)
	aEqual(t, "DM cursor", aText(after.DMCursorTS), aText(before.DMCursorTS))
	aEqual(t, "DM", js.Stringify(aDMOf(after)), js.Stringify(aDMOf(before)))
}

// aDMOf is the DM of PR 7's record (nil when it has none).
func aDMOf(file *reviews.File) *reviews.DMPost {
	entry, found := file.PRs.Get(keyOf(7))
	if !found {
		return nil
	}

	return entry.DM
}

// Bun: 'a post attaches to its PR and lists it, and the cursor moves up and never back'.
func TestPostAttachesAndCursorNeverMovesBack(t *testing.T) {
	t.Parallel()

	// 7 is found by its post alone; 8 and 9 request me, and have no post yet.
	tab, fakes := setup(t, setupOpts{open: []*reviews.PR{
		aNumbered(7), aRequested(8), aRequested(9),
	}})

	fakes.with(func(fakes *world) { fakes.scanResult = aPosts(aPost(7, aTS)) })
	tickAndSettle(tab)

	shown := view(t, tab)
	aEqual(t, "slack", shown.Slack, "ok")

	links := aRowsBy(shown.Rows, func(row *reviews.Row) string {
		return fmt.Sprintf("%d %s", row.Number, aText(row.SlackURL))
	})
	aEqual(t, "links", aSortedStrings(links), []string{"7 " + aThreadLink, "8 <nil>", "9 <nil>"})

	saved := aEntry(t, fakes, keyOf(7))
	aEqual(t, "post ts", saved.Slack.TS, aTS)
	aEqual(t, "posted at", saved.Slack.PostedAt, float64(1_799_999_000_000))
	// The run read the channel up to when it started: the cursor is that moment less the overlap.
	aEqual(t, "cursor", aCursor(fakes), reviews.MsTS(aMinutes(-15)))

	aCursorSteps(t, tab, fakes)
}

// aCursorSteps are the later scans of TestPostAttachesAndCursorNeverMovesBack.
func aCursorSteps(t *testing.T, tab *reviews.Reviews, fakes *world) {
	t.Helper()

	// The next scan reads from the cursor, and an older post cannot pull it back.
	fakes.with(func(fakes *world) {
		fakes.now = aMinutes(10)
		fakes.scanResult = aPosts(aPost(8, "1799990000.000001"))
	})
	tickAndSettle(tab)
	// PRs 8 and 9 have no post: the scan reads from 15 minutes before the older of the cursor and
	// their first sighting.
	aEqual(t, "second since", aSince(fakes.snapshotCalls().scan[1], aCodeReview),
		reviews.MsTS(aMinutes(-30)))
	aEqual(t, "second cursor", aCursor(fakes), reviews.MsTS(aMinutes(-5)))

	// A post newer than the run's start moves it past that.
	fakes.with(func(fakes *world) {
		fakes.now = aMinutes(30)
		fakes.scanResult = aPosts(aPost(8, reviews.MsTS(aMinutes(20))))
	})
	tickAndSettle(tab)
	aEqual(t, "third cursor", aCursor(fakes), reviews.MsTS(aMinutes(20)))

	// A run that finds Slack signed out, or fails, moves no cursor.
	fakes.with(func(fakes *world) {
		fakes.now = aMinutes(60)
		fakes.scanResult = aNeedsAuth
	})
	aRefresh(t, tab)
	tickAndSettle(tab)
	aEqual(t, "signed-out cursor", aCursor(fakes), reviews.MsTS(aMinutes(20)))
}

// Bun: 'a post about a PR not known yet is read from GitHub before it counts: open, it is listed
// with its post; closed or gone, it is dropped'.
func TestPostAboutUnknownPRIsReadFirst(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{open: []*reviews.PR{
		aRequested(7),
		prOf(func(pr *reviews.PR) { pr.Number, pr.Title = 8, "Found in Slack" }),
		aNumbered(12, aGadgets),
	}})

	fakes.with(func(fakes *world) {
		fakes.endedAs, fakes.endedAt = textOf(reviews.EndedClosed), worldT0-minuteMs
		fakes.known[keyOf(9)] = aNumbered(9)
		fakes.scanResult = aPosts(aPost(8, aTS), aPost(9, "1799999000.000200"),
			aPost(10, "1799999000.000300"), aPost(12, "1799999000.000400", aGadgets))
	})
	tickAndSettle(tab)

	// One read for the leads, after the scan: 8 is open, 9 closed, 10 is not on GitHub, 12 is
	// open in another allowed repo.
	aEqual(t, "reads", fakes.snapshotCalls().fetch,
		[][]string{{keyOf(8), keyOf(9), keyOf(10), keyOf(12, aGadgets)}})

	rows := aRowsBy(view(t, tab).Rows, func(row *reviews.Row) string {
		return row.Key + " " + aText(row.SlackURL)
	})

	aEqual(t, "rows", aSortedStrings(rows), []string{
		keyOf(12, aGadgets) + " https://acme.slack.com/archives/C1/p12",
		keyOf(7) + " <nil>",
		keyOf(8) + " https://acme.slack.com/archives/C1/p8",
	})

	saved := reviews.LoadFile(fakes.file)
	aEqual(t, "records", aSortedStrings(saved.PRs.Keys()),
		[]string{keyOf(12, aGadgets), keyOf(7), keyOf(8)})
	aEqual(t, "channel", aText(aEntry(t, fakes, keyOf(8)).Slack.Channel), aCodeReview)
	aEqual(t, "every lead was answered", saved.Found.Len(), 0)
	// The cursor moved past all four posts: they were kept as leads, so none is lost.
	aEqual(t, "cursor", aCursor(fakes), reviews.MsTS(aMinutes(-15)))
}
