package reviews_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

// aPoll moves the clock a poll interval on and ticks twice.
func aPoll(tab *reviews.Reviews, fakes *world) {
	fakes.advance(pollMs)
	tickAndSettle(tab)
}

// A PR that leaves the searches gets read by number, and shows as done when it merged.
func TestPRLeavingSearchesIsReadByNumber(t *testing.T) {
	t.Parallel()

	t.Run("not tracked", func(t *testing.T) {
		t.Parallel()

		tab, fakes := setup(t, setupOpts{noClient: true})

		tickAndSettle(tab)
		fakes.with(func(fakes *world) { fakes.open = []*reviews.PR{} })
		aPoll(tab, fakes)
		aEqual(t, "reads of ended PRs", fakes.snapshotCalls().ended, []int64{7})
		aEqual(t, "a PR nothing tracked beyond a GitHub request is not kept",
			len(view(t, tab).Rows), 0)
	})

	t.Run("reviewed", func(t *testing.T) {
		t.Parallel()

		// One that I reviewed (so it is tracked) stays, greyed.
		tab, fakes := setup(t, setupOpts{noClient: true, open: []*reviews.PR{aReviewedPR()}})

		tickAndSettle(tab)
		fakes.with(func(fakes *world) { fakes.open = []*reviews.PR{} })
		aPoll(tab, fakes)
		aPoll(tab, fakes)
		aEqual(t, "reads of ended PRs", fakes.snapshotCalls().ended, []int64{7})
		aEqual(t, "rows", aStates(view(t, tab).Rows), []string{"7 done merged"})

		// For a day, and then it drops off without another lookup.
		fakes.advance(25 * hourMs)
		aEqual(t, "rows a day on", len(view(t, tab).Rows), 0)
		aEqual(t, "reads a day on", fakes.snapshotCalls().ended, []int64{7})
	})
}

// A failed read by number is tried again on the next poll (the last rows stay meanwhile),
// and the PR then shows as done.
func TestFailedReadByNumberIsTriedAgain(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true, open: []*reviews.PR{aReviewedPR()}})

	tickAndSettle(tab)
	fakes.with(func(fakes *world) {
		fakes.open = []*reviews.PR{}
		fakes.endedFailures = 1
	})
	aPoll(tab, fakes)
	aEqual(t, "rows", aStates(view(t, tab).Rows), []string{"7 rereview <nil>"})

	if !strings.Contains(aText(view(t, tab).Error), "hiccup") {
		t.Errorf("error %q", aText(view(t, tab).Error))
	}

	aPoll(tab, fakes)
	aEqual(t, "rows after", aStates(view(t, tab).Rows), []string{"7 done merged"})
	aEqual(t, "reads", fakes.snapshotCalls().ended, []int64{7, 7})
	aPoll(tab, fakes)
	aEqual(t, "not looked up again once it answered", fakes.snapshotCalls().ended, []int64{7, 7})
}

// A PR that comes back to the open list is open again.
func TestPRBackOnOpenListIsOpenAgain(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true, open: []*reviews.PR{aReviewedPR()}})

	tickAndSettle(tab)
	fakes.with(func(fakes *world) { fakes.open = []*reviews.PR{} })
	aPoll(tab, fakes)
	fakes.with(func(fakes *world) { fakes.open = []*reviews.PR{aReviewedPR()} })
	aPoll(tab, fakes)
	aEqual(t, "rows", aStates(view(t, tab).Rows), []string{"7 rereview <nil>"})
}

// A PR GitHub stops reporting as requesting me (I reviewed it) is read by number and keeps
// its state; its own read says when it ended.
func TestPRNoLongerRequestingMeIsReadByNumber(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})

	tickAndSettle(tab)
	aEqual(t, "state", view(t, tab).Rows[0].State, "new")

	// I review it: GitHub drops the request, so the search no longer lists it.
	fakes.with(func(fakes *world) {
		fakes.open = []*reviews.PR{prOf(func(pr *reviews.PR) {
			pr.Reviews = []reviews.Review{myReview(func(review *reviews.Review) {
				review.SubmittedAt = worldT0
			})}
		})}
	})
	aPoll(tab, fakes)
	aEqual(t, "reads", fakes.snapshotCalls().fetch, [][]string{{keyOf(7)}})
	aEqual(t, "rows", aStates(view(t, tab).Rows), []string{"7 waiting <nil>"})

	// Merged: its own read says so, and it shows as done.
	fakes.with(func(fakes *world) { fakes.open = []*reviews.PR{} })
	aPoll(tab, fakes)
	aEqual(t, "rows merged", aStates(view(t, tab).Rows), []string{"7 done merged"})

	// An ended PR is not read again.
	reads := len(fakes.snapshotCalls().fetch)
	aPoll(tab, fakes)
	aEqual(t, "reads after", len(fakes.snapshotCalls().fetch), reads)
}

