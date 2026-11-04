package myprs_test

// The worktree sweep: a PR that left the open list has the worktrees of its rounds removed from
// the clone, never forced, never one Conn did not write.

import (
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
)

var (
	errBNotARepo = errors.New("fatal: not a git repository")
	errBBoom     = errors.New("boom")
)

// bGitClean is the bound on a sweep's git call.
const bGitClean = 30 * time.Second

// bSweepOpts are a swept world's: the list (nil: org/repo#8 alone), whether it is capped, the
// clones (nil: the world's), the entry's key (empty: org/repo#7) and its seenAt (nil: 2 h ago).
type bSweepOpts struct {
	list   []*myprs.PR
	capped bool
	clones map[string]string
	key    string
	seenAt *float64
}

// bSweep is a world whose file has a PR with launches in worktrees, and My PRs over it.
type bSweep struct {
	t     *testing.T
	world *World
	tab   *myprs.MyPrs
	file  string
	log   *bLog
}

// bLaunchAt is a disarmed round of org/repo#7 in worktree.
func bLaunchAt(worktree string, round float64) *myprs.Launch {
	name := "self-repo-7"
	if round > 1 {
		name += "-" + js.Number(round)
	}

	return &myprs.Launch{
		At: worldT0 - 9*hourMs, Model: bSonnet, Name: name, Worktree: worktree, HeadSha: "abc",
		Round: round, Armed: false, AskedSha: nil, AskedAt: nil, ClosedAt: nil, AskTry: nil,
		Manual: false,
	}
}

// bSwept is a sweep where the PR of opts.key has launches in worktrees, all present, and is off
// the list unless opts.list has it.
func bSwept(t *testing.T, worktrees []string, opts bSweepOpts) *bSweep {
	t.Helper()

	file := bFile(t)
	key := opts.key

	if key == "" {
		key = myprs.EntryKey(bRepo, 7)
	}

	seenAt := float64(worldT0 - 2*hourMs)
	if opts.seenAt != nil {
		seenAt = *opts.seenAt
	}

	launches := make([]*myprs.Launch, 0, len(worktrees))
	for index, worktree := range worktrees {
		launches = append(launches, bLaunchAt(worktree, float64(index+1)))
	}

	bSeed(t, file, key, seenAt, launches...)

	list := opts.list
	if list == nil {
		list = []*myprs.PR{pr(func(p *myprs.PR) { p.Number = 8 })}
	}

	world := newWorld(t)
	world.Source = func() (myprs.Search, error) {
		return myprs.Search{PRs: list, Capped: opts.capped, Failed: nil, Rate: nil}, nil
	}

	if opts.clones != nil {
		world.Clones = opts.clones
	}

	for _, worktree := range worktrees {
		world.Present[worktree] = true
	}

	logged := &bLog{mu: sync.Mutex{}, lines: nil}
	deps := world.Deps(file)
	deps.Logger = log.New(logged, "", 0)

	return &bSweep{t: t, world: world, tab: bNew(deps), file: file, log: logged}
}

// poll ticks once and waits for the poll and the sweep it started.
func (s *bSweep) poll() { tick(s.tab) }

// next moves the clock to the next poll and polls.
func (s *bSweep) next() {
	s.world.Advance(pollMs)
	s.poll()
}

// gits are the git calls, as their first argument.
func (s *bSweep) gits() []string {
	out := []string{}

	s.world.Change(func(w *World) {
		for _, call := range w.GitCalls {
			out = append(out, call.Args[0])
		}
	})

	return out
}

// calls are the git calls.
func (s *bSweep) calls() []GitCall {
	out := []GitCall{}

	s.world.Change(func(w *World) { out = append(out, w.GitCalls...) })

	return out
}

// wheres are the git calls as their first argument and the folder they ran in.
func (s *bSweep) wheres() [][2]string {
	calls := s.calls()
	out := make([][2]string, 0, len(calls))

	for _, call := range calls {
		out = append(out, [2]string{call.Args[0], call.Dir})
	}

	return out
}

// exists are the paths exists was asked about.
func (s *bSweep) exists() []string {
	out := []string{}

	s.world.Change(func(w *World) { out = append(out, w.ExistsCalls...) })

	return out
}

// forget clears the recorded git and exists calls.
func (s *bSweep) forget() {
	s.world.Change(func(w *World) {
		w.GitCalls = nil
		w.ExistsCalls = nil
	})
}

