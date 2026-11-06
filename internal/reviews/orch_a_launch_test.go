package reviews_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
)

// aPinAndEnd shows PR 7's session at seenAt (one tick pins the launch to it), then has it gone at
// goneAt (one tick closes the launch).
func aPinAndEnd(t *testing.T, tab *reviews.Reviews, fakes *world, seenAt, goneAt float64) {
	t.Helper()

	fakes.with(func(fakes *world) {
		fakes.now = seenAt
		fakes.sessions = []sessions.LiveSession{
			aSession(aName7, worktreeOf(fakes.dir, worldRepo, 7), "idle", "s-1", 501),
		}
	})
	aOnce(tab)
	aEqual(t, "pinned", aText(aLaunches(t, fakes)[0].SessionID), "s-1")

	fakes.with(func(fakes *world) {
		fakes.sessions = nil
		fakes.now = goneAt
	})
	aOnce(tab)

	if aLaunches(t, fakes)[0].ClosedAt == nil {
		t.Error("the launch is not closed")
	}
}

// A launch whose session has appeared (pinned) and then ended no longer blocks a start; one
// that closed unpinned does not either.
func TestEndedPinnedLaunchNoLongerBlocks(t *testing.T) {
	t.Parallel()

	tab, fakes := aReady(t, setupOpts{})
	aStart(t, tab, worldRepo, "7", aSonnet)
	aPinAndEnd(t, tab, fakes, worldT0+60_000, worldT0+61_000)
	aEqual(t, "start", aBody(aStart(t, tab, worldRepo, "7", aSonnet)), aOKName2)
}

// A launch that was pinned within the 30 s window and whose session ended still blocks a
// start until 30 s are up, then does not.
func TestPinnedLaunchEndedEarlyBlocksForThirtySeconds(t *testing.T) {
	t.Parallel()

	tab, fakes := aReady(t, setupOpts{})
	aStart(t, tab, worldRepo, "7", aSonnet)
	// The session shows up about 10 s in and is gone a moment later.
	aPinAndEnd(t, tab, fakes, worldT0+10_000, worldT0+11_000)

	// 29 s after the click: still the double-click window.
	fakes.with(func(fakes *world) { fakes.now = worldT0 + 29_000 })

	early := aStart(t, tab, worldRepo, "7", aOpus)
	aEqual(t, "early", early.Code, 409)

	if !strings.Contains(aField(early, "error"), "just started") {
		t.Errorf("error %q", aField(early, "error"))
	}

	aEqual(t, "launches", aLaunchCount(fakes), 1)

	// 31 s after it: the other rules decide, and they allow it.
	fakes.with(func(fakes *world) { fakes.now = worldT0 + 31_000 })
	aEqual(t, "later", aBody(aStart(t, tab, worldRepo, "7", aOpus)), aOKName2)
	aEqual(t, "launches after", aLaunchCount(fakes), 2)
}

// aStarted is a Start's answer from another goroutine.
type aStarted struct {
	reply reviews.Reply
	err   error
}

// Two clicks at once launch once.
func TestTwoClicksAtOnceLaunchOnce(t *testing.T) {
	t.Parallel()

	tab, fakes := aReady(t, setupOpts{})
	held := newGate()

	fakes.with(func(fakes *world) {
		fakes.launchResult = func(opts reviews.LaunchOpts) reviews.LaunchResult {
			held.pass()

			return reviews.LaunchResult{
				OK: true, Worktree: worktreeOf(fakes.dir, opts.Repo, opts.Number), Error: "",
				ManualCommand: "",
			}
		}
	})

	first := make(chan aStarted, 1)

	go func() {
		reply, err := tab.Start(t.Context(), worldRepo, "7", aSonnet)
		first <- aStarted{reply: reply, err: err}
	}()

	held.reached(t)
	aEqual(t, "second", aStart(t, tab, worldRepo, "7", aSonnet).Code, 409)
	held.open()

	answer := <-first
	if answer.err != nil {
		t.Fatal(answer.err)
	}

	aEqual(t, "first", answer.reply.Code, 200)
	aEqual(t, "launches", aLaunchCount(fakes), 1)
}

