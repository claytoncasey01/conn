package openwork_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/openwork"
	"github.com/claytoncasey01/conn/internal/settings"
)

// Open work's gates, continued: runs from before Settings, the list and the follow-up
// poll, OPEN PR, FIX FAILING CHECKS, the cleanup with real git, a repository whose only branch
// is main from START to the PR, and what the Settings cards learn.

func withoutRepo(record *js.Object) *js.Object {
	record.Delete("repo")
	record.Delete("clone")

	return record
}

func TestALegacyRunInARepositoryOffTheListRunsNothing(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	gadgets := filepath.Join(base, "legacy-og", "gadgets")

	err := os.MkdirAll(gadgets, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	gitRun(t, gadgets, "init", "-q", "-b", "main")
	gitRun(t, gadgets, "remote", "add", "origin", "https://github.com/other/gadgets.git")
	gitRun(t, gadgets, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	current := body(t, `{"version":1,"repos":{"allow":["other/*"],"clones":{"other/gadgets":`+
		js.Stringify(gadgets)+`},"cloneRoot":null},"atlassian":{"enabled":true,"jira":`+
		`{"projects":["PROJ"],"buildRepo":"other/gadgets"}}}`)
	worktree := gitClone(t, base, "main")
	worktreeAt := `"worktree":` + js.Stringify(worktree)
	made := newWorld(t, worldOpts{base: base, settings: current, runs: []any{
		withoutRepo(runRecord(t, "PROJ-1", `{`+worktreeAt+`,"state":"failed","stage":"research",`+
			`"worktreeReady":true,"ticketCategory":"indeterminate"}`)),
		withoutRepo(runRecord(t, "PROJ-2", `{`+worktreeAt+`,"state":"asking","stage":"ask",`+
			`"questions":[{"id":"q1","text":"q?","why":"w","choices":[{"id":"a","label":"a"}]}]}`)),
		withoutRepo(runRecord(t, "PROJ-3", `{`+worktreeAt+`,"claudeSessionId":"`+
			sessionUUIDText+`"}`)),
	}})

	same(t, "retry", made.work.Retry("PROJ-1").Code, 200)
	until(t, "the failure", func() bool {
		record := made.record("PROJ-1")
		if record.State != "failed" {
			return false
		}

		return strings.Contains(*record.Error, "acme/widgets is not an allowed repository")
	})

	for _, reply := range []openwork.Reply{
		made.work.Answer("PROJ-2", body(t, `{"q1":"a"}`)), made.work.Resume("PROJ-3"),
	} {
		same(t, "code", reply.Code, 409)

		if !strings.Contains(errorOf(reply), "acme/widgets is not an allowed repository") {
			t.Error(errorOf(reply))
		}
	}

	made.work.Settle()
	same(t, "calls", helperOrGit(made), []string{})

	// The list widened to the worktree's repository, but Settings building elsewhere: made for
	// another repository.
	made.update("repos", `{"allow":["other/*","acme/*"],"clones":{"other/gadgets":`+
		js.Stringify(gadgets)+`},"cloneRoot":null}`)

	reply := made.work.Resume("PROJ-3")
	same(t, "moved", reply.Code, 409)

	if !strings.Contains(errorOf(reply), "this run was made for acme/widgets") {
		t.Error(errorOf(reply))
	}

	if made.has("cmux") {
		t.Error("a session was opened")
	}
}

func TestALegacyRunWhoseWorktreeIsNotMadeIsNotTakenForAnEnclosingRepository(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	clone := gitClone(t, base, "main")
	enclosing := filepath.Join(base, "enclosing-dotfiles")

	err := os.MkdirAll(filepath.Join(enclosing, ".git"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	writeJSON(t, filepath.Join(enclosing, ".git", "config"),
		"[remote \"origin\"]\n\turl = https://github.com/jane/dotfiles.git\n")

	made := newWorld(t, worldOpts{base: base, settings: workSettings(clone, ""), runs: []any{
		withoutRepo(runRecord(t, "PROJ-1", `{"state":"failed","stage":"research",`+
			`"worktreeReady":false,"worktree":`+js.Stringify(filepath.Join(enclosing,
			"widgets-proj-1"))+`,"ticketCategory":"indeterminate"}`)),
	}})

	same(t, "retry", made.work.Retry("PROJ-1").Code, 200)
	until(t, "the fetch in the clone Settings builds in", func() bool {
		return made.has("sh git fetch origin main")
	})
	made.work.Settle()

	if made.record("PROJ-1").Error != nil {
		if strings.Contains(*made.record("PROJ-1").Error, "dotfiles") {
			t.Error(*made.record("PROJ-1").Error)
		}
	}
}

func TestARunResearchingWhenItsRepositoryLeavesTheListIsInterrupted(t *testing.T) {
	t.Parallel()

	made := startable(t, "indeterminate")
	readers := newLatch()
	killed := atomic.Int32{}
	made.helper = func(ctx context.Context, opts claude.Opts) (claude.Answer, error) {
		made.call("claude", optsOf(opts))
		readers.wait(ctx)

		if ctx.Err() != nil {
			killed.Add(1)
		}

		return claude.Answer{Text: "", Structured: body(t, `{"summary":"s","facts":[],`+
			`"questions":[]}`)}, nil
	}

	same(t, "start", made.work.Start("PROJ-2", body(t, `{"model":"haiku","force":true}`)).Code,
		200)
	until(t, "the readers", func() bool { return made.has(`claude {"name":"jira-`) })
	made.update("repos", othersOnly)
	same(t, "state", made.state("PROJ-2"), "interrupted")

	if !strings.Contains(*made.record("PROJ-2").Error, "acme/widgets is not an allowed "+
		"repository: add it in Settings. The research stopped") {
		t.Error(*made.record("PROJ-2").Error)
	}

	readers.release()
	made.work.Settle()

	if made.has(`claude {"name":"synth-`) {
		t.Error("the synthesis ran")
	}

	if killed.Load() == 0 {
		t.Error("the readers in flight were not stopped")
	}
}

// lastLog is PROJ-1's last log line.
func lastLog(made *world) string {
	log := made.record("PROJ-1").Log

	return log[len(log)-1].Text
}

func TestAMoveIsNotMadeWhileAtlassianIsOffAndANoOpMoveSaysSo(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	made := newWorld(t, worldOpts{
		base: base, settings: workSettings(gitClone(t, base, "main"), ""),
		runs: []any{runRecord(t, "PROJ-1", "")},
	})
	made.transition = func(context.Context, string, jira.Move) (jira.Moved, error) {
		return jira.Moved{To: "Doing", Moved: false}, nil
	}

	openwork.MoveJira(made.work, "PROJ-1", jira.MoveReview())
	same(t, "review", lastLog(made), "Jira PROJ-1 was not moved: the workflow has no "+
		"single review status, so it stays Doing")

	openwork.MoveJira(made.work, "PROJ-1", jira.MoveTo("Doing"))
	same(t, "status", lastLog(made), "Jira PROJ-1 was not moved: it is already "+
		"there, so it stays Doing")

	made.update("atlassian", atlassianOff)
	before := made.countPrefix("transition")
	openwork.MoveJira(made.work, "PROJ-1", jira.MoveStarted())
	same(t, "moves", made.countPrefix("transition"), before)
	same(t, "off", lastLog(made), "Atlassian is off in Settings: PROJ-1 stays Doing")
}

func TestTheSprintIsReadOnlyWhileOpenWorkIsOnAndAgainForOtherProjects(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	clone := gitClone(t, base, "main")
	stale := body(t, `{"tickets":[],"fetchedAt":0,"projects":"PROJ"}`)
	stale.Set("tickets", []any{ticket("PROJ-1", "new")})

	off := newWorld(t, worldOpts{base: base, settings: workSettings(clone,
		`,"atlassian":`+atlassianOff), cache: stale})
	off.work.Tickets()
	off.work.Refresh()
	same(t, "off", off.has("fetchSprint"), false)

	on := newWorld(t, worldOpts{base: base, settings: workSettings(clone, ""), cache: stale})
	on.work.Tickets()
	until(t, "the read", func() bool { return on.countPrefix("fetchSprint") == 1 })

	fresh := newWorld(t, worldOpts{
		base: base, settings: workSettings(clone, ""),
		cache: cacheOf(ticket("PROJ-1", "new")),
	})
	fresh.work.Tickets()
	same(t, "fresh", fresh.has("fetchSprint"), false)
	fresh.update("atlassian", `{"enabled":true,"jira":{"projects":["OTHER"],`+
		`"buildRepo":"acme/widgets"}}`)
	same(t, "other", len(fresh.work.Tickets().Tickets), 0)
	until(t, "the read", func() bool { return fresh.countPrefix("fetchSprint") == 1 })
}

func openPR(status string) string {
	return `{"url":"https://github.com/acme/widgets/pull/9","number":9,"status":{"state":` +
		`"open","checks":{"state":"` + status + `","total":1,"pending":1,"failed":[]},` +
		`"review":"","mergeable":"","at":1}}`
}

const prView = `{"state":"OPEN","isDraft":false,"reviewDecision":"","mergeStateStatus":"CLEAN",` +
	`"statusCheckRollup":[]}`

func TestTheFollowUpPollRunsOnlyWhileOnAndReadingAPRNeedsGitHub(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	clone := gitClone(t, base, "main")
	runs := func() []any {
		return []any{runRecord(t, "PROJ-4", ""), runRecord(t, "PROJ-5",
			`{"state":"pr_open","stage":"pr","pr":`+openPR("pending")+`}`)}
	}
	cases := []struct {
		name, update, body string
		session, gh        bool
	}{
		{"on", "", "", true, true},
		{"github off", "github", `{"enabled":false}`, true, false},
		{"atlassian off", "atlassian", atlassianOff, false, false},
	}

	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			made := newWorld(t, worldOpts{
				base: base, settings: workSettings(clone, ""),
				runs: runs(),
			})
			made.sh = func(string, []string, string) (string, error) { return prView, nil }

			if row.update != "" {
				made.update(row.update, row.body)
			}

			made.work.Tick()
			made.work.Settle()
			same(t, "session", made.has("findSession PROJ-4"), row.session)
			same(t, "gh", made.has("sh gh pr view https://github.com/acme/widgets/pull/9"),
				row.gh)
		})
	}
}

func TestTheFollowUpPollReadsNoPROfARepositoryOffTheList(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	clone := gitClone(t, base, "main")
	extra := `{"state":"pr_open","stage":"pr","pr":` + openPR("pending") + `,"repo":` +
		`"acme/widgets","clone":` + js.Stringify(clone) + `}`

	allowed := newWorld(t, worldOpts{
		base: base, settings: workSettings(clone, ""),
		runs: []any{runRecord(t, "PROJ-5", extra)},
	})
	allowed.sh = func(string, []string, string) (string, error) { return prView, nil }
	allowed.work.Tick()
	allowed.work.Settle()
	same(t, "allowed", allowed.has("sh gh pr view"), true)

	for _, record := range []*js.Object{
		runRecord(t, "PROJ-5", extra),
		runRecord(t, "PROJ-5", `{"state":"pr_open","stage":"pr","pr":`+openPR("pending")+`}`),
	} {
		gone := newWorld(t, worldOpts{
			base: base, settings: workSettings(clone, ""),
			runs: []any{record},
		})
		gone.sh = allowed.sh
		before := gone.files()
		gone.update("repos", othersOnly)
		gone.work.Tick()
		gone.work.Settle()
		same(t, "gone", gone.has("sh gh "), false)
		same(t, "file", gone.files(), before)
		same(t, "status", gone.record("PROJ-5").Repos[0].PR.Status.State, "open")
	}
}

// openPRWorld is a world over a building run of PROJ-3 on branch proj-3-fix with a PR body
// written, whose git and gh answer as a session that committed twice.
func openPRWorld(t *testing.T, extra string) (*world, string) {
	t.Helper()

	base := realTemp(t)
	clone := gitClone(t, base, "main")
	worktree := filepath.Join(base, "openpr-wt")

	err := os.MkdirAll(filepath.Join(worktree, ".conn"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	writeJSON(t, filepath.Join(worktree, ".conn", "pr-body.md"), "body")

	record := runRecord(t, "PROJ-3", `{"branch":"proj-3-fix","worktree":`+
		js.Stringify(worktree)+`,"repo":"acme/widgets","clone":`+js.Stringify(clone)+`}`)
	if extra != "" {
		over := body(t, extra)
		for _, name := range over.Keys() {
			value, _ := over.Get(name)
			if value == nil {
				record.Delete(name)

				continue
			}

			record.Set(name, value)
		}
	}

	made := newWorld(t, worldOpts{
		base: base, settings: workSettings(clone, ""),
		runs: []any{record},
	})
	made.sh = func(name string, args []string, _ string) (string, error) {
		if name == "gh" {
			return "https://github.com/acme/widgets/pull/5\n", nil
		}

		if args[0] == "rev-list" {
			return "2\n", nil
		}

		return "", nil
	}

	return made, worktree
}

func TestOpenPRPushesOnlyABranchThisToolMadeToAnAllowedRepository(t *testing.T) {
	t.Parallel()

	made, _ := openPRWorld(t, "")
	same(t, "ok", made.work.OpenPR("PROJ-3").Code, 200)
	until(t, "the PR", func() bool { return made.state("PROJ-3") == "pr_open" })

	if !made.has("sh git push -u origin proj-3-fix") {
		t.Error(strings.Join(made.lines(), "\n"))
	}

	if !made.has("sh gh pr create --repo acme/widgets --base main --head proj-3-fix ") {
		t.Error(strings.Join(made.lines(), "\n"))
	}

	refused := []string{
		`{"branch":"main"}`, `{"branch":"trunk"}`, `{"branch":"proj-4-fix"}`,
		`{"branch":"other"}`, `{"branch":"proj-3"}`,
		`{"branch":"proj-3-x:refs/heads/main"}`, `{"branch":"proj-3-x:main"}`,
		`{"branch":"proj-3-x y"}`, `{"branch":"proj-3-x\n"}`, `{"branch":"proj-3-..x"}`,
		`{"base":"main:x","prBase":"main:x"}`, `{"prBase":"-x","base":"-x"}`,
		`{"base":"ma in","prBase":"ma in"}`,
	}

	for _, extra := range refused {
		odd, _ := openPRWorld(t, extra)
		same(t, extra, odd.work.OpenPR("PROJ-3").Code, 400)
		same(t, extra+" calls", odd.lines(), []string{})
	}
}

func TestOpenPRIsRefusedForARepositoryOffTheListOrWithGitHubOff(t *testing.T) {
	t.Parallel()

	gone, _ := openPRWorld(t, `{"repo":"other/thing"}`)
	reply := gone.work.OpenPR("PROJ-3")
	same(t, "gone", reply.Code, 409)

	if !strings.Contains(errorOf(reply), "other/thing is not an allowed repository") {
		t.Error(errorOf(reply))
	}

	same(t, "gone calls", gone.lines(), []string{})

	// A record from before Settings names no repository: the worktree's own origin says, and a
	// folder that is no repository is refused.
	unknown, _ := openPRWorld(t, `{"repo":null,"clone":null}`)
	same(t, "unknown", unknown.work.OpenPR("PROJ-3").Code, 409)
	same(t, "unknown calls", unknown.lines(), []string{})

	off, _ := openPRWorld(t, "")
	off.update("github", `{"enabled":false}`)
	reply = off.work.OpenPR("PROJ-3")
	same(t, "github off", reply.Code, 409)

	if !strings.Contains(errorOf(reply), "GitHub is off") {
		t.Error(errorOf(reply))
	}

	same(t, "off calls", off.lines(), []string{})
}

func TestOpenPRChecksAgainOnceThePushIsDone(t *testing.T) {
	t.Parallel()

	changes := []struct{ name, section, body string }{
		{"github off", "github", `{"enabled":false}`},
		{"the repository off the list", "repos", othersOnly},
		{"atlassian off", "atlassian", atlassianOff},
		{"github off while the body is made", "github", `{"enabled":false}`},
	}

	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			t.Parallel()

			made, worktree := openPRWorld(t, "")
			held := "push"

			if strings.Contains(change.name, "body") {
				held = "log"

				_ = os.Remove(filepath.Join(worktree, ".conn", "pr-body.md"))
			}

			waiting := newLatch()
			answer := made.sh
			made.sh = func(name string, args []string, dir string) (string, error) {
				if args[0] == held {
					waiting.wait(t.Context())
				}

				return answer(name, args, dir)
			}

			same(t, "open", made.work.OpenPR("PROJ-3").Code, 200)
			until(t, "the held call", func() bool { return made.has("sh git " + held) })
			made.update(change.section, change.body)
			waiting.release()
			until(t, "the refusal", func() bool {
				record := made.record("PROJ-3")
				if record.Error == nil {
					return false
				}

				return strings.Contains(*record.Error, "no pull request was opened")
			})

			same(t, "gh", made.has("sh gh "), false)
			same(t, "move", made.has("transition"), false)
			same(t, "state", made.state("PROJ-3"), "building")

			if made.record("PROJ-3").Repos[0].PR != nil {
				t.Error("a PR was recorded")
			}
		})
	}
}

