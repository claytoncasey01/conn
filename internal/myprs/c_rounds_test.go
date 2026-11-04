package myprs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
)

// The rounds' lifecycle: when a round's session counts as live or gone, when a round is
// disarmed, and when its push is asked for (myPrs.test.ts, from line 1757).

func TestCAskedRoundAfterRestartShowsItsStatusAtOnce(t *testing.T) {
	t.Parallel()

	round := cNewRound(t, cRoundOpts{
		launch: []func(launch *myprs.Launch){cAskedNew}, session: "idle",
		pull: []func(pull *myprs.PR){cReviewed("new")},
	})
	round.poll(0)

	row := round.row()
	cSame(t, "status", row.Status, "ready")
	cSame(t, "canStart", row.CanStart, false)
}

func TestCDryAsksNothingAndReadsNoRepliesFile(t *testing.T) {
	t.Parallel()

	round := cNewRound(t, cRoundOpts{deps: func(deps *myprs.Deps) { deps.Config.Dry = true }})
	round.world.Advance(10 * cMinuteMs)
	round.poll(0)

	round.world.Change(func(w *World) {
		cSame(t, "asks", len(w.Asks), 0)
		cSame(t, "replies calls", len(w.RepliesCalls), 0)
		cSame(t, "searches", w.Calls, 0)
	})
}

func TestCSessionInItsWorktreeThroughASymlinkIsLive(t *testing.T) {
	t.Parallel()

	folder := t.TempDir()
	realWorktree := filepath.Join(folder, "real-round", "wt")

	err := os.MkdirAll(realWorktree, 0o700)
	if err != nil {
		t.Fatalf("making the worktree: %v", err)
	}

	link := filepath.Join(folder, "linked-round")

	err = os.Symlink(filepath.Join(folder, "real-round"), link)
	if err != nil {
		t.Fatalf("linking: %v", err)
	}

	round := cNewRound(t, cRoundOpts{
		pull: []func(pull *myprs.PR){cHead("old")},
		launch: []func(launch *myprs.Launch){func(launch *myprs.Launch) {
			launch.Worktree = filepath.Join(link, "wt")
		}},
	})
	round.setSessions(cSession("self-repo-7-2", realWorktree, "busy"))
	round.poll(2 * cMinuteMs)
	round.poll(2 * cMinuteMs)
	cSame(t, "armed", round.saved()[0].Armed, true)
	cSame(t, "status", round.status(), "running")
	// Its push shows up while it still works: no ask until the round is over.
	round.setPR(pr(cHead("new")))
	round.poll(2 * cMinuteMs)
	cSame(t, "asks while busy", len(round.asks()), 0)
	round.setSessions()
	round.poll(2 * cMinuteMs)
	cSame(t, "asks once gone", len(round.asks()), 1)
}

func TestCRoundEndedWithoutAPushIsDisarmed(t *testing.T) {
	t.Parallel()

	round := cNewRound(t, cRoundOpts{pull: []func(pull *myprs.PR){cHead("old")}, session: "idle"})
	round.poll(0)
	round.setSessions()
	// Gone, a minute and more after it was last live, at the head the round started from.
	round.poll(2 * cMinuteMs)
	cSame(t, "armed", round.saved()[0].Armed, false)
	round.setPR(pr(cHead("pushed-later")))
	round.poll(2 * cMinuteMs)
	round.poll(10 * cMinuteMs)
	cSame(t, "asks", len(round.asks()), 0)
}

func TestCJustEndedSessionIsNotDisarmedWithinAMinute(t *testing.T) {
	t.Parallel()

	round := cNewRound(t, cRoundOpts{pull: []func(pull *myprs.PR){cHead("old")}, session: "idle"})
	round.poll(0)
	round.setSessions()
	// Gone, but the list is only 30 s after the session was last live.
	round.poll(30_000)
	cSame(t, "armed", round.saved()[0].Armed, true)
	round.setPR(pr(cHead("new")))
	round.poll(30_000)
	cSame(t, "asks", len(round.asks()), 1)
	cSame(t, "askedSha", round.saved()[0].AskedSha, new("new"))
}