// A successful start launches with the PR's base branch, answers the name, and saves the
// Launch with the head sha.
func TestSuccessfulStartLaunchesAndSaves(t *testing.T) {
	t.Parallel()

	tab, fakes := aReady(t, setupOpts{open: []*reviews.PR{prOf(func(pr *reviews.PR) {
		pr.Requested, pr.BaseRef, pr.HeadSha = []string{worldMe}, "staging", "c0ffee1"
	})}})
	bumps := fakes.snapshotCalls().bump
	reply := aStart(t, tab, worldRepo, "7", aOpus)

	aEqual(t, "code", reply.Code, 200)
	aEqual(t, "body", aBody(reply), aOKName)
	// The launch is made in the repo's own clone.
	fakes.with(func(fakes *world) {
		aEqual(t, "launches", fakes.launches, []reviews.LaunchOpts{{
			Repo: worldRepo, Number: 7, BaseRef: "staging", Model: aOpus, Name: aName7,
			RepoPath: aClone, DataDir: "", ReReviewSha: nil,
		}})
	})
	aEqual(t, "saved", aLaunches(t, fakes), []*reviews.Launch{{
		At: worldT0, Model: aOpus, Name: aName7, HeadSha: "c0ffee1",
		Worktree: worktreeOf(fakes.dir, worldRepo, 7), SessionID: nil, ClosedAt: nil, Clone: aClone,
	}})

	if fakes.snapshotCalls().bump <= bumps {
		t.Error("no bump")
	}

	aEqual(t, "row launches", view(t, tab).Rows[0].Launches, 1)
}

// aLaunchOK is a launch that worked.
func aLaunchOK(fakes *world) func(opts reviews.LaunchOpts) reviews.LaunchResult {
	return func(opts reviews.LaunchOpts) reviews.LaunchResult {
		return reviews.LaunchResult{
			OK: true, Worktree: worktreeOf(fakes.dir, opts.Repo, opts.Number), Error: "",
			ManualCommand: "",
		}
	}
}

// aLaunchFails is a launch that failed with why (and the command to paste, when manual is set).
func aLaunchFails(why, manual string) func(reviews.LaunchOpts) reviews.LaunchResult {
	return func(reviews.LaunchOpts) reviews.LaunchResult {
		return reviews.LaunchResult{OK: false, Worktree: "", Error: why, ManualCommand: manual}
	}
}

// A cmux failure is a 200 with the command to paste, records no Launch, and can be tried
// again at once.
func TestCmuxFailureAnswersTheCommandToPaste(t *testing.T) {
	t.Parallel()

	tab, fakes := aReady(t, setupOpts{})
	manual := "cd /w && claude --name pr-7"

	fakes.with(func(fakes *world) {
		fakes.launchResult = aLaunchFails("cmux is not running", manual)
	})

	reply := aStart(t, tab, worldRepo, "7", aSonnet)
	aEqual(t, "code", reply.Code, 200)
	aEqual(t, "body", aBody(reply),
		`{"ok":false,"error":"cmux is not running","manualCommand":"`+manual+`"}`)
	aEqual(t, "saved", len(aLaunches(t, fakes)), 0)
	aEqual(t, "row launches", view(t, tab).Rows[0].Launches, 0)

	fakes.with(func(fakes *world) { fakes.launchResult = aLaunchOK(fakes) })
	aEqual(t, "again", aStart(t, tab, worldRepo, "7", aSonnet).Code, 200)
	aEqual(t, "launches", aLaunchCount(fakes), 2)
}

// A review set to open as a command (or one whose session's start failed after the worktree was
// made) is recorded as launched, by hand: the worktree is the launch's, the next press is the next
// round, and the answer is the command to paste.
func TestAReviewOpenedAsACommandIsRecorded(t *testing.T) {
	t.Parallel()

	tab, fakes := aReady(t, setupOpts{})
	manual := "cd /w && claude --name pr-7"

	fakes.with(func(fakes *world) {
		fakes.launchResult = func(opts reviews.LaunchOpts) reviews.LaunchResult {
			return reviews.LaunchResult{
				OK: false, Worktree: worktreeOf(fakes.dir, opts.Repo, opts.Number),
				Error: "sessions open as a command", ManualCommand: manual,
			}
		}
	})

	reply := aStart(t, tab, worldRepo, "7", aSonnet)
	aEqual(t, "code", reply.Code, 200)
	aEqual(t, "body", aBody(reply),
		`{"ok":false,"error":"sessions open as a command","manualCommand":"`+manual+`"}`)
	saved := aLaunches(t, fakes)
	aEqual(t, "saved", len(saved), 1)

	if len(saved) == 1 {
		aEqual(t, "worktree", saved[0].Worktree, worktreeOf(fakes.dir, worldRepo, 7))
		aEqual(t, "name", saved[0].Name, aName7)
	}

	aEqual(t, "row launches", view(t, tab).Rows[0].Launches, 1)

	// Like any launch, it holds the PR while its session may still appear.
	fakes.with(func(fakes *world) { fakes.now = worldT0 + 31_000 })
	aEqual(t, "while it may appear", aStart(t, tab, worldRepo, "7", aSonnet).Code, 409)

	fakes.with(func(fakes *world) {
		fakes.now = worldT0 + 6*60_000
		fakes.launchResult = aLaunchOK(fakes)
	})
	aEqual(t, "the next round", aBody(aStart(t, tab, worldRepo, "7", aSonnet)), aOKName2)
}

