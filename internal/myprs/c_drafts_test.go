package myprs_test

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
)

// The Slack draft of a ready round (myPrs.test.ts, "The Slack draft").

const cTwoHoursMs = 2 * hourMs

// cNewerReviewed is the PR at head newer, which Greptile reviewed.
func cNewerReviewed() *myprs.PR { return pr(cHead("newer"), cReviewed("newer")) }

// cAskNewer records that the newest launch asked for head newer.
func cAskNewer(entry *myprs.Entry) {
	entry.Launches[len(entry.Launches)-1].AskedSha = new("newer")
}

// cPollWorld moves world's clock on and runs a poll of myPrs, waiting for all it started.
func cPollWorld(world *World, myPrs *myprs.MyPrs, advanceMs float64) {
	world.Advance(advanceMs)
	myPrs.Refresh()
	tick(myPrs)
}

func TestCDraftsOncePerShaAcrossPolls(t *testing.T) {
	t.Parallel()

	round := cReadyRound(t, cRoundOpts{})
	round.poll(0)
	cSame(t, "status", round.status(), "ready")

	drafts := round.drafts()
	if len(drafts) != 1 {
		t.Fatalf("drafts: got %d, want 1", len(drafts))
	}

	cSame(t, "channel", drafts[0].Channel, "#code-review")

	for _, part := range []string{"Add a thing", "https://github.com/org/repo/pull/7", "round 2"} {
		if !strings.Contains(drafts[0].Text, part) {
			t.Errorf("the draft %q lacks %q", drafts[0].Text, part)
		}
	}

	want := &myprs.SlackDraft{
		Sha: "new", Text: drafts[0].Text, State: "saved", Error: nil, At: round.now(), Tries: 1,
		Channel: "#code-review",
	}
	cSame(t, "saved draft", round.savedDraft(), want)
	cSame(t, "row's draft", round.row().SlackDraft, want)
	round.poll(cTwoHoursMs)
	round.poll(cTwoHoursMs)
	cSame(t, "drafts", len(round.drafts()), 1)
	// A restart (a new poller on the same file) does not draft again either.
	world, again := cRestart(t, round.file, round.cur)
	tick(again)
	world.Change(func(w *World) { cSame(t, "searches", w.Calls, 1) })
	cSame(t, "drafts after the restart", len(cDrafts(world)), 0)
	cSame(t, "state", round.savedDraft().State, "saved")
}

func TestCNewHeadWithACleanReviewIsDraftedAgain(t *testing.T) {
	t.Parallel()

	round := cReadyRound(t, cRoundOpts{})
	round.poll(0)
	cSame(t, "drafts", len(round.drafts()), 1)
	// The round pushes again, Greptile is asked for it and is clean.
	round.editFile(func(entry *myprs.Entry) { entry.Launches[0].AskedSha = new("newer") })

	world, again := cRestart(t, round.file, cNewerReviewed())
	tick(again)
	cSame(t, "drafts after", len(cDrafts(world)), 1)
	cSame(t, "sha", round.savedDraft().Sha, "newer")
}

func TestCFailedDraftIsStoredAndNotRetriedEveryPoll(t *testing.T) {
	t.Parallel()

	round := cReadyRound(t, cRoundOpts{})
	round.setDraft(myprs.DraftResult{OK: false, Error: "Slack is signed out", Final: false})
	round.poll(0)

	drafts := round.drafts()
	cSame(t, "drafts", len(drafts), 1)

	saved := round.savedDraft()
	cSame(t, "state", saved.State, "failed")
	cSame(t, "error", saved.Error, new("Slack is signed out"))
	cSame(t, "sha", saved.Sha, "new")
	cSame(t, "text", saved.Text, drafts[0].Text)
	cSame(t, "status", round.status(), "draft-failed")
	cSame(t, "row's draft", round.row().SlackDraft, saved)
	// The row says when it is tried again.
	cSame(t, "retry at", round.row().DraftRetryAt, new(saved.At+myprs.DraftRetryMs))
	// Polls inside the back-off do not draft.
	round.poll(cMinuteMs)
	round.poll(5 * cMinuteMs)
	round.poll(myprs.DraftRetryMs - 7*cMinuteMs)
	cSame(t, "drafts in the back-off", len(round.drafts()), 1)
	cSame(t, "status in the back-off", round.status(), "draft-failed")
	// After it, one retry; success replaces the failure.
	round.setDraft(myprs.DraftResult{OK: true, Error: "", Final: false})
	round.poll(2 * cMinuteMs)
	cSame(t, "drafts after the retry", len(round.drafts()), 2)
	cSame(t, "state after", round.savedDraft().State, "saved")
	cSame(t, "error after", round.savedDraft().Error, (*string)(nil))
	cSame(t, "status after", round.status(), "ready")
	cSame(t, "retry at after", round.row().DraftRetryAt, (*float64)(nil))
}

