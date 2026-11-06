package openwork_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/openwork"
)

// A run that builds in several repositories, from START to its worktrees: the choice, one
// worktree and branch per repository, a Retry that resumes where the last try stopped, and a
// ticket started again over a run that shares some of its repositories.

// originClone is a clone of acme/<name> whose origin is a bare repository beside it (reached
// through git's insteadOf, so the clone's origin still reads as GitHub's), with one commit on
// main.
func originClone(t *testing.T, root, name string) string {
	t.Helper()

	bare := filepath.Join(root, name+".git")
	seed := filepath.Join(root, name+"-seed")
	clone := filepath.Join(root, name)
	remote := "https://github.com/acme/" + name + ".git"

	gitRun(t, root, "init", "-q", "--bare", "-b", "main", bare)
	gitRun(t, root, "clone", "-q", bare, seed)
	gitRun(t, seed, "checkout", "-q", "-b", "main")
	writeJSON(t, filepath.Join(seed, "a"), "a")
	gitRun(t, seed, "add", "a")
	gitRun(t, seed, "commit", "-qm", "a")
	gitRun(t, seed, "push", "-q", "origin", "main")
	gitRun(t, root, "clone", "-q", bare, clone)
	gitRun(t, clone, "remote", "set-url", "origin", remote)
	gitRun(t, clone, "config", "url."+bare+".insteadOf", remote)

	return clone
}

// twoClones are the settings for acme/* with clones of widgets and gadgets, widgets the default.
func twoClones(widgets, gadgets string) any {
	text := `{"version":1,"repos":{"allow":["acme/*"],"clones":{"acme/widgets":` +
		js.Stringify(widgets) + `,"acme/gadgets":` + js.Stringify(gadgets) + `},` +
		`"cloneRoot":null},"github":{"enabled":true},"atlassian":{"enabled":true,"jira":` +
		`{"projects":["PROJ"],"buildRepo":"acme/widgets"}}}`

	parsed, _ := js.Parse([]byte(text))

	return parsed
}

// twoRepoWorld is a world over real clones of widgets and gadgets, with PROJ-2 to do.
func twoRepoWorld(t *testing.T) (*world, string, string) {
	t.Helper()

	root := realTemp(t)
	widgets := originClone(t, root, "widgets")
	gadgets := originClone(t, root, "gadgets")
	made := newWorld(t, worldOpts{
		base: root, settings: twoClones(widgets, gadgets),
		cache: cacheOf(ticket("PROJ-2", "new")),
	})
	made.sh = func(name string, args []string, dir string) (string, error) {
		return realSh(t.Context(), name, args, dir, 0)
	}

	return made, widgets, gadgets
}

const bothRepos = `{"model":"haiku","repos":["acme/widgets","acme/gadgets"],"layout":"split"}`

func head(t *testing.T, worktree string) string {
	t.Helper()

	return strings.TrimSpace(gitRun(t, worktree, "rev-parse", "--abbrev-ref", "HEAD"))
}

func TestTheListOffersEveryAllowedRepositoryWithItsBase(t *testing.T) {
	t.Parallel()

	made, _, _ := twoRepoWorld(t)

	// The default repository is named first, so it is listed first.
	same(t, "build", parsed(t, made.work.Tickets().Build), body(t, `{"repos":[`+
		`{"repo":"acme/widgets","base":"main","ready":true,"reason":null},`+
		`{"repo":"acme/gadgets","base":"main","ready":true,"reason":null}],`+
		`"default":"acme/widgets","layout":"shared"}`))
}

func TestStartingInTwoRepositoriesMakesAWorktreeAndABranchInEach(t *testing.T) {
	t.Parallel()

	made, widgets, gadgets := twoRepoWorld(t)

	same(t, "start", made.work.Start("PROJ-2", body(t, bothRepos)).Code, 200)
	until(t, "the questions", func() bool { return made.state("PROJ-2") == "asking" })

	record := made.record("PROJ-2")
	same(t, "layout", record.Layout, "split")
	same(t, "legs", len(record.Repos), 2)

	for index, clone := range []string{widgets, gadgets} {
		leg := record.Repos[index]
		worktree := clone + "-proj-2"
		same(t, "repo", leg.Repo, "acme/"+filepath.Base(clone))
		same(t, "worktree", leg.Worktree, worktree)
		same(t, "ready", leg.WorktreeReady, true)
		same(t, "made", exists(filepath.Join(worktree, "a")), true)
		same(t, "head", head(t, worktree), "proj-2-ticket-proj-2")
	}
}

func TestStartRefusesABadChoiceOfRepositories(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name, body, message string
		code                int
	}{
		{
			"not a list", `{"model":"haiku","repos":"acme/widgets"}`,
			"repos must be a list of repository names", 400,
		},
		{
			"not names", `{"model":"haiku","repos":["acme/widgets",3]}`,
			"repos must be a list of repository names", 400,
		},
		{
			"an unknown layout", `{"model":"haiku","layout":"sideways"}`,
			"layout must be shared or split", 400,
		},
		{"twice and not allowed", `{"model":"haiku","repos":["acme/widgets","ACME/widgets",` +
			`"other/x"]}`, "ACME/widgets is chosen twice. other/x is not an allowed " +
			"repository: add it in Settings.", 409},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			made, _, _ := twoRepoWorld(t)
			reply := made.work.Start("PROJ-2", body(t, row.body))
			same(t, "reply", []any{reply.Code, errorOf(reply)}, []any{row.code, row.message})
			same(t, "runs", string(openwork.Snapshot(made.work)), "[]")
		})
	}
}

