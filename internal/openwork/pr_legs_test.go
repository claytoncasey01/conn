package openwork_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/openwork"
)

// A run in several repositories from OPEN PR to its cleanup: each leg pushed and opened on its
// own, the ticket moved to review as Settings says, each PR polled, its checks fixed by the
// session working in its folder, and each worktree removed on its own.

// prNumber is the number of the PR the fake gh opens in repo.
func prNumber(repo string) string {
	if repo == "acme/gadgets" {
		return "6"
	}

	return "5"
}

// prLegsWorld is PROJ-1 in widgets and gadgets, sharing one session, in the state the fields
// over runRecord's say, each worktree with a PR body written. Git counts ahead[i] commits in leg
// i and gh opens PR 5 in widgets and 6 in gadgets.
func prLegsWorld(t *testing.T, ahead [2]string, fields string,
	legExtras ...string,
) (*world, legsRun) {
	t.Helper()

	made, folders := legsWorld(t, "shared", fields, legExtras...)

	for _, worktree := range folders.worktrees {
		writeBody(t, worktree)
	}

	made.sh = func(name string, args []string, dir string) (string, error) {
		if name == "gh" {
			repo := args[slices.Index(args, "--repo")+1]

			return "https://github.com/" + repo + "/pull/" + prNumber(repo) + "\n", nil
		}

		if args[0] == "rev-list" {
			return ahead[slices.Index(folders.worktrees, dir)], nil
		}

		return "", nil
	}

	return made, folders
}