func TestCFailedRetryWaitsTheBackOffAgain(t *testing.T) {
	t.Parallel()

	round := cReadyRound(t, cRoundOpts{})
	round.setDraft(myprs.DraftResult{OK: false, Error: "nope", Final: false})
	round.poll(0)
	round.poll(myprs.DraftRetryMs)
	cSame(t, "drafts", len(round.drafts()), 2)
	round.poll(cMinuteMs)
	cSame(t, "drafts a minute on", len(round.drafts()), 2)
}

func TestCDraftBackOffIsAtLeastTenMinutes(t *testing.T) {
	t.Parallel()

	if myprs.DraftRetryMs < 10*cMinuteMs {
		t.Errorf("DraftRetryMs is %v, under ten minutes", myprs.DraftRetryMs)
	}
}

func TestCFailedDraftForAnOlderShaIsNotOnANewerHead(t *testing.T) {
	t.Parallel()

	round := cReadyRound(t, cRoundOpts{})
	round.setDraft(myprs.DraftResult{OK: false, Error: "nope", Final: false})
	round.poll(0)
	cSame(t, "status", round.status(), "draft-failed")
	round.editFile(func(entry *myprs.Entry) { entry.Launches[0].AskedSha = new("newer") })

	world, again := cRestart(t, round.file, cNewerReviewed())
	tick(again)
	cSame(t, "drafts", len(cDrafts(world)), 1)
	cSame(t, "status after", again.Snapshot().Rows[0].Status, "ready")
}

func TestCNoDraftForAPRWithoutALaunch(t *testing.T) {
	t.Parallel()

	world := newWorld(t, pr(cHead("new"), cReviewed("new")))
	myPrs, _ := world.New()
	tick(myPrs)
	cSame(t, "drafts", len(cDrafts(world)), 0)
	cSame(t, "row's draft", myPrs.Snapshot().Rows[0].SlackDraft, (*myprs.SlackDraft)(nil))
}

func TestCNoDraftWhenTheRoundIsNotReady(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts cRoundOpts
	}{
		{name: "a draft PR", opts: cRoundOpts{
			pull: []func(pull *myprs.PR){func(pull *myprs.PR) { pull.Draft = true }},
		}},
		{name: "partial", opts: cRoundOpts{
			pull: []func(pull *myprs.PR){func(pull *myprs.PR) { pull.Partial = true }},
		}},
		{name: "findings open", opts: cRoundOpts{pull: []func(pull *myprs.PR){cBotThread}}},
		{name: "Greptile not reviewed yet", opts: cRoundOpts{
			pull: []func(pull *myprs.PR){func(pull *myprs.PR) { pull.Reviews = nil }},
		}},
		{name: "asked for another sha", opts: cRoundOpts{
			launch: []func(launch *myprs.Launch){func(launch *myprs.Launch) {
				launch.AskedSha = new("older")
			}},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			round := cReadyRound(t, tt.opts)
			round.poll(10 * cMinuteMs)
			cSame(t, "drafts", len(round.drafts()), 0)
		})
	}
}