// answer makes git answer with answer.
func (s *bSweep) answer(answer func(args []string, dir string) (string, error)) {
	s.world.Change(func(w *World) { w.GitAnswer = answer })
}

// bDirty is what git status says of a worktree with a change.
const bDirty = " M a.ts\n"

// A PR that left the list has its clean worktree removed from the clone, without force.
func TestBCleanWorktreeOfAPROffTheListIsRemoved(t *testing.T) {
	t.Parallel()

	swept := bSwept(t, []string{bWT}, bSweepOpts{})
	swept.poll()
	bEqual(t, "git", swept.calls(), []GitCall{
		{Args: []string{"status", "--porcelain"}, Dir: bWT, Timeout: bGitClean},
		{Args: []string{"worktree", "remove", bWT}, Dir: "/clone", Timeout: bGitClean},
	})

	for _, call := range swept.calls() {
		for _, arg := range call.Args {
			if strings.HasPrefix(arg, "-f") {
				t.Errorf("forced: %v", call.Args)
			}

			if strings.Contains(arg, "force") {
				t.Errorf("forced: %v", call.Args)
			}
		}
	}
}

// Rounds that share one worktree are swept once; each distinct worktree is swept.
func TestBSharedWorktreeIsSweptOnce(t *testing.T) {
	t.Parallel()

	other := "/data/myprs/org-repo-7/other"
	swept := bSwept(t, []string{bWT, bWT, other}, bSweepOpts{})
	swept.poll()

	removed := []string{}
	statuses := 0

	for _, call := range swept.calls() {
		switch call.Args[0] {
		case "worktree":
			removed = append(removed, call.Args[2])
		case "status":
			statuses++
		}
	}

	bEqual(t, "removed", removed, []string{bWT, other})
	bEqual(t, "statuses", statuses, 2)
}

// A dirty worktree is kept and looked at again on the next poll, then removed once
// clean, saying nothing.
func TestBDirtyWorktreeIsKeptThenRemovedOnceClean(t *testing.T) {
	t.Parallel()

	swept := bSwept(t, []string{bWT}, bSweepOpts{})
	dirty := bFlag{}
	dirty.set(true)
	swept.answer(func(args []string, _ string) (string, error) {
		if args[0] == "status" {
			if dirty.get() {
				return bDirty, nil
			}
		}

		return "", nil
	})
	swept.poll()
	bEqual(t, "first", swept.gits(), []string{"status"})
	swept.next()
	bEqual(t, "second", swept.gits(), []string{"status", "status"})
	dirty.set(false)
	swept.next()
	bEqual(t, "clean", swept.gits(), []string{"status", "status", "status", "worktree"})
	bEqual(t, "logged", swept.log.all(), []string{})
}

// A git failure keeps the worktree and is reported once, not on every poll.
func TestBGitFailureIsReportedOnce(t *testing.T) {
	t.Parallel()

	swept := bSwept(t, []string{bWT}, bSweepOpts{})
	swept.answer(func([]string, string) (string, error) { return "", errBNotARepo })
	swept.poll()
	swept.next()
	bEqual(t, "git", swept.gits(), []string{"status", "status"})

	logged := swept.log.all()
	bEqual(t, "logged", len(logged), 1)

	if len(logged) == 1 {
		bContains(t, "line", logged[0], "not a git repository")
	}
}

// Nothing is removed when the list was capped or the poll failed (the failed poll, and
// the whole list on the poll after).
func TestBFailedPollRemovesNothingAndTheNextWholeListSweeps(t *testing.T) {
	t.Parallel()

	swept := bSwept(t, []string{bWT}, bSweepOpts{})
	swept.world.Change(func(w *World) {
		w.Source = func() (myprs.Search, error) { return myprs.Search{}, errBBoom }
	})
	swept.poll()
	bEqual(t, "failed", swept.gits(), []string{})
	// A list that is whole sweeps on the poll after.
	swept.world.SetList(pr(func(p *myprs.PR) { p.Number = 8 }))
	swept.next()
	bEqual(t, "after", swept.gits(), []string{"status", "worktree"})
}