func writeBody(t *testing.T, worktree string) {
	t.Helper()

	err := os.MkdirAll(filepath.Join(worktree, ".conn"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	writeJSON(t, filepath.Join(worktree, ".conn", "pr-body.md"), "body")
}

// openAndSettle presses OPEN PR for repo ("" every leg) and waits for it to end.
func openAndSettle(made *world, repo string) openwork.Reply {
	reply := made.work.OpenPR("PROJ-1", repo)
	made.work.Settle()

	return reply
}

// pushes are how many times the branch was pushed from worktree.
func (w *world) pushes(worktree string) int {
	return w.countPrefix("sh git push -u origin proj-1-a-ticket " + worktree + " ")
}

// prURLs are each leg's PR URL, "" for none.
func (w *world) prURLs() []string {
	out := []string{}

	for _, leg := range w.record("PROJ-1").Repos {
		if leg.PR == nil {
			out = append(out, "")

			continue
		}

		out = append(out, leg.PR.URL)
	}

	return out
}

const (
	widgetsPR = "https://github.com/acme/widgets/pull/5"
	gadgetsPR = "https://github.com/acme/gadgets/pull/6"
)

func TestOpenPRSkipsALegWithNoCommitsAndSaysWhich(t *testing.T) {
	t.Parallel()

	made, folders := prLegsWorld(t, [2]string{"2\n", "0\n"}, "")

	same(t, "open", openAndSettle(made, "").Code, 200)
	same(t, "pushed", []int{made.pushes(folders.worktrees[0]), made.pushes(folders.worktrees[1])},
		[]int{1, 0})
	same(t, "PRs", made.prURLs(), []string{widgetsPR, ""})
	same(t, "state", made.state("PROJ-1"), "pr_open")
	same(t, "skipped", made.hasLog("Skipped acme/gadgets: the branch has no commits yet"), true)
	same(t, "no error", made.record("PROJ-1").Error, (*string)(nil))
	same(t, "moved", made.countPrefix("transition PROJ-1"), 1)
	same(t, "recorded", made.record("PROJ-1").ReviewMoved, true)
}

func TestOpenPRWithNoCommitsAnywhereSaysTheBranchHasNone(t *testing.T) {
	t.Parallel()

	made, _ := prLegsWorld(t, [2]string{"0\n", "0\n"}, "")

	same(t, "open", openAndSettle(made, "").Code, 200)
	same(t, "error", *made.record("PROJ-1").Error, "the branch has no commits yet")
	same(t, "log", made.hasLog("Pull request failed: the branch has no commits yet"), true)
	same(t, "state", made.state("PROJ-1"), "building")
}

func TestASecondLegsFailedPushKeepsTheFirstPRAndOnlyItIsRetried(t *testing.T) {
	t.Parallel()

	made, folders := prLegsWorld(t, [2]string{"2\n", "1\n"}, "")
	answer := made.sh
	failing := true
	made.sh = func(name string, args []string, dir string) (string, error) {
		if args[0] == "push" {
			if dir == folders.worktrees[1] {
				if failing {
					return "", fakeError("the remote hung up")
				}
			}
		}

		return answer(name, args, dir)
	}

	same(t, "open", openAndSettle(made, "").Code, 200)
	same(t, "PRs", made.prURLs(), []string{widgetsPR, ""})
	same(t, "state", made.state("PROJ-1"), "pr_open")
	same(t, "error", *made.record("PROJ-1").Error, "acme/gadgets: the remote hung up")
	same(t, "log", made.hasLog("Pull request failed: acme/gadgets: the remote hung up"), true)
	same(t, "not moved while gadgets lacks its PR", made.countPrefix("transition"), 0)

	failing = false

	same(t, "again", openAndSettle(made, "").Code, 200)
	same(t, "widgets pushed once", made.pushes(folders.worktrees[0]), 1)
	same(t, "gadgets pushed twice", made.pushes(folders.worktrees[1]), 2)
	same(t, "PRs", made.prURLs(), []string{widgetsPR, gadgetsPR})
	same(t, "no error", made.record("PROJ-1").Error, (*string)(nil))
	same(t, "moved once every leg has its PR", made.countPrefix("transition"), 1)
	same(t, "nothing left", errorOf(made.work.OpenPR("PROJ-1", "")), "PROJ-1 is pr_open, "+
		"not building")
}

func TestEachLegIsRefusedOnItsOwn(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name string
		// gadgets are gadgets' own fields.
		gadgets string
		// offList is that gadgets leaves the list, noClone that its clone leaves Settings.
		offList, noClone bool
		said             string
	}{
		{
			"a protected branch", `{"branch":"main"}`, false, false,
			"acme/gadgets: refusing to push main",
		},
		{
			"a branch of another key", `{"branch":"proj-2-x"}`, false, false,
			"acme/gadgets: refusing to push proj-2-x",
		},
		{
			"a repository off the list", "", true, false,
			"acme/gadgets is not an allowed repository: add it in Settings.",
		},
		{
			"a repository with no clone", "", false, true,
			"No local clone of acme/gadgets: set a folder in Settings.",
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			made, folders := prLegsWorld(t, [2]string{"2\n", "2\n"}, "", "",
				row.gadgets)
			if row.offList {
				allowOnly(made, folders)
			}

			if row.noClone {
				made.update("repos", `{"allow":["acme/*"],"clones":{"acme/widgets":`+
					js.Stringify(folders.clones[0])+`},"cloneRoot":"/nowhere"}`)
			}

			same(t, "open", openAndSettle(made, "").Code, 200)
			same(t, "PRs", made.prURLs(), []string{widgetsPR, ""})
			same(t, "gadgets not pushed", made.pushes(folders.worktrees[1]), 0)
			same(t, "said", made.hasLog(row.said), true)

			reply := openAndSettle(made, "acme/gadgets")
			same(t, "named", []any{reply.Code > 399, errorOf(reply)}, []any{true, row.said})
		})
	}
}

func TestALegWithNoSessionOfItsOwnGetsAPRBodyMadeFromItsCommits(t *testing.T) {
	t.Parallel()

	made, folders := prLegsWorld(t, [2]string{"2\n", "2\n"}, "")

	// In the shared layout only the lead's worktree has .conn/: the brief is written there.
	err := os.RemoveAll(filepath.Join(folders.worktrees[1], ".conn"))
	if err != nil {
		t.Fatal(err)
	}

	answer := made.sh
	made.sh = func(name string, args []string, dir string) (string, error) {
		if args[0] == "log" {
			return "- Add the gadget", nil
		}

		return answer(name, args, dir)
	}

	same(t, "open", openAndSettle(made, "").Code, 200)
	same(t, "PRs", made.prURLs(), []string{widgetsPR, gadgetsPR})
	same(t, "body", strings.Contains(text(fileOrNil(filepath.Join(folders.worktrees[1], ".conn",
		"pr-body.md"))), "- Add the gadget"), true)
}

