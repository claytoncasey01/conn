package reviews_test

// The worktree sweep: a review's worktree stays after its session is closed (RESUME works while
// the PR is open) and is removed once the PR's own read says it ended. Bun's reviews.test.ts
// "---- The worktree sweep ----", first half.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
)

// wt7 is PR 7's worktree in the harness's data folder.
func (run *bHarness) wt7() string { return worktreeOf(run.fakes.dir, worldRepo, bPR) }

// bSwept fails the test unless git was asked to check and remove the worktree, and nothing else.
func bSwept(t *testing.T, fakes *world, worktree string) {
	t.Helper()

	bSame(t, "git", bGits(fakes), []string{bStatusOf(worktree), bRemoveOf(worktree)})
}

// bWork is someone's session working in cwd.
func bWork(cwd, status string) []sessions.LiveSession {
	return []sessions.LiveSession{{
		Name: "work", Cwd: cwd, Status: status, SessionID: "other", PID: 777,
	}}
}

func TestBClosedReviewOfAnOpenPRKeepsItsWorktree(t *testing.T) {
	t.Parallel()

	run := bClosedLaunch(t)

	for index := range 4 {
		run.refresh()
		run.at(float64(3 + index))
	}

	bSame(t, "git", bGits(run.fakes), []string{})

	// No longer asking me, or a draft again, or merely missing from a search: its own read says
	// open, so it stays.
	run.fakes.with(func(fakes *world) {
		fakes.open = []*reviews.PR{prOf(func(pr *reviews.PR) { pr.IsDraft = true })}
	})
	run.refresh()
	run.at(10)
	run.fakes.with(func(fakes *world) { fakes.open = []*reviews.PR{prOf(nil)} })
	run.refresh()
	run.at(11)
	bSame(t, "git after", bGits(run.fakes), []string{})
}

