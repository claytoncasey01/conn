package myprs_test

// The tries at the Greptile ask (a try whose answer was lost may have landed), and the row's
// status once a round is asked for: waiting, findings, ready, or idle when the head moved on.

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
)

// bMine is conn's own ask as the GitHub source reads it: by Jane unless by says otherwise.
func bMine(at float64, by string) myprs.Comment {
	return myprs.Comment{By: by, At: at, EditedAt: nil, Body: defaults().GreptileTrigger}
}

// bComments makes a PR's comments these.
func bComments(comments ...myprs.Comment) func(p *myprs.PR) {
	return func(p *myprs.PR) { p.Comments = comments }
}

// bReviewed makes a PR reviewed by Greptile at its head "new", at at.
func bReviewed(at float64) func(p *myprs.PR) {
	return func(p *myprs.PR) {
		p.Reviews = []myprs.Review{{By: greptileBot, Sha: "new", At: at}}
	}
}

// now is the world's clock.
func (r *bRun) now() float64 {
	at := 0.0

	r.world.Change(func(w *World) { at = w.Now })

	return at
}

// lostOnce fails the asks as lost, moves past the wait for the session and polls: one try.
func (r *bRun) lostOnce() float64 {
	r.failAsks(errBLost)
	r.world.Advance(6 * bMin)
	r.poll(0)

	return r.now()
}

// restart is a second My PRs over the same world and file, polled after advance.
func (r *bRun) restart(advance float64) *myprs.MyPrs {
	again := bNew(r.world.Deps(r.file))
	r.world.Advance(advance)
	again.Refresh()
	tick(again)

	return again
}

// bWrites are GitHub writes whose ask reads the saved try while it is being made.
type bWrites struct {
	mu     sync.Mutex
	file   string
	during *myprs.AskTry
	called bool
}

func (x *bWrites) TriggerReview(context.Context, string, float64) error {
	launches := bLaunches(x.file, bRepo, 7)

	x.mu.Lock()
	defer x.mu.Unlock()

	x.called = true
	if len(launches) > 0 {
		x.during = launches[0].AskTry
	}

	return errBLost
}

func (x *bWrites) ReplyToThread(context.Context, string, string) error { return nil }

func (x *bWrites) ResolveThread(context.Context, string) error { return nil }

// The try is saved before the call, so a restart while gh hangs knows it was made.
func TestBTryIsSavedBeforeTheCall(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{})
	writes := &bWrites{mu: sync.Mutex{}, file: run.file, during: nil, called: false}
	run.tab.SetDeps(func(deps *myprs.Deps) { deps.Writes = writes })
	run.world.Advance(6 * bMin)
	run.poll(0)

	now := run.now()
	want := `{"sha":"new","at":` + js.Number(now) + `,"firstAt":` + js.Number(now) + `,"tries":1}`

	writes.mu.Lock()
	bEqual(t, "called", writes.called, true)
	bEqual(t, "during", string(js.Marshal(writes.during)), want)
	writes.mu.Unlock()
	bEqual(t, "after", string(js.Marshal(run.saved()[0].AskTry)), want)
	bEqual(t, "armed", run.saved()[0].Armed, true)
}

// An ask whose answer was lost is not posted again: my own comment since the try settles
// the round.
func TestBLostAskIsNotPostedAgainWhenMyCommentShows(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{})
	tried := run.lostOnce()
	bEqual(t, "asks", len(run.asks()), 1)
	// GitHub took it after all: the next list has my comment (the login compared without case).
	run.list(bComments(bMine(tried+5000, "Jane")))
	run.failAsks(nil)
	run.poll(2 * bMin)
	run.poll(2 * bMin)
	bEqual(t, "asks after", len(run.asks()), 1)

	launch := run.saved()[0]
	bEqual(t, "launch", []any{bOpt(launch.AskedSha), *launch.AskedAt, launch.Armed},
		[]any{"new", tried, false})
	bEqual(t, "error", bOpt(run.row().Error), nil)
	bEqual(t, "status", run.status(), "waiting")
}