func TestOpenPRRefusesWhenEveryLegIsRefused(t *testing.T) {
	t.Parallel()

	made, _ := prLegsWorld(t, [2]string{"2\n", "2\n"}, "", `{"branch":"main"}`,
		`{"branch":"main"}`)

	reply := made.work.OpenPR("PROJ-1", "")
	same(t, "refused", []any{reply.Code, errorOf(reply)}, []any{400, "acme/widgets: refusing " +
		"to push main; acme/gadgets: refusing to push main"})
	same(t, "calls", made.has("sh "), false)
}

func TestOpenPRForOneRepositoryOpensOnlyItsPR(t *testing.T) {
	t.Parallel()

	made, folders := prLegsWorld(t, [2]string{"2\n", "2\n"}, "")
	route := openwork.Route{Action: openwork.ActionPR, Key: "PROJ-1"}

	reply, err := made.work.Handle(route, body(t, `{"repo":"acme/gadgets"}`))
	made.work.Settle()
	same(t, "reply", []any{reply.Code, err}, []any{200, nil})
	same(t, "PRs", made.prURLs(), []string{"", gadgetsPR})
	same(t, "widgets not counted", made.has("sh git rev-list --count origin/main..HEAD "+
		folders.worktrees[0]), false)

	refusals := []struct{ body, message string }{
		{`{"repo":"acme/gadgets"}`, "acme/gadgets already has a pull request"},
		{`{"repo":"ACME/Gadgets"}`, "acme/gadgets already has a pull request"},
		{`{"repo":"acme/other"}`, "PROJ-1 does not build in acme/other"},
		{`{"repo":3}`, "repo must be a repository name"},
	}

	for _, refusal := range refusals {
		t.Run(refusal.body, func(t *testing.T) {
			t.Parallel()

			reply, _ := made.work.Handle(route, body(t, refusal.body))
			same(t, "refused", errorOf(reply), refusal.message)
		})
	}
}

func TestALegWhoseRepositoryLeavesTheListWhileAnotherPushesIsNeverPushed(t *testing.T) {
	t.Parallel()

	made, folders := prLegsWorld(t, [2]string{"2\n", "2\n"}, "")
	answer := made.sh
	made.sh = func(name string, args []string, dir string) (string, error) {
		if args[0] == "push" {
			if dir == folders.worktrees[0] {
				allowOnly(made, folders)
			}
		}

		return answer(name, args, dir)
	}

	same(t, "open", openAndSettle(made, "").Code, 200)
	same(t, "gadgets not pushed", made.pushes(folders.worktrees[1]), 0)
	same(t, "PRs", made.prURLs(), []string{widgetsPR, ""})
	same(t, "error", *made.record("PROJ-1").Error, "acme/gadgets is not an allowed repository: "+
		"add it in Settings. proj-1-a-ticket was not pushed: press OPEN PR again once that is "+
		"fixed.")
}

// reviewSettings are twoClones' Atlassian settings moving the ticket to review as policy says.
func reviewSettings(policy string) string {
	return `{"enabled":true,"jira":{"projects":["PROJ"],"buildRepo":"acme/widgets",` +
		`"reviewMove":"` + policy + `"}}`
}

