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

// The real-git cases and what the Settings cards learn: the linked
// worktree check, the cleanup guard, a repository whose only branch is main from START to the
// PR, and what Open work tells the Settings cards.

func TestLinkedWorktreeRepoFindsTheRepositoryOfALinkedWorktreeAndNothingElse(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	main := gitClone(t, base, "main")
	worktree := filepath.Join(base, "anything-at-all")
	gitRun(t, main, "worktree", "add", "-q", "-b", "proj-7-fix", worktree)

	linked := func(path string) (string, bool) {
		return openwork.LinkedWorktreeRepo(t.Context(), path, realSh)
	}

	repo, found := linked(worktree)
	same(t, "linked", []any{repo, found}, []any{main, true})

	plain := filepath.Join(base, "plain")

	err := os.MkdirAll(filepath.Join(worktree, "sub"), 0o755)
	if err == nil {
		err = os.MkdirAll(plain, 0o755)
	}

	if err != nil {
		t.Fatal(err)
	}

	// The main checkout, a folder inside a worktree, a folder git does not list, one in no
	// repository, a path that is not there.
	for _, path := range []string{
		main, filepath.Join(worktree, "sub"), plain, filepath.Join(base, "nope"),
	} {
		_, found = linked(path)
		same(t, path, found, false)
	}

	// Another repository's worktree folder, once git no longer lists it.
	gitRun(t, main, "worktree", "remove", "--force", worktree)

	err = os.MkdirAll(worktree, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	_, found = linked(worktree)
	same(t, "unlisted", found, false)
}

func mergedRecord(t *testing.T, key, worktree, extra string) *js.Object {
	t.Helper()

	return runRecord(t, key, `{"worktree":`+js.Stringify(worktree)+`,"state":"pr_open",`+
		`"stage":"pr","pr":{"url":"https://github.com/acme/widgets/pull/9","number":9,`+
		`"status":{"state":"merged","checks":{"state":"passing","total":1,"pending":0,`+
		`"failed":[]},"review":"APPROVED","mergeable":"","at":1}}`+extra+`}`)
}

func TestCleanupRemovesTheRecordedWorktreeOnlyWhenItIsALinkedOne(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	main := gitClone(t, base, "main")
	parent := filepath.Dir(main)
	legacy := filepath.Join(parent, "old-style-7")
	renamed := filepath.Join(parent, "widgets-proj-8")
	unlisted := filepath.Join(parent, "unlisted")

	gitRun(t, main, "worktree", "add", "-q", "-b", "old-7-fix", legacy)
	gitRun(t, main, "worktree", "add", "-q", "-b", "proj-8-fix", renamed)

	err := os.MkdirAll(unlisted, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	writeJSON(t, filepath.Join(unlisted, "precious"), "x")

	made := newWorld(t, worldOpts{base: base, settings: workSettings(main, ""), runs: []any{
		mergedRecord(t, "PROJ-7", legacy, ""), mergedRecord(t, "PROJ-8", renamed, ""),
		mergedRecord(t, "PROJ-9", main, ""), mergedRecord(t, "PROJ-10", unlisted, ""),
		mergedRecord(t, "PROJ-11", "relative/widgets-proj-11", ""),
	}})
	made.sh = func(name string, args []string, dir string) (string, error) {
		return realSh(t.Context(), name, args, dir, 0)
	}

	for _, key := range []string{"PROJ-7", "PROJ-8"} {
		same(t, key, made.work.Cleanup(key).Code, 200)
	}

	same(t, "legacy", exists(legacy), false)
	same(t, "renamed", exists(renamed), false)

	for _, key := range []string{"PROJ-9", "PROJ-10", "PROJ-11"} {
		same(t, key, made.work.Cleanup(key).Code, 400)
	}

	same(t, "clone", exists(filepath.Join(main, "a")), true)
	same(t, "precious", exists(filepath.Join(unlisted, "precious")), true)

	if !strings.Contains(gitRun(t, main, "branch", "--list", "proj-8-fix"), "proj-8-fix") {
		t.Error("the branch went with the worktree")
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

func TestCleanupRemovesAWorktreeOnlyOfTheRepositoryTheRunRecorded(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	mine := gitClone(t, base, "main")
	theirs := filepath.Join(base, "theirs")

	err := os.MkdirAll(theirs, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	gitRun(t, theirs, "init", "-q")

	worktree := filepath.Join(base, "cleanup-wt")
	gitRun(t, mine, "worktree", "add", "-q", "-b", "proj-1-x", worktree)

	for _, row := range []struct {
		clone string
		code  int
	}{{theirs, 400}, {mine, 200}} {
		made := newWorld(t, worldOpts{
			base: base, settings: workSettings(mine, ""),
			runs: []any{mergedRecord(t, "PROJ-1", worktree, `,"clone":`+js.Stringify(row.clone))},
		})
		made.sh = func(name string, args []string, dir string) (string, error) {
			return realSh(t.Context(), name, args, dir, 0)
		}

		reply := made.work.Cleanup("PROJ-1")
		same(t, row.clone, reply.Code, row.code)

		if row.code == 400 {
			if !strings.Contains(errorOf(reply), "belongs to another repository") {
				t.Error(errorOf(reply))
			}

			same(t, "kept", exists(worktree), true)
		}
	}

	same(t, "removed", exists(worktree), false)
}

func TestARepositoryWithOnlyAMainBranchWorksFromStartToThePR(t *testing.T) {
	t.Parallel()

	root := realTemp(t)
	bare := filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	clone := filepath.Join(root, "widgets")

	gitRun(t, root, "init", "-q", "--bare", "-b", "main", bare)
	gitRun(t, root, "clone", "-q", bare, seed)
	gitRun(t, seed, "checkout", "-q", "-b", "main")
	writeJSON(t, filepath.Join(seed, "a"), "a")
	gitRun(t, seed, "add", "a")
	gitRun(t, seed, "commit", "-qm", "a")
	gitRun(t, seed, "push", "-q", "origin", "main")
	gitRun(t, root, "clone", "-q", bare, clone)
	gitRun(t, clone, "remote", "set-url", "origin", "https://github.com/acme/widgets.git")
	gitRun(t, clone, "config", "url."+bare+".insteadOf", "https://github.com/acme/widgets.git")

	made := newWorld(t, worldOpts{
		base: root, settings: workSettings(clone, ""),
		cache: cacheOf(ticket("PROJ-2", "new")),
	})

	ghCalls := [][]string{}
	made.sh = func(name string, args []string, dir string) (string, error) {
		if name == "gh" {
			ghCalls = append(ghCalls, args)

			return "https://github.com/acme/widgets/pull/5\n", nil
		}

		return realSh(t.Context(), name, args, dir, 0)
	}

	same(t, "build", parsed(t, made.work.Tickets().Build), body(t, `{"repos":[{"repo":`+
		`"acme/widgets","base":"main","ready":true,"reason":null}],"default":"acme/widgets",`+
		`"layout":"shared"}`))
	same(t, "start", made.work.Start("PROJ-2", body(t, `{"model":"haiku"}`)).Code, 200)
	until(t, "the questions", func() bool { return made.state("PROJ-2") == "asking" })

	worktree := filepath.Join(root, "widgets-proj-2")
	same(t, "worktree", exists(filepath.Join(worktree, "a")), true)
	same(t, "head", strings.TrimSpace(gitRun(t, worktree, "rev-parse", "--abbrev-ref", "HEAD")),
		"proj-2-ticket-proj-2")

	record := made.record("PROJ-2")
	same(t, "bases", []any{record.Repos[0].Base, record.Repos[0].PRBase}, []any{"main", "main"})

	answers := js.NewObject()
	for _, question := range record.Questions {
		answers.Set(question.ID, "a")
	}

	same(t, "answer", made.work.Answer("PROJ-2", answers).Code, 200)
	until(t, "the build session", func() bool { return made.state("PROJ-2") == "building" })

	brief, _ := fileOrNil(filepath.Join(worktree, ".conn", "brief.md")).(string)
	if !strings.Contains(brief, "The pull request will target main.") {
		t.Error(brief)
	}

	writeJSON(t, filepath.Join(worktree, "b"), "b")
	gitRun(t, worktree, "add", "b")
	gitRun(t, worktree, "commit", "-qm", "b")
	same(t, "open", made.work.OpenPR("PROJ-2").Code, 200)
	until(t, "the PR", func() bool { return made.state("PROJ-2") == "pr_open" })
	made.work.Settle()

	if !strings.Contains(gitRun(t, bare, "branch", "--list", "proj-2-ticket-proj-2"),
		"proj-2-ticket-proj-2") {
		t.Error("the branch was not pushed")
	}

	same(t, "gh", ghCalls[0][:8], []string{
		"pr", "create", "--repo", "acme/widgets", "--base",
		"main", "--head", "proj-2-ticket-proj-2",
	})
}

// records are what Open work told the Settings cards, as `<module> ok|failing: <detail>`.
func (w *world) records() []string {
	out := []string{}

	for _, call := range w.recorded() {
		parts := listOf(call)
		if parts[0] != "record" {
			continue
		}

		state := "failing"
		if parts[2] == true {
			state = "ok"
		}

		out = append(out, text(parts[1])+" "+state+": "+text(parts[3]))
	}

	return out
}

func TestARunTellsTheSettingsCardsHowEachReaderAndTheMoveAnswered(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	clone := gitClone(t, base, "main")
	newRun := func(fail string) *world {
		made := newWorld(t, worldOpts{
			base: base, settings: workSettings(clone, slackOn),
			cache: cacheOf(ticket("PROJ-2", "new")),
		})
		made.failReader = fail

		made.work.Start("PROJ-2", body(t, `{"model":"haiku"}`))
		until(t, "the questions", func() bool { return made.state("PROJ-2") == "asking" })

		return made
	}

	all := newRun("").records()
	slices.Sort(all)
	same(t, "all", all, []string{
		"atlassian ok: Open work moved a Jira ticket",
		"atlassian ok: Open work's Code mapper worked",
		"atlassian ok: Open work's Confluence reader worked",
		"atlassian ok: Open work's Jira reader worked",
		"slack ok: Open work's Slack reader worked",
	})

	for reader, want := range map[string]string{
		"jira":  "atlassian failing: Open work's Jira reader failed",
		"slack": "slack failing: Open work's Slack reader failed",
	} {
		if !slices.Contains(newRun(reader).records(), want) {
			t.Errorf("%s: no %q", reader, want)
		}
	}
}

func TestAMoveTellsTheSettingsCardsOnlyWhatTheConnectorSaid(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	clone := gitClone(t, base, "main")
	cases := []struct {
		name, extra string
		err         error
		want        []string
	}{
		{
			"failed", "", fakeError("no such transition"),
			[]string{"atlassian failing: Open work could not move a Jira ticket"},
		},
		{"stopped", `{"state":"interrupted"}`, fakeError("killed"), []string{}},
		{
			"refused here", "", &jira.MoveRefusedError{
				Message: "PROJ-1 is not in a Jira project listed in Settings", Called: false,
			},
			[]string{},
		},
		{
			"refused by the workflow", "", &jira.MoveRefusedError{
				Message: "two review statuses", Called: true,
			},
			[]string{"atlassian ok: Open work asked Jira to move a ticket"},
		},
		{"signed out", "", &jira.SignedOutError{}, []string{
			"atlassian failing: Atlassian is signed out: sign in to the claude.ai Atlassian " +
				"connector",
		}},
	}

	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			made := newWorld(t, worldOpts{
				base: base, settings: workSettings(clone, ""),
				runs: []any{runRecord(t, "PROJ-1", row.extra)},
			})
			made.transition = func(context.Context, string, jira.Move) (jira.Moved, error) {
				return jira.Moved{}, row.err
			}

			openwork.MoveJira(made.work, "PROJ-1", jira.MoveReview())
			same(t, "records", made.records(), row.want)

			if row.name == "refused here" {
				if !strings.Contains(lastLog(made), "Could not move PROJ-1") {
					t.Error(lastLog(made))
				}
			}
		})
	}
}

func TestReadingTheSprintTellsTheSettingsCardsHowAtlassianAnswered(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	clone := gitClone(t, base, "main")
	stale := body(t, `{"tickets":[],"fetchedAt":0,"projects":"PROJ"}`)
	cases := []struct {
		name  string
		fetch func(ctx context.Context, hooks openwork.Hooks) (jira.Sprint, error)
		want  string
	}{
		{"read", func(context.Context, openwork.Hooks) (jira.Sprint, error) {
			return jira.Sprint{Tickets: []*js.Object{ticket("PROJ-1", "new")}, Truncated: false},
				nil
		}, "atlassian ok: Open work read the sprint: 1 ticket"},
		{"signed out", func(_ context.Context, hooks openwork.Hooks) (jira.Sprint, error) {
			hooks.OnAuth([]string{"claude.ai Atlassian"})

			return jira.Sprint{}, fakeError("not signed in")
		}, "atlassian failing: Atlassian is signed out: sign in to the claude.ai Atlassian " +
			"connector"},
		{"failed", func(context.Context, openwork.Hooks) (jira.Sprint, error) {
			return jira.Sprint{}, fakeError("boom")
		}, "atlassian failing: Open work could not read the sprint from Jira"},
	}

	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			made := newWorld(t, worldOpts{
				base: base, settings: workSettings(clone, ""),
				cache: stale,
			})
			made.fetch = row.fetch
			made.work.Refresh()
			until(t, "the record", func() bool { return len(made.records()) > 0 })
			same(t, "records", made.records(), []string{row.want})
		})
	}
}

