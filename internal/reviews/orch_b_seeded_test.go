package reviews_test

// The worktree sweep over launches a run before a restart left in reviews.json, and when it
// runs. Bun's reviews.test.ts "---- The worktree sweep ----", second half.

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/reviews"
)

const bSeeded = 9

// errBGitBroke is git failing every call.
var errBGitBroke = errors.New("git broke")

// bWt9 is PR 9's worktree in the world's data folder.
func bWt9(fakes *world) string { return worktreeOf(fakes.dir, worldRepo, bSeeded) }

// bSeedGone is a world whose reviews.json holds a closed launch of PR 9 at worktree, a PR
// GitHub no longer has.
func bSeedGone(t *testing.T, opts setupOpts, worktree func(dir string) string) (
	*reviews.Reviews, *world,
) {
	t.Helper()

	tab, fakes := setup(t, opts)
	bSeed(t, fakes.file, worktree(fakes.dir))
	fakes.with(func(fakes *world) { fakes.gone[keyOf(bSeeded)] = true })

	return tab, fakes
}

func TestBPREndedWhileTheServerWasDownIsSweptAfterARestart(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{open: []*reviews.PR{bListed(8)}})
	bSeed(t, fakes.file, bWt9(fakes))
	fakes.with(func(fakes *world) {
		fakes.known[keyOf(bSeeded)] = prOf(func(pr *reviews.PR) { pr.Number = bSeeded })
	})
	tickAndSettle(tab)
	bSwept(t, fakes, bWt9(fakes))
}

func TestBOldLayoutLaunchIsSweptTheSameWay(t *testing.T) {
	t.Parallel()

	tab, fakes := bSeedGone(t, setupOpts{open: []*reviews.PR{}}, func(dir string) string {
		return bOldWorktree(dir, bSeeded)
	})
	tickAndSettle(tab)
	bSwept(t, fakes, bOldWorktree(fakes.dir, bSeeded))
}

func TestBLaunchPointingAnywhereElseIsNeverSwept(t *testing.T) {
	t.Parallel()

	elsewhere := []func(dir string) string{
		func(dir string) string { return dir + "/reviews/pr-9/../pr-9/widgets" },
		func(dir string) string { return filepath.Join(dir, "reviews", "pr-9") },
		func(dir string) string { return filepath.Join(dir, "reviews", "pr-8", "widgets") },
		func(dir string) string { return filepath.Join(dir, "reviews", "pr-9", "widgets", "sub") },
		func(string) string { return "/etc/elsewhere" },
		func(string) string { return "reviews/pr-9/widgets" },
		func(dir string) string { return worktreeOf(dir, "jane/tools", bSeeded) },
	}

	for index, worktree := range elsewhere {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			t.Parallel()

			tab, fakes := bSeedGone(t, setupOpts{open: []*reviews.PR{}}, worktree)
			tickAndSettle(tab)
			bSame(t, worktree(fakes.dir), bGits(fakes), []string{})
		})
	}
}

func TestBRepoNotAllowedOrCloneNotKnownKeepsItsWorktrees(t *testing.T) {
	t.Parallel()

	t.Run("not allowed", func(t *testing.T) {
		t.Parallel()

		current := settingsOf(settingsOpts{allow: []string{"jane/*"}})
		tab, fakes := bSeedGone(t, setupOpts{open: []*reviews.PR{}, settings: &current}, bWt9OfDir)
		tickAndSettle(tab)
		bSame(t, "git", bGits(fakes), []string{})

		if !reviews.LoadFile(fakes.file).PRs.Has(keyOf(bSeeded)) {
			t.Error("the record is gone")
		}
	})

	// The clone a worktree was made from is not known (no record, no readable .git pointer).
	t.Run("no clone", func(t *testing.T) {
		t.Parallel()

		tab, fakes := bSeedGone(t, setupOpts{open: []*reviews.PR{}}, bWt9OfDir)
		fakes.with(func(fakes *world) { fakes.worktreeOf = func(string) string { return "" } })
		tickAndSettle(tab)
		bSame(t, "git", bGits(fakes), []string{})
	})
}

func bWt9OfDir(dir string) string { return worktreeOf(dir, worldRepo, bSeeded) }

// bStatuses is how many git status calls were made.
func bStatuses(fakes *world) int {
	fakes.mu.Lock()
	defer fakes.mu.Unlock()

	found := 0

	for _, call := range fakes.git {
		if call.args[0] == "status" {
			found++
		}
	}

	return found
}