func TestTheReviewMoveRunsOnceAndAgainOnlyAfterAFailure(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name   string
		policy string
		// fails is that Jira fails the first move.
		fails bool
		// moves are the moves made after each leg's PR opens, in total.
		moves []int
	}{
		{"first", "first", false, []int{1, 1}},
		{"first, retried after a failure", "first", true, []int{1, 2}},
		{"all", "all", false, []int{0, 1}},
		{"never", "never", false, []int{0, 0}},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			// The tick counted both legs' commits before either PR was opened.
			made, _ := prLegsWorld(t, [2]string{"2\n", "2\n"}, "", `{"ahead":2}`,
				`{"ahead":2}`)
			made.update("atlassian", reviewSettings(row.policy))

			failed := false
			made.transition = func(context.Context, string, jira.Move) (jira.Moved, error) {
				if row.fails {
					if !failed {
						failed = true

						return jira.Moved{}, fakeError("Jira is down")
					}
				}

				return jira.Moved{To: "In Review", Moved: true}, nil
			}

			for index, repo := range []string{"acme/widgets", "acme/gadgets"} {
				same(t, repo, openAndSettle(made, repo).Code, 200)
				same(t, repo+" moves", made.countPrefix("transition"), row.moves[index])
			}

			same(t, "recorded", made.record("PROJ-1").ReviewMoved, row.moves[1] > 0)
		})
	}
}

func TestTheReviewMoveIsKeptFromThePage(t *testing.T) {
	t.Parallel()

	made, _ := prLegsWorld(t, [2]string{"2\n", "2\n"}, `{"reviewMoved":true}`)
	same(t, "read", made.record("PROJ-1").ReviewMoved, true)

	if strings.Contains(string(openwork.Snapshot(made.work)), "reviewMoved") {
		t.Error("the page is shown reviewMoved")
	}

	openwork.TouchAll(made.work)

	if !strings.Contains(text(get(made.files(), "PROJ-1.json")), `"reviewMoved":true`) {
		t.Error("reviewMoved was not saved")
	}

	if strings.Contains(string(openwork.Snapshot(made.work)), "reviewMoved") {
		t.Error("the page is shown reviewMoved once saved")
	}
}

// failingPR is a PR of repo numbered number whose checks named failed are red.
func failingPR(repo, number, failed string) string {
	return `{"pr":{"url":"https://github.com/` + repo + `/pull/` + number + `","number":` +
		number + `,"status":{"state":"open","checks":{"state":"failing","total":1,"pending":0,` +
		`"failed":["` + failed + `"]},"review":"","mergeable":"","at":1}}}`
}

// replies are the instructions typed into sessions, as "<session id> <text>".
func (w *world) replies() []string {
	out := []string{}

	for _, line := range w.lines() {
		if sent, isReply := strings.CutPrefix(line, "reply "); isReply {
			out = append(out, sent)
		}
	}

	return out
}

func TestFixChecksTypesEachLegsInstructionIntoTheSessionWorkingThere(t *testing.T) {
	t.Parallel()

	rows := []struct {
		layout string
		// sessions are the session each leg's instruction goes to.
		sessions []string
	}{
		{"split", []string{"70", "71"}},
		{"shared", []string{"70", "70"}},
	}

	for _, row := range rows {
		t.Run(row.layout, func(t *testing.T) {
			t.Parallel()

			made, folders := legsWorld(t, row.layout, `{"state":"pr_open","stage":"pr"}`,
				failingPR("acme/widgets", "5", "Lint"), failingPR("acme/gadgets", "6", "Unit"))
			made.setSessionIn(folders.worktrees[0], found("70"))
			made.setSessionIn(folders.worktrees[1], found("71"))

			reply, err := made.work.FixChecks("PROJ-1", "")
			same(t, "fix", []any{reply.Code, err}, []any{200, nil})

			sent := made.replies()
			same(t, "sent", len(sent), 2)

			for index, want := range []string{"5 -R acme/widgets", "6 -R acme/gadgets"} {
				same(t, want, strings.HasPrefix(sent[index], row.sessions[index]+" "), true)
				same(t, want+" named", strings.Contains(sent[index], "`gh pr checks "+want+"`"),
					true)
			}

			same(t, "one leg", strings.Contains(sent[1], "Lint"), false)
		})
	}
}