// A comment of mine a little before the try (clock skew) counts, an older one or one by
// someone else does not.
func TestBMyCommentAroundTheTry(t *testing.T) {
	t.Parallel()

	t.Run("a little before", func(t *testing.T) {
		t.Parallel()

		run := bRound(t, bRoundOpts{})
		tried := run.lostOnce()
		run.list(bComments(bMine(tried-bMin, "Jane")))
		run.poll(2 * bMin)
		bEqual(t, "asks", len(run.asks()), 1)
		bEqual(t, "askedSha", bOpt(run.saved()[0].AskedSha), "new")
	})

	t.Run("older, or someone else's", func(t *testing.T) {
		t.Parallel()

		run := bRound(t, bRoundOpts{})
		tried := run.lostOnce()
		run.list(bComments(bMine(tried-3*bMin, "Jane"), bMine(tried+bSecond, "sam")))
		run.poll(2 * bMin)
		bEqual(t, "asks", len(run.asks()), 2)
		bEqual(t, "tries", bTries(run.saved()[0]), 2)
	})
}

// Only my Greptile ask counts as a try that landed: another comment of mine since the try
// does not settle the round, and the ask is made again.
func TestBOnlyMyGreptileAskCountsAsLanded(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{})
	tried := run.lostOnce()
	// The try really failed; meanwhile I thanked someone on the PR.
	thanks := bMine(tried+30*bSecond, "Jane")
	thanks.Body = "thanks, fixed"
	run.list(bComments(thanks))
	run.failAsks(nil)
	run.poll(2 * bMin)
	bEqual(t, "asks", len(run.asks()), 2)

	launch := run.saved()[0]
	bEqual(t, "launch", []any{bOpt(launch.AskedSha), bTries(launch)}, []any{"new", 2})
}

// A restart keeps the minute between two tries: the saved try counts.
func TestBRestartKeepsTheMinuteBetweenTries(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{})
	run.lostOnce()
	bEqual(t, "asks", len(run.asks()), 1)

	again := run.restart(30 * bSecond)
	bEqual(t, "30 s", len(run.asks()), 1)
	run.world.Advance(31 * bSecond)
	again.Refresh()
	tick(again)
	bEqual(t, "61 s", len(run.asks()), 2)
	bEqual(t, "tries", bTries(run.saved()[0]), 2)
}

// bStopped is the row's error once the three tries are used.
const bStopped = "Could not ask Greptile for a review: the ask could not be confirmed after 3 " +
	"tries, so conn has stopped. Look at the PR's comments, and ask by hand if none is there. " +
	"(gh: timed out after 30000 ms)"

// At most three tries per head; then the row says the ask could not be confirmed, and a
// new head starts again.
func TestBAtMostThreeTriesPerHead(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{})
	run.lostOnce()

	for range 6 {
		run.poll(2 * bMin)
	}

	bEqual(t, "asks", len(run.asks()), 3)

	launch := run.saved()[0]
	bEqual(t, "launch", []any{bOpt(launch.AskedSha), launch.Armed, bTries(launch)},
		[]any{nil, true, 3})
	// One whole sentence that says conn has stopped, never "trying again".
	bEqual(t, "error", bOpt(run.row().Error), bStopped)
	// A restart does not start the count again.
	again := run.restart(2 * bMin)
	bEqual(t, "after restart", len(run.asks()), 3)

	shown, _ := bOpt(bRow(t, again).Error).(string)
	bContains(t, "restart error", shown, "could not be confirmed")
	// A new head is a new ask.
	run.list(func(p *myprs.PR) { p.HeadSha = "newer" })
	run.failAsks(nil)
	run.poll(2 * bMin)
	bEqual(t, "new head", len(run.asks()), 4)

	launch = run.saved()[0]
	bEqual(t, "new launch", []any{bOpt(launch.AskedSha), bTries(launch)}, []any{"newer", 1})
}

