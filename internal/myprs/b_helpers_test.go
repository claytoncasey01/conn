package myprs_test

// Helpers of the b tests: Bun's myPrs.test.ts from "session names that read the same" to "a
// round that is over while its session stays open" (polled starts, the worktree sweep, the
// Greptile ask and the ready rule).

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/testtime"
)

const (
	bMin    = 60_000
	bSecond = 1000
	bDay    = 86_400_000
	bRepo   = "org/repo"
	bSonnet = "sonnet"
	// bWT is a worktree of org/repo#7 under <dataDir>/myprs.
	bWT = "/data/myprs/org-repo-7/clone"
	// bAWT is the worktree of the round the ask tests seed.
	bAWT = "/data/myprs/org-repo-7/self-repo-7"
	// bWaitMax bounds a wait on the goroutines of My PRs.
	bWaitMax = 10 * time.Second
	// bWaitStep is how often a wait looks again.
	bWaitStep = time.Millisecond
)

// bEqual fails the test unless got and want are deeply equal.
func bEqual(t *testing.T, label string, got, want any) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", label, got, want)
	}
}

// bContains fails the test unless text contains part.
func bContains(t *testing.T, label, text, part string) {
	t.Helper()

	if !strings.Contains(text, part) {
		t.Errorf("%s: %q does not contain %q", label, text, part)
	}
}

// bOpt is a nullable text as Bun's JSON has it: nil, or the text.
func bOpt(value *string) any {
	if value == nil {
		return nil
	}

	return *value
}

// bLive is a live session.
func bLive(name, cwd, status string) sessions.LiveSession {
	return sessions.LiveSession{Name: name, Cwd: cwd, Status: status, SessionID: "s1", PID: 1}
}

// bSessions makes the live sessions exactly these.
func bSessions(world *World, live ...sessions.LiveSession) {
	world.Change(func(w *World) { w.Sessions = live })
}

// bPolled is My PRs over a world listing prs, with these clones (nil: the world's), polled once
// (Bun's polled()).
func bPolled(t *testing.T, clones map[string]string, prs ...*myprs.PR) (
	*World, *myprs.MyPrs, string,
) {
	t.Helper()

	world := newWorld(t, prs...)
	if clones != nil {
		world.Clones = clones
	}

	tab, file := world.New()
	tick(tab)

	return world, tab, file
}

// bStart presses SELF-REVIEW for repo#number.
func bStart(t *testing.T, tab *myprs.MyPrs, repo string, number float64, model string,
) reviews.Reply {
	t.Helper()

	return tab.Start(t.Context(), repo, number, model)
}

// bBody is a reply's body as JSON.
func bBody(reply reviews.Reply) string { return js.Stringify(reply.Body) }

// bError is a reply's error text.
func bError(reply reviews.Reply) string {
	why, _ := reply.Body.Get("error")
	text, _ := why.(string)

	return text
}

// bLaunches are the saved launches of repo#number.
func bLaunches(file, repo string, number float64) []*myprs.Launch {
	entry, found := myprs.LoadFile(file).PRs.Get(myprs.EntryKey(repo, number))
	if !found {
		return nil
	}

	return entry.Launches
}

// bRow is the first row of the snapshot.
func bRow(t *testing.T, tab *myprs.MyPrs) *myprs.Row {
	t.Helper()

	rows := tab.Snapshot().Rows
	if len(rows) == 0 {
		t.Fatal("no row")
	}

	return rows[0]
}

// bRowOf is the row of repo.
func bRowOf(t *testing.T, tab *myprs.MyPrs, repo string) *myprs.Row {
	t.Helper()

	for _, row := range tab.Snapshot().Rows {
		if row.Repo == repo {
			return row
		}
	}

	t.Fatalf("no row of %s", repo)

	return nil
}

// bCounts are how many launches and origin reads the world saw.
func bCounts(world *World) [2]int {
	counts := [2]int{}

	world.Change(func(w *World) { counts = [2]int{len(w.Launched), len(w.Matched)} })

	return counts
}

// bLaunched are the launches the world was asked for.
func bLaunched(world *World) []myprs.LaunchOpts {
	out := []myprs.LaunchOpts{}

	world.Change(func(w *World) { out = append(out, w.Launched...) })

	return out
}

// bAnswer makes launch answer result.
func bAnswer(world *World, result myprs.LaunchResult) {
	world.Change(func(w *World) {
		w.LaunchResult = func(myprs.LaunchOpts) myprs.LaunchResult { return result }
	})
}

// bLog is a log the tests read: every line My PRs logged.
type bLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *bLog) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.lines = append(l.lines, strings.TrimSuffix(string(data), "\n"))

	return len(data), nil
}

func (l *bLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]string{}, l.lines...)
}

// bWait waits until done is true, failing the test after bWaitMax.
func bWait(t *testing.T, what string, done func() bool) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(bWaitMax))

	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("waited too long for %s", what)
		}

		time.Sleep(bWaitStep)
	}
}

// bReceive waits for a value on signal, failing the test after bWaitMax.
func bReceive(t *testing.T, what string, signal <-chan struct{}) {
	t.Helper()

	select {
	case <-signal:
	case <-time.After(testtime.Scaled(bWaitMax)):
		t.Fatalf("waited too long for %s", what)
	}
}

// bNew is My PRs over deps.
func bNew(deps myprs.Deps) *myprs.MyPrs { return myprs.New(context.Background(), deps) }

// bFile is a fresh myprs.json path.
func bFile(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "myprs.json")
}

// bSeed writes a file whose entry key has launches, first seen at seenAt.
func bSeed(t *testing.T, file, key string, seenAt float64, launches ...*myprs.Launch) {
	t.Helper()

	seeded := myprs.LoadFile(file)
	entry := myprs.TrackOf(seeded, key, seenAt)
	entry.Launches = append(entry.Launches, launches...)

	err := myprs.SaveFile(file, seeded, nil)
	if err != nil {
		t.Fatal(err)
	}
}