func TestCLaterRoundThatPushedNothingKeepsAnEarlierRoundsReadiness(t *testing.T) {
	t.Parallel()

	// Round 1 asked for this head and Greptile reviewed it; round 2 started from it, pushed
	// nothing and was disarmed.
	earlier := []func(launch *myprs.Launch){cAskedNew}
	round := cReadyRound(t, cRoundOpts{
		extra:  earlier,
		launch: []func(launch *myprs.Launch){cLaunchHead("new"), cNeverAsked, cDisarmed},
	})
	round.poll(10 * cMinuteMs)
	cSame(t, "status", round.status(), "ready")

	drafts := round.drafts()
	if len(drafts) != 1 {
		t.Fatalf("drafts: got %d, want 1", len(drafts))
	}

	if !strings.Contains(drafts[0].Text, "round 2") {
		t.Errorf("the draft %q lacks round 2", drafts[0].Text)
	}

	// Findings and waiting read the same launch.
	waiting := cNewRound(t, cRoundOpts{
		extra: earlier, launch: []func(launch *myprs.Launch){cLaunchHead("new"), cDisarmed},
	})
	waiting.poll(10 * cMinuteMs)
	cSame(t, "status waiting", waiting.status(), "waiting")
}

func TestCLaterArmedRoundHidesAnEarlierRoundsReadiness(t *testing.T) {
	t.Parallel()

	// Round 2 is a round to paste whose command has not been pasted: armed, at this head.
	round := cReadyRound(t, cRoundOpts{
		extra: []func(launch *myprs.Launch){cAskedNew},
		launch: []func(launch *myprs.Launch){
			cLaunchHead("new"), cNeverAsked, cManual,
			func(launch *myprs.Launch) { launch.Armed = true },
		},
	})
	round.poll(10 * cMinuteMs)
	cSame(t, "armed", round.saved()[1].Armed, true)
	cSame(t, "status", round.status(), "idle")
	cSame(t, "drafts", len(round.drafts()), 0)
}

// cRounds are the saved launches' rounds, armed flags and asked shas.
func cRounds(launches []*myprs.Launch) []string {
	out := make([]string, 0, len(launches))

	for _, launch := range launches {
		asked := "null"
		if launch.AskedSha != nil {
			asked = *launch.AskedSha
		}

		out = append(out, js.Number(launch.Round)+" "+js.String(launch.Armed)+" "+asked)
	}

	return out
}

func TestCRoundToPasteThenEmptyRoundLeavesTheReviewedHeadReady(t *testing.T) {
	t.Parallel()

	// Round 2 asked for this head, Greptile reviewed it and the draft is saved (round 1 is long
	// over).
	round := cReadyRound(t, cRoundOpts{extra: []func(launch *myprs.Launch){cDisarmed}})
	round.poll(0)
	cSame(t, "status", round.status(), "ready")
	cSame(t, "draft", round.savedDraft().State, "saved")
	// Round 3: cmux fails, the command is never pasted.
	round.world.Change(func(w *World) {
		w.LaunchResult = func(myprs.LaunchOpts) myprs.LaunchResult {
			return myprs.LaunchResult{
				OK: false, Error: "cmux is not running", ManualCommand: "cd /wt && claude",
				Worktree: "/wt/self-repo-7-3", HeadSha: "new",
			}
		}
	})

	command, _ := round.start().Get("manualCommand")
	cSame(t, "manual command", command, "cd /wt && claude")
	cSame(t, "status after round 3", round.status(), "idle")
	// Round 4, six minutes later: it finds nothing to fix, pushes nothing and ends.
	round.world.Advance(6 * cMinuteMs)
	round.world.Change(func(w *World) { w.LaunchResult = nil })
	cSame(t, "round 4", js.Stringify(round.start()), `{"ok":true,"name":"self-repo-7-4"}`)
	cSame(t, "launches", cRounds(round.saved()),
		[]string{"1 false null", "2 false new", "3 false null", "4 true null"})
	round.poll(10 * cMinuteMs)
	cSame(t, "launches after", cRounds(round.saved()),
		[]string{"1 false null", "2 false new", "3 false null", "4 false null"})
	cSame(t, "status after round 4", round.status(), "ready")
	cSame(t, "asks", len(round.asks()), 0)
	// The round to paste, pasted after all, is not the one that decides: its push would not be
	// asked for (the head moving ends readiness).
	pasted := cSession("self-repo-7-3", "/wt/self-repo-7-3", "busy")
	pasted.SessionID = "s3"
	pasted.PID = 3
	round.setSessions(pasted)
	round.setPR(pr(cHead("newer")))
	round.poll(cMinuteMs)
	round.setSessions()
	round.poll(10 * cMinuteMs)
	cSame(t, "asks at the end", len(round.asks()), 0)
}

