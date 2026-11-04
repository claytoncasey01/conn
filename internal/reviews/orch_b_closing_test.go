package reviews_test

// Auto-close beside REVIEW, the close's memory, refused and failed closes, and which worktrees
// are ever matched.

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
)

// bHeldClose is a launch whose review landed, at the moment its close is held at the gate.
func bHeldClose(t *testing.T) (*bHarness, *gate) {
	t.Helper()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.review(1)
	run.at(2)

	held := newGate()

	run.fakes.with(func(fakes *world) {
		fakes.closeGate = held
		fakes.now = worldT0 + (bAfterMin+3)*minuteMs
	})
	run.tab.Tick()
	held.reached(t)

	return run, held
}

// bStillClosing fails the test unless REVIEW is refused while the close is running.
func bStillClosing(t *testing.T, reply reviews.Reply) {
	t.Helper()

	if reply.Code != 409 {
		t.Errorf("start answered %d", reply.Code)
	}

	why, _ := reply.Body.Get("error")
	text, _ := why.(string)

	if !strings.Contains(text, "still being closed") {
		t.Errorf("start said %v", why)
	}
}

func TestBReviewNamesTheCloseInProgressBeforeTheRunningSession(t *testing.T) {
	t.Parallel()

	run, held := bHeldClose(t)
	// The session is still listed as the PR's: both refusals apply.
	bStillClosing(t, run.start(bModel))
	held.open()
	run.tab.Settle()
}

func TestBReviewIsRefusedWhileThePreviousLaunchIsClosedThenWorks(t *testing.T) {
	t.Parallel()

	run, held := bHeldClose(t)
	bSame(t, "closes", bCloses(run.fakes), []string{bPidText})
	run.fakes.with(func(fakes *world) { fakes.sessions = nil })
	bStillClosing(t, run.start(bModel))

	run.fakes.with(func(fakes *world) {
		if len(fakes.launches) != 1 {
			t.Errorf("launches %d", len(fakes.launches))
		}
	})

	held.open()
	run.tab.Settle()
	bClosed(t, "closed", run.launch())

	if reply := run.start(bModel); reply.Code != 200 {
		t.Errorf("start after the close answered %d", reply.Code)
	}
}

func TestBReviewClosingAnEarlierLaunchDropsItsIdleMemory(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.at(1)

	if got := run.tab.BIdleCount(); got != 1 {
		t.Errorf("idle before %d", got)
	}

	run.fakes.with(func(fakes *world) {
		fakes.sessions = nil
		fakes.now = worldT0 + 40*minuteMs
	})

	if reply := run.start(bModel); reply.Code != 200 {
		t.Errorf("start answered %d", reply.Code)
	}

	bClosed(t, "earlier", run.launch())

	if got := run.tab.BIdleCount(); got != 0 {
		t.Errorf("idle after %d", got)
	}
}

func TestBClosedLaunchLeavesNoIdleOrLookMemory(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.review(1)
	run.at(2)
	run.at(bAfterMin + 3)
	bClosed(t, "closed", run.launch())

	if got := run.tab.BIdleCount(); got != 0 {
		t.Errorf("idle %d", got)
	}

	if got := run.tab.BLookCount(); got != 0 {
		t.Errorf("looks %d", got)
	}
}

func TestBPinnedIDFoundInAnotherFolderIsNeverClosed(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.at(1)
	run.fakes.with(func(fakes *world) {
		fakes.sessions = bOnly(fakes.dir, func(session *sessions.LiveSession) {
			session.Cwd = "/Users/someone/elsewhere"
		})
	})
	run.review(2)

	for _, minutes := range []float64{10, 70, 130} {
		run.at(minutes)
	}

	bNone(t, "elsewhere", run.fakes)
	bClosed(t, "elsewhere", run.launch())
}

func TestBAnotherSessionInTheFolderDoesNotStopTheClose(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.review(1)
	run.at(2)
	run.fakes.with(func(fakes *world) {
		fakes.sessions = []sessions.LiveSession{
			bMine(fakes.dir, bPR, nil),
			bMine(fakes.dir, bPR, func(session *sessions.LiveSession) {
				session.SessionID, session.PID = "s-9", 777
			}),
		}
	})
	run.at(bAfterMin + 3)
	bSame(t, "closes", bCloses(run.fakes), []string{bPidText})
	bClosed(t, "closed", run.launch())
	bSame(t, "git", bGits(run.fakes), []string{})
}

