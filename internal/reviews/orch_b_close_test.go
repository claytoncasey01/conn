package reviews_test

// Auto-close: a review session Conn launched is closed once it is finished, and only that one.

import (
	"slices"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
)

// bSame fails the test when got is not want.
func bSame(t *testing.T, label string, got, want []string) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("%s = %q, want %q", label, got, want)
	}
}

// bNone fails the test when nothing was read, closed or run is not so.
func bNone(t *testing.T, label string, fakes *world) {
	t.Helper()

	bSame(t, label+": screen reads", bInputs(fakes), []string{})
	bSame(t, label+": closes", bCloses(fakes), []string{})
	bSame(t, label+": git", bGits(fakes), []string{})
}

// bClosed fails the test unless the launch is closed.
func bClosed(t *testing.T, label string, launch *reviews.Launch) {
	t.Helper()

	if launch.ClosedAt == nil {
		t.Errorf("%s: the launch is not closed", label)
	}
}

// bOpen fails the test unless the launch is open.
func bOpen(t *testing.T, label string, launch *reviews.Launch) {
	t.Helper()

	if launch.ClosedAt != nil {
		t.Errorf("%s: the launch was closed at %v", label, *launch.ClosedAt)
	}
}

// bClosedAtMin fails the test unless the launch closed min minutes after worldT0.
func bClosedAtMin(t *testing.T, launch *reviews.Launch, minutes float64) {
	t.Helper()

	if launch.ClosedAt == nil {
		t.Fatal("the launch is not closed")
	}

	if *launch.ClosedAt != worldT0+minutes*minuteMs {
		t.Errorf("closed at %v, want %v", *launch.ClosedAt, worldT0+minutes*minuteMs)
	}
}

// bPinned fails the test unless the launch is pinned to id ("" for not pinned).
func bPinned(t *testing.T, launch *reviews.Launch, sessionID string) {
	t.Helper()

	got := ""
	if launch.SessionID != nil {
		got = *launch.SessionID
	}

	if got != sessionID {
		t.Errorf("pinned to %q, want %q", got, sessionID)
	}
}

func bOnly(dir string, change func(session *sessions.LiveSession)) []sessions.LiveSession {
	return []sessions.LiveSession{bMine(dir, bPR, change)}
}

func TestBIdleWithReviewAndEmptyBoxIsClosedOnceAndWorktreeLeft(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.review(1)
	run.at(1)
	bPinned(t, run.launch(), bSession)

	// The screen is not read before a threshold is met on time alone.
	run.at(bAfterMin - 1)
	run.at(bAfterMin)
	bSame(t, "reads before", bInputs(run.fakes), []string{})
	bSame(t, "closes before", bCloses(run.fakes), []string{})

	run.at(bAfterMin + 1)
	bSame(t, "reads", bInputs(run.fakes), []string{bPidText})
	bSame(t, "closes", bCloses(run.fakes), []string{bPidText})
	bClosedAtMin(t, run.launch(), bAfterMin+1)
	// No git call at all: a close never removes the worktree.
	bSame(t, "git", bGits(run.fakes), []string{})

	// Once.
	run.at(bAfterMin + 5)
	run.at(bAfterMin + 60)

	if got := len(bCloses(run.fakes)); got != 1 {
		t.Errorf("closes %d", got)
	}

	if got := len(bInputs(run.fakes)); got != 1 {
		t.Errorf("reads %d", got)
	}

	// A later poll, with the PR still open, leaves it too.
	run.refresh()
	run.at(bAfterMin + 70)

	if got := run.fakes.snapshotCalls().list; got < 2 {
		t.Errorf("searches %d", got)
	}

	bSame(t, "git after", bGits(run.fakes), []string{})
}

func TestBFirstPinNeedsTheLaunchName(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: func(dir string) []sessions.LiveSession {
		return bOnly(dir, func(session *sessions.LiveSession) { session.Name = "something-else" })
	}})
	run.at(1)
	run.at(bAfterMin + 3)
	bPinned(t, run.launch(), "")
	bSame(t, "reads", bInputs(run.fakes), []string{})
	bSame(t, "closes", bCloses(run.fakes), []string{})

	// It never appeared under the name claude was started with: given up after five minutes,
	// and nothing is closed.
	run.at(6)
	bClosed(t, "after five minutes", run.launch())
	bSame(t, "closes after", bCloses(run.fakes), []string{})
}