// The alerts half of Bun's test (diffAlerts from src/lib/alerts.ts) is page code: only the
// server half is here.
func TestCReReviewThatPushesNothingLeavesTheRowReadyAgain(t *testing.T) {
	t.Parallel()

	round := cReadyRound(t, cRoundOpts{extra: []func(launch *myprs.Launch){cDisarmed}})
	round.poll(0)
	cSame(t, "draft", round.savedDraft().State, "saved")
	cSame(t, "press", js.Stringify(round.start()), `{"ok":true,"name":"self-repo-7-3"}`)

	working := cSession("self-repo-7-3", "/wt/self-repo-7-3", "busy")
	working.SessionID = "s3"
	working.PID = 3
	round.setSessions(working)
	round.poll(cMinuteMs)
	cSame(t, "status running", round.status(), "running")
	round.setSessions()
	// The round ended with nothing pushed.
	round.poll(2 * cMinuteMs)

	launches := round.saved()
	cSame(t, "armed", launches[len(launches)-1].Armed, false)
	cSame(t, "status", round.status(), "ready")
	cSame(t, "asks", len(round.asks()), 0)
}

// cPollNoWait runs a poll and waits for it alone, not for the drafts it started.
func cPollNoWait(t *testing.T, round *cRound, advanceMs float64) {
	t.Helper()

	round.world.Advance(advanceMs)
	seq := round.myPrs.Refresh()
	round.myPrs.Tick()
	cWaitFor(t, "the poll", func() bool { return round.myPrs.Snapshot().Refresh.Done >= seq })
}

func TestCTwoPollsNeverRunTwoDraftsForOnePR(t *testing.T) {
	t.Parallel()

	round := cReadyRound(t, cRoundOpts{})
	release := make(chan struct{})

	var started atomic.Int32

	round.world.Change(func(w *World) {
		w.DraftResult = func() myprs.DraftResult {
			started.Add(1)
			<-release

			return myprs.DraftResult{OK: true, Error: "", Final: false}
		}
	})

	var once sync.Once

	defer once.Do(func() { close(release) })

	cPollNoWait(t, round, 0)
	cWaitFor(t, "the draft", func() bool { return started.Load() == 1 })
	cPollNoWait(t, round, cMinuteMs)
	cPollNoWait(t, round, cMinuteMs)
	cSame(t, "started", started.Load(), int32(1))
	// The draft being held back does not hold the poll up.
	refresh := round.myPrs.Snapshot().Refresh
	cSame(t, "done", refresh.Done, refresh.Seq)
	once.Do(func() { close(release) })
	round.myPrs.Settle()
	cSame(t, "state", round.savedDraft().State, "saved")
	round.poll(cMinuteMs)
	cSame(t, "started after", started.Load(), int32(1))
}

func TestCDryRunsNoDraft(t *testing.T) {
	t.Parallel()

	round := cReadyRound(t, cRoundOpts{deps: func(deps *myprs.Deps) { deps.Config.Dry = true }})
	round.poll(10 * cMinuteMs)
	cSame(t, "drafts", len(round.drafts()), 0)
}

