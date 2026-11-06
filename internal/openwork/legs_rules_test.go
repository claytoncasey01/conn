package openwork_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/answers"
	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/openwork"
)

// The rules over a run's legs that outlive one press: one review move at a time, each press's
// error kept, a run file whose legs are usable, the run's PR state for History, the worktree's
// origin before a push, the shared session's reach, and the page's per-leg rules held to the
// server's.

func TestTwoPressesInQuickSuccessionMoveTheTicketOnce(t *testing.T) {
	t.Parallel()

	for _, policy := range []string{"first", "all"} {
		t.Run(policy, func(t *testing.T) {
			t.Parallel()

			made, _ := prLegsWorld(t, [2]string{"2\n", "2\n"}, "", `{"ahead":2}`, `{"ahead":2}`)
			made.update("atlassian", reviewSettings(policy))

			waiting := newLatch()
			made.transition = func(ctx context.Context, _ string, _ jira.Move) (jira.Moved,
				error,
			) {
				waiting.wait(ctx)

				return jira.Moved{To: "In Review", Moved: true}, nil
			}

			same(t, "widgets", made.work.OpenPR("PROJ-1", "acme/widgets").Code, 200)

			if policy == "first" {
				until(t, "the move", func() bool { return made.has("transition") })
			}

			same(t, "gadgets", made.work.OpenPR("PROJ-1", "acme/gadgets").Code, 200)
			until(t, "both PRs", func() bool { return made.shownPR(1) })
			until(t, "the move", func() bool { return made.has("transition") })
			waiting.release()
			made.work.Settle()

			same(t, "moved once", made.countPrefix("transition"), 1)

			moving := 0

			for _, line := range made.record("PROJ-1").Log {
				if line.Text == "Moving PROJ-1 to its review status" {
					moving++
				}
			}

			same(t, "said once", moving, 1)
			same(t, "recorded", made.record("PROJ-1").ReviewMoved, true)
		})
	}
}

func TestEachPressKeepsItsOwnError(t *testing.T) {
	t.Parallel()

	made, folders := prLegsWorld(t, [2]string{"2\n", "2\n"}, "")
	answer := made.sh
	waiting := newLatch()
	made.sh = func(name string, args []string, dir string) (string, error) {
		if args[0] != "push" {
			return answer(name, args, dir)
		}

		if dir == folders.worktrees[0] {
			waiting.wait(context.Background())

			return "", fakeError("widgets hung up")
		}

		return "", fakeError("gadgets hung up")
	}

	same(t, "widgets", made.work.OpenPR("PROJ-1", "acme/widgets").Code, 200)
	until(t, "widgets' push", func() bool { return made.pushes(folders.worktrees[0]) == 1 })
	same(t, "gadgets", made.work.OpenPR("PROJ-1", "acme/gadgets").Code, 200)
	until(t, "gadgets' error", func() bool { return made.record("PROJ-1").Error != nil })
	waiting.release()
	made.work.Settle()

	same(t, "both", *made.record("PROJ-1").Error, "acme/gadgets: gadgets hung up; "+
		"acme/widgets: widgets hung up")
}