func TestOneRepositoryIsAlwaysTheSharedLayout(t *testing.T) {
	t.Parallel()

	made, _, _ := twoRepoWorld(t)

	same(t, "start", made.work.Start("PROJ-2", body(t,
		`{"model":"haiku","repos":["acme/gadgets"],"layout":"split"}`)).Code, 200)
	made.work.Settle()

	record := made.record("PROJ-2")
	same(t, "one", []any{len(record.Repos), record.Repos[0].Repo, record.Layout},
		[]any{1, "acme/gadgets", "shared"})
}

func TestRetryAfterTheSecondWorktreeFailedKeepsTheFirst(t *testing.T) {
	t.Parallel()

	made, widgets, gadgets := twoRepoWorld(t)

	var failing atomic.Bool

	failing.Store(true)

	made.sh = func(name string, args []string, dir string) (string, error) {
		if dir == gadgets {
			if strings.Join(args[:min(2, len(args))], " ") == "worktree add" {
				if failing.Load() {
					return "", fakeError("fatal: no room")
				}
			}
		}

		return realSh(t.Context(), name, args, dir, 0)
	}

	same(t, "start", made.work.Start("PROJ-2", body(t, bothRepos)).Code, 200)
	until(t, "the failure", func() bool { return made.state("PROJ-2") == "failed" })
	same(t, "error", *made.record("PROJ-2").Error, "acme/gadgets: fatal: no room")
	same(t, "first made", made.record("PROJ-2").Repos[0].WorktreeReady, true)
	same(t, "second not", made.record("PROJ-2").Repos[1].WorktreeReady, false)

	failing.Store(false)
	same(t, "retry", made.work.Retry("PROJ-2").Code, 200)
	until(t, "the questions", func() bool { return made.state("PROJ-2") == "asking" })

	same(t, "fetched once in widgets", made.countPrefix("sh git fetch origin main "+widgets), 1)
	same(t, "added once in widgets", made.countPrefix("sh git worktree add -b proj-2-ticket-"+
		"proj-2 "+widgets+"-proj-2"), 1)
	same(t, "made", head(t, gadgets+"-proj-2"), "proj-2-ticket-proj-2")
}

// legText is a leg of a run file in the new shape, its worktree made.
func legText(repo, base, worktree string) string {
	return `{"repo":"` + repo + `","clone":"","base":"` + base + `","prBase":"` + base + `",` +
		`"branch":"proj-2-ticket-proj-2","worktree":` + js.Stringify(worktree) + `,` +
		`"worktreeReady":true,"session":null,"claudeSessionId":null,"manualCommand":null,` +
		`"ahead":0,"dirty":0,"pr":null,"cleaned":null}`
}

// failedRun is PROJ-2's run file, failed, in the new shape with legs.
func failedRun(legs ...string) string {
	return `{"key":"PROJ-2","title":"Ticket PROJ-2","model":"haiku","state":"failed",` +
		`"stage":"research","startedAt":1,"updatedAt":1,"jiraStatus":"Doing","agents":[],` +
		`"answered":[],"questions":[],"log":[],"error":"x","brief":"","desc":"",` +
		`"ticketStatus":"Doing","repos":[` + strings.Join(legs, ",") + `],"layout":"shared"}`
}

func TestStartingATicketAgainReusesTheWorktreesItShares(t *testing.T) {
	t.Parallel()

	root := realTemp(t)
	widgets := originClone(t, root, "widgets")
	gadgets := originClone(t, root, "gadgets")
	gitRun(t, widgets, "worktree", "add", "-q", "-b", "proj-2-ticket-proj-2",
		widgets+"-proj-2", "origin/main")

	made := newWorld(t, worldOpts{
		base: root, settings: twoClones(widgets, gadgets),
		cache: cacheOf(ticket("PROJ-2", "new")), runs: []any{failedRun(
			legText("acme/widgets", "main", widgets+"-proj-2"),
			legText("acme/gadgets", "main", gadgets+"-proj-2"),
		)},
	})
	made.sh = func(name string, args []string, dir string) (string, error) {
		return realSh(t.Context(), name, args, dir, 0)
	}

	same(t, "start", made.work.Start("PROJ-2", body(t,
		`{"model":"haiku","repos":["acme/widgets"]}`)).Code, 200)
	until(t, "the questions", func() bool { return made.state("PROJ-2") == "asking" })

	log := made.record("PROJ-2").Log
	logged := make([]string, 0, len(log))

	for _, line := range log {
		logged = append(logged, line.Text)
	}

	same(t, "started", logged[0], "Run started for PROJ-2: branch from main, research on "+
		"HAIKU. "+gadgets+"-proj-2 of the last run is left as it was.")
	same(t, "reused", logged[2], "Reusing "+widgets+"-proj-2")
	same(t, "one leg", len(made.record("PROJ-2").Repos), 1)
}