func TestBSecondLaunchIsPinnedOnlyToTheSessionNamedForIt(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.at(1)
	// The first session ends; the second review is launched; the old name is not adopted by it.
	run.fakes.with(func(fakes *world) { fakes.sessions = nil })
	run.at(2)
	bClosed(t, "first", run.launch())
	run.fakes.with(func(fakes *world) { fakes.now = worldT0 + 3*minuteMs })

	reply := run.start("opus")
	if got := js.Stringify(reply.Body); got != `{"ok":true,"name":"pr-7.2-acme--widgets"}` {
		t.Errorf("start %d %s", reply.Code, got)
	}

	run.fakes.with(func(fakes *world) {
		fakes.sessions = bOnly(fakes.dir, func(session *sessions.LiveSession) {
			session.SessionID, session.PID = "s-old", 777
		})
	})
	run.at(4)

	launches := bLaunches(run.fakes.file, bPR)
	if len(launches) != 2 {
		t.Fatalf("launches %d", len(launches))
	}

	bPinned(t, launches[1], "")
	run.fakes.with(func(fakes *world) {
		fakes.sessions = bOnly(fakes.dir, func(session *sessions.LiveSession) {
			session.Name, session.SessionID, session.PID = "pr-7.2-acme--widgets", "s-new", 778
		})
	})
	run.at(5)
	bPinned(t, bLaunches(run.fakes.file, bPR)[1], "s-new")
}

func TestBIdleWithNoReviewWaitsForTheCap(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.at(0)
	run.at(bAfterMin + 5)
	run.at(bCapMin - 1)
	bSame(t, "reads", bInputs(run.fakes), []string{})
	bSame(t, "closes", bCloses(run.fakes), []string{})
	bOpen(t, "before the cap", run.launch())
	run.at(bCapMin)
	bSame(t, "closes at the cap", bCloses(run.fakes), []string{bPidText})
	bClosed(t, "at the cap", run.launch())
}

func TestBReviewBeforeTheLaunchDoesNotCount(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.fakes.with(func(fakes *world) {
		fakes.open = []*reviews.PR{prOf(func(pr *reviews.PR) {
			pr.Requested = []string{worldMe}
			pr.Reviews = []reviews.Review{myReview(func(review *reviews.Review) {
				review.SubmittedAt = worldT0 - minuteMs
			})}
		})}
	})
	run.refresh()
	run.at(0)
	run.at(bAfterMin + 5)
	bSame(t, "closes", bCloses(run.fakes), []string{})
}

func TestBTypedDraftBlocksTheCloseAndTheScreenIsNotReadEveryTick(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.fakes.with(func(fakes *world) { fakes.inputState = "draft" })
	run.review(1)
	run.at(bAfterMin + 2)

	if got := len(bInputs(run.fakes)); got != 1 {
		t.Errorf("reads %d", got)
	}

	// 600 ms ticks for a while: no more reads.
	for range 20 {
		run.fakes.advance(600)
		run.tab.Tick()
		run.tab.Settle()
	}

	if got := len(bInputs(run.fakes)); got != 1 {
		t.Errorf("reads after ticks %d", got)
	}

	run.at(bCapMin + 30)
	bSame(t, "closes", bCloses(run.fakes), []string{})

	if got := len(bInputs(run.fakes)); got < 2 {
		t.Errorf("reads by the cap %d", got)
	}

	// The draft is sent or cleared: closed at the next look.
	run.fakes.with(func(fakes *world) { fakes.inputState = "empty" })
	run.at(bCapMin + 40)
	bSame(t, "closes after", bCloses(run.fakes), []string{bPidText})
}

func TestBUnreadableOrOtherScreenNeverCloses(t *testing.T) {
	t.Parallel()

	for _, state := range []string{"unknown", "none"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()

			run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
			run.fakes.with(func(fakes *world) { fakes.inputState = state })
			run.review(1)
			run.at(bAfterMin + 2)
			run.at(bCapMin + 5)

			if len(bInputs(run.fakes)) < 1 {
				t.Error("the screen was not read")
			}

			bSame(t, "closes", bCloses(run.fakes), []string{})
		})
	}
}

