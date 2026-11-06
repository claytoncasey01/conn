package openwork_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/openwork"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// The dry run (CONN_WORK_DRY): the whole pipeline on timers, touching nothing outside this
// process, from START to a merged PR, the ticket move and the cleanup.

func TestTheDryRunFollowsThePipelineToAMergedPR(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	made := newWorld(t, worldOpts{
		base: base, settings: workSettings(makeClone(t, base), ""),
		dry: true,
	})

	made.work.Tickets()
	until(t, "the dry list", func() bool { return len(made.work.Tickets().Tickets) == 6 })
	same(t, "start", made.work.Start("PROJ-2", body(t, `{"model":"haiku"}`)).Code, 200)
	until(t, "the questions", func() bool { return made.state("PROJ-2") == "asking" })
	same(t, "answers", made.work.Answer("PROJ-2", body(t, `{"q1":"c1","q2":"own words"}`)).Code,
		200)
	// The build's dry session has opened before Resume is pressed: a press made while it is
	// still opening starts none of its own.
	made.work.Settle()

	lead := made.record("PROJ-2").Repos[0]
	same(t, "session", parsed(t, lead.Session), body(t, `{"name":"PROJ-2","sessionId":null,`+
		`"pid":null,"status":"busy"}`))
	same(t, "counts", []float64{lead.Ahead, lead.Dirty}, []float64{3, 2})
	same(t, "resume", made.work.Resume("PROJ-2", "").Code, 200)
	same(t, "open", made.work.OpenPR("PROJ-2", "").Code, 200)
	until(t, "the PR", func() bool { return made.state("PROJ-2") == "pr_open" })
	same(t, "url", made.record("PROJ-2").Repos[0].PR.URL,
		"https://github.com/acme/widgets/pull/1702")

	states := []string{}

	for range 6 {
		made.setClock(made.now() + 5000)
		made.work.Tick()
		made.work.Settle()

		if status := made.record("PROJ-2").Repos[0].PR.Status; status != nil {
			states = append(states, status.State+"/"+status.Checks.State+"/"+status.Review)
		}
	}

	same(t, "polls", states, []string{
		"open/pending/REVIEW_REQUIRED", "open/pending/REVIEW_REQUIRED",
		"open/passing/REVIEW_REQUIRED", "open/passing/APPROVED", "merged/passing/APPROVED",
		"merged/passing/APPROVED",
	})
	same(t, "move", made.work.MoveTicket("PROJ-2", " Done ").Code, 200)
	made.work.Settle()
	same(t, "jira", made.record("PROJ-2").JiraStatus, "Done")
	same(t, "cleanup", made.work.Cleanup("PROJ-2", "").Code, 200)
	same(t, "again", errorOf(made.work.Cleanup("PROJ-2", "")), "the worktree is already removed")
	same(t, "resume", errorOf(made.work.Resume("PROJ-2", "")), "PROJ-2 is cleaned up: nothing to "+
		"resume")
	same(t, "nothing ran", helperOrGit(made), []string{})
	same(t, "a dry session for the build and for the resume", made.count("dry-session"), 2)
}

// TestADryResumeReusesItsRunningSession: Resume in a dry run opens no second session while the
// run's dry session still runs, so presses cannot fill the host.
func TestADryResumeReusesItsRunningSession(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	made := newWorld(t, worldOpts{
		base: base, settings: workSettings(makeClone(t, base), ""), dry: true,
	})

	made.work.Tickets()
	until(t, "the dry list", func() bool { return len(made.work.Tickets().Tickets) == 6 })
	same(t, "start", made.work.Start("PROJ-2", body(t, `{"model":"haiku"}`)).Code, 200)
	until(t, "the questions", func() bool { return made.state("PROJ-2") == "asking" })
	same(t, "answers", made.work.Answer("PROJ-2", body(t, `{"q1":"c1","q2":"x"}`)).Code, 200)
	made.work.Settle()

	made.mu.Lock()
	made.dryRunning = true
	made.mu.Unlock()

	for range 3 {
		same(t, "resume", made.work.Resume("PROJ-2", "").Code, 200)
	}

	same(t, "one dry session", made.count("dry-session"), 1)
}

// TestADryResumeWhileOneOpensOpensNoSecond: a press of Resume while another press's dry session
// is still being opened (the lock let go for the host) opens no second one, as one that runs
// does not.
func TestADryResumeWhileOneOpensOpensNoSecond(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	made := newWorld(t, worldOpts{
		base: base, settings: workSettings(makeClone(t, base), ""), dry: true,
	})

	made.work.Tickets()
	until(t, "the dry list", func() bool { return len(made.work.Tickets().Tickets) == 6 })
	same(t, "start", made.work.Start("PROJ-2", body(t, `{"model":"haiku"}`)).Code, 200)
	until(t, "the questions", func() bool { return made.state("PROJ-2") == "asking" })
	same(t, "answers", made.work.Answer("PROJ-2", body(t, `{"q1":"c1","q2":"x"}`)).Code, 200)
	made.work.Settle()

	gate := make(chan struct{})

	made.mu.Lock()
	made.dryOpenGate = gate
	made.mu.Unlock()

	first := make(chan int)

	go func() { first <- made.work.Resume("PROJ-2", "").Code }()

	until(t, "the first open", func() bool { return made.count("dry-session") == 2 })

	second := make(chan int, 1)

	go func() { second <- made.work.Resume("PROJ-2", "").Code }()

	select {
	case code := <-second:
		same(t, "second resume", code, 200)
	case <-time.After(testtime.Scaled(secondPressWait)):
		close(gate)
		t.Fatal("the second press is opening a session of its own")
	}

	close(gate)
	same(t, "first resume", <-first, 200)
	made.work.Settle()
	same(t, "one dry session opened by the presses", made.count("dry-session"), 2)
}

