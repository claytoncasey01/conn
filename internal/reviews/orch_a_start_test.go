package reviews_test

import (
	"math"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
)

const (
	aName7   = "pr-7-acme--widgets"
	aName7b  = "pr-7.2-acme--widgets"
	aClone   = "/nowhere/clones/widgets"
	aOKName  = `{"ok":true,"name":"` + aName7 + `"}`
	aOKName2 = `{"ok":true,"name":"` + aName7b + `"}`
)

// aReady is Reviews with one listed PR (7, unless opts say otherwise) whose rows are built, and no
// live session.
func aReady(t *testing.T, opts setupOpts) (*reviews.Reviews, *world) {
	t.Helper()

	tab, fakes := setup(t, opts)
	tickAndSettle(tab)

	return tab, fakes
}

// aLaunches are the saved launches of PR 7.
func aLaunches(t *testing.T, fakes *world) []*reviews.Launch {
	t.Helper()

	entry, found := reviews.LoadFile(fakes.file).PRs.Get(keyOf(7))
	if !found {
		return []*reviews.Launch{}
	}

	return entry.Launches
}

// aLaunchCount is how many launches the fake was asked for.
func aLaunchCount(fakes *world) int {
	count := 0

	fakes.with(func(fakes *world) { count = len(fakes.launches) })

	return count
}

// Start refuses a bad number, a bad model, a disabled tab and a PR that is not listed and
// open, before any launch (the numbers).
func TestStartRefusesABadNumber(t *testing.T) {
	t.Parallel()

	numbers := []any{
		"12abc", "0", "-1", "1e9999", "1.5", "", " 7", "7 ", "+7", "0x7", "Infinity",
		"99999999999999999999", 7.5, float64(0), float64(-1), math.Inf(1), math.NaN(), nil,
		js.NewObject(),
		[]any{float64(7)},
		true,
	}

	for _, number := range numbers {
		t.Run(js.Stringify(number), func(t *testing.T) {
			t.Parallel()

			tab, fakes := aReady(t, setupOpts{})
			reply := aStart(t, tab, worldRepo, number, aSonnet)
			aEqual(t, "code", reply.Code, 400)

			why, _ := reply.Body.Get("error")
			if _, isText := why.(string); !isText {
				t.Errorf("error %#v", why)
			}

			aEqual(t, "launches", aLaunchCount(fakes), 0)
		})
	}
}

// Start refuses a bad number, a bad model, a disabled tab and a PR that is not listed and
// open, before any launch (the models, the rule order, a PR not listed).
func TestStartRefusesABadModelAndAnUnlistedPR(t *testing.T) {
	t.Parallel()

	models := []any{
		"haiku", "SONNET", "sonnet ", "", "opus; rm -rf /", nil, float64(5), js.NewObject(),
		[]any{aOpus},
	}

	for _, model := range models {
		t.Run(js.Stringify(model), func(t *testing.T) {
			t.Parallel()

			tab, fakes := aReady(t, setupOpts{})
			aEqual(t, "code", aStart(t, tab, worldRepo, "7", model).Code, 400)
			aEqual(t, "launches", aLaunchCount(fakes), 0)
		})
	}

	t.Run("order and listing", func(t *testing.T) {
		t.Parallel()

		tab, fakes := aReady(t, setupOpts{})
		// Rule order: the number is checked before the model.
		aEqual(t, "number first", aStart(t, tab, worldRepo, "0", "haiku").Code, 400)
		aEqual(t, "not listed", aStart(t, tab, worldRepo, "8", aSonnet).Code, 404)
		aEqual(t, "not listed, a number", aStart(t, tab, worldRepo, float64(8), aSonnet).Code,
			404)
		aEqual(t, "launches", aLaunchCount(fakes), 0)
	})
}

// Start is refused while the tab is off, with the reason, and a draft, an own PR or an
// ended PR is not there to start.
func TestStartRefusedWhileOffAndForDraftOwnOrEnded(t *testing.T) {
	t.Parallel()

	t.Run("off", func(t *testing.T) {
		t.Parallel()

		tab, fakes := aReady(t, setupOpts{settings: aSettingsOf(settingsOpts{github: new(false)})})
		reply := aStart(t, tab, worldRepo, "7", aSonnet)
		aEqual(t, "code", reply.Code, 409)

		if !strings.Contains(aField(reply, "disabledReason"), "GitHub is off") {
			t.Errorf("reason %q", aField(reply, "disabledReason"))
		}

		aEqual(t, "error", aField(reply, "error"), aField(reply, "disabledReason"))
		aEqual(t, "launches", aLaunchCount(fakes), 0)
	})

	t.Run("draft, own, ended", func(t *testing.T) {
		t.Parallel()

		tab, fakes := aReady(t, setupOpts{open: []*reviews.PR{
			aReviewedPR(),
			prOf(func(pr *reviews.PR) {
				pr.Number, pr.IsDraft, pr.Requested = 8, true, []string{worldMe}
			}),
			prOf(func(pr *reviews.PR) {
				pr.Number, pr.Author, pr.Requested = 9, worldMe, []string{worldMe}
			}),
		}})

		aEqual(t, "draft", aStart(t, tab, worldRepo, "8", aSonnet).Code, 404)
		aEqual(t, "own", aStart(t, tab, worldRepo, "9", aSonnet).Code, 404)
		fakes.with(func(fakes *world) { fakes.open = []*reviews.PR{} })
		aRefresh(t, tab)
		tickAndSettle(tab)
		aEqual(t, "ended", aText(view(t, tab).Rows[0].Ended), "merged")
		aEqual(t, "start ended", aStart(t, tab, worldRepo, "7", aSonnet).Code, 404)
		aEqual(t, "launches", aLaunchCount(fakes), 0)
	})
}