func TestWhileOpenWorkIsOffTheListIsEmptyAndNothingStarts(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	clone := gitClone(t, base, "main")
	listed := ticket("PROJ-1", "new")
	listed.Set("assignee", "")
	listed.Set("mine", false)

	made := newWorld(t, worldOpts{
		base: base, settings: workSettings(clone,
			`,"atlassian":{"enabled":false,"jira":{"projects":["PROJ"],"buildRepo":null}}`),
		cache: cacheOf(listed),
	})

	same(t, "tickets", len(made.work.Tickets().Tickets), 0)
	same(t, "start", made.work.Start("PROJ-1", body(t, `{"model":"haiku","force":true}`)).Code,
		404)
	same(t, "off", errorOf(made.work.Start("PROJ-1", body(t, `{}`))), jira.Off)
	same(t, "move", made.work.MoveTicket("PROJ-1", "Done").Code, 404)
	made.work.Refresh()
	same(t, "fetching", made.work.Tickets().Fetching, false)

	made.update("atlassian", `{"enabled":true,"jira":{"projects":["PROJ"],"buildRepo":null}}`)
	same(t, "on", len(made.work.Tickets().Tickets), 1)
	same(t, "projects", made.work.Tickets().Projects, []string{"PROJ"})

	for _, key := range []string{"OTHER-1", "PROJ-1\"; x"} {
		same(t, key, made.work.Start(key, body(t, `{"model":"haiku","force":true}`)).Code, 400)
	}
}

