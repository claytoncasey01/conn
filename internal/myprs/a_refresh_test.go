package myprs_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// The REFRESH, bump, dry-run and seenAt tests.

// aGiveUpMs is how long a press waits for a poll that never answers.
const aGiveUpMs = 150_000

var errABumpBroke = errors.New("bump broke")

// aHeld is a search that waits to be let go: each call sends entered the channel whose close
// lets it answer prs.
func aHeld(entered chan<- chan struct{}, prs ...*myprs.PR) func() (myprs.Search, error) {
	return func() (myprs.Search, error) {
		release := make(chan struct{})
		entered <- release

		<-release

		return myprs.Search{PRs: prs, Capped: false, Failed: nil, Rate: nil}, nil
	}
}

// aEntered is the release of the next search to start, failing the test when none starts.
func aEntered(t *testing.T, entered <-chan chan struct{}) chan struct{} {
	t.Helper()

	select {
	case release := <-entered:
		return release
	case <-time.After(testtime.Scaled(5 * time.Second)):
		t.Fatal("no search started")

		return nil
	}
}

// aSeenAt is the seenAt the file on disk holds for org/repo#7.
func aSeenAt(t *testing.T, file string) float64 {
	t.Helper()

	entry, found := myprs.LoadFile(file).PRs.Get(myprs.EntryKey("org/repo", 7))
	if !found {
		t.Fatal("org/repo#7 is not in the file")
	}

	return entry.SeenAt
}

// aAt sets the clock.
func aAt(world *World, now float64) { world.Change(func(w *World) { w.Now = now }) }

func TestAPressIsDoneOnceItsPollHasFinished(t *testing.T) {
	t.Parallel()

	world, poller, _ := aPolled(t, pr())
	if got := poller.Snapshot().Refresh; got != (myprs.Refresh{Seq: 0, Done: 0}) {
		t.Fatalf("refresh = %+v, want 0 0", got)
	}

	entered := make(chan chan struct{}, 1)
	aSetSource(world, aHeld(entered, pr()))

	if seq := poller.Refresh(); seq != 1 {
		t.Errorf("seq = %d, want 1", seq)
	}

	if got := poller.Snapshot().Refresh; got != (myprs.Refresh{Seq: 1, Done: 0}) {
		t.Errorf("refresh = %+v, want 1 0", got)
	}

	// A press is polled on the next tick, well inside the poll interval.
	poller.Tick()

	release := aEntered(t, entered)

	if done := poller.Snapshot().Refresh.Done; done != 0 {
		t.Errorf("done = %d while the poll is in flight, want 0", done)
	}

	close(release)
	poller.Settle()

	if got := poller.Snapshot().Refresh; got != (myprs.Refresh{Seq: 1, Done: 1}) {
		t.Errorf("refresh = %+v, want 1 1", got)
	}

	if seq := poller.Refresh(); seq != 2 {
		t.Errorf("next seq = %d, want 2", seq)
	}
}

func TestAPressDuringAPollWaitsForTheNextPoll(t *testing.T) {
	t.Parallel()

	entered := make(chan chan struct{}, 1)
	world := newWorld(t)
	aSetSource(world, aHeld(entered, pr()))
	poller, _ := world.New()

	// The first poll, started before the press.
	poller.Tick()

	first := aEntered(t, entered)
	seq := poller.Refresh()
	// Still polling: no second one yet.
	poller.Tick()
	close(first)
	poller.Settle()

	if done := poller.Snapshot().Refresh.Done; done != 0 {
		t.Errorf("done = %d: the poll that was already running answered the press", done)
	}

	poller.Tick()
	close(aEntered(t, entered))
	poller.Settle()

	if done := poller.Snapshot().Refresh.Done; done != seq {
		t.Errorf("done = %d, want %d", done, seq)
	}
}

func TestAFailedPollFinishesAPress(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	aSetSource(world, aFailing(errABoom))
	poller, _ := world.New()
	seq := poller.Refresh()
	tick(poller)

	view := poller.Snapshot()
	if view.Refresh != (myprs.Refresh{Seq: seq, Done: seq}) {
		t.Errorf("refresh = %+v, want %d %d", view.Refresh, seq, seq)
	}

	if !strings.Contains(aText(view.Error), "boom") {
		t.Errorf("error = %q, want boom", aText(view.Error))
	}
}

func TestAPressWithoutAClientIsLetGo(t *testing.T) {
	t.Parallel()

	world := newWorld(t, pr())
	world.Clients = false
	poller, _ := world.New()
	seq := poller.Refresh()
	tick(poller)

	if calls, _ := aCounters(world); calls != 0 {
		t.Errorf("searches = %d, want 0", calls)
	}

	if done := poller.Snapshot().Refresh.Done; done != seq {
		t.Errorf("done = %d, want %d", done, seq)
	}
}