func TestCOldLaunchWithoutSessionOrPushIsDisarmedAfterARestart(t *testing.T) {
	t.Parallel()

	round := cNewRound(t, cRoundOpts{pull: []func(pull *myprs.PR){cHead("old")}})
	round.poll(10 * cMinuteMs)
	cSame(t, "armed", round.saved()[0].Armed, false)
	cSame(t, "asks", len(round.asks()), 0)
}

func TestCRoundToPasteIsTrackedWhenPastedLate(t *testing.T) {
	t.Parallel()

	round := cNewRound(t, cRoundOpts{
		launch: []func(launch *myprs.Launch){cManual},
		pull:   []func(pull *myprs.PR){cHead("old")},
	})
	// Nobody has pasted it yet: not gone, not disarmed.
	round.poll(30 * cMinuteMs)
	cSame(t, "armed", round.saved()[0].Armed, true)
	round.setSessions(cSession("self-repo-7-2", cCloneWorktree, "busy"))
	round.poll(cMinuteMs)
	round.setPR(pr(cHead("new")))
	round.setSessions()
	round.poll(2 * cMinuteMs)
	cSame(t, "asks", round.asks(), []Ask{{Repo: cRepo, Number: cNumber}})
	cSame(t, "askedSha", round.saved()[0].AskedSha, new("new"))
}

func TestCRoundToPasteNeverPastedIsDisarmedWhenTheHeadMoves(t *testing.T) {
	t.Parallel()

	round := cNewRound(t, cRoundOpts{
		launch: []func(launch *myprs.Launch){cManual},
		pull:   []func(pull *myprs.PR){cHead("old")},
	})
	round.poll(10 * cMinuteMs)
	cSame(t, "armed before", round.saved()[0].Armed, true)
	round.setPR(pr(cHead("pushed-by-hand")))
	round.poll(2 * cMinuteMs)
	cSame(t, "armed after", round.saved()[0].Armed, false)
	round.poll(10 * cMinuteMs)
	cSame(t, "asks", len(round.asks()), 0)
}

var cAlreadyRunning = regexp.MustCompile(`already running`)

func TestCRoundToPasteDisarmedThenPastedRunsAndIsNotAsked(t *testing.T) {
	t.Parallel()

	round := cNewRound(t, cRoundOpts{
		launch: []func(launch *myprs.Launch){cManual},
		pull:   []func(pull *myprs.PR){cHead("old")},
	})
	round.setPR(pr(cHead("pushed-by-hand")))
	round.poll(10 * cMinuteMs)
	cSame(t, "armed", round.saved()[0].Armed, false)
	// Pasted now: at work, it is running and a press is refused as for any running session.
	round.setSessions(cSession("self-repo-7-2", cCloneWorktree, "busy"))
	round.poll(cMinuteMs)

	row := round.row()
	cSame(t, "status running", row.Status, "running")
	cSame(t, "canStart", row.CanStart, false)
	cSame(t, "reason running", row.Reason, new("a session named self-repo-7-2 is running"))

	refused, _ := round.start().Get("error")
	if !cAlreadyRunning.MatchString(js.String(refused)) {
		t.Errorf("the press: got %v, want already running", refused)
	}

	// It pushes and sits idle: only then is it one to close, and its push is never asked for.
	round.setPR(pr(cHead("pushed-by-the-pasted-round")))
	round.setSessions(cSession("self-repo-7-2", cCloneWorktree, "idle"))
	round.poll(2 * cMinuteMs)

	row = round.row()
	cSame(t, "status idle", row.Status, "idle")
	cSame(t, "reason idle", row.Reason,
		new("its session (self-repo-7-2) is still open: close it to start the next round"))
	round.setSessions()
	round.poll(10 * cMinuteMs)
	cSame(t, "asks", len(round.asks()), 0)
}