func TestFixChecksAsksOnlyTheLegsWhoseChecksFail(t *testing.T) {
	t.Parallel()

	made, folders := legsWorld(t, "split", `{"state":"pr_open","stage":"pr"}`,
		`{"pr":{"url":"https://github.com/acme/widgets/pull/5","number":5}}`,
		failingPR("acme/gadgets", "6", "Unit"))
	made.setSessionIn(folders.worktrees[0], found("70"))
	made.setSessionIn(folders.worktrees[1], found("71"))

	reply, _ := made.work.FixChecks("PROJ-1", "")
	same(t, "fix", reply.Code, 200)
	same(t, "sent", len(made.replies()), 1)
	same(t, "to gadgets", strings.HasPrefix(made.replies()[0], "71 "), true)

	reply, _ = made.work.FixChecks("PROJ-1", "acme/widgets")
	same(t, "named", []any{reply.Code, errorOf(reply)}, []any{409, "acme/widgets: PROJ-1 has " +
		"no open PR being watched"})

	route := openwork.Route{Action: openwork.ActionFixChecks, Key: "PROJ-1"}
	reply, err := made.work.Handle(route, body(t, `{"repo":"acme/gadgets"}`))
	same(t, "route", []any{reply.Code, err}, []any{200, nil})
	same(t, "sent again", len(made.replies()), 2)
}

// settledPR is a PR of repo in state.
func settledPR(repo, state string) string {
	return `{"pr":{"url":"https://github.com/` + repo + `/pull/1","number":1,"status":{"state":"` +
		state + `","checks":{"state":"passing","total":1,"pending":0,"failed":[]},"review":"",` +
		`"mergeable":"","at":1}}}`
}

// cleanupWorld is PROJ-1 with its PR open, widgets' PR in widgets and gadgets' in gadgets, and
// both worktrees already gone from the disk.
func cleanupWorld(t *testing.T, layout, widgets, gadgets string) (*world, legsRun) {
	t.Helper()

	made, folders := legsWorld(t, layout, `{"state":"pr_open","stage":"pr"}`,
		settledPR("acme/widgets", widgets), settledPR("acme/gadgets", gadgets))

	for _, worktree := range folders.worktrees {
		err := os.RemoveAll(worktree)
		if err != nil {
			t.Fatal(err)
		}
	}

	return made, folders
}

// cleaned are whether each leg is cleaned up.
func (w *world) cleaned() []bool {
	legs := w.record("PROJ-1").Repos
	out := make([]bool, 0, len(legs))

	for _, leg := range legs {
		out = append(out, leg.Cleaned != nil)
	}

	return out
}

func TestCleanupRemovesEachLegsWorktreeOnItsOwn(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name, widgets, gadgets, repo string
		code                         int
		cleaned                      []bool
	}{
		{"every settled leg", "merged", "closed", "", 200, []bool{true, true}},
		{"one named", "merged", "merged", "acme/gadgets", 200, []bool{false, true}},
		{"an open PR is left", "merged", "open", "", 200, []bool{true, false}},
		{"the open one named", "merged", "open", "acme/gadgets", 409, []bool{false, false}},
		{"named in another case", "merged", "merged", "ACME/Gadgets", 200, []bool{false, true}},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			made, _ := cleanupWorld(t, "split", row.widgets, row.gadgets)
			route := openwork.Route{Action: openwork.ActionCleanup, Key: "PROJ-1"}

			reply, err := made.work.Handle(route, body(t, `{"repo":"`+row.repo+`"}`))
			same(t, "reply", []any{reply.Code, err}, []any{row.code, nil})
			same(t, "cleaned", made.cleaned(), row.cleaned)
		})
	}
}

func TestCleanupSaysEveryLegsReasonWhenNoneMayGo(t *testing.T) {
	t.Parallel()

	made, _ := cleanupWorld(t, "split", "merged", "open")
	same(t, "first", made.work.Cleanup("PROJ-1", "").Code, 200)

	reply := made.work.Cleanup("PROJ-1", "")
	same(t, "again", []any{reply.Code, errorOf(reply)}, []any{409, "acme/widgets: the worktree " +
		"is already removed; acme/gadgets: the PR is still open"})
}