// Nothing is removed when the list was capped (the capped list), 'a PR that is still
// listed keeps its worktree', 'a worktree that is already gone costs no git call', 'an entry
// with no launch has nothing to sweep, and the sweep touches only paths its entries recorded',
// 'a PR that is not in the open list but has no local clone keeps its worktree' and 'a
// recorded path that is relative or starts with a dash never reaches git'.
func TestBSweepLeavesTheseAlone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		worktrees []string
		opts      bSweepOpts
		before    func(w *World)
	}{
		{"capped", []string{bWT}, bSweepOpts{capped: true}, nil},
		{"still listed", []string{bWT}, bSweepOpts{list: []*myprs.PR{pr()}}, nil},
		{"already gone", []string{bWT}, bSweepOpts{}, func(w *World) {
			delete(w.Present, bWT)
		}},
		{"no launch", []string{}, bSweepOpts{}, func(w *World) {
			w.Present["/data/myprs/org-repo-9/clone"] = true
		}},
		{"no clone", []string{bWT}, bSweepOpts{clones: map[string]string{}}, nil},
		{"relative or a dash", []string{"-f", "relative/dir", ""}, bSweepOpts{}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			swept := bSwept(t, tt.worktrees, tt.opts)
			if tt.before != nil {
				swept.world.Change(tt.before)
			}

			swept.poll()
			bEqual(t, "git", swept.calls(), []GitCall{})
		})
	}
}

// A worktree is kept while a live session works in it or under it.
func TestBWorktreeIsKeptWhileASessionWorksInIt(t *testing.T) {
	t.Parallel()

	swept := bSwept(t, []string{bWT}, bSweepOpts{})
	bSessions(swept.world, bLive("self-repo-7", bWT+"/src", "idle"))
	swept.poll()
	bEqual(t, "kept", swept.gits(), []string{})
	bSessions(swept.world, bLive("x", bWT+"-other", "idle"))
	swept.next()
	bEqual(t, "removed", swept.gits(), []string{"status", "worktree"})
}

// A session that appears while git status runs stops the removal.
func TestBSessionAppearingDuringStatusStopsTheRemoval(t *testing.T) {
	t.Parallel()

	swept := bSwept(t, []string{bWT}, bSweepOpts{})
	swept.answer(func(args []string, _ string) (string, error) {
		if args[0] == "status" {
			bSessions(swept.world, bLive("self-repo-7", bWT, "idle"))
		}

		return "", nil
	})
	swept.poll()
	bEqual(t, "git", swept.gits(), []string{"status"})
}

// A worktree is removed from its own repo's clone.
func TestBWorktreeIsRemovedFromItsOwnClone(t *testing.T) {
	t.Parallel()

	worktree := "/data/myprs/other-lib-3/lib"
	swept := bSwept(t, []string{worktree}, bSweepOpts{
		key:    myprs.EntryKey("other/lib", 3),
		clones: map[string]string{bRepo: "/clone", "other/lib": "/lib-clone"},
	})
	swept.world.Change(func(w *World) { w.Scope.Allow = []string{"org/*", "other/*"} })
	swept.poll()
	bEqual(t, "git", swept.wheres(), [][2]string{{"status", worktree}, {"worktree", "/lib-clone"}})
}

// A swept worktree is not looked at again, but a dirty one, or one that could not be
// removed, is.
func TestBSweptWorktreeIsNotLookedAtAgain(t *testing.T) {
	t.Parallel()

	done := "/data/myprs/org-repo-7/clone"
	dirty := "/data/myprs/org-repo-7/dirty"
	swept := bSwept(t, []string{done, dirty}, bSweepOpts{})
	swept.answer(func(args []string, dir string) (string, error) {
		if args[0] == "status" {
			if dir == dirty {
				return bDirty, nil
			}
		}

		return "", nil
	})
	swept.poll()
	bEqual(t, "first", swept.exists(), []string{done, dirty})
	swept.world.Change(func(w *World) { delete(w.Present, done) })
	swept.forget()
	swept.next()
	bEqual(t, "second", swept.exists(), []string{dirty})
	bEqual(t, "git", swept.wheres(), [][2]string{{"status", dirty}})
}

// A worktree found gone is not looked at again.
func TestBWorktreeFoundGoneIsNotLookedAtAgain(t *testing.T) {
	t.Parallel()

	swept := bSwept(t, []string{bWT}, bSweepOpts{})
	swept.world.Change(func(w *World) { delete(w.Present, bWT) })
	swept.poll()
	swept.next()
	bEqual(t, "exists", swept.exists(), []string{bWT})
}