func TestBEndedPRCleanWorktreeIsRemovedWithItsEmptyFolder(t *testing.T) {
	t.Parallel()

	run := bClosedLaunch(t)
	worktree := run.wt7()

	err := os.MkdirAll(worktree, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	run.fakes.with(func(fakes *world) {
		fakes.onGit = func(args []string) {
			if args[0] == "worktree" {
				_ = os.RemoveAll(worktree)
			}
		}
	})
	run.leaves(3)
	bSwept(t, run.fakes, worktree)

	if !slices.Contains(run.fakes.snapshotCalls().clone, worldRepo) {
		t.Error("the clone was not looked up")
	}

	for _, call := range bGits(run.fakes) {
		for _, forced := range []string{" --force ", " -f "} {
			if strings.Contains(" "+call, forced) {
				t.Errorf("forced: %s", call)
			}
		}
	}

	_, err = os.Stat(filepath.Dir(worktree))
	if err == nil {
		t.Error("the empty PR folder is still there")
	}

	// The folder is gone from the disk: the next poll has nothing to do.
	run.fakes.with(func(fakes *world) {
		fakes.exists = func(path string) bool {
			_, err := os.Stat(path)

			return err == nil
		}
	})
	run.refresh()
	run.at(4)

	if got := len(bGits(run.fakes)); got != 2 {
		t.Errorf("git calls %d", got)
	}
}

func TestBPRFolderThatCannotBeRemovedIsLeft(t *testing.T) {
	t.Parallel()

	run := bClosedLaunch(t)
	other := filepath.Join(filepath.Dir(run.wt7()), "other")

	err := os.MkdirAll(other, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	run.leaves(3)
	bSwept(t, run.fakes, run.wt7())

	_, err = os.Stat(other)
	if err != nil {
		t.Errorf("the other folder: %v", err)
	}
}

func TestBDirtyWorktreeIsKeptAndLookedAtAgain(t *testing.T) {
	t.Parallel()

	run := bClosedLaunch(t)
	run.fakes.with(func(fakes *world) { fakes.porcelain = " M src/a.ts\n" })
	run.leaves(3)
	bSame(t, "git", bGits(run.fakes), []string{bStatusOf(run.wt7())})
	run.fakes.with(func(fakes *world) { fakes.porcelain = "" })
	run.refresh()
	run.at(4)
	bSame(t, "git after", bGits(run.fakes), []string{
		bStatusOf(run.wt7()), bStatusOf(run.wt7()), bRemoveOf(run.wt7()),
	})
}

func TestBSessionInTheFolderKeepsTheWorktree(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cwd  func(worktree string) string
		// sibling is the session that does not keep the worktree, at the next poll ("" for
		// none).
		sibling func(worktree string) string
	}{
		// Anyone's session, whatever its name, status or pid.
		{name: "in the folder", cwd: func(worktree string) string { return worktree }},
		{
			name: "in a subfolder",
			cwd:  func(worktree string) string { return worktree + "/packages/api" },
			// A sibling whose name merely starts the same is not inside it.
			sibling: func(worktree string) string { return worktree + "-copy" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			run := bClosedLaunch(t)
			run.fakes.with(func(fakes *world) { fakes.sessions = bWork(tt.cwd(run.wt7()), bBusy) })
			run.leaves(3)
			bSame(t, "git", bGits(run.fakes), []string{})

			live := []sessions.LiveSession(nil)
			if tt.sibling != nil {
				live = bWork(tt.sibling(run.wt7()), bIdle)
			}

			run.fakes.with(func(fakes *world) { fakes.sessions = live })
			run.refresh()
			run.at(4)
			bSwept(t, run.fakes, run.wt7())
		})
	}
}

func TestBSessionReachingTheFolderThroughASymlinkKeepsIt(t *testing.T) {
	t.Parallel()

	run := bClosedLaunch(t)
	reviewsDir := filepath.Join(run.fakes.dir, "reviews")

	err := os.MkdirAll(run.wt7(), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(run.fakes.dir, "link-to-reviews")

	err = os.Symlink(reviewsDir, link)
	if err != nil {
		t.Fatal(err)
	}

	inside, err := filepath.Rel(reviewsDir, run.wt7())
	if err != nil {
		t.Fatal(err)
	}

	cwd := filepath.Join(link, inside)

	run.fakes.with(func(fakes *world) { fakes.sessions = bWork(cwd, bIdle) })
	run.leaves(3)
	bSame(t, "git", bGits(run.fakes), []string{})
}

func TestBFailedReadByNumberKeepsTheWorktree(t *testing.T) {
	t.Parallel()

	run := bClosedLaunch(t)
	run.fakes.with(func(fakes *world) {
		fakes.open = []*reviews.PR{}
		fakes.readFails[keyOf(bPR)] = true
	})
	run.refresh()
	run.at(3)
	run.tab.Settle()
	bSame(t, "git", bGits(run.fakes), []string{})

	// The read answers: merged, and swept.
	run.fakes.with(func(fakes *world) { clear(fakes.readFails) })
	run.refresh()
	run.at(4)
	run.tab.Settle()
	bSwept(t, run.fakes, run.wt7())
}

func TestBSessionAppearingWhileGitStatusRunsStopsTheRemoval(t *testing.T) {
	t.Parallel()

	run := bClosedLaunch(t)
	run.fakes.with(func(fakes *world) {
		dir := fakes.dir
		fakes.onGit = func(args []string) {
			if args[0] != "status" {
				return
			}

			run.fakes.with(func(fakes *world) {
				fakes.sessions = bOnly(dir, func(session *sessions.LiveSession) {
					session.SessionID, session.PID = "s-9", 777
				})
			})
		}
	})
	run.leaves(3)

	calls := bGits(run.fakes)
	first := make([]string, 0, len(calls))

	for _, call := range calls {
		head, _, _ := strings.Cut(call, " ")
		first = append(first, head)
	}

	bSame(t, "git", first, []string{"status"})
}

func TestBOpenLaunchKeepsTheWorktreeUntilItIsClosed(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: func(string) []sessions.LiveSession {
		return nil
	}})
	run.leaves(2)
	bOpen(t, "two minutes in", run.launch())
	bSame(t, "git", bGits(run.fakes), []string{})
	run.refresh()
	run.at(4)
	bSame(t, "git at four minutes", bGits(run.fakes), []string{})

	// Five minutes: the launch is closed in this tick, before that tick's poll ends, so that
	// poll's sweep takes the folder.
	run.refresh()
	run.at(5)
	bClosed(t, "five minutes in", run.launch())
	bSwept(t, run.fakes, run.wt7())
}