func TestCFailingDraftIsAFailedDraftNotAFailedPoll(t *testing.T) {
	t.Parallel()

	round := cReadyRound(t, cRoundOpts{})
	round.setDraft(myprs.DraftResult{OK: false, Error: "boom \x1b[31m", Final: false})
	round.poll(0)
	cSame(t, "error", round.myPrs.Snapshot().Error, (*string)(nil))

	saved := round.savedDraft()
	cSame(t, "state", saved.State, "failed")

	if saved.Error == nil {
		t.Fatal("the failed draft has no error")
	}

	if strings.Contains(*saved.Error, "\x1b") {
		t.Errorf("the error %q keeps an escape", *saved.Error)
	}
}

var cPullRequest = regexp.MustCompile(`(?i)pull request`)

func TestCPRWhoseURLIsNotAPullRequestIsNotDrafted(t *testing.T) {
	t.Parallel()

	round := cReadyRound(t, cRoundOpts{pull: []func(pull *myprs.PR){func(pull *myprs.PR) {
		pull.URL = "https://evil.example/org/repo/pull/7"
	}}})
	round.poll(0)
	cSame(t, "drafts", len(round.drafts()), 0)

	saved := round.savedDraft()
	cSame(t, "state", saved.State, "failed")
	cSame(t, "text", saved.Text, "")

	if saved.Error == nil {
		t.Fatal("the failed draft has no error")
	}

	if !cPullRequest.MatchString(*saved.Error) {
		t.Errorf("the error %q does not say pull request", *saved.Error)
	}
}

func TestCSavedDraftForAnOlderHeadIsNotShown(t *testing.T) {
	t.Parallel()

	round := cReadyRound(t, cRoundOpts{pull: []func(pull *myprs.PR){cHead("new")}})
	round.poll(0)
	round.setPR(pr(cHead("moved")))
	round.poll(0)
	cSame(t, "row's draft", round.row().SlackDraft, (*myprs.SlackDraft)(nil))
}

func TestCDraftRetriesStopAfterTheCap(t *testing.T) {
	t.Parallel()

	cSame(t, "DraftTriesMax", myprs.DraftTriesMax, 3)

	round := cReadyRound(t, cRoundOpts{})
	round.setDraft(myprs.DraftResult{OK: false, Error: "timed out after 90s", Final: false})
	round.poll(0)
	cSame(t, "tries 1", round.savedDraft().Tries, 1)
	round.poll(myprs.DraftRetryMs)
	cSame(t, "tries 2", round.savedDraft().Tries, 2)
	round.poll(myprs.DraftRetryMs)
	cSame(t, "tries 3", round.savedDraft().Tries, 3)

	for range 12 {
		round.poll(cTwoHoursMs)
	}

	drafts := round.drafts()
	cSame(t, "drafts", len(drafts), 3)
	// The row keeps the failure and the text to copy.
	saved := round.savedDraft()
	cSame(t, "state", saved.State, "failed")
	cSame(t, "error", saved.Error, new("timed out after 90s"))
	cSame(t, "tries", saved.Tries, 3)
	cSame(t, "status", round.status(), "draft-failed")
	cSame(t, "row's text", round.row().SlackDraft.Text, drafts[0].Text)
	// Nothing tries it again: no retry time.
	cSame(t, "retry at", round.row().DraftRetryAt, (*float64)(nil))
}

func TestCNoRetryTimeOnceThePRIsNoLongerClean(t *testing.T) {
	t.Parallel()

	round := cReadyRound(t, cRoundOpts{})
	round.setDraft(myprs.DraftResult{OK: false, Error: "nope", Final: false})
	round.poll(0)
	cSame(t, "retry at", round.row().DraftRetryAt, new(round.now()+myprs.DraftRetryMs))
	round.setPR(pr(cHead("new"), cReviewed("new"), cBotThread))
	round.poll(cMinuteMs)
	cSame(t, "status", round.status(), "findings")
	cSame(t, "retry at after", round.row().DraftRetryAt, (*float64)(nil))
}

