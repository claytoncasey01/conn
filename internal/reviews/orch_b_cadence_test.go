package reviews_test

// A boot with GitHub off, and the poll cadence and the budget shared with My PRs. Bun's
// reviews.test.ts after "---- Auto-close ----" and "---- The poll cadence ... ----".

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
)

// bGitHubOff are the settings with GitHub off.
func bGitHubOff() *settings.Settings {
	current := settingsOf(settingsOpts{github: new(false)})

	return &current
}

func TestBBootWithGitHubOffGivesUpALaunchThatNeverShowed(t *testing.T) {
	t.Parallel()

	first := bLaunched(t, bLaunchOpts{number: 0, sessions: func(string) []sessions.LiveSession {
		return nil
	}})
	bOpen(t, "first", first.launch())

	// A new process: GitHub is off, so nothing polls and nothing else opens the file.
	tab, fakes := setup(t, setupOpts{file: first.fakes.file, settings: bGitHubOff()})
	fakes.with(func(fakes *world) { fakes.now = worldT0 + 6*minuteMs })
	tab.Tick()
	tab.Settle()
	bClosedAtMin(t, bLaunches(first.fakes.file, bPR)[0], 6)

	got := fakes.snapshotCalls()
	if got.login+got.list+len(got.fetch) != 0 {
		t.Errorf("GitHub was asked: login %d, searches %d, reads %d", got.login, got.list,
			len(got.fetch))
	}
}

func TestBBootWithGitHubOffClosesAFinishedSessionAtTheCap(t *testing.T) {
	t.Parallel()

	first := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	first.at(1)
	bPinned(t, first.launch(), bSession)

	tab, fakes := setup(t, setupOpts{
		file: first.fakes.file, settings: bGitHubOff(),
		sessions: bOnly(first.fakes.dir, nil),
	})
	moveTo := func(minutes float64) {
		fakes.with(func(fakes *world) { fakes.now = worldT0 + minutes*minuteMs })
		tab.Tick()
		tab.Settle()
	}

	moveTo(2)
	// Idle for the review delay, but no review can have been seen: it waits.
	moveTo(2 + bAfterMin + 1)
	bSame(t, "closes before", bCloses(fakes), []string{})
	moveTo(2 + bCapMin + 1)
	bSame(t, "closes", bCloses(fakes), []string{bPidText})
	bClosedAtMin(t, bLaunches(first.fakes.file, bPR)[0], 2+bCapMin+1)

	got := fakes.snapshotCalls()
	if got.login+got.list != 0 {
		t.Errorf("GitHub was asked: login %d, searches %d", got.login, got.list)
	}
}

func TestBBootWithGitHubOffReadsOnlyAVersion2File(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{settings: bGitHubOff()})
	versionOne := `{"version":1,"cursorTs":null,"prs":{}}`

	err := os.WriteFile(fakes.file, []byte(versionOne), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	fakes.advance(minuteMs)
	tickAndSettle(tab)

	kept, err := os.ReadFile(fakes.file)
	if err != nil {
		t.Fatal(err)
	}

	if string(kept) != versionOne {
		t.Errorf("the file is now %s", kept)
	}

	entries, err := os.ReadDir(filepath.Dir(fakes.file))
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), filepath.Base(fakes.file)+".") {
			t.Errorf("a copy was made: %s", entry.Name())
		}
	}
}

func TestBReadsByNumberCountInWhatAPollCost(t *testing.T) {
	t.Parallel()

	noSession := func(string) []sessions.LiveSession { return nil }

	// GitHub says the read cost 400 points, 4000 left, reset in an hour: due at 576 s by the
	// hour's budget, and at about 600 s by what is left.
	t.Run("paced by the reads", func(t *testing.T) {
		t.Parallel()

		run := bLaunched(t, bLaunchOpts{number: 0, sessions: noSession})
		searches := len(run.fakes.snapshotCalls().search)
		run.fakes.setSettings(settingsOf(settingsOpts{requested: new(false)}))
		run.fakes.with(func(fakes *world) {
			fakes.fetchRate = &ghbudget.RateInfo{
				Cost: 400, Remaining: 4000, ResetAt: worldT0 + hourMs,
			}
		})
		run.tab.SettingsChanged()
		run.at(0)

		polls := len(run.fakes.snapshotCalls().fetch)
		if polls < 1 {
			t.Fatal("the settings change did not poll at once and read the tracked PR")
		}

		// Nothing is searched: "Requests me" is off, and Slack is the only source.
		if got := len(run.fakes.snapshotCalls().search); got != searches {
			t.Errorf("searches %d, want %d", got, searches)
		}

		for _, step := range []struct {
			minutes float64
			want    int
		}{{minutes: 5, want: polls}, {minutes: 9, want: polls}, {minutes: 11, want: polls + 1}} {
			run.at(step.minutes)

			if got := len(run.fakes.snapshotCalls().fetch); got != step.want {
				t.Errorf("reads at %v minutes %d, want %d", step.minutes, got, step.want)
			}
		}
	})

	// With GitHub sending no figure the same poll is paced by the base interval.
	t.Run("no figure", func(t *testing.T) {
		t.Parallel()

		run := bLaunched(t, bLaunchOpts{number: 0, sessions: noSession})
		run.fakes.setSettings(settingsOf(settingsOpts{requested: new(false)}))
		run.tab.SettingsChanged()
		run.at(0)

		polls := len(run.fakes.snapshotCalls().fetch)
		run.at(5)

		if got := len(run.fakes.snapshotCalls().fetch); got != polls+1 {
			t.Errorf("reads %d, want %d", got, polls+1)
		}
	})
}