// A recorded path outside <dataDir>/myprs (a linked worktree of the clone, a review
// worktree, the data root, the myprs root) never reaches git.
func TestBPathOutsideTheMyprsFolderNeverReachesGit(t *testing.T) {
	t.Parallel()

	paths := []string{
		"/Users/x/projects/repo-feature", "/data/reviews/pr-1/clone", "/data", "/data/myprs",
		"/data/myprs/", "/data/myprs/../reviews/pr-1", "/data/myprs-x/org-repo-7/clone",
	}
	swept := bSwept(t, paths, bSweepOpts{})
	swept.poll()
	bEqual(t, "git", len(swept.calls()), 0)
	// Not ours: settled at once, so not even looked for on the next poll.
	swept.forget()
	swept.next()
	bEqual(t, "exists", len(swept.exists()), 0)
}

// A path inside <dataDir>/myprs still sweeps.
func TestBPathInsideTheMyprsFolderSweeps(t *testing.T) {
	t.Parallel()

	swept := bSwept(t, []string{"/data/myprs/org-repo-7/clone"}, bSweepOpts{})
	swept.poll()
	bEqual(t, "git", swept.gits(), []string{"status", "worktree"})
}

// An entry off the list for 14 days is swept in the poll that prunes it.
func TestBEntryPrunedIsSweptInTheSamePoll(t *testing.T) {
	t.Parallel()

	swept := bSwept(t, []string{bWT}, bSweepOpts{seenAt: new(float64(worldT0 - 15*bDay))})
	swept.poll()
	bEqual(t, "git", swept.calls(), []GitCall{
		{Args: []string{"status", "--porcelain"}, Dir: bWT, Timeout: bGitClean},
		{Args: []string{"worktree", "remove", bWT}, Dir: "/clone", Timeout: bGitClean},
	})
	bEqual(t, "pruned", myprs.LoadFile(swept.file).PRs.Has(myprs.EntryKey(bRepo, 7)), false)
}

// A dirty worktree of an entry pruned in the same poll is left on disk, and nothing is
// retried.
func TestBDirtyWorktreeOfAPrunedEntryIsLeft(t *testing.T) {
	t.Parallel()

	swept := bSwept(t, []string{bWT}, bSweepOpts{seenAt: new(float64(worldT0 - 15*bDay))})
	swept.answer(func(args []string, _ string) (string, error) {
		if args[0] == "status" {
			return bDirty, nil
		}

		return "", nil
	})
	swept.poll()
	bEqual(t, "first", swept.gits(), []string{"status"})
	bEqual(t, "pruned", myprs.LoadFile(swept.file).PRs.Has(myprs.EntryKey(bRepo, 7)), false)
	swept.next()
	bEqual(t, "second", swept.gits(), []string{"status"})
}

// A poll that prunes an entry while a sweep is still running does not lose its last
// chance.
func TestBPruneDuringASweepKeepsItsLastChance(t *testing.T) {
	t.Parallel()

	// Not yet 14 days at the first poll, over 14 days at the second.
	swept := bSwept(t, []string{bWT}, bSweepOpts{seenAt: new(float64(worldT0 - 14*bDay + 1))})
	gate := make(chan struct{})
	started := make(chan struct{})
	statuses := 0

	swept.answer(func(args []string, _ string) (string, error) {
		if args[0] != "status" {
			return "", nil
		}

		statuses++
		if statuses == 1 {
			close(started)
			<-gate

			return bDirty, nil
		}

		return "", nil
	})
	swept.tab.Tick()
	bReceive(t, "the first status", started)
	// The second poll runs while the first sweep waits on git: it prunes the entry and queues
	// it for the sweep in flight.
	swept.world.Advance(pollMs)
	seq := swept.tab.Refresh()
	swept.tab.Tick()
	bWait(t, "the second poll", func() bool { return swept.tab.Snapshot().Refresh.Done >= seq })
	bEqual(t, "pruned", myprs.LoadFile(swept.file).PRs.Has(myprs.EntryKey(bRepo, 7)), false)
	close(gate)
	swept.tab.Settle()
	bEqual(t, "git", swept.gits(), []string{"status", "status", "worktree"})
}

// bFlag is a flag a fake reads from My PRs' goroutines.
type bFlag struct {
	mu    sync.Mutex
	value bool
}

func (f *bFlag) set(value bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.value = value
}

func (f *bFlag) get() bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.value
}