func TestAPressIsLetGoAfterTheGiveUpTime(t *testing.T) {
	t.Parallel()

	never := make(chan struct{})
	world := newWorld(t)
	aSetSource(world, func() (myprs.Search, error) {
		<-never

		return myprs.Search{PRs: nil, Capped: false, Failed: nil, Rate: nil}, nil
	})
	poller, _ := world.New()
	// Registered after the temp folder's, so it runs first: the late poll writes nothing into
	// a removed folder.
	t.Cleanup(func() {
		close(never)
		poller.Settle()
	})

	seq := poller.Refresh()
	poller.Tick()

	if done := poller.Snapshot().Refresh.Done; done != 0 {
		t.Errorf("done = %d before the give-up time, want 0", done)
	}

	world.Advance(aGiveUpMs)
	poller.Tick()

	if done := poller.Snapshot().Refresh.Done; done != seq {
		t.Errorf("done = %d after the give-up time, want %d", done, seq)
	}
}

// aPanicOf is what call panicked with, nil when it did not.
func aPanicOf(call func()) any {
	var failure any

	func() {
		defer func() { failure = recover() }()

		call()
	}()

	return failure
}

func TestABumpThatPanicsBreaksNothing(t *testing.T) {
	t.Parallel()

	world := newWorld(t, pr())
	deps := world.Deps(aFile(t))
	deps.Bump = func() { panic(errABumpBroke) }
	poller := myprs.New(context.Background(), deps)

	if failure := aPanicOf(poller.Tick); failure != nil {
		t.Fatalf("tick panicked: %v", failure)
	}

	poller.Settle()

	if rows := len(poller.Snapshot().Rows); rows != 1 {
		t.Errorf("rows = %d, want 1", rows)
	}

	_, err := os.Stat(deps.File)
	if err != nil {
		t.Errorf("the file was not saved: %v", err)
	}

	// A press bumps too, and the next poll runs as usual.
	if failure := aPanicOf(func() { poller.Refresh() }); failure != nil {
		t.Fatalf("refresh panicked: %v", failure)
	}

	tick(poller)

	if calls, _ := aCounters(world); calls != 2 {
		t.Errorf("searches = %d, want 2", calls)
	}
}

func TestADryRunPollsNothing(t *testing.T) {
	t.Parallel()

	world := newWorld(t, pr())
	deps := world.Deps(aFile(t))
	deps.Config.Dry = true
	poller := myprs.New(context.Background(), deps)
	poller.Tick()

	seq := poller.Refresh()
	tick(poller)

	calls, logins := aCounters(world)
	if calls != 0 {
		t.Errorf("searches = %d, want 0", calls)
	}

	if logins != 0 {
		t.Errorf("logins = %d, want 0", logins)
	}

	view := poller.Snapshot()
	if view.Refresh != (myprs.Refresh{Seq: seq, Done: seq}) {
		t.Errorf("refresh = %+v, want %d %d", view.Refresh, seq, seq)
	}

	if got := string(js.Marshal(view.Rows)); got != "[]" {
		t.Errorf("rows = %s, want []", got)
	}
}

func TestASeenAtIsSavedAboutOnceADay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		capped bool
	}{
		{name: "uncapped", capped: false},
		// A capped list also saves a stale seenAt.
		{name: "capped", capped: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := newWorld(t)
			aSetSource(world, aListing(tt.capped, pr()))
			poller, file := world.New()
			tick(poller)

			if got := aSeenAt(t, file); got != worldT0 {
				t.Errorf("seenAt = %v, want T0", got)
			}

			// A poll within the day, the set unchanged: the file is not written again.
			aAt(world, worldT0+23*hourMs)
			tick(poller)

			if got := aSeenAt(t, file); got != worldT0 {
				t.Errorf("seenAt within the day = %v, want T0", got)
			}

			// A day on: the stamp is saved, so a restart does not read a PR that is still open
			// as long gone.
			aAt(world, worldT0+25*hourMs)
			tick(poller)

			if got := aSeenAt(t, file); got != worldT0+25*hourMs {
				t.Errorf("seenAt a day on = %v, want T0 + 25 h", got)
			}

			aAt(world, worldT0+26*hourMs)
			tick(poller)

			if got := aSeenAt(t, file); got != worldT0+25*hourMs {
				t.Errorf("seenAt an hour later = %v, want T0 + 25 h", got)
			}
		})
	}
}

func TestAOldSeenAtIsSavedByTheFirstPoll(t *testing.T) {
	t.Parallel()

	// After a restart, a listed PR whose saved seenAt is old.
	file := aFile(t)
	aSeed(t, file, worldT0-20*aDayMs, myprs.EntryKey("org/repo", 7))
	poller := aNew(newWorld(t, pr()), file)
	tick(poller)

	if got := aSeenAt(t, file); got != worldT0 {
		t.Errorf("seenAt = %v, want T0", got)
	}
}