func TestBBusyOrWaitingResetsTheIdleTimer(t *testing.T) {
	t.Parallel()

	for _, interrupt := range []string{bBusy, "waiting"} {
		t.Run(interrupt, func(t *testing.T) {
			t.Parallel()

			run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
			run.review(1)
			run.at(2)
			run.at(bAfterMin)
			run.fakes.with(func(fakes *world) {
				fakes.sessions = bOnly(fakes.dir, func(session *sessions.LiveSession) {
					session.Status = interrupt
				})
			})
			run.at(bAfterMin + 1)
			run.fakes.with(func(fakes *world) { fakes.sessions = bOnly(fakes.dir, nil) })
			run.at(bAfterMin + 2)
			// Ten minutes since the first idle, but only eight since it went idle again.
			run.at(bAfterMin + 2 + bAfterMin - 1)
			bSame(t, "reads", bInputs(run.fakes), []string{})
			bSame(t, "closes", bCloses(run.fakes), []string{})
			run.at(bAfterMin + 2 + bAfterMin)
			bSame(t, "closes after", bCloses(run.fakes), []string{bPidText})
		})
	}
}

func TestBScreenReadChangesStopTheClose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(session *sessions.LiveSession)
	}{
		// A session that turns busy while the screen is read is not closed.
		{name: "busy", change: func(session *sessions.LiveSession) { session.Status = bBusy }},
		// A session whose folder changes during the screen read is not closed.
		{name: "folder", change: func(session *sessions.LiveSession) {
			session.Cwd = "/Users/someone/elsewhere"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
			run.review(1)
			run.at(2)
			run.fakes.with(func(fakes *world) {
				dir := fakes.dir
				fakes.onInputState = func() {
					run.fakes.with(func(fakes *world) { fakes.sessions = bOnly(dir, tt.change) })
				}
			})
			run.at(bAfterMin + 3)

			if got := len(bInputs(run.fakes)); got != 1 {
				t.Errorf("reads %d", got)
			}

			bSame(t, "closes", bCloses(run.fakes), []string{})
		})
	}
}

func TestBCloseReadsNoGitStatusCleanOrDirty(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.fakes.with(func(fakes *world) { fakes.porcelain = " M src/a.ts\n?? notes.md\n" })
	run.review(1)
	run.at(2)
	run.at(bAfterMin + 3)
	bSame(t, "closes", bCloses(run.fakes), []string{bPidText})
	bClosed(t, "closed", run.launch())
	bSame(t, "git", bGits(run.fakes), []string{})
}

func TestBManualSessionInAnotherFolderIsNeverClosedOrPinned(t *testing.T) {
	t.Parallel()

	const number = 1234

	run := bLaunched(t, bLaunchOpts{number: number, sessions: func(string) []sessions.LiveSession {
		return []sessions.LiveSession{{
			Name: bName(number), Cwd: "/Users/someone/projects/repo", Status: bIdle,
			SessionID: "manual", PID: 900,
		}}
	}})
	run.review(1)

	for _, minutes := range []float64{2, 15, 61, 120} {
		run.at(minutes)
	}

	bNone(t, "manual", run.fakes)
	// The launch's own session never appeared: after five minutes it is marked closed, with no
	// close call.
	bPinned(t, run.launch(), "")
	bClosed(t, "never appeared", run.launch())
}

func TestBOncePinnedOnlyThatSessionIDIsFollowed(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.at(1)
	bPinned(t, run.launch(), bSession)
	run.fakes.with(func(fakes *world) {
		fakes.sessions = []sessions.LiveSession{
			bMine(fakes.dir, bPR, func(session *sessions.LiveSession) { session.Status = bBusy }),
			{
				Name: bName(bPR), Cwd: "/Users/someone/projects/repo", Status: bIdle,
				SessionID: "manual", PID: 900,
			},
		}
	})

	for _, minutes := range []float64{10, 30, 70, 120} {
		run.at(minutes)
	}

	bSame(t, "reads", bInputs(run.fakes), []string{})
	bSame(t, "closes", bCloses(run.fakes), []string{})
	bOpen(t, "followed", run.launch())
}

func TestBLaunchWhoseSessionNeverAppearsIsClosedAfterFiveMinutes(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: func(string) []sessions.LiveSession {
		return nil
	}})
	run.at(4)
	bOpen(t, "at four minutes", run.launch())
	run.at(5)
	bClosedAtMin(t, run.launch(), 5)
	bSame(t, "closes", bCloses(run.fakes), []string{})
	bSame(t, "git", bGits(run.fakes), []string{})
}

