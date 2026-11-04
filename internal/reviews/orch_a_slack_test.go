package reviews_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

// aInChannel are the rows' "<number> <inChannel>", sorted.
func aInChannel(rows []*reviews.Row) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, fmt.Sprintf("%d %v", row.Number, row.InChannel))
	}

	return aSortedStrings(out)
}

// aNoDMs are the settings with the DM scan off: it would read Slack fine and clear the state the
// test builds.
func aNoDMs() *bool { return new(false) }

// A signed-out Slack lists nothing it alone would list (no fallback to every open PR), and
// burns no try.
func TestSignedOutSlackListsNothingItAloneWould(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{
		open: []*reviews.PR{aNumbered(7), prOf(func(pr *reviews.PR) {
			pr.Number, pr.AuthorIsBot, pr.Author = 8, true, "devin[bot]"
			pr.Requested = []string{worldMe}
		})},
		settings: aSettingsOf(settingsOpts{dms: aNoDMs()}),
	})

	fakes.with(func(fakes *world) { fakes.scanResult = aNeedsAuth })
	tickAndSettle(tab)

	shown := view(t, tab)
	aEqual(t, "slack", shown.Slack, "needs-auth")
	aEqual(t, "slack error", aText(shown.SlackError), "<nil>")
	// 7 asks nobody on GitHub: while Slack cannot be read it is not listed; 8 requests me.
	aEqual(t, "rows", aInChannel(shown.Rows), []string{"8 true"})
	aEqual(t, "tries", aEntry(t, fakes, keyOf(8)).Scan.Tries, float64(0))

	// It is asked again after a poll interval, not on every tick.
	tickAndSettle(tab)
	aEqual(t, "scans", aScans(fakes), 1)
	fakes.with(func(fakes *world) {
		fakes.now += pollMs
		fakes.scanResult = aPosts(aPost(7, aTS))
	})
	tickAndSettle(tab)
	aEqual(t, "scans after", aScans(fakes), 2)

	back := view(t, tab)
	aEqual(t, "slack back", back.Slack, "ok")
	aEqual(t, "rows back", aInChannel(back.Rows), []string{"7 true", "8 true"})

	aSignedOutAgain(t, tab, fakes)
}

// aSignedOutAgain are the last steps of TestSignedOutSlackListsNothingItAloneWould.
func aSignedOutAgain(t *testing.T, tab *reviews.Reviews, fakes *world) {
	t.Helper()

	// Signed out again: a PR found in Slack before drops out too, and nothing is forgotten.
	fakes.with(func(fakes *world) {
		fakes.now += 10 * minuteMs
		fakes.scanResult = aNeedsAuth
	})
	aRefresh(t, tab)
	tickAndSettle(tab)
	aEqual(t, "slack again", view(t, tab).Slack, "needs-auth")
	aEqual(t, "rows again", aNumbers(view(t, tab).Rows), []int64{8})

	if aEntry(t, fakes, keyOf(7)).Slack == nil {
		t.Error("the post of 7 was forgotten")
	}

	// Slack reads again: it is back, with its link.
	fakes.with(func(fakes *world) {
		fakes.now += 10 * minuteMs
		fakes.scanResult = aEmptyScan
	})
	aRefresh(t, tab)
	tickAndSettle(tab)

	links := aRowsBy(view(t, tab).Rows, func(row *reviews.Row) string {
		return fmt.Sprintf("%d %v", row.Number, row.SlackURL != nil)
	})

	aEqual(t, "links", aSortedStrings(links), []string{"7 true", "8 false"})
}

// A failed channel scan hides what only Slack lists, and a failed DM scan what only a DM
// lists, until each reads again.
func TestFailedScansHideWhatOnlyTheyList(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{open: []*reviews.PR{
		aRequested(7), aNumbered(8), aNumbered(9),
	}})

	fakes.with(func(fakes *world) {
		fakes.scanResult = aPosts(aPost(8, aTS))
		fakes.dmResult = aDMs(aDM(9, aTS, aTS))
	})
	tickAndSettle(tab)
	aEqual(t, "rows", aSortedNumbers(view(t, tab).Rows), []int64{7, 8, 9})

	fakes.with(func(fakes *world) {
		fakes.scanResult = func() (reviews.ScanResult, error) {
			return aEmpty(), errATimedOut
		}
		fakes.dmResult = func() ([]reviews.DMFind, error) { return nil, errASearch }
		fakes.now += 10 * minuteMs
	})
	aRefresh(t, tab)
	tickAndSettle(tab)

	shown := view(t, tab)
	aEqual(t, "slack", shown.Slack, "error")
	aEqual(t, "DM error", aText(shown.DMError), "search failed")
	aEqual(t, "rows after", aNumbers(shown.Rows), []int64{7})
	// REVIEW follows what is shown.
	aEqual(t, "start", aStart(t, tab, worldRepo, "8", aSonnet).Code, 404)
}