func TestTheBranchStartMakesIsOneOpenPRAccepts(t *testing.T) {
	t.Parallel()

	titles := []string{
		strings.Repeat("x", 400), "Fix the export... of //every// report.lock", "..", "   ",
		"Ünïcödé ✓ \u65e5\u672c\u8a9e", strings.Repeat("word ", 80) + "end.",
	}

	for index, title := range titles {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			t.Parallel()

			base := realTemp(t)
			key := "PROJ-123456789"
			listed := ticket(key, "new")
			listed.Set("title", title)
			made := newWorld(t, worldOpts{
				base:     base,
				settings: workSettings(gitClone(t, base, "main"), ""), cache: cacheOf(listed),
			})

			same(t, "start", made.work.Start(key, body(t, `{"model":"haiku"}`)).Code, 200)

			branch := made.record(key).Repos[0].Branch
			parsedBranch, valid := settings.ParseBranch(branch)

			if !valid {
				t.Errorf("%q -> %q", title, branch)
			}

			same(t, "branch", parsedBranch, branch)

			if !strings.HasPrefix(branch, "proj-123456789-") {
				t.Error(branch)
			}

			made.work.Cancel(key)
		})
	}
}

func TestFixFailingChecksNamesThePRsRepositoryAndNeedsGitHubAndTheList(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	clone := gitClone(t, base, "main")
	failing := `{"url":"https://github.com/acme/widgets/pull/9","number":9,"status":{"state":` +
		`"open","checks":{"state":"failing","total":1,"pending":0,"failed":["Backend tests"]},` +
		`"review":"","mergeable":"","at":1}}`
	newFixWorld := func() *world {
		made := newWorld(t, worldOpts{
			base: base, settings: workSettings(clone, ""),
			runs: []any{runRecord(t, "PROJ-5", `{"state":"pr_open","stage":"pr","pr":`+failing+
				`,"repo":"acme/widgets","clone":`+js.Stringify(clone)+`}`)},
		})
		made.session = &openwork.Found{ID: "4242", PID: 4242, Status: "idle", SessionID: "s"}

		return made
	}

	fine := newFixWorld()
	reply, err := fine.work.FixChecks("PROJ-5")
	same(t, "ok", []any{reply.Code, err}, []any{200, nil})

	sent := slices.DeleteFunc(fine.lines(), func(line string) bool {
		return !strings.HasPrefix(line, "reply ")
	})
	same(t, "sent", len(sent), 1)

	if !strings.Contains(sent[0], "`gh pr checks 9 -R acme/widgets`") {
		t.Error(sent[0])
	}

	if !strings.Contains(sent[0], "`gh run view --log-failed -R acme/widgets`") {
		t.Error(sent[0])
	}

	for _, change := range []struct{ section, body, want string }{
		{"github", `{"enabled":false}`, "GitHub is off"},
		{"repos", othersOnly, "acme/widgets is not an allowed repository"},
	} {
		refused := newFixWorld()
		refused.update(change.section, change.body)
		reply, _ = refused.work.FixChecks("PROJ-5")
		same(t, change.section, reply.Code, 409)

		if !strings.Contains(errorOf(reply), change.want) {
			t.Error(errorOf(reply))
		}

		same(t, change.section+" reply", refused.has("reply "), false)
	}
}

func TestTheBranchesConnNeverPushes(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	clone := gitClone(t, base, "trunk")
	made := newWorld(t, worldOpts{
		base: base, settings: workSettings(clone,
			`,"atlassian":{"enabled":true,"jira":{"projects":["PROJ"],"buildRepo":"acme/widgets",`+
				`"baseBranch":"release/9"}}`),
		runs: []any{runRecord(t, "PROJ-1", `{"clone":`+js.Stringify(clone)+
			`,"base":"topic","prBase":"topic"}`)},
	})

	for _, branch := range []string{
		"main", "master", "develop", "development", "staging", "hotfix", "topic", "trunk",
		"release/9",
	} {
		same(t, branch, openwork.IsProtected(made.work, "PROJ-1", branch), true)
	}

	same(t, "own", openwork.IsProtected(made.work, "PROJ-1", "proj-1-fix"), false)
}