// A retry that succeeds records the first try's time, so a Greptile answer to the first
// one still makes the PR ready.
func TestBRetryRecordsTheFirstTrysTime(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{})
	first := run.lostOnce()
	// The first try reached Greptile, which answered with its summary comment, but my comment
	// is not in the list yet.
	run.list(bComments(myprs.Comment{
		By: greptileBot, At: first + 30*bSecond, EditedAt: nil, Body: "",
	}))
	run.failAsks(nil)
	run.poll(2 * bMin)
	bEqual(t, "asks", len(run.asks()), 2)

	launch := run.saved()[0]
	bEqual(t, "launch", []any{bOpt(launch.AskedSha), *launch.AskedAt}, []any{"new", first})
	bEqual(t, "status", run.status(), "ready")
}

// A comment of mine that shows up after the tries are used still settles the round.
func TestBMyLateCommentSettlesTheRound(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{})
	first := run.lostOnce()

	for range 3 {
		run.poll(2 * bMin)
	}

	bEqual(t, "asks", len(run.asks()), 3)

	try := run.saved()[0].AskTry
	if try == nil {
		t.Fatal("no try saved")
	}

	bEqual(t, "try", []any{try.FirstAt, try.At}, []any{first, first + 4*bMin})
	// The first try's comment, late: it is older than the last try, but it is one of this
	// head's.
	run.list(bComments(bMine(first+bSecond, "Jane")))
	run.poll(2 * bMin)
	bEqual(t, "asks after", len(run.asks()), 3)

	launch := run.saved()[0]
	bEqual(t, "launch", []any{bOpt(launch.AskedSha), *launch.AskedAt, launch.Armed},
		[]any{"new", first, false})
	bEqual(t, "error", bOpt(run.row().Error), nil)
}

// bAskedNew makes the launch asked for head "new" at T0, disarmed.
func bAskedNew(l *myprs.Launch) {
	l.Armed, l.AskedSha, l.AskedAt = false, new("new"), new(float64(worldT0))
}

// The row goes waiting after the ask, findings when Greptile reviewed and bot threads are
// open, ready when clear.
func TestBRowGoesWaitingFindingsReady(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{launch: bAskedNew})
	run.poll(0)
	bEqual(t, "asked", run.status(), "waiting")
	run.list(bReviewed(worldT0+bMin), func(p *myprs.PR) {
		p.Threads = []myprs.Thread{thread(func(one *myprs.Thread) { one.By = greptileBot })}
	})
	run.poll(0)
	bEqual(t, "bot thread", run.status(), "findings")
	run.list(bReviewed(worldT0+bMin), func(p *myprs.PR) {
		p.Threads = []myprs.Thread{thread(func(one *myprs.Thread) { one.By = "sam" })}
	})
	run.poll(0)

	row := run.row()
	bEqual(t, "clear", []any{row.Status, row.CanStart, row.Label},
		[]any{"ready", true, "Re-review"})
	bEqual(t, "asks", run.asks(), []Ask{})
}

// bState is a row's status, whether it can start, and why not.
func bState(row *myprs.Row) []any { return []any{row.Status, row.CanStart, bOpt(row.Reason)} }

// A PR whose head moved on after the ask is idle, and a live session shows running only
// while its round is not over (the round asked for an older head).
func TestBHeadMovedOnAfterTheAskIsIdle(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{launch: func(l *myprs.Launch) {
		l.Armed, l.AskedSha, l.AskedAt = false, new("older"), new(float64(worldT0))
	}})
	run.poll(0)
	bEqual(t, "status", run.status(), "idle")
	// The round is over (conn has disarmed it) and its session sits idle: it does not hide the
	// row's status, but no next round starts beside it.
	run.sessions("idle")
	bEqual(t, "idle session", bState(run.row()),
		[]any{"idle", false, "its session (self-repo-7-2) is still open: close it to start " +
			"the next round"})
	// At work again (Jane typed into it, or it is waiting on a prompt), it is running, not one
	// to close.
	for _, status := range []string{"busy", "waiting"} {
		run.sessions(status)
		bEqual(t, status, bState(run.row()),
			[]any{"running", false, "a session named self-repo-7-2 is running"})
	}
}