func TestBPollWithSearchesAndReadsIsPacedByBothCosts(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: func(string) []sessions.LiveSession {
		return nil
	}})
	// The tracked PR is not in the search: it is read by number.
	run.fakes.with(func(fakes *world) {
		fakes.open = []*reviews.PR{}
		fakes.rate = &ghbudget.RateInfo{Cost: 100, Remaining: 5000, ResetAt: worldT0 + 1}
		fakes.fetchRate = &ghbudget.RateInfo{Cost: 200, Remaining: 5000, ResetAt: worldT0 + 1}
	})
	run.refresh()
	run.at(0)

	polls := run.fakes.snapshotCalls().list
	if len(run.fakes.snapshotCalls().fetch) < 1 {
		t.Error("the tracked PR was not read")
	}

	// 100 + 200 = 300 points: 432 s; the searches alone would be 144 s, the reads alone 288 s.
	run.at(5)

	if got := run.fakes.snapshotCalls().list; got != polls {
		t.Errorf("polled at 5 minutes: %d, want %d", got, polls)
	}

	run.at(8)

	if got := run.fakes.snapshotCalls().list; got != polls+1 {
		t.Errorf("polls at 8 minutes %d, want %d", got, polls+1)
	}
}

func TestBReviewsWaitsForMyPRsRateLimitAndItsOwnPausesMyPRs(t *testing.T) {
	t.Parallel()

	budget := ghbudget.New()
	tab, fakes := setup(t, setupOpts{budget: budget})
	moveTo := func(minutes float64) {
		fakes.with(func(fakes *world) { fakes.now = worldT0 + minutes*minuteMs })
		tab.Tick()
		tab.Settle()
	}

	// My PRs hit the limit: GitHub resets in 20 minutes.
	budget.Pause(worldT0 + 20*minuteMs)
	moveTo(0)
	bSearches(t, "while the limit is in force", fakes, 0)

	if paused := view(t, tab).PausedUntil; paused == nil {
		t.Error("not paused")
	} else if *paused != worldT0+20*minuteMs {
		t.Errorf("paused until %v", *paused)
	}

	moveTo(19)
	bSearches(t, "a minute before the reset", fakes, 0)
	moveTo(20)
	bSearches(t, "at the reset", fakes, 1)

	if paused := view(t, tab).PausedUntil; paused != nil {
		t.Errorf("still paused until %v", *paused)
	}

	// Its answered poll makes it a spender the other one shares with.
	if got := budget.Parties("myPrs", worldT0+20*minuteMs); got != 2 {
		t.Errorf("parties %d", got)
	}

	// Its own limit is the other one's.
	resetAt := float64(worldT0 + 50*minuteMs)

	fakes.with(func(fakes *world) {
		fakes.listErr = &ghbudget.RateLimitError{ResetAt: &resetAt}
		fakes.now = worldT0 + 21*minuteMs
	})
	bRefresh(t, tab)
	tab.Tick()
	tab.Settle()

	if paused := budget.PausedUntil(worldT0 + 21*minuteMs); paused == nil {
		t.Error("My PRs is not paused")
	} else if *paused != resetAt {
		t.Errorf("My PRs paused until %v", *paused)
	}
}

// bSearches fails the test unless GitHub was searched want times.
func bSearches(t *testing.T, label string, fakes *world, want int) {
	t.Helper()

	if got := fakes.snapshotCalls().list; got != want {
		t.Errorf("%s: searches %d, want %d", label, got, want)
	}
}

// bRefresh is REFRESH pressed, answering its number.
func bRefresh(t *testing.T, tab *reviews.Reviews) int {
	t.Helper()

	seq, err := tab.Refresh()
	if err != nil {
		t.Fatal(err)
	}

	return seq
}

func TestBReviewsTakesABudgetShareOnlyWhileItAsksGitHub(t *testing.T) {
	t.Parallel()

	rate := &ghbudget.RateInfo{Cost: 100, Remaining: 5000, ResetAt: worldT0 + 1}

	// Alone: 144 s is under the 180 s base, so the next poll is at 3 minutes.
	t.Run("alone", func(t *testing.T) {
		t.Parallel()

		tab, fakes := setup(t, setupOpts{budget: ghbudget.New()})
		fakes.with(func(fakes *world) { fakes.rate = rate })
		tickAndSettle(tab)
		bSearches(t, "first", fakes, 1)
		fakes.with(func(fakes *world) { fakes.now = worldT0 + 4*minuteMs })
		tab.Tick()
		tab.Settle()
		bSearches(t, "at 4 minutes", fakes, 2)
	})

	// With My PRs answered a moment ago: 288 s.
	t.Run("shared", func(t *testing.T) {
		t.Parallel()

		budget := ghbudget.New()
		budget.Note("myPrs", worldT0)
		tab, fakes := setup(t, setupOpts{budget: budget})
		fakes.with(func(fakes *world) { fakes.rate = rate })
		tickAndSettle(tab)
		bSearches(t, "first", fakes, 1)

		for _, step := range []struct {
			minutes float64
			want    int
		}{{minutes: 4, want: 1}, {minutes: 5, want: 2}} {
			fakes.with(func(fakes *world) { fakes.now = worldT0 + step.minutes*minuteMs })
			tab.Tick()
			tab.Settle()
			bSearches(t, "shared", fakes, step.want)
		}

		// GitHub off: it is no longer a spender.
		if got := budget.Parties("myPrs", worldT0+5*minuteMs); got != 2 {
			t.Errorf("parties %d", got)
		}

		fakes.setSettings(settingsOf(settingsOpts{github: new(false)}))
		tab.Tick()

		if got := budget.Parties("myPrs", worldT0+5*minuteMs); got != 1 {
			t.Errorf("parties with GitHub off %d", got)
		}
	})
}

// errBGhDown is GitHub failing a poll.
var errBGhDown = errors.New("gh down")
