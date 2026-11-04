package myprs_test

// The Greptile ask and the ready rule: when a round is over its push is asked for once, a round
// that pushed nothing is disarmed, and a press waits for the poll that settles the last round.

import (
	"errors"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/myprs"
)

// bAskOwed is why a press waits for the last round to be settled.
const bAskOwed = "the last round of this PR has not been settled yet: conn asks Greptile for " +
	"its push (a failed ask is tried again, up to 3 tries) or sees it made none, on its polls. " +
	"REFRESH starts one (a round that pushed nothing needs a minute after its session closed)"

var (
	// errBLost is a try whose answer gh lost (it gives up after 30 s): GitHub may still have
	// taken it.
	errBLost = errors.New("gh: timed out after 30000 ms")
	// errBForbidden is an ask GitHub refused, with control and bidi characters and a long tail.
	errBForbidden = errors.New("gh: \u001b[31mHTTP 403\u202e\n" + strings.Repeat("z", 500))
)

// bRoundOpts are a round's: the listed PR's changes (its head is "new"), the newest launch's
// changes, the session of that launch ("" none), the mtime of its replies file (nil none), and
// older launches before it.
type bRoundOpts struct {
	pull    func(p *myprs.PR)
	launch  func(l *myprs.Launch)
	session string
	replies *float64
	extra   []func(l *myprs.Launch)
}

// bRun is a listed PR with recorded launches, and My PRs over it.
type bRun struct {
	t     *testing.T
	world *World
	tab   *myprs.MyPrs
	file  string
}

// bHead is the PR the round lists: org/repo#7 at head "new", with changes.
func bHead(changes ...func(p *myprs.PR)) *myprs.PR {
	return pr(append([]func(p *myprs.PR){func(p *myprs.PR) { p.HeadSha = "new" }}, changes...)...)
}

// bRoundLaunch is a seeded launch of org/repo#7 in bAWT, armed, at head "old".
func bRoundLaunch(at float64, name string, round float64, change func(l *myprs.Launch),
) *myprs.Launch {
	launch := &myprs.Launch{
		At: at, Model: bSonnet, Name: name, Worktree: bAWT, HeadSha: "old", Round: round,
		Armed: true, AskedSha: nil, AskedAt: nil, ClosedAt: nil, AskTry: nil, Manual: false,
	}
	if change != nil {
		change(launch)
	}

	return launch
}

// bRound is one recorded launch (the newest, round 2) of a listed PR, not yet
// polled.
func bRound(t *testing.T, opts bRoundOpts) *bRun {
	t.Helper()

	file := bFile(t)
	launches := make([]*myprs.Launch, 0, len(opts.extra)+1)

	for _, change := range opts.extra {
		launches = append(launches, bRoundLaunch(worldT0-3*hourMs, "self-repo-7", 1, change))
	}

	launches = append(launches,
		bRoundLaunch(worldT0-hourMs, "self-repo-7-2", 2, opts.launch))
	bSeed(t, file, myprs.EntryKey(bRepo, 7), worldT0-hourMs, launches...)

	changes := []func(p *myprs.PR){}
	if opts.pull != nil {
		changes = append(changes, opts.pull)
	}

	world := newWorld(t, bHead(changes...))
	if opts.session != "" {
		world.Sessions = append(world.Sessions, bLive("self-repo-7-2", bAWT, opts.session))
	}

	if opts.replies != nil {
		world.Replies[bAWT] = *opts.replies
	}

	return &bRun{t: t, world: world, tab: bNew(world.Deps(file)), file: file}
}

// poll moves the clock on by advance, presses REFRESH and polls.
func (r *bRun) poll(advance float64) {
	r.world.Advance(advance)
	r.tab.Refresh()
	tick(r.tab)
}

// list makes the list the round's PR with changes.
func (r *bRun) list(changes ...func(p *myprs.PR)) { r.world.SetList(bHead(changes...)) }

// saved are the saved launches.
func (r *bRun) saved() []*myprs.Launch { return bLaunches(r.file, bRepo, 7) }

// row is the PR's row.
func (r *bRun) row() *myprs.Row {
	r.t.Helper()

	return bRow(r.t, r.tab)
}

// status is the PR's status.
func (r *bRun) status() string {
	r.t.Helper()

	return r.row().Status
}

// asks are the Greptile asks made.
func (r *bRun) asks() []Ask {
	out := []Ask{}

	r.world.Change(func(w *World) { out = append(out, w.Asks...) })

	return out
}