// A signed-out Slack waits longer after each outcome in a row (to an hour), and a success
// starts it over.
func TestSignedOutSlackBacksOffToAnHour(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{settings: aSettingsOf(settingsOpts{dms: aNoDMs()})})

	fakes.with(func(fakes *world) { fakes.scanResult = aNeedsAuth })

	tickAt := func(minutes float64, scans int, why string) {
		t.Helper()
		fakes.with(func(fakes *world) { fakes.now = aMinutes(minutes) })
		tickAndSettle(tab)
		aEqual(t, why, aScans(fakes), scans)
	}

	tickAt(0, 1, "first scan")
	tickAt(2, 1, "not before a poll interval")
	tickAt(3, 2, "after a poll interval")
	tickAt(3+5, 2, "the delay has doubled to 6 minutes")
	tickAt(3+6, 3, "after 6 minutes")
	tickAt(9+11, 3, "and doubled again to 12")
	tickAt(9+12, 4, "after 12 minutes")

	// The delay stops at an hour.
	minutes := 21.0
	for _, delay := range []float64{24, 48, 60, 60} {
		tickAt(minutes+delay-1, aScans(fakes), fmt.Sprintf("not yet at %v", delay))
		tickAt(minutes+delay, aScans(fakes)+1, fmt.Sprintf("due after %v", delay))
		minutes += delay
	}

	// A scan that succeeds resets it: signed out again waits one poll interval.
	fakes.with(func(fakes *world) { fakes.scanResult = aPosts(aPost(8, aTS)) })
	tickAt(minutes+60, aScans(fakes)+1, "a success")
	fakes.with(func(fakes *world) {
		fakes.scanResult = aNeedsAuth
		fakes.open = []*reviews.PR{aRequested(9)}
		fakes.now += 3 * minuteMs
	})
	tickAndSettle(tab)

	scans := aScans(fakes)

	aEqual(t, "slack", view(t, tab).Slack, "needs-auth")
	fakes.advance(3 * minuteMs)
	tickAndSettle(tab)
	aEqual(t, "a poll interval, not an hour", aScans(fakes), scans+1)
}

// Refresh() clears the signed-out wait.
func TestRefreshClearsSignedOutWait(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})

	fakes.with(func(fakes *world) { fakes.scanResult = aNeedsAuth })
	tickAndSettle(tab)
	tickAndSettle(tab)
	aEqual(t, "scans", aScans(fakes), 1)
	aRefresh(t, tab)
	tickAndSettle(tab)
	aEqual(t, "scans after refresh", aScans(fakes), 2)
}

// A PR first seen while no client was connected is scanned once for every step already
// due, and the schedule is spent.
func TestPRSeenWithoutClientScansOnceForEveryDueStep(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})

	tickAndSettle(tab)
	fakes.with(func(fakes *world) { fakes.now = aMinutes(120) })
	tickAndSettle(tab)
	aEqual(t, "scans without a client", aScans(fakes), 0)

	fakes.with(func(fakes *world) { fakes.clients = true })
	tickAndSettle(tab)
	aEqual(t, "scans", aScans(fakes), 1)
	aEqual(t, "tries", aEntry(t, fakes, keyOf(7)).Scan.Tries,
		float64(len(reviews.ScanBackoffMin())))

	fakes.advance(1000 * minuteMs)
	tickAndSettle(tab)
	aEqual(t, "scans later", aScans(fakes), 1)
}

// A ScanError that carries needsAuth is the same signed-out state, with no try burned.
func TestScanErrorWithNeedsAuthIsSignedOut(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{settings: aSettingsOf(settingsOpts{dms: aNoDMs()})})

	fakes.with(func(fakes *world) {
		fakes.scanResult = func() (reviews.ScanResult, error) {
			return aEmpty(), &reviews.ScanError{
				Message: "Slack scan failed: not signed in", NeedsAuth: []string{aSignedOut},
			}
		}
	})
	tickAndSettle(tab)

	shown := view(t, tab)
	aEqual(t, "slack", shown.Slack, "needs-auth")
	// It requests me: listed, with no NOT IN tag.
	aEqual(t, "rows", aInChannel(shown.Rows), []string{"7 true"})
	aEqual(t, "tries", aEntry(t, fakes, keyOf(7)).Scan.Tries, float64(0))

	// No try was burned: less than the 10-minute step later, a signed-in Slack is asked again.
	fakes.with(func(fakes *world) {
		fakes.now += 4 * minuteMs
		fakes.scanResult = aPosts(aPost(7, aTS))
	})
	tickAndSettle(tab)
	aEqual(t, "scans", aScans(fakes), 2)
	aEqual(t, "slack after", view(t, tab).Slack, "ok")
}

// Any other scan failure shows the error, keeps the last rows, and counts a try so it
// cannot loop.
func TestOtherScanFailureCountsATry(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})

	fakes.with(func(fakes *world) {
		fakes.scanResult = func() (reviews.ScanResult, error) {
			return aEmpty(), &reviews.ScanError{
				Message: "Slack scan failed: timed out", NeedsAuth: []string{},
			}
		}
	})
	tickAndSettle(tab)

	shown := view(t, tab)
	aEqual(t, "slack", shown.Slack, "error")

	if !strings.Contains(aText(shown.SlackError), "timed out") {
		t.Errorf("slack error %q", aText(shown.SlackError))
	}

	aEqual(t, "rows", len(shown.Rows), 1)
	aEqual(t, "tries", aEntry(t, fakes, keyOf(7)).Scan.Tries, float64(1))

	// A plain failure counts too.
	fakes.with(func(fakes *world) {
		fakes.now = aMinutes(10)
		fakes.scanResult = func() (reviews.ScanResult, error) {
			return aEmpty(), errABoom
		}
	})
	tickAndSettle(tab)
	aEqual(t, "tries after", aEntry(t, fakes, keyOf(7)).Scan.Tries, float64(2))
	aEqual(t, "slack error after", aText(view(t, tab).SlackError), "boom")
}