func TestARunWhoseLegsRepeatOrStrayIsNotRead(t *testing.T) {
	t.Parallel()

	leg := func(repo, worktree string) string {
		return `{"repo":"` + repo + `","clone":"/x/c","base":"main","prBase":"main",` +
			`"branch":"proj-1-fix","worktree":"` + worktree + `","worktreeReady":true,` +
			`"session":null,"claudeSessionId":null,"manualCommand":null,"ahead":0,"dirty":0,` +
			`"pr":null,"cleaned":null}`
	}

	rows := []struct {
		name, repos string
		read        bool
	}{
		{"two distinct legs", leg("acme/a", "/x/a") + "," + leg("acme/b", "/x/b"), true},
		{"a repeated repository", leg("acme/a", "/x/a") + "," + leg("acme/a", "/x/b"), false},
		{"in another case", leg("acme/a", "/x/a") + "," + leg("ACME/A", "/x/b"), false},
		{"two with no repository", leg("", "/x/a") + "," + leg("", "/x/b"), false},
		{"one with no repository", leg("", "/x/a"), true},
		{"a repeated worktree", leg("acme/a", "/x/a") + "," + leg("acme/b", "/x/a"), false},
		{"a relative worktree", leg("acme/a", "x/a") + "," + leg("acme/b", "/x/b"), false},
		{"an unclean worktree", leg("acme/a", "/x/../x/a") + "," + leg("acme/b", "/x/b"), false},
		{"one leg, a relative worktree", leg("acme/a", "x/a"), true},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			_, read := openwork.DecodeRecord([]byte(`{"key":"PROJ-1","title":"t",` +
				`"state":"failed","agents":[],"questions":[],"layout":"split","repos":[` +
				row.repos + `]}`))
			same(t, "read", read, row.read)
		})
	}

	legacy := strings.Replace(legacyRunText, `"/x/widgets-proj-1"`, `"x/../widgets-proj-1"`, 1)
	_, read := openwork.DecodeRecord([]byte(legacy))
	same(t, "a legacy run with a relative worktree", read, true)
}

func TestHistorySaysARunIsMergedOnceItCountsAsMerged(t *testing.T) {
	t.Parallel()

	rows := []struct{ widgets, gadgets, want string }{
		{"closed", "merged", "merged"},
		{"merged", "closed", "merged"},
		{"merged", "open", "merged"},
		{"open", "merged", "open"},
		{"closed", "closed", "closed"},
	}

	for _, row := range rows {
		t.Run(row.widgets+" "+row.gadgets, func(t *testing.T) {
			t.Parallel()

			made, _ := legsWorld(t, "split", `{"state":"pr_open","stage":"pr"}`,
				settledPR("acme/widgets", row.widgets), settledPR("acme/gadgets", row.gadgets))

			runs := made.work.Runs()
			same(t, "state", runs[0].Facts().PRState, row.want)
		})
	}
}

func TestTheSharedSessionReachesOnlyTheWorktreesStillThere(t *testing.T) {
	t.Parallel()

	known := `{"claudeSessionId":"` + sessionUUIDText + `"}`

	cleaned, worktrees := legsWorld(t, "shared", "", known, `{"cleaned":true}`)
	same(t, "resume", cleaned.work.Resume("PROJ-1", "").Code, 200)
	same(t, "cleaned", cleaned.cmuxLines(), []string{resumeLine(worktrees.worktrees[0])})

	gone, worktrees := legsWorld(t, "shared", "", known, "")

	err := os.RemoveAll(worktrees.worktrees[1])
	if err != nil {
		t.Fatal(err)
	}

	same(t, "resume", gone.work.Resume("PROJ-1", "").Code, 200)
	same(t, "gone", gone.cmuxLines(), []string{resumeLine(worktrees.worktrees[0])})
}

// The page's per-leg rules (src/lib/runLegs.ts) and the server's, held to the same answers in
// testdata/answers/openwork/legs.json.
func TestTheLegRulesMatchTheRecordedAnswers(t *testing.T) {
	t.Parallel()

	for _, recorded := range answers.Load(t, "openwork/legs.json", "legs") {
		record, read := openwork.DecodeRecord([]byte(js.Stringify(recorded.Args[0])))
		if !read {
			t.Fatalf("%s is not a run", js.Stringify(recorded.Args[0]))
		}

		answers.Check(t, recorded, openwork.LegRules(record))
	}
}

// shownPR is whether PROJ-1's leg at index has a PR, as the page is shown it (read under the
// lock, so it may be asked while a press is out).
func (w *world) shownPR(index int) bool {
	for _, run := range listOf(runsOf(w.t, w.work)) {
		if get(run, "key") == "PROJ-1" {
			return get(listOf(get(run, "repos"))[index], "pr") != nil
		}
	}

	return false
}