// A PR whose head moved on after the ask is idle, and a live session shows running only
// while its round is not over (an armed round at work, and a session conn has no record of).
func TestBLiveSessionOfAnOpenRoundIsRunning(t *testing.T) {
	t.Parallel()

	t.Run("armed round at work", func(t *testing.T) {
		t.Parallel()

		run := bRound(t, bRoundOpts{session: "busy"})
		run.poll(0)
		bEqual(t, "state", bState(run.row()),
			[]any{"running", false, "a session named self-repo-7-2 is running"})
	})

	t.Run("a session with no record", func(t *testing.T) {
		t.Parallel()

		run := bRound(t, bRoundOpts{launch: func(l *myprs.Launch) {
			bAskedNew(l)
			l.Name = "self-repo-7-9"
		}})

		folder, err := myprs.Folder("/data", bRepo, 7)
		if err != nil {
			t.Fatal(err)
		}

		bSessions(run.world, bLive("self-repo-7-3", filepath.Join(folder, "repo"), "idle"))
		run.poll(0)
		bEqual(t, "status", run.status(), "running")
	})
}

// A finished round that pushed nothing is disarmed while its session stays open, so a
// later push outside the round is never asked.
func TestBFinishedRoundThatPushedNothingIsDisarmed(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{
		pull: func(p *myprs.PR) { p.HeadSha = "old" }, session: "idle",
		replies: new(float64(worldT0)),
	})
	// First seen idle.
	run.poll(0)
	// Quiet a minute, the replies file a minute old, the head still the one the round started
	// from.
	run.world.Advance(bMin)
	run.tab.Tick()
	run.poll(0)
	bEqual(t, "armed", run.saved()[0].Armed, false)
	bEqual(t, "row", []any{run.status(), run.row().CanStart}, []any{"idle", false})
	run.list(func(p *myprs.PR) { p.HeadSha = "pushed-later" })
	run.poll(2 * bMin)
	run.poll(10 * bMin)
	bEqual(t, "asks", run.asks(), []Ask{})
}

// An open finished session is not disarmed on a list fetched within a minute of its
// replies file: its push may not be in that list yet.
func TestBOpenFinishedSessionWaitsAMinuteAfterItsReplies(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{
		pull: func(p *myprs.PR) { p.HeadSha = "old" }, session: "idle",
		replies: new(float64(worldT0 + 30*bSecond)),
	})
	run.poll(0)
	run.world.Advance(bMin)
	run.tab.Tick()
	// Over, but the list is only 30 s after the replies file.
	run.poll(0)
	bEqual(t, "armed", run.saved()[0].Armed, true)
	// The push of the round shows up.
	run.list()
	run.poll(30 * bSecond)
	bEqual(t, "asks", len(run.asks()), 1)
	bEqual(t, "askedSha", bOpt(run.saved()[0].AskedSha), "new")
}

// A round that is over while its session stays open shows its real status: ready with the
// draft saved, it needs me, and it alerts once. The alerts and needsMe are the page's own code
// (src/lib), so only the server's half is here: the status, the draft and the press.
func TestBRoundOverWithItsSessionOpenShowsItsRealStatus(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{session: "idle", replies: new(float64(worldT0))})
	run.poll(0)
	run.world.Advance(bMin)
	run.tab.Tick()
	// Over by its replies file: asked, while the session stays open.
	run.poll(0)
	bEqual(t, "asks", len(run.asks()), 1)
	bEqual(t, "waiting", bState(run.row()), []any{
		"waiting", false,
		"its session (self-repo-7-2) is still open: close it to start the next round",
	})
	run.list(bReviewed(run.now() + bMin))
	run.poll(2 * bMin)

	row := run.row()
	state := ""

	if row.SlackDraft != nil {
		state = row.SlackDraft.State
	}

	bEqual(t, "ready", []any{row.Status, state, row.CanStart}, []any{"ready", "saved", false})
}