func TestStartingATicketAgainIsRefusedForEachSharedRepositoryCutFromAnotherBase(t *testing.T) {
	t.Parallel()

	root := realTemp(t)
	widgets := originClone(t, root, "widgets")
	gadgets := originClone(t, root, "gadgets")

	made := newWorld(t, worldOpts{
		base: root, settings: twoClones(widgets, gadgets),
		cache: cacheOf(ticket("PROJ-2", "new")), runs: []any{failedRun(
			legText("acme/widgets", "release", widgets+"-proj-2"),
			legText("acme/gadgets", "release", gadgets+"-proj-2"),
		)},
	})

	reply := made.work.Start("PROJ-2", body(t, bothRepos))
	same(t, "refused", []any{reply.Code, errorOf(reply)}, []any{409, "PROJ-2 already has " +
		"proj-2-ticket-proj-2 cut from release in acme/widgets, and runs start from main now. " +
		"Remove " + widgets + "-proj-2 and the branch first. PROJ-2 already has " +
		"proj-2-ticket-proj-2 cut from release in acme/gadgets, and runs start from main now. " +
		"Remove " + gadgets + "-proj-2 and the branch first."})

	// Only the repositories the two runs share are asked about.
	reply = made.work.Start("PROJ-2", body(t, `{"model":"haiku","repos":["acme/gadgets"]}`))
	same(t, "one shared", []any{reply.Code, errorOf(reply)}, []any{409, "PROJ-2 already has " +
		"proj-2-ticket-proj-2 cut from release in acme/gadgets, and runs start from main now. " +
		"Remove " + gadgets + "-proj-2 and the branch first."})
	same(t, "runs", len(made.record("PROJ-2").Repos), 2)
}

// fakeClone is a folder that is a clone of acme/<name> as far as Conn can tell, with no git.
func fakeClone(t *testing.T, base, name string) string {
	t.Helper()

	return fakeCloneAt(t, filepath.Join(base, "clones", name), name)
}

// fakeCloneAt is fakeClone in the folder clone.
func fakeCloneAt(t *testing.T, clone, name string) string {
	t.Helper()

	heads := filepath.Join(clone, ".git", "refs", "remotes", "origin")

	err := os.MkdirAll(heads, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	writeJSON(t, filepath.Join(clone, ".git", "config"),
		"[remote \"origin\"]\n\turl = https://github.com/acme/"+name+".git\n")
	writeJSON(t, filepath.Join(heads, "HEAD"), "ref: refs/remotes/origin/main\n")

	return clone
}

func TestTheDryRunMakesEveryLegsBranch(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	made := newWorld(t, worldOpts{
		base: base, dry: true,
		settings: twoClones(fakeClone(t, base, "widgets"), fakeClone(t, base, "gadgets")),
	})

	made.work.Tickets()
	until(t, "the dry list", func() bool { return len(made.work.Tickets().Tickets) == 6 })
	same(t, "start", made.work.Start("PROJ-2", body(t, bothRepos)).Code, 200)
	until(t, "the questions", func() bool { return made.state("PROJ-2") == "asking" })

	branches := []string{}

	for _, line := range made.record("PROJ-2").Log {
		if line.Kind == "branch" {
			branches = append(branches, line.Text)
		}
	}

	same(t, "branches", branches, []string{
		"Branch proj-2-nightly-job-skipped-when-the-batch created from origin/main (dry run)",
		"Branch proj-2-nightly-job-skipped-when-the-batch created from origin/main (dry run)",
	})

	for _, leg := range made.record("PROJ-2").Repos {
		same(t, leg.Repo, leg.WorktreeReady, true)
	}

	same(t, "nothing ran", helperOrGit(made), []string{})
}

func TestARepositoryFoundOnThisMachineIsOfferedWithNothingNamedInSettings(t *testing.T) {
	t.Parallel()

	current := body(t, `{"version":1,"repos":{"allow":["acme/*"],"clones":{},"cloneRoot":null},`+
		`"atlassian":{"enabled":true,"jira":{"projects":["PROJ"],"buildRepo":null}}}`)
	made := newWorld(t, worldOpts{
		settings: current, cache: cacheOf(ticket("PROJ-2", "new")),
		seen: []string{"acme/widgets", "acme/elsewhere"},
	})
	fakeCloneAt(t, filepath.Join(made.home, "projects", "widgets"), "widgets")

	same(t, "build", parsed(t, made.work.Tickets().Build), body(t, `{"repos":[`+
		`{"repo":"acme/widgets","base":"main","ready":true,"reason":null},`+
		`{"repo":"acme/elsewhere","base":null,"ready":false,"reason":"No local clone of `+
		`acme/elsewhere: set a folder in Settings."}],"default":null,"layout":"shared"}`))
	same(t, "start", made.work.Start("PROJ-2", body(t,
		`{"model":"haiku","repos":["acme/widgets"]}`)).Code, 200)
}