func TestBSweepRunsOncePerSuccessfulPoll(t *testing.T) {
	t.Parallel()

	tab, fakes := bSeedGone(t, setupOpts{open: []*reviews.PR{bListed(8)}}, bWt9OfDir)
	fakes.with(func(fakes *world) { fakes.porcelain = " M dirty\n" })
	tickAndSettle(tab)
	bCount(t, "first poll", bStatuses(fakes), 1)

	for range 10 {
		fakes.advance(600)
		tickAndSettle(tab)
	}

	bCount(t, "ticks between polls", bStatuses(fakes), 1)

	// A failed poll sweeps nothing.
	fakes.with(func(fakes *world) {
		fakes.listErr = errBGhDown
		fakes.now += pollMs
	})
	tickAndSettle(tab)
	bCount(t, "failed poll", bStatuses(fakes), 1)

	// A poll that succeeds does, once.
	fakes.with(func(fakes *world) {
		fakes.listErr = nil
		fakes.now += pollMs
	})
	tickAndSettle(tab)
	bCount(t, "good poll", bStatuses(fakes), 2)

	bNoSecondSweep(t, tab, fakes)
}

// bNoSecondSweep: while a sweep is waiting on git, the next poll starts no second one.
func bNoSecondSweep(t *testing.T, tab *reviews.Reviews, fakes *world) {
	t.Helper()

	held := newGate()

	tab.SetDeps(func(deps *reviews.Deps) {
		inner := deps.Git
		deps.Git = func(ctx context.Context, args []string, dir string, timeout time.Duration) (
			string, error,
		) {
			held.pass()

			return inner(ctx, args, dir, timeout)
		}
	})
	t.Cleanup(held.open)

	fakes.advance(pollMs)
	tab.Tick()
	held.reached(t)

	fakes.advance(pollMs)
	tab.Tick()

	now := fakes.clock()

	bWaitFor(t, "the second poll", func() bool { return bPolledAt(t, tab) == now })
	tab.Tick()
	held.open()
	tab.Settle()
	bCount(t, "no second sweep while the first still ran", bStatuses(fakes), 3)
}

// bCount fails the test when got is not want.
func bCount(t *testing.T, label string, got, want int) {
	t.Helper()

	if got != want {
		t.Errorf("%s: %d, want %d", label, got, want)
	}
}

func TestBSweepThatFailsNeverBreaksTheTickOrThePoll(t *testing.T) {
	t.Parallel()

	tab, fakes := bSeedGone(t, setupOpts{open: []*reviews.PR{bListed(bPR)}}, bWt9OfDir)
	logged := bLogTo(tab)

	var gits atomic.Int64

	tab.SetDeps(func(deps *reviews.Deps) {
		deps.Git = func(context.Context, []string, string, time.Duration) (string, error) {
			gits.Add(1)

			return "", errBGitBroke
		}
	})
	tickAndSettle(tab)

	// The poll itself was fine.
	shown := view(t, tab)
	if shown.Error != nil {
		t.Errorf("error %s", *shown.Error)
	}

	numbers := make([]int64, 0, len(shown.Rows))
	for _, row := range shown.Rows {
		numbers = append(numbers, row.Number)
	}

	if len(numbers) != 1 {
		t.Errorf("rows %v", numbers)
	} else if numbers[0] != bPR {
		t.Errorf("rows %v", numbers)
	}

	bCount(t, "git", int(gits.Load()), 1)
	bCount(t, "logged", logged.count("could not remove"), 1)

	// The sweep is not stuck: another poll runs it again, and the same failure is not logged
	// twice.
	fakes.advance(pollMs)
	tab.Tick()
	tab.Settle()
	bCount(t, "git again", int(gits.Load()), 2)
	bCount(t, "logged again", logged.count("could not remove"), 1)
}

func TestBSweepDoesNothingWhileGitHubIsOffOrDry(t *testing.T) {
	t.Parallel()

	t.Run("GitHub off", func(t *testing.T) {
		t.Parallel()

		tab, fakes := setup(t, setupOpts{open: []*reviews.PR{}, settings: bGitHubOff()})
		bSeed(t, fakes.file, bWt9(fakes))
		tickAndSettle(tab)
		bCount(t, "git", len(bGits(fakes)), 0)
	})

	t.Run("dry", func(t *testing.T) {
		t.Parallel()

		_, fakes := setup(t, setupOpts{open: []*reviews.PR{}})
		bSeed(t, fakes.file, bWt9(fakes))

		dry := bNewDry(t, fakes)
		tickAndSettle(dry)
		bCount(t, "git", len(bGits(fakes)), 0)
		bCount(t, "searches", fakes.snapshotCalls().list, 0)
	})
}
