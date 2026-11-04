package reviews_test

// REFRESH stays busy until the work it asked for has finished. Bun's reviews.test.ts
// "---- REFRESH stays busy until the work it asked for has finished ----", up to the press while
// GitHub is off.

import (
	"encoding/json"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

// bPresses are the REFRESH presses counted, as the snapshot carries them.
type bPresses struct {
	Seq  int `json:"seq"`
	Done int `json:"done"`
}

// bPressesOf is the snapshot's refresh count, of a live or a dry view.
func bPressesOf(t *testing.T, tab *reviews.Reviews) bPresses {
	t.Helper()

	data, err := json.Marshal(tab.Snapshot())
	if err != nil {
		t.Fatal(err)
	}

	var shown struct {
		Refresh bPresses `json:"refresh"`
	}

	err = json.Unmarshal(data, &shown)
	if err != nil {
		t.Fatal(err)
	}

	return shown.Refresh
}

// bPressed fails the test unless the snapshot counts seq presses, done of them finished.
func bPressed(t *testing.T, label string, tab *reviews.Reviews, seq, done int) {
	t.Helper()

	got := bPressesOf(t, tab)
	if got != (bPresses{Seq: seq, Done: done}) {
		t.Errorf("%s: refresh %+v, want seq %d done %d", label, got, seq, done)
	}
}

func bBumps(fakes *world) int { return fakes.snapshotCalls().bump }

func TestBFreshViewHasNoRefreshPendingAndRefreshCounts(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})
	bPressed(t, "fresh", tab, 0, 0)

	bumps := bBumps(fakes)

	bCount(t, "first press", bRefresh(t, tab), 1)
	bCount(t, "second press", bRefresh(t, tab), 2)
	bPressed(t, "pressed", tab, 2, 0)

	if bBumps(fakes) <= bumps {
		t.Error("no snapshot was sent")
	}
}

func TestBRefreshIsPendingUntilItsPollHasFinished(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})
	tickAndSettle(tab)

	held := newGate()
	t.Cleanup(held.open)
	fakes.with(func(fakes *world) { fakes.listGate = held })
	bRefresh(t, tab)
	tab.Tick()
	held.reached(t)
	bPressed(t, "polling", tab, 1, 0)

	bumps := bBumps(fakes)

	held.open()
	tab.Settle()
	bPressed(t, "polled", tab, 1, 1)

	if bBumps(fakes) <= bumps {
		t.Error("the finished refresh did not go out on a snapshot")
	}
}

func TestBPollThatFailsStillEndsTheRefresh(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})
	tickAndSettle(tab)
	fakes.with(func(fakes *world) { fakes.listErr = errBGhDown })
	bRefresh(t, tab)
	tickAndSettle(tab)

	if shown := view(t, tab).Error; shown == nil {
		t.Error("no error")
	} else if *shown != "gh down" {
		t.Errorf("error %s", *shown)
	}

	bPressed(t, "failed", tab, 1, 1)
}

func TestBRefreshThatTriggersAScanIsPendingUntilTheScanHasFinished(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{})
	signedOut := reviews.ScanResult{
		Posts: []reviews.Post{}, Asks: []reviews.Ask{}, NeedsAuth: []string{"claude.ai Slack"},
	}

	fakes.with(func(fakes *world) {
		fakes.scanResult = func() (reviews.ScanResult, error) { return signedOut, nil }
	})
	tickAndSettle(tab)
	tickAndSettle(tab)
	bCount(t, "scans", len(fakes.snapshotCalls().scan), 1)

	held := newGate()
	t.Cleanup(held.open)
	fakes.with(func(fakes *world) {
		fakes.scanResult = func() (reviews.ScanResult, error) {
			held.pass()

			return signedOut, nil
		}
		// A moment on, so the end of this poll shows in the snapshot.
		fakes.now++
	})
	bRefresh(t, tab)
	tab.Tick()
	held.reached(t)
	bCount(t, "the scan started in the same tick as the poll", len(fakes.snapshotCalls().scan), 2)

	now := fakes.clock()

	bWaitFor(t, "the poll", func() bool { return bPolledAt(t, tab) == now })
	bPressed(t, "the poll is over, the scan is not", tab, 1, 0)
	held.open()
	tab.Settle()
	bPressed(t, "scanned", tab, 1, 1)
}

func TestBRefreshDuringABackgroundPollIsNotFinishedByIt(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})
	background := newGate()
	t.Cleanup(background.open)
	fakes.with(func(fakes *world) { fakes.listGate = background })
	// The first, scheduled poll.
	tab.Tick()
	background.reached(t)
	bRefresh(t, tab)
	// A poll is running: no second one starts.
	tab.Tick()

	asked := newGate()
	t.Cleanup(asked.open)
	fakes.with(func(fakes *world) { fakes.listGate = asked })
	background.open()
	tab.Settle()
	bPressed(t, "the older poll does not count for it", tab, 1, 0)

	// The poll the refresh asked for.
	tab.Tick()
	asked.reached(t)
	bSearches(t, "polls", fakes, 2)
	bPressed(t, "asked poll running", tab, 1, 0)
	asked.open()
	tab.Settle()
	bPressed(t, "asked poll over", tab, 1, 1)
}

func TestBTwoRefreshesDuringOnePollWaitForTheNextPoll(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})
	tickAndSettle(tab)

	first := newGate()
	t.Cleanup(first.open)
	fakes.with(func(fakes *world) { fakes.listGate = first })
	bRefresh(t, tab)
	tab.Tick()
	first.reached(t)
	// Arrives while the first forced poll is running.
	bRefresh(t, tab)

	second := newGate()
	t.Cleanup(second.open)
	fakes.with(func(fakes *world) { fakes.listGate = second })
	first.open()
	tab.Settle()
	bPressed(t, "the first forced poll started before the second refresh", tab, 2, 0)
	tab.Tick()
	second.reached(t)
	bPressed(t, "second poll running", tab, 2, 0)
	second.open()
	tab.Settle()
	bPressed(t, "second poll over", tab, 2, 2)
}

func TestBRefreshInAModeThatCannotPollIsFinishedAtOnce(t *testing.T) {
	t.Parallel()

	t.Run("GitHub off", func(t *testing.T) {
		t.Parallel()

		tab, _ := setup(t, setupOpts{settings: bGitHubOff()})
		tickAndSettle(tab)

		if view(t, tab).Enabled {
			t.Error("the tab is enabled")
		}

		bCount(t, "press", bRefresh(t, tab), 1)
		bPressed(t, "off", tab, 1, 1)
	})

	t.Run("dry", func(t *testing.T) {
		t.Parallel()

		_, fakes := setup(t, setupOpts{})
		dry := bNewDry(t, fakes)
		bCount(t, "press", bRefresh(t, dry), 1)
		bPressed(t, "dry", dry, 1, 1)
	})
}

func TestBRefreshNotFinishedAfter150SecondsIsLetGo(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{noClient: true})
	tickAndSettle(tab)

	// A poll that never answers (let go when the test ends).
	never := newGate()

	t.Cleanup(func() {
		never.open()
		tab.Settle()
	})
	fakes.with(func(fakes *world) { fakes.listGate = never })
	bRefresh(t, tab)
	tab.Tick()
	never.reached(t)
	fakes.advance(150_000 - 1)
	tab.Tick()
	bPressed(t, "before 150 s", tab, 1, 0)

	bumps := bBumps(fakes)
	fakes.advance(1)
	tab.Tick()
	bPressed(t, "at 150 s", tab, 1, 1)

	if bBumps(fakes) <= bumps {
		t.Error("no snapshot was sent")
	}
}