func TestCleanupRefusesALegOffTheListOrStillWorkedIn(t *testing.T) {
	t.Parallel()

	gone, folders := cleanupWorld(t, "split", "merged", "merged")
	allowOnly(gone, folders)
	same(t, "others go on", gone.work.Cleanup("PROJ-1", "").Code, 200)
	same(t, "gone kept", gone.cleaned(), []bool{true, false})
	same(t, "said", gone.hasLog("acme/gadgets is not an allowed repository: add it in Settings."),
		true)

	// In the shared layout the lead's session reaches every worktree.
	shared, folders := cleanupWorld(t, "shared", "merged", "merged")
	shared.setSessionIn(folders.worktrees[0], found("70"))

	reply := shared.work.Cleanup("PROJ-1", "acme/gadgets")
	same(t, "shared", []any{reply.Code, errorOf(reply)}, []any{409, "acme/gadgets: the session " +
		"is still running: close it first"})
	same(t, "shared kept", shared.cleaned(), []bool{false, false})
}

func TestMoveJiraIsOfferedOnceEveryPRIsSettledAndOneMerged(t *testing.T) {
	t.Parallel()

	open, _ := cleanupWorld(t, "split", "merged", "open")
	same(t, "open", errorOf(open.work.MoveTicket("PROJ-1", "Done")), "the PR has not merged")

	settled, _ := cleanupWorld(t, "split", "merged", "closed")
	same(t, "settled", settled.work.MoveTicket("PROJ-1", "Done").Code, 200)
	settled.work.Settle()
	same(t, "moved", settled.has("transition PROJ-1"), true)
}

// openPRsWorld is PROJ-1 with an open PR in each leg, whose gh reads widgets' as open and fails
// to read gadgets'.
func openPRsWorld(t *testing.T) *world {
	t.Helper()

	made, _ := legsWorld(t, "shared", `{"state":"pr_open","stage":"pr"}`,
		`{"pr":{"url":"`+widgetsPR+`","number":5}}`, `{"pr":{"url":"`+gadgetsPR+`","number":6}}`)
	made.sh = func(name string, args []string, _ string) (string, error) {
		if name != "gh" {
			return "", nil
		}

		if args[2] == gadgetsPR {
			return "", fakeError("gh: the login expired")
		}

		return pendingView, nil
	}

	return made
}

// pendingView is a PR whose one check is still running.
const pendingView = `{"state":"OPEN","isDraft":false,"reviewDecision":"","mergeStateStatus":` +
	`"BLOCKED","statusCheckRollup":[{"__typename":"CheckRun","name":"Unit","status":` +
	`"IN_PROGRESS","conclusion":""}]}`

func TestEachLegsPRIsPolledOnItsOwnScheduleAndARepeatingErrorIsSaidOnce(t *testing.T) {
	t.Parallel()

	made := openPRsWorld(t)
	start := made.now()
	reads := func() []int {
		return []int{
			made.countPrefix("sh gh pr view " + widgetsPR + " "),
			made.countPrefix("sh gh pr view " + gadgetsPR + " "),
		}
	}
	tickAt := func(offsetMs float64) {
		made.setClock(start + offsetMs)
		made.work.Tick()
		made.work.Settle()
	}

	tickAt(0)
	same(t, "both read", reads(), []int{1, 1})
	same(t, "checks", made.record("PROJ-1").Repos[0].PR.Status.Checks.State, "pending")

	tickAt(30_000)
	same(t, "pending checks are read again sooner", reads(), []int{2, 1})

	tickAt(60_000)
	same(t, "both again", reads(), []int{3, 2})

	said := 0

	for _, line := range made.record("PROJ-1").Log {
		if line.Text == "acme/gadgets: Could not read PR #6 from GitHub: gh: the login expired" {
			said++
		}
	}

	same(t, "said once", said, 1)
}