// failAsks makes an ask fail with err (nil: succeed).
func (r *bRun) failAsks(err error) { r.world.Change(func(w *World) { w.AskErr = err }) }

// sessions makes the live sessions the round's own in these states.
func (r *bRun) sessions(status string) {
	if status == "" {
		bSessions(r.world)

		return
	}

	bSessions(r.world, bLive("self-repo-7-2", bAWT, status))
}

// press presses SELF-REVIEW and answers the code.
func (r *bRun) press() int {
	r.t.Helper()

	return bStart(r.t, r.tab, bRepo, 7, bSonnet).Code
}

// bAsked is the one ask of org/repo#7.
func bAsked() []Ask { return []Ask{{Repo: bRepo, Number: 7}} }

// An ask is made once the session is gone, and the launch records askedSha, askedAt and
// is disarmed.
func TestBAskOnceTheSessionIsGone(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{})
	// Past the wait for a session that never appeared.
	run.world.Advance(6 * bMin)
	run.poll(0)
	bEqual(t, "asks", run.asks(), bAsked())

	launch := run.saved()[0]
	bEqual(t, "launch", []any{bOpt(launch.AskedSha), *launch.AskedAt, launch.Armed},
		[]any{"new", float64(worldT0 + 6*bMin), false})
	bEqual(t, "status", run.status(), "waiting")
}

// No second ask for the same sha across polls.
func TestBNoSecondAskForTheSameSha(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{})
	run.world.Advance(6 * bMin)
	run.poll(0)
	run.poll(2 * bMin)
	run.poll(2 * bMin)
	bEqual(t, "asks", len(run.asks()), 1)
}

// A round whose session pushed nothing is never asked, and once its session is gone the
// status is idle, not running.
func TestBRoundThatPushedNothingIsNeverAsked(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{
		pull: func(p *myprs.PR) { p.HeadSha = "old" }, session: "idle",
		replies: new(float64(worldT0)),
	})
	run.poll(10 * bMin)
	bEqual(t, "live", run.status(), "running")
	run.sessions("")
	run.poll(10 * bMin)
	bEqual(t, "gone", run.status(), "idle")
	bEqual(t, "asks", run.asks(), []Ask{})
}

// A session that has not shown up yet is not gone until the wait for it is over.
func TestBSessionNotShownUpYetIsNotGone(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{launch: func(l *myprs.Launch) { l.At = worldT0 - bMin }})
	run.poll(0)
	bEqual(t, "asks", run.asks(), []Ask{})
	run.poll(5 * bMin)
	bEqual(t, "asks after", len(run.asks()), 1)
}

// A live session that is idle, quiet 60 s with replies.json newer than the launch is
// asked; before 60 s it is not.
func TestBIdleSessionQuietAMinuteIsAsked(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{session: "idle", replies: new(float64(worldT0 - bMin))})
	// The session is first seen idle here (the poll loads the file the ticker watches).
	run.poll(0)
	bEqual(t, "first", run.asks(), []Ask{})
	run.world.Advance(59 * bSecond)
	run.tab.Tick()
	run.poll(0)
	bEqual(t, "59 s", run.asks(), []Ask{})
	run.world.Advance(bSecond)
	run.tab.Tick()
	run.poll(0)
	bEqual(t, "60 s", len(run.asks()), 1)
}

// A session that stopped on a prompt (waiting) is not asked without replies.json, nor
// while waiting.
func TestBWaitingSessionIsNotAsked(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{session: "waiting"})
	run.poll(5 * bMin)
	run.poll(5 * bMin)
	bEqual(t, "asks", run.asks(), []Ask{})
	bEqual(t, "status", run.status(), "running")
	run.world.Change(func(w *World) { w.Replies[bAWT] = w.Now })
	run.poll(5 * bMin)
	bEqual(t, "with replies", run.asks(), []Ask{})
}

// A session that is busy again resets the quiet time.
func TestBBusyAgainResetsTheQuietTime(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{session: "idle", replies: new(float64(worldT0 - bMin))})
	run.poll(0)
	run.world.Advance(50 * bSecond)
	run.sessions("busy")
	run.tab.Tick()
	run.world.Advance(50 * bSecond)
	run.sessions("idle")
	run.tab.Tick()
	run.poll(30 * bSecond)
	bEqual(t, "asks", run.asks(), []Ask{})
}

// Replies.json older than the launch (a reused worktree) does not end the round.
func TestBRepliesOlderThanTheLaunchDoNotEndTheRound(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{session: "idle", replies: new(float64(worldT0 - 2*hourMs))})
	run.poll(0)
	run.tab.Tick()
	run.poll(5 * bMin)
	bEqual(t, "asks", run.asks(), []Ask{})
}