// A review launched by hand leaves the earlier launch as it was: nothing started yet, so the
// earlier session keeps its tracking (its close after the review) until a session really starts.
func TestAReviewLaunchedByHandRetiresNoEarlierLaunch(t *testing.T) {
	t.Parallel()

	tab, fakes := aReady(t, setupOpts{})
	fakes.with(func(fakes *world) { fakes.launchResult = aLaunchOK(fakes) })
	aEqual(t, "first", aStart(t, tab, worldRepo, "7", aSonnet).Code, 200)

	fakes.with(func(fakes *world) {
		fakes.now = worldT0 + 6*60_000
		fakes.launchResult = func(opts reviews.LaunchOpts) reviews.LaunchResult {
			return reviews.LaunchResult{
				OK: false, Worktree: worktreeOf(fakes.dir, opts.Repo, opts.Number),
				Error: "sessions open as a command", ManualCommand: "cd /w && claude",
			}
		}
	})
	aEqual(t, "by hand", aStart(t, tab, worldRepo, "7", aSonnet).Code, 200)

	saved := aLaunches(t, fakes)
	aEqual(t, "launches", len(saved), 2)

	if saved[0].ClosedAt != nil {
		t.Error("the earlier launch was retired by a launch nothing started")
	}
}

// aPanicked is what a call panicked with ("" when it did not).
func aPanicked(call func()) string {
	found := ""

	func() {
		defer func() {
			if value := recover(); value != nil {
				found = fmt.Sprint(value)
			}
		}()

		call()
	}()

	return found
}

// A git failure (no manual command) is the same shape, and a launch that panics leaves
// start free to try again.
func TestFailedOrThrowingLaunchLeavesStartFree(t *testing.T) {
	t.Parallel()

	tab, fakes := aReady(t, setupOpts{})

	fakes.with(func(fakes *world) {
		fakes.launchResult = aLaunchFails("fatal: could not read from remote", "")
	})
	aEqual(t, "git failure", aBody(aStart(t, tab, worldRepo, "7", aSonnet)),
		`{"ok":false,"error":"fatal: could not read from remote"}`)

	fakes.with(func(fakes *world) {
		fakes.launchResult = func(reviews.LaunchOpts) reviews.LaunchResult { panic("boom") }
	})

	thrown := aPanicked(func() {
		_, _ = tab.Start(t.Context(), worldRepo, "7", aSonnet)
	})
	aEqual(t, "thrown", thrown, "boom")
	// Nothing was saved, so a 409 now can only be the start still marked as running.
	aEqual(t, "saved", len(aLaunches(t, fakes)), 0)

	fakes.with(func(fakes *world) { fakes.launchResult = aLaunchOK(fakes) })

	again := aStart(t, tab, worldRepo, "7", aSonnet)
	aEqual(t, "start after a throw", again.Code, 200)
	aEqual(t, "start after a throw (body)", aBody(again), aOKName)
}

// REVIEW closes out an earlier launch that is still open (the new one owns the worktree).
func TestReviewClosesOutAnEarlierOpenLaunch(t *testing.T) {
	t.Parallel()

	tab, fakes := aReady(t, setupOpts{})
	aStart(t, tab, worldRepo, "7", aSonnet)
	// The first launch never showed up as a session; its 5 minutes are over, and no tick has
	// closed it yet.
	fakes.advance(5 * minuteMs)
	aStart(t, tab, worldRepo, "7", aSonnet)

	saved := aLaunches(t, fakes)
	aEqual(t, "launches", len(saved), 2)

	if saved[0].ClosedAt == nil {
		t.Fatal("the first launch is open")
	}

	aEqual(t, "first closed", *saved[0].ClosedAt, fakes.clock())
	aEqual(t, "second open", saved[1].ClosedAt, (*float64)(nil))
	fakes.with(func(fakes *world) { aEqual(t, "closes", len(fakes.closeCalls), 0) })
}
