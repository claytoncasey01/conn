package reviews_test

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
)

// aPostedRecord saves a record of PR 7 whose post is already known.
func aPostedRecord(t *testing.T, fakes *world) {
	t.Helper()

	file := reviews.LoadFile(fakes.file)
	file.PRs.Set(keyOf(7), &reviews.Entry{
		Slack: &reviews.SlackPost{
			TS: aThreadTS, Permalink: aThreadLink, PostedAt: 1_799_990_000_000, Channel: nil,
		},
		SlackAskAt: nil, DM: nil, DMLastAt: nil, FirstSeenAt: worldT0 - 3*hourMs,
		Launches: []*reviews.Launch{}, Scan: reviews.Scan{Tries: 0, LastAt: 0},
		ThreadScan: reviews.ThreadScan{Tries: 0, LastAt: 0, HeadSha: ""}, Requested: false,
		Ended: nil, SeenAt: worldT0 - 3*hourMs, PlacedAt: nil,
	})

	err := reviews.SaveFile(fakes.file, file, nil)
	if err != nil {
		t.Fatal(err)
	}
}

// aReviewedAt is PR 7 at head, which I reviewed at aaa an hour before T0.
func aReviewedAt(head string) *reviews.PR {
	return prOf(func(pr *reviews.PR) {
		pr.HeadSha = head
		pr.Reviews = []reviews.Review{myReview(func(review *reviews.Review) {
			review.Commit, review.SubmittedAt = "aaa", worldT0-hourMs
		})}
	})
}

// A waiting PR whose head moved gets a thread check naming the post and the time of the
// review.
func TestHeadMovedGetsThreadCheck(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{open: []*reviews.PR{aReviewedAt("bbb")}})

	aPostedRecord(t, fakes)
	tickAndSettle(tab)

	row := view(t, tab).Rows[0]
	aEqual(t, "state", row.State, "waiting")
	aEqual(t, "updated", row.Updated, true)

	scans := fakes.snapshotCalls().scan
	aEqual(t, "scans", len(scans), 1)
	aEqual(t, "unmatched", scans[0].Unmatched, []string{})
	aEqual(t, "threads", scans[0].Threads, []reviews.Thread{{
		PR: keyOf(7), Channel: aCodeReview, TS: aThreadTS, SinceMs: worldT0 - hourMs,
	}})
}

// A thread ask makes the waiting PR rereview; with no ask it is checked on the back-off,
// which restarts when the head moves.
func TestThreadAskMakesRereviewAndBackoffRestarts(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{open: []*reviews.PR{aReviewedAt("bbb")}})

	aPostedRecord(t, fakes)
	tickAndSettle(tab)
	aEqual(t, "scans", aScans(fakes), 1)
	aEqual(t, "tries", aEntry(t, fakes, keyOf(7)).ThreadScan.Tries, float64(1))
	aEqual(t, "head", aEntry(t, fakes, keyOf(7)).ThreadScan.HeadSha, "bbb")

	// Same schedule as the post scan: 10, 30 and 90 minutes after the head moved, then nothing.
	for _, step := range []struct {
		scans   int
		minutes float64
	}{{2, 10}, {3, 30}, {4, 90}} {
		fakes.with(func(fakes *world) { fakes.now = aMinutes(step.minutes) - 1 })
		tickAndSettle(tab)
		aEqual(t, "before the step", aScans(fakes), step.scans-1)
		fakes.with(func(fakes *world) { fakes.now = aMinutes(step.minutes) })
		tickAndSettle(tab)
		aEqual(t, "at the step", aScans(fakes), step.scans)
	}

	fakes.with(func(fakes *world) { fakes.now = aMinutes(5000) })
	tickAndSettle(tab)
	aEqual(t, "scans at the end", aScans(fakes), 4)

	aAuthorPushesAgain(t, tab, fakes)
}