func TestARunNeedsARepositoryAndAClone(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	listed := ticket("PROJ-1", "new")
	made := newWorld(t, worldOpts{
		base: base, settings: workSettings("",
			`,"atlassian":{"enabled":true,"jira":{"projects":["PROJ"],"buildRepo":null}}`),
		cache: cacheOf(listed),
	})

	reply := made.work.Start("PROJ-1", body(t, `{"model":"haiku"}`))
	same(t, "no repo", []any{reply.Code, errorOf(reply)}, []any{
		409,
		"Choose at least one repository.",
	})
	same(t, "no repo build", parsed(t, made.work.Tickets().Build), body(t, `{"repos":[],`+
		`"default":null,"layout":"shared"}`))

	made.update("repos", `{"allow":["acme/*"],"clones":{"acme/widgets":`+
		js.Stringify(filepath.Join(base, "does-not-exist"))+`},"cloneRoot":null}`)
	made.update("atlassian", atlassianOn)

	reply = made.work.Start("PROJ-1", body(t, `{"model":"haiku"}`))
	missing := "No local clone of acme/widgets: set a folder in Settings."
	same(t, "no clone", []any{reply.Code, errorOf(reply)}, []any{409, missing})
	same(t, "no clone build", parsed(t, made.work.Tickets().Build), body(t, `{"repos":[`+
		`{"repo":"acme/widgets","base":null,"ready":false,"reason":`+js.Stringify(missing)+
		`}],"default":"acme/widgets","layout":"shared"}`))
	same(t, "runs", string(openwork.Snapshot(made.work)), "[]")

	clone := gitClone(t, base, "main")
	made.update("repos", `{"allow":["acme/*"],"clones":{"acme/widgets":`+js.Stringify(clone)+
		`},"cloneRoot":null}`)
	same(t, "ready", parsed(t, made.work.Tickets().Build), body(t, `{"repos":[{"repo":`+
		`"acme/widgets","base":"main","ready":true,"reason":null}],"default":"acme/widgets",`+
		`"layout":"shared"}`))
}