// TestADryOpenThatFailedLeavesTheSlotFree: a dry session that could not be opened leaves
// nothing marked, so the next Resume opens one.
func TestADryOpenThatFailedLeavesTheSlotFree(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	made := newWorld(t, worldOpts{
		base: base, settings: workSettings(makeClone(t, base), ""), dry: true,
	})

	made.mu.Lock()
	made.dryOpenFails = true
	made.mu.Unlock()

	made.work.Tickets()
	until(t, "the dry list", func() bool { return len(made.work.Tickets().Tickets) == 6 })
	same(t, "start", made.work.Start("PROJ-2", body(t, `{"model":"haiku"}`)).Code, 200)
	until(t, "the questions", func() bool { return made.state("PROJ-2") == "asking" })
	same(t, "answers", made.work.Answer("PROJ-2", body(t, `{"q1":"c1","q2":"x"}`)).Code, 200)
	made.work.Settle()

	made.mu.Lock()
	made.dryOpenFails = false
	made.mu.Unlock()

	same(t, "resume", made.work.Resume("PROJ-2", "").Code, 200)
	same(t, "the failed open, then the resume's", made.count("dry-session"), 2)
}

// TestADryOpenWithNoHostedIDLeavesTheSlotFree: an open that says it started but names no hosted
// session leaves nothing that reads as still opening, so the next Resume opens one.
func TestADryOpenWithNoHostedIDLeavesTheSlotFree(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	made := newWorld(t, worldOpts{
		base: base, settings: workSettings(makeClone(t, base), ""), dry: true,
	})

	made.mu.Lock()
	made.dryOpenNoID = true
	made.mu.Unlock()

	made.work.Tickets()
	until(t, "the dry list", func() bool { return len(made.work.Tickets().Tickets) == 6 })
	same(t, "start", made.work.Start("PROJ-2", body(t, `{"model":"haiku"}`)).Code, 200)
	until(t, "the questions", func() bool { return made.state("PROJ-2") == "asking" })
	same(t, "answers", made.work.Answer("PROJ-2", body(t, `{"q1":"c1","q2":"x"}`)).Code, 200)
	made.work.Settle()
	same(t, "resume", made.work.Resume("PROJ-2", "").Code, 200)
	same(t, "the build's open, then the resume's", made.count("dry-session"), 2)
}

// secondPressWait is how long a second press may take to answer without opening anything.
const secondPressWait = 5 * time.Second

// TestTheDryRunOpensItsSessionOnlyInsideConn: set to open sessions elsewhere, the dry run opens
// no session at all, as before there was one inside Conn.
func TestTheDryRunOpensItsSessionOnlyInsideConn(t *testing.T) {
	t.Parallel()

	for _, where := range []string{"terminal", "command"} {
		t.Run(where, func(t *testing.T) {
			t.Parallel()

			base := realTemp(t)
			made := newWorld(t, worldOpts{
				base: base, dry: true,
				settings: workSettings(makeClone(t, base), `,"sessions":{"where":"`+where+`"}`),
			})

			made.work.Tickets()
			until(t, "the dry list", func() bool { return len(made.work.Tickets().Tickets) == 6 })
			same(t, "start", made.work.Start("PROJ-2", body(t, `{"model":"haiku"}`)).Code, 200)
			until(t, "the questions", func() bool { return made.state("PROJ-2") == "asking" })
			same(t, "answers", made.work.Answer("PROJ-2", body(t, `{"q1":"c1","q2":"x"}`)).Code,
				200)
			made.work.Settle()
			same(t, "dry sessions", made.count("dry-session"), 0)
			same(t, "nothing ran", helperOrGit(made), []string{})
		})
	}
}

func TestAnOddTicketsDryChecksFailUntilFixChecksIsPressed(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	made := newWorld(t, worldOpts{
		base: base, settings: workSettings(makeClone(t, base), ""),
		dry: true, runs: []any{runRecord(t, "PROJ-3", `{"state":"pr_open","stage":"pr","pr":`+
			`{"url":"https://github.com/acme/widgets/pull/1703","number":1703}}`)},
	})

	poll := func() string {
		made.setClock(made.now() + 5000)
		made.work.Tick()
		made.work.Settle()

		return made.record("PROJ-3").Repos[0].PR.Status.Checks.State
	}

	same(t, "first", poll(), "pending")
	same(t, "failing", poll(), "failing")

	reply, err := made.work.FixChecks("PROJ-3", "")
	same(t, "fix", []any{reply.Code, err}, []any{200, nil})
	same(t, "fixed", []string{poll(), poll(), poll()}, []string{"pending", "pending", "passing"})
}

func TestNewMakesTheRunsFolderEvenWhileOpenWorkIsOff(t *testing.T) {
	t.Parallel()

	data := filepath.Join(t.TempDir(), "data")
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)

	openwork.New(ctx, openwork.Deps{DataDir: data})

	info, err := os.Stat(filepath.Join(data, "runs"))
	if err != nil {
		t.Fatal(err)
	}

	same(t, "folder", info.IsDir(), true)
}