func TestBPinnedSessionGoneIsClosedWithNothingRemoved(t *testing.T) {
	t.Parallel()

	t.Run("gone", func(t *testing.T) {
		t.Parallel()

		run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
		run.at(1)
		run.fakes.with(func(fakes *world) { fakes.sessions = nil })
		run.at(2)
		bClosedAtMin(t, run.launch(), 2)
		bSame(t, "closes", bCloses(run.fakes), []string{})
		bSame(t, "git", bGits(run.fakes), []string{})
	})

	// A registry that dropped the session for one pass reads the same: closed, nothing deleted
	// under a session that comes back.
	t.Run("back", func(t *testing.T) {
		t.Parallel()

		run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
		run.at(1)
		run.fakes.with(func(fakes *world) { fakes.sessions = nil })
		run.at(2)
		run.fakes.with(func(fakes *world) { fakes.sessions = bOnly(fakes.dir, nil) })
		run.at(3)
		run.at(60)
		bClosed(t, "back", run.launch())
		bSame(t, "git", bGits(run.fakes), []string{})
		bSame(t, "closes", bCloses(run.fakes), []string{})
	})
}

func TestBClearRepinsTheLaunchWhichClosesUnderTheNewID(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.at(1)
	bPinned(t, run.launch(), bSession)
	run.fakes.with(func(fakes *world) {
		fakes.sessions = bOnly(fakes.dir, func(session *sessions.LiveSession) {
			session.SessionID = "s-2"
		})
	})
	run.review(2)
	bPinned(t, run.launch(), "s-2")
	bOpen(t, "re-pinned", run.launch())
	run.at(2 + bAfterMin - 1)
	bSame(t, "closes before", bCloses(run.fakes), []string{})
	run.at(2 + bAfterMin)
	bSame(t, "closes", bCloses(run.fakes), []string{bPidText})
	bClosed(t, "closed", run.launch())
	bSame(t, "git", bGits(run.fakes), []string{})
}

func TestBSessionOfAnotherProcessInTheWorktreeIsNotAdopted(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.at(1)
	bPinned(t, run.launch(), bSession)
	// Jane's own claude, run in the review folder.
	run.fakes.with(func(fakes *world) {
		fakes.sessions = bOnly(fakes.dir, func(session *sessions.LiveSession) {
			session.SessionID, session.PID = "s-jane", 999
		})
	})
	run.review(2)

	for _, minutes := range []float64{10, 70, 130} {
		run.at(minutes)
	}

	bClosed(t, "left", run.launch())
	bPinned(t, run.launch(), bSession)
	bNone(t, "another process", run.fakes)
}

func TestBAfterRestartNoSessionIsAdoptedByAPinnedLaunch(t *testing.T) {
	t.Parallel()

	t.Run("new id", func(t *testing.T) {
		t.Parallel()

		run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
		run.at(1)
		run.tab.BForgetPids()
		run.fakes.with(func(fakes *world) {
			fakes.sessions = bOnly(fakes.dir, func(session *sessions.LiveSession) {
				session.SessionID = "s-2"
			})
		})
		run.review(2)

		for _, minutes := range []float64{30, 90} {
			run.at(minutes)
		}

		bClosed(t, "not adopted", run.launch())
		bNone(t, "not adopted", run.fakes)
	})

	// One that still has the pinned id carries on.
	t.Run("same id", func(t *testing.T) {
		t.Parallel()

		run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
		run.review(1)
		run.at(2)
		run.tab.BForgetPids()
		run.at(bAfterMin + 3)
		bSame(t, "closes", bCloses(run.fakes), []string{bPidText})
	})
}

func TestBTwoProcessesWithTheSameIDCloseTheOneWhoseScreenWasRead(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.review(1)
	run.at(2)
	run.fakes.with(func(fakes *world) {
		fakes.sessions = []sessions.LiveSession{
			bMine(fakes.dir, bPR, nil),
			bMine(fakes.dir, bPR, func(session *sessions.LiveSession) { session.PID = 777 }),
		}
	})
	run.at(bAfterMin + 3)
	bSame(t, "closes", bCloses(run.fakes), []string{bPidText})
	bSame(t, "git", bGits(run.fakes), []string{})
}
