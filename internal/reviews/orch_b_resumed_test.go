package reviews_test

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
)

// bAppearMin is how long a launched session may take to appear, in minutes (appearMs).
const bAppearMin = 5

// launchedInStore is whether the store on disk still holds the harness's launch open, as the
// next launch of Conn reads it to resume the review.
func launchedInStore(run *bHarness) bool {
	launch := run.launch()
	if launch.SessionID == nil {
		return false
	}

	file := reviews.LoadFile(run.fakes.file)
	_, found := reviews.LaunchedIn(file, launch.Worktree, launch.Name, *launch.SessionID)

	return found
}

// relaunch is Conn launching again: its sessions stopped by the quit, the session list empty,
// and the launch resuming its recorded sessions until settle.
func (run *bHarness) relaunch() {
	run.fakes.with(func(fakes *world) {
		fakes.sessions = nil
		fakes.resuming = true
	})
}

// settle is the launch done resuming at minutes, with the resumed session back on its own
// conversation and worktree.
func (run *bHarness) settle(minutes float64, back bool) {
	run.fakes.with(func(fakes *world) {
		fakes.resuming = false
		fakes.settledAt = worldT0 + minutes*minuteMs

		if back {
			fakes.sessions = []sessions.LiveSession{bMine(fakes.dir, bPR, nil)}
		}
	})
}

// A review a launch resumes is not closed while the launch resumes nor before its session can
// register, so it stays open and resumes again at every later launch.
func TestBAResumedReviewStaysOpenAcrossRelaunches(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.at(1)

	for relaunch, minutes := range []float64{2, 20} {
		run.relaunch()
		run.at(minutes)
		run.settle(minutes+0.1, false)
		run.at(minutes + 0.15)
		run.settle(minutes+0.1, true)
		run.at(minutes + 0.3)

		if !launchedInStore(run) {
			t.Fatalf("relaunch %d: the resumed review's launch was closed", relaunch+1)
		}
	}
}

// A session that does not come back once the launch has settled is gone: its launch is closed
// after the time a session takes to appear.
func TestBASessionNotBackAfterTheLaunchSettledIsClosed(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.at(1)
	run.relaunch()
	run.at(2)
	run.settle(2, false)
	run.at(3)
	bOpen(t, "within the time to appear", run.launch())
	run.at(2 + bAppearMin)
	bClosedAtMin(t, run.launch(), 2+bAppearMin)
}