// dryLegsRun is a dry world with key started in widgets and gadgets and its questions answered.
func dryLegsRun(t *testing.T, key string) *world {
	t.Helper()

	base := realTemp(t)
	made := newWorld(t, worldOpts{
		base: base, dry: true,
		settings: twoClones(fakeClone(t, base, "widgets"), fakeClone(t, base, "gadgets")),
	})

	made.work.Tickets()
	until(t, "the dry list", func() bool { return len(made.work.Tickets().Tickets) == 6 })
	same(t, "start", made.work.Start(key, body(t, `{"model":"haiku","repos":`+
		`["acme/widgets","acme/gadgets"],"layout":"split"}`)).Code, 200)
	until(t, "the questions", func() bool { return made.state(key) == "asking" })
	same(t, "answers", made.work.Answer(key, body(t, `{"q1":"c1","q2":"c2"}`)).Code, 200)

	return made
}

// dryPoll ticks five seconds on and says each leg's PR as state/checks/review.
func dryPoll(made *world, key string) []string {
	made.setClock(made.now() + 5000)
	made.work.Tick()
	made.work.Settle()

	legs := made.record(key).Repos
	out := make([]string, 0, len(legs))

	for _, leg := range legs {
		status := leg.PR.Status
		out = append(out, status.State+"/"+status.Checks.State+"/"+status.Review)
	}

	return out
}

func TestTheDryRunWalksEveryLegsPRToMergedAndCleansEachUp(t *testing.T) {
	t.Parallel()

	made := dryLegsRun(t, "PROJ-2")
	same(t, "open", made.work.OpenPR("PROJ-2", "").Code, 200)
	made.work.Settle()

	record := made.record("PROJ-2")
	same(t, "urls", []string{record.Repos[0].PR.URL, record.Repos[1].PR.URL}, []string{
		"https://github.com/acme/widgets/pull/1702", "https://github.com/acme/gadgets/pull/1802",
	})
	same(t, "in review", []any{record.JiraStatus, record.ReviewMoved}, []any{"In Review", true})

	walk := []string{
		"open/pending/REVIEW_REQUIRED", "open/pending/REVIEW_REQUIRED",
		"open/passing/REVIEW_REQUIRED", "open/passing/APPROVED", "merged/passing/APPROVED",
	}

	for _, want := range walk {
		same(t, want, dryPoll(made, "PROJ-2"), []string{want, want})
	}

	same(t, "move", made.work.MoveTicket("PROJ-2", "Done").Code, 200)
	made.work.Settle()
	same(t, "cleanup", made.work.Cleanup("PROJ-2", "").Code, 200)

	for _, leg := range made.record("PROJ-2").Repos {
		same(t, leg.Repo+" cleaned", leg.Cleaned != nil, true)
	}

	same(t, "nothing ran", helperOrGit(made), []string{})
}

func TestAnOddTicketsDryLegsFailUntilEachIsFixed(t *testing.T) {
	t.Parallel()

	made := dryLegsRun(t, "PROJ-3")
	same(t, "open", made.work.OpenPR("PROJ-3", "").Code, 200)
	made.work.Settle()

	failing := "open/failing/REVIEW_REQUIRED"

	same(t, "first", dryPoll(made, "PROJ-3"), []string{
		"open/pending/REVIEW_REQUIRED", "open/pending/REVIEW_REQUIRED",
	})
	same(t, "red", dryPoll(made, "PROJ-3"), []string{failing, failing})

	reply, err := made.work.FixChecks("PROJ-3", "acme/gadgets")
	same(t, "fix one", []any{reply.Code, err}, []any{200, nil})
	same(t, "one fixed", dryPoll(made, "PROJ-3"), []string{
		failing, "open/pending/REVIEW_REQUIRED",
	})
}