// A PR GitHub no longer has is gone: no row, and it is not read again.
func TestPRGitHubNoLongerHasIsGone(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})

	tickAndSettle(tab)
	fakes.with(func(fakes *world) {
		fakes.open = []*reviews.PR{}
		fakes.gone[keyOf(7)] = true
	})
	aPoll(tab, fakes)
	aEqual(t, "rows", len(view(t, tab).Rows), 0)

	ended := aEntry(t, fakes, keyOf(7)).Ended
	if ended == nil {
		t.Fatal("not ended")
	}

	aEqual(t, "how", ended.How, "gone")

	reads := len(fakes.snapshotCalls().fetch)
	aPoll(tab, fakes)
	aEqual(t, "reads after", len(fakes.snapshotCalls().fetch), reads)
}

// A read by number that fails for one PR keeps what was known of it; a read that fails
// outright keeps the rows and shows the error.
func TestFailedReadByNumberKeepsWhatWasKnown(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})

	tickAndSettle(tab)
	fakes.with(func(fakes *world) {
		fakes.open = []*reviews.PR{aNumbered(7)}
		fakes.readFails[keyOf(7)] = true
	})
	aPoll(tab, fakes)
	aEqual(t, "rows", aNumbers(view(t, tab).Rows), []int64{7})
	aEqual(t, "error", aText(view(t, tab).Error), "<nil>")

	fakes.with(func(fakes *world) {
		clear(fakes.readFails)
		fakes.endedFailures = 1
	})
	aPoll(tab, fakes)
	aEqual(t, "rows after", aNumbers(view(t, tab).Rows), []int64{7})

	if !strings.Contains(aText(view(t, tab).Error), "hiccup") {
		t.Errorf("error %q", aText(view(t, tab).Error))
	}

	aPoll(tab, fakes)
	aEqual(t, "error after", aText(view(t, tab).Error), "<nil>")
}

// "every open PR": one search per pattern (capped at 200), every non-draft PR I did not
// write, tagged not in a channel, and capped is only a notice.
func TestEveryOpenPRSearchesPerPattern(t *testing.T) {
	t.Parallel()

	allow := []string{"acme/*", aTools}
	tab, fakes := setup(t, setupOpts{
		noClient: true,
		open: []*reviews.PR{
			aNumbered(7), prOf(func(pr *reviews.PR) { pr.Number, pr.IsDraft = 8, true }),
			prOf(func(pr *reviews.PR) { pr.Number, pr.Author = 9, worldMe }), aNumbered(3, aTools),
		},
		settings: aSettingsOf(settingsOpts{
			allow: allow, allOpen: true, requested: new(false),
		}),
	})

	fakes.with(func(fakes *world) { fakes.openCapped = true })
	tickAndSettle(tab)
	aEqual(t, "searches", fakes.snapshotCalls().search, [][]string{{
		"is:pr is:open draft:false archived:false -author:@me user:acme",
		"is:pr is:open draft:false archived:false -author:@me repo:jane/tools",
	}})

	shown := view(t, tab)
	tagged := aRowsBy(shown.Rows, func(row *reviews.Row) string {
		return fmt.Sprintf("%s %v", row.Key, row.InChannel)
	})

	aEqual(t, "rows", aSortedStrings(tagged),
		[]string{keyOf(7) + " false", keyOf(3, aTools) + " false"})
	aEqual(t, "capped", shown.Capped, allow)

	// A cut-off list drops nothing: the PR that fell off it is read by number and stays.
	fakes.with(func(fakes *world) {
		fakes.open = []*reviews.PR{aNumbered(3, aTools)}
		fakes.endedAs = nil
	})
	aPoll(tab, fakes)
	aEqual(t, "rows after", aSortedKeys(view(t, tab).Rows), []string{keyOf(7), keyOf(3, aTools)})

	// Off again: the capped notice goes with it.
	fakes.setSettings(settingsOf(settingsOpts{allow: allow}))
	aEqual(t, "capped off", view(t, tab).Capped, []string{})
}

// A finished PR is never missing from the rows while its lookup is awaited.
func TestFinishedPRStaysWhileItsLookupIsOut(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true, open: []*reviews.PR{aReviewedPR()}})

	tickAndSettle(tab)
	aEqual(t, "rows", len(view(t, tab).Rows), 1)

	held := newGate()

	fakes.with(func(fakes *world) {
		fakes.open = []*reviews.PR{}
		fakes.endedGate = held
		fakes.now += pollMs
	})
	tab.Tick()
	held.reached(t)
	aEqual(t, "the lookup is in flight", fakes.snapshotCalls().ended, []int64{7})
	aEqual(t, "still the open row", aStates(view(t, tab).Rows), []string{"7 rereview <nil>"})

	held.open()
	tab.Settle()
	aEqual(t, "rows after", aStates(view(t, tab).Rows), []string{"7 done merged"})
}