// aAuthorPushesAgain is the end of TestThreadAskMakesRereviewAndBackoffRestarts.
func aAuthorPushesAgain(t *testing.T, tab *reviews.Reviews, fakes *world) {
	t.Helper()

	// The author pushes again: the schedule starts over.
	fakes.with(func(fakes *world) {
		fakes.open = []*reviews.PR{aReviewedAt("ccc")}
		fakes.now = aMinutes(6000) + pollMs
		fakes.scanResult = func() (reviews.ScanResult, error) {
			result := aEmpty()
			result.Asks = []reviews.Ask{{Key: keyOf(7), AskedAt: worldT0 - 1000}}

			return result, nil
		}
	})
	tickAndSettle(tab)

	saved := aEntry(t, fakes, keyOf(7))
	aEqual(t, "head", saved.ThreadScan.HeadSha, "ccc")
	aEqual(t, "scans", aScans(fakes), 5)

	if saved.SlackAskAt == nil {
		t.Fatal("no ask")
	}

	aEqual(t, "asked at", *saved.SlackAskAt, float64(worldT0-1000))

	row := view(t, tab).Rows[0]
	aEqual(t, "row", []string{row.State, aText(row.ReAsked)}, []string{"rereview", "slack"})

	// Answered: no more thread checks for it.
	fakes.advance(1000 * minuteMs)
	tickAndSettle(tab)
	aEqual(t, "scans after", aScans(fakes), 5)
}

// A thread check is not made for a PR whose head is the reviewed commit.
func TestNoThreadCheckAtTheReviewedCommit(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{open: []*reviews.PR{prOf(func(pr *reviews.PR) {
		pr.HeadSha, pr.Reviews = "aaa", []reviews.Review{myReview(nil)}
	})}})

	aPostedRecord(t, fakes)
	tickAndSettle(tab)
	aEqual(t, "scans", aScans(fakes), 0)
}

// aBusy is a busy session named name in cwd.
func aBusy(name, cwd string) sessions.LiveSession {
	return aSession(name, cwd, "busy", "sid-"+name, 1)
}

// A PR is reviewing while the session its launch record names is live; a session merely
// named like one is not.
func TestReviewingOnlyWhileTheLaunchSessionIsLive(t *testing.T) {
	t.Parallel()

	// A session named for PR 8, but Conn launched nothing for it: not a review of 8.
	tab, fakes := setup(t, setupOpts{
		noClient: true, open: []*reviews.PR{aRequested(7), aRequested(8)},
		sessions: []sessions.LiveSession{aBusy("pr-8-acme--widgets", "/w"), aBusy("pr-8", "/w")},
	})

	tickAndSettle(tab)

	for _, row := range view(t, tab).Rows {
		if row.State == "reviewing" {
			t.Errorf("%d is reviewing", row.Number)
		}
	}

	fakes.with(func(fakes *world) { fakes.sessions = nil })
	aEqual(t, "start", aStart(t, tab, worldRepo, "7", aSonnet).Code, 200)

	name := "pr-7-acme--widgets"

	fakes.with(func(fakes *world) {
		fakes.sessions = []sessions.LiveSession{aBusy(name, worktreeOf(fakes.dir, worldRepo, 7))}
	})

	reviewing := []int64{}
	rows := view(t, tab).Rows

	for _, row := range rows {
		if row.State == "reviewing" {
			reviewing = append(reviewing, row.Number)
		}
	}

	aEqual(t, "reviewing", reviewing, []int64{7})
	aEqual(t, "session", aRow(rows, 7).Session, &reviews.RowSession{Name: name, Status: "busy"})

	// The same name in another folder is not the launch's session.
	fakes.with(func(fakes *world) {
		fakes.sessions = []sessions.LiveSession{aBusy(name, "/elsewhere")}
	})
	aEqual(t, "elsewhere", aRow(view(t, tab).Rows, 7).State, "new")
}

// A draft PR is not listed and starts no scan.
func TestDraftIsNotListedAndStartsNoScan(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{open: []*reviews.PR{prOf(func(pr *reviews.PR) {
		pr.IsDraft = true
	})}})

	tickAndSettle(tab)
	aEqual(t, "scans", aScans(fakes), 0)
	aEqual(t, "rows", len(view(t, tab).Rows), 0)
}