func TestCDraftRefusedAsOneIsWaitingIsNotTriedAgain(t *testing.T) {
	t.Parallel()

	round := cReadyRound(t, cRoundOpts{})
	why := "a draft is already waiting in #code-review: send or delete it; the text to copy is " +
		"below"
	round.setDraft(myprs.DraftResult{OK: false, Error: why, Final: true})
	round.poll(0)

	drafts := round.drafts()
	cSame(t, "drafts", len(drafts), 1)
	// The attempts are used at once, so the row needs me now (the alert and the badge go by
	// the count).
	saved := round.savedDraft()
	cSame(t, "state", saved.State, "failed")
	cSame(t, "error", saved.Error, new(why))
	cSame(t, "tries", saved.Tries, myprs.DraftTriesMax)
	cSame(t, "text", saved.Text, drafts[0].Text)
	cSame(t, "status", round.status(), "draft-failed")

	for range 4 {
		round.poll(myprs.DraftRetryMs)
	}

	cSame(t, "drafts later", len(round.drafts()), 1)
	// A new head is a new draft, counted from one.
	round.setDraft(myprs.DraftResult{OK: true, Error: "", Final: false})
	round.setPR(cNewerReviewed())
	round.editFile(cAskNewer)

	again := myprs.New(context.Background(), round.world.Deps(round.file))
	round.world.Advance(cMinuteMs)
	again.Refresh()
	tick(again)
	cSame(t, "drafts after", len(round.drafts()), 2)

	saved = round.savedDraft()
	cSame(t, "sha after", saved.Sha, "newer")
	cSame(t, "state after", saved.State, "saved")
	cSame(t, "tries after", saved.Tries, 1)
}

func TestCDraftTriesSurviveARestart(t *testing.T) {
	t.Parallel()

	round := cReadyRound(t, cRoundOpts{})
	round.setDraft(myprs.DraftResult{OK: false, Error: "nope", Final: false})
	round.poll(0)
	round.poll(myprs.DraftRetryMs)
	cSame(t, "tries", round.savedDraft().Tries, 2)
	// A new poller on the same file: one more attempt is left, then none.
	world, again := cRestart(t, round.file, round.cur)
	cSetDraft(world, myprs.DraftResult{OK: false, Error: "nope", Final: false})
	world.Change(func(w *World) { w.Now = round.now() + myprs.DraftRetryMs })
	tick(again)
	cSame(t, "drafts", len(cDrafts(world)), 1)
	cSame(t, "tries after", round.savedDraft().Tries, 3)

	for range 4 {
		cPollWorld(world, again, cTwoHoursMs)
	}

	cSame(t, "drafts later", len(cDrafts(world)), 1)
	// And another restart after the cap drafts nothing.
	third, last := cRestart(t, round.file, round.cur)
	later := cWorldNow(world) + 24*hourMs

	third.Change(func(w *World) { w.Now = later })
	tick(last)
	cSame(t, "drafts at the end", len(cDrafts(third)), 0)
}

func cWorldNow(world *World) float64 {
	var now float64

	world.Change(func(w *World) { now = w.Now })

	return now
}

func TestCNewHeadStartsAgainAtOneAttempt(t *testing.T) {
	t.Parallel()

	round := cReadyRound(t, cRoundOpts{})
	round.setDraft(myprs.DraftResult{OK: false, Error: "nope", Final: false})
	round.poll(0)
	round.poll(myprs.DraftRetryMs)
	round.poll(myprs.DraftRetryMs)
	cSame(t, "drafts", len(round.drafts()), 3)
	round.editFile(func(entry *myprs.Entry) { entry.Launches[0].AskedSha = new("newer") })

	world, again := cRestart(t, round.file, cNewerReviewed())
	cSetDraft(world, myprs.DraftResult{OK: false, Error: "again", Final: false})
	tick(again)
	cSame(t, "drafts after", len(cDrafts(world)), 1)
	cSame(t, "sha", round.savedDraft().Sha, "newer")
	cSame(t, "tries", round.savedDraft().Tries, 1)
	// The retry that works is attempt two, and is final.
	cSetDraft(world, myprs.DraftResult{OK: true, Error: "", Final: false})
	cPollWorld(world, again, myprs.DraftRetryMs)
	cSame(t, "state at the end", round.savedDraft().State, "saved")
	cSame(t, "tries at the end", round.savedDraft().Tries, 2)
}