// A tab still showing a PR whose repository has just left the list cannot start it: 404,
// and no git, launch or cmux, before any poll.
func TestStartRefusedForRepoJustOffTheList(t *testing.T) {
	t.Parallel()

	tab, fakes := aReady(t, setupOpts{})
	aEqual(t, "rows", aNumbers(view(t, tab).Rows), []int64{7})

	// The list changes and the PR is still in memory: the next poll has not dropped it.
	fakes.setSettings(settingsOf(settingsOpts{allow: []string{"other/*"}}))
	tab.SettingsChanged()

	gitBefore := 0

	fakes.with(func(fakes *world) { gitBefore = len(fakes.git) })
	aEqual(t, "start", aStart(t, tab, worldRepo, "7", aSonnet).Code, 404)
	aEqual(t, "launches", aLaunchCount(fakes), 0)
	fakes.with(func(fakes *world) { aEqual(t, "git", len(fakes.git), gitBefore) })
}

// A live session named for the PR, in any folder, refuses; one for another PR does not.
func TestStartRefusedBesideALiveSessionOfThePR(t *testing.T) {
	t.Parallel()

	for _, name := range []string{aName7, aName7b, "pr-7.13-acme--widgets"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tab, fakes := aReady(t, setupOpts{sessions: []sessions.LiveSession{
				aSession(name, "/somewhere/else", "idle", "m", 1),
			}})
			reply := aStart(t, tab, worldRepo, "7", aSonnet)
			aEqual(t, "code", reply.Code, 409)

			if !strings.Contains(aField(reply, "error"), aName7) {
				t.Errorf("error %q", aField(reply, "error"))
			}

			aEqual(t, "launches", aLaunchCount(fakes), 0)
		})
	}

	t.Run("other PRs", func(t *testing.T) {
		t.Parallel()

		others := []string{
			"pr-70-acme--widgets", "pr-17-acme--widgets", "pr-7x", "pr-7-a", "work",
		}
		live := make([]sessions.LiveSession, 0, len(others))

		for index, name := range others {
			live = append(live, aSession(name, "/x", "idle", "m"+js.Number(float64(index)),
				float64(index+1)))
		}

		tab, fakes := aReady(t, setupOpts{sessions: live})
		aEqual(t, "start", aStart(t, tab, worldRepo, "7", aSonnet).Code, 200)
		aEqual(t, "launches", aLaunchCount(fakes), 1)
	})
}

// A second start inside 30 seconds is a 409 with no second launch, and one after the
// launch had its 5 minutes is allowed (named for the second launch).
func TestSecondStartWithinThirtySecondsIsRefused(t *testing.T) {
	t.Parallel()

	tab, fakes := aReady(t, setupOpts{})
	aEqual(t, "first", aBody(aStart(t, tab, worldRepo, "7", aSonnet)), aOKName)

	fakes.advance(29_000)

	again := aStart(t, tab, worldRepo, "7", aOpus)
	aEqual(t, "again", again.Code, 409)

	if !strings.Contains(aField(again, "error"), "just started") {
		t.Errorf("error %q", aField(again, "error"))
	}

	aEqual(t, "launches", aLaunchCount(fakes), 1)
	fakes.with(func(fakes *world) { fakes.now = aMinutes(5) })
	aEqual(t, "later", aBody(aStart(t, tab, worldRepo, "7", aOpus)), aOKName2)
	aEqual(t, "launches after", aLaunchCount(fakes), 2)
}

// While the newest launch has not appeared as a session, a start is a 409 even past 30
// seconds; it is allowed once the launch is closed or 5 minutes old.
func TestStartRefusedWhileNewestLaunchHasNotAppeared(t *testing.T) {
	t.Parallel()

	tab, fakes := aReady(t, setupOpts{})
	aEqual(t, "first", aBody(aStart(t, tab, worldRepo, "7", aSonnet)), aOKName)

	fakes.advance(60_000)

	again := aStart(t, tab, worldRepo, "7", aOpus)
	aEqual(t, "again", again.Code, 409)
	aEqual(t, "error", aField(again, "error"),
		"a review of this PR was started a moment ago and has not appeared yet")
	aEqual(t, "launches", aLaunchCount(fakes), 1)

	// Still refused a millisecond before 5 minutes are up, with no tick in between.
	fakes.with(func(fakes *world) { fakes.now = aMinutes(5) - 1 })
	aEqual(t, "just before", aStart(t, tab, worldRepo, "7", aOpus).Code, 409)
	aEqual(t, "launches before", aLaunchCount(fakes), 1)
	fakes.with(func(fakes *world) { fakes.now = aMinutes(5) })
	aEqual(t, "at 5 minutes", aBody(aStart(t, tab, worldRepo, "7", aOpus)), aOKName2)
	aEqual(t, "launches after", aLaunchCount(fakes), 2)

	if aLaunches(t, fakes)[0].ClosedAt == nil {
		t.Error("the first launch is not closed")
	}
}