// RepliesAt is only asked about a worktree inside <dataDir>/myprs.
func TestBRepliesAtOnlyInsideTheMyprsFolder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		worktree string
	}{
		{"elsewhere", "/wt/elsewhere"},
		{"climbs out", "/data/myprs/../x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			run := bRound(t, bRoundOpts{launch: func(l *myprs.Launch) { l.Worktree = tt.worktree }})
			run.poll(6 * bMin)
			run.world.Change(func(w *World) { bEqual(t, "asked", w.RepliesCalls, []string(nil)) })
		})
	}

	t.Run("inside", func(t *testing.T) {
		t.Parallel()

		run := bRound(t, bRoundOpts{session: "idle"})
		run.poll(0)
		run.world.Change(func(w *World) {
			if len(w.RepliesCalls) == 0 {
				t.Error("repliesAt was never asked")
			}

			for _, worktree := range w.RepliesCalls {
				bEqual(t, "asked", worktree, bAWT)
			}
		})
	})
}

// Only the newest launch decides the ask: an older armed launch is not used.
func TestBOnlyTheNewestLaunchDecidesTheAsk(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{
		pull: func(p *myprs.PR) { p.HeadSha = "old" },
		extra: []func(l *myprs.Launch){func(l *myprs.Launch) {
			l.HeadSha, l.Armed = "base", true
		}},
	})
	run.poll(10 * bMin)
	bEqual(t, "asks", run.asks(), []Ask{})
	bEqual(t, "armed", run.saved()[0].Armed, true)
}

// A press between a pushing round's close and the next poll is refused, so the push is
// still asked for, and goes through once it was.
func TestBPressBeforeThePollAfterAPushingRoundIsRefused(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{session: "busy"})
	// The session is seen at work.
	run.poll(0)
	run.sessions("")
	run.world.Advance(20 * bSecond)

	row := run.row()
	bEqual(t, "row", []any{row.Status, row.CanStart, bOpt(row.Reason)},
		[]any{"idle", false, bAskOwed})

	refused := bStart(t, run.tab, bRepo, 7, bSonnet)
	bEqual(t, "refused", refused.Code, 409)
	bContains(t, "error", bError(refused), "not been settled yet")
	bEqual(t, "launched", len(bLaunched(run.world)), 0)
	bEqual(t, "saved", bArmedRounds(run.saved()), [][]any{{2.0, true}})
	run.poll(2 * bMin)
	bEqual(t, "asks", run.asks(), bAsked())
	bEqual(t, "status", run.status(), "waiting")
	bEqual(t, "press", run.press(), 200)

	saved := run.saved()
	asked := make([][]any, 0, len(saved))

	for _, launch := range saved {
		asked = append(asked, []any{launch.Round, launch.Armed, bOpt(launch.AskedSha)})
	}

	bEqual(t, "after", asked, [][]any{{2.0, false, "new"}, {2.0, true, nil}})
}

// bArmedRounds are the launches as their round and whether they are armed.
func bArmedRounds(launches []*myprs.Launch) [][]any {
	out := make([][]any, 0, len(launches))
	for _, launch := range launches {
		out = append(out, []any{launch.Round, launch.Armed})
	}

	return out
}

// A press after a round that pushed nothing waits for the poll too: the list can be
// older than a push, and the poll disarms the round.
func TestBPressAfterARoundThatPushedNothingWaitsForThePoll(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{pull: func(p *myprs.PR) { p.HeadSha = "old" }, session: "busy"})
	run.poll(0)
	run.sessions("")
	run.world.Advance(20 * bSecond)
	bEqual(t, "first press", run.press(), 409)
	// A REFRESH 20 s after the session closed settles nothing: the list must be fetched a minute
	// after it was last live.
	run.poll(0)
	bEqual(t, "after refresh", []any{run.saved()[0].Armed, run.row().CanStart, run.press()},
		[]any{true, false, 409})
	run.poll(2 * bMin)
	bEqual(t, "settled", []any{run.saved()[0].Armed, len(run.asks())}, []any{false, 0})
	bEqual(t, "press", run.press(), 200)
}

// The wait for the poll never holds a press for good: not on a draft PR, nor once the
// three tries at the ask are used (the draft PR).
func TestBWaitForThePollNeverHoldsADraftPR(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{pull: func(p *myprs.PR) { p.Draft = true }, session: "busy"})
	run.poll(0)
	run.sessions("")
	run.world.Advance(20 * bSecond)
	bEqual(t, "draft", []any{run.row().CanStart, run.press()}, []any{true, 200})
}