func TestBRefusedCloseIsTriedAgainLaterNotEveryTick(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.fakes.with(func(fakes *world) {
		fakes.closeResult = func() (int, string) {
			return 409, "the session's input box is not visibly empty"
		}
	})
	run.review(1)
	run.at(bAfterMin + 2)

	if got := len(bCloses(run.fakes)); got != 1 {
		t.Errorf("closes %d", got)
	}

	bOpen(t, "refused", run.launch())

	for range 10 {
		run.fakes.advance(600)
		run.tab.Tick()
		run.tab.Settle()
	}

	if got := len(bCloses(run.fakes)); got != 1 {
		t.Errorf("closes after ticks %d", got)
	}

	run.fakes.with(func(fakes *world) {
		fakes.closeResult = func() (int, string) { return 200, "" }
	})
	run.at(bAfterMin + 4)

	if got := len(bCloses(run.fakes)); got != 2 {
		t.Errorf("closes later %d", got)
	}

	bClosed(t, "later", run.launch())
}

// The screen read, the close and the session list cannot fail by type (no error return), so the
// failures tried are a close that answers 500 (logged) and a screen that cannot be read: both
// are tried again by a later tick.
func TestBFailureInsideTheCloseIsCaughtAndALaterTickRunsAgain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		fault  func(fakes *world)
		calls  func(fakes *world) int
		logged bool
	}{
		{
			name: "closeWorkspace",
			fault: func(fakes *world) {
				fakes.closeResult = func() (int, string) { return 500, "closeWorkspace broke" }
			},
			calls:  func(fakes *world) int { return len(bCloses(fakes)) },
			logged: true,
		},
		{
			name:   "inputState",
			fault:  func(fakes *world) { fakes.inputState = "unknown" },
			calls:  func(fakes *world) int { return len(bInputs(fakes)) },
			logged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
			logged := bLogTo(run.tab)
			run.review(1)
			run.at(2)
			run.fakes.with(tt.fault)
			run.at(bAfterMin + 3)

			first := tt.calls(run.fakes)
			if first < 1 {
				t.Errorf("calls %d", first)
			}

			if tt.logged {
				if logged.total() == 0 {
					t.Error("nothing was logged")
				}
			}

			run.fakes.advance(31_000)
			run.tab.Tick()
			run.tab.Settle()

			if got := tt.calls(run.fakes); got <= first {
				t.Errorf("a later tick did not run again: %d", got)
			}
		})
	}
}

// bForeign are worktrees that are not exactly the one Conn makes for PR 7 under dir.
func bForeign() []func(dir string) string {
	return []func(dir string) string{
		func(dir string) string { return dir + "/reviews/pr-7/../pr-7/repo" },
		func(dir string) string { return worktreeOf(dir, worldRepo, bPR) + "/" },
		func(dir string) string { return filepath.Join(dir, "reviews") },
		func(dir string) string { return filepath.Join(dir, "reviews") + "/" },
		func(dir string) string { return filepath.Join(dir, "reviews-evil", "pr-7", "repo") },
		func(dir string) string { return worktreeOf(dir, worldRepo, 8) },
		func(dir string) string { return worktreeOf(dir, "jane/widgets", bPR) },
		func(dir string) string { return filepath.Join(dir, "reviews", "pr-7") },
		func(dir string) string { return filepath.Join(dir, "reviews", "pr-8", "widgets") },
		func(dir string) string { return filepath.Join(dir, "reviews", "pr-7", "repo", "sub") },
		func(dir string) string {
			return filepath.Join(filepath.Dir(worktreeOf(dir, worldRepo, bPR)), "other")
		},
		func(string) string { return "/etc/elsewhere" },
		func(string) string { return "reviews/pr-7/repo" },
	}
}

func TestBOnlyTheExactWorktreeIsEverMatchedOrClosed(t *testing.T) {
	t.Parallel()

	for index, worktreeIn := range bForeign() {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			t.Parallel()

			var worktree string

			live := func(dir string) []sessions.LiveSession {
				worktree = worktreeIn(dir)

				return bOnly(dir, func(session *sessions.LiveSession) { session.Cwd = worktree })
			}
			run := bLaunched(t, bLaunchOpts{number: 0, sessions: live})
			// As if the file had been edited.
			run.tab.WithFile(func(file *reviews.File) {
				entry, _ := file.PRs.Get(keyOf(bPR))
				entry.Launches[0].Worktree = worktree
			})
			run.review(1)

			for _, minutes := range []float64{2, 30, 90} {
				run.at(minutes)
			}

			bNone(t, worktree, run.fakes)
			bClosed(t, worktree, run.launch())
			bPinned(t, run.launch(), "")
		})
	}
}

func TestBAutoCloseDoesNothingWhileNoLaunchIsOpen(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{open: []*reviews.PR{bListed(bPR)}})
	fakes.with(func(fakes *world) { fakes.sessions = bOnly(fakes.dir, nil) })
	tickAndSettle(tab)
	fakes.advance(3 * hourMs)
	tab.Tick()
	tab.Settle()
	bSame(t, "reads", bInputs(fakes), []string{})
	bSame(t, "closes", bCloses(fakes), []string{})
}