func TestASecondFixChecksWhileTheFirstIsTypingIsRefused(t *testing.T) {
	t.Parallel()

	made, folders := legsWorld(t, "split", `{"state":"pr_open","stage":"pr"}`,
		failingPR("acme/widgets", "5", "Lint"), failingPR("acme/gadgets", "6", "Unit"))
	made.setSessionIn(folders.worktrees[0], found("70"))
	made.setSessionIn(folders.worktrees[1], found("71"))

	var second *openwork.Reply

	made.onReply = func() {
		if second == nil {
			reply, _ := made.work.FixChecks("PROJ-1", "")
			second = &reply
		}
	}

	reply, err := made.work.FixChecks("PROJ-1", "")
	same(t, "first", []any{reply.Code, err}, []any{200, nil})
	same(t, "second", []any{second.Code, strings.Contains(errorOf(*second),
		"another change to this run is in progress")}, []any{409, true})
	same(t, "typed once each", len(made.replies()), 2)
}

// linkedWorld is PROJ-1 with both PRs merged and both worktrees linked worktrees of their clones
// as the fake git tells it; during the first removal, during runs.
func linkedWorld(t *testing.T, layout string, during func(made *world)) (*world, legsRun) {
	t.Helper()

	made, folders := legsWorld(t, layout, `{"state":"pr_open","stage":"pr"}`,
		settledPR("acme/widgets", "merged"), settledPR("acme/gadgets", "merged"))
	removing := false
	made.sh = func(_ string, args []string, dir string) (string, error) {
		switch args[0] {
		case "rev-parse":
			clone := folders.clones[slices.Index(folders.worktrees, dir)]

			return clone + "/.git/worktrees/w\n" + clone + "/.git\n", nil
		case "worktree":
			if args[1] == "list" {
				return "worktree " + folders.worktrees[0] + "\nworktree " + folders.worktrees[1] +
					"\n", nil
			}

			if !removing {
				removing = true

				during(made)
			}
		}

		return "", nil
	}

	return made, folders
}

func TestASecondCleanupWhileTheFirstRemovesIsRefused(t *testing.T) {
	t.Parallel()

	var second openwork.Reply

	made, _ := linkedWorld(t, "split", func(made *world) {
		second = made.work.Cleanup("PROJ-1", "")
	})

	same(t, "first", made.work.Cleanup("PROJ-1", "").Code, 200)
	same(t, "second", []any{second.Code, strings.Contains(errorOf(second),
		"another change to this run is in progress")}, []any{409, true})
	same(t, "removed once each", made.countPrefix("sh git worktree remove"), 2)
	same(t, "cleaned", made.cleaned(), []bool{true, true})
}

func TestTheSharedLeadIsKeptWhileAnotherLegsPRIsOpen(t *testing.T) {
	t.Parallel()

	kept := "acme/widgets: the run's session works in this worktree for acme/gadgets, whose PR " +
		"is still open"

	for repo, want := range map[string]string{
		"acme/widgets": kept, "": "acme/gadgets: the PR is still open; " + kept,
	} {
		made, _ := cleanupWorld(t, "shared", "merged", "open")
		reply := made.work.Cleanup("PROJ-1", repo)
		same(t, repo, []any{reply.Code, errorOf(reply)}, []any{409, want})
		same(t, repo+" kept", made.cleaned(), []bool{false, false})
	}

	split, _ := cleanupWorld(t, "split", "merged", "open")
	same(t, "split", split.work.Cleanup("PROJ-1", "acme/widgets").Code, 200)
}

func TestEveryLegIsCleanedUpBeforeTheSharedLead(t *testing.T) {
	t.Parallel()

	made, folders := cleanupWorld(t, "shared", "merged", "closed")
	same(t, "cleanup", made.work.Cleanup("PROJ-1", "").Code, 200)

	order := []string{}

	for _, line := range made.record("PROJ-1").Log {
		for index, worktree := range folders.worktrees {
			if strings.Contains(line.Text, "Worktree "+worktree+" removed") {
				order = append(order, []string{"widgets", "gadgets"}[index])
			}
		}
	}

	same(t, "order", order, []string{"gadgets", "widgets"})
}

func TestNoLegLeftToRefuseSaysEveryLegHasAPR(t *testing.T) {
	t.Parallel()

	same(t, "empty", openwork.RefusedAllOf(&openwork.Record{Key: "PROJ-1"}),
		"every leg already has a pull request")
}