// The wait for the poll never holds a press for good: not on a draft PR, nor once the
// three tries at the ask are used (the tries).
func TestBWaitForThePollEndsWhenTheTriesAreUsed(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{session: "busy"})
	run.poll(0)
	run.sessions("")
	run.failAsks(errBLost)
	run.world.Advance(6 * bMin)
	run.poll(0)
	bEqual(t, "asks", len(run.asks()), 1)
	// A try is still to come.
	bEqual(t, "held", run.press(), 409)

	for range 6 {
		run.poll(2 * bMin)
	}

	launch := run.saved()[0]
	bEqual(t, "tries", []any{len(run.asks()), launch.Armed, bTries(launch)}, []any{3, true, 3})
	bEqual(t, "free", []any{run.row().CanStart, run.press()}, []any{true, 200})
}

// bTries is the launch's tries at the ask, -1 with none.
func bTries(launch *myprs.Launch) int {
	if launch.AskTry == nil {
		return -1
	}

	return launch.AskTry.Tries
}

// A round to paste that was never pasted is armed for good but never waited for: only a
// session that was seen is owed a settling.
func TestBRoundNeverPastedIsNotWaitedFor(t *testing.T) {
	t.Parallel()

	// The head has not moved and the round is manual and unseen, so the poll leaves it armed:
	// only the seen check lets the press through.
	run := bRound(t, bRoundOpts{
		pull: func(p *myprs.PR) { p.HeadSha = "old" },
		launch: func(l *myprs.Launch) {
			l.Manual, l.At = true, worldT0-10*bMin
		},
	})
	run.poll(0)
	bEqual(t, "armed", run.saved()[0].Armed, true)
	bEqual(t, "row", []any{run.row().CanStart, bOpt(run.row().Reason)}, []any{true, nil})
	bEqual(t, "press", run.press(), 200)
}

// A round whose session never appeared is settled by the poll, and no press is held for
// it.
func TestBRoundWhoseSessionNeverAppearedIsSettled(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{launch: func(l *myprs.Launch) { l.At = worldT0 - 10*bMin }})
	run.poll(0)
	bEqual(t, "row", []any{run.row().CanStart, bOpt(run.row().Reason)}, []any{true, nil})
	bEqual(t, "press", run.press(), 200)
}

// A draft PR is never asked or ready.
func TestBDraftPRIsNeverAskedOrReady(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{pull: func(p *myprs.PR) {
		p.Draft = true
		p.Reviews = []myprs.Review{{By: greptileBot, Sha: "new", At: worldT0 + bMin}}
	}})
	run.poll(10 * bMin)
	bEqual(t, "asks", run.asks(), []Ask{})
	bEqual(t, "draft", run.row().Draft, true)

	if run.status() == "ready" {
		t.Error("a draft PR is ready")
	}
}

// A failed comment leaves state unchanged, shows the cleaned error on the row, and
// retries no sooner than 60 s.
func TestBFailedAskShowsTheCleanedErrorAndRetriesAfterAMinute(t *testing.T) {
	t.Parallel()

	run := bRound(t, bRoundOpts{})
	run.failAsks(errBForbidden)
	run.world.Advance(6 * bMin)
	run.poll(0)
	bEqual(t, "asks", len(run.asks()), 1)

	launch := run.saved()[0]
	bEqual(t, "launch", []any{bOpt(launch.AskedSha), launch.AskedAt, launch.Armed},
		[]any{nil, (*float64)(nil), true})

	// One whole sentence from the server: the row shows it as it is.
	shown, _ := bOpt(run.row().Error).(string)
	if !strings.HasPrefix(shown,
		"Could not ask Greptile for a review, trying again: gh: [31mHTTP 403 ") {
		t.Errorf("error %q", shown)
	}

	if len([]rune(shown)) > 300 {
		t.Errorf("error is %d long", len([]rune(shown)))
	}

	if strings.ContainsAny(shown, "\u202e\u001b\n") {
		t.Errorf("error %q keeps a control character", shown)
	}

	run.poll(30 * bSecond)
	bEqual(t, "too soon", len(run.asks()), 1)
	run.failAsks(nil)
	run.poll(30 * bSecond)
	bEqual(t, "retried", len(run.asks()), 2)
	bEqual(t, "error", bOpt(run.row().Error), nil)
	bEqual(t, "armed", run.saved()[0].Armed, false)
}
