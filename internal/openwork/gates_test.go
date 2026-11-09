package openwork_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/openwork"
	command "github.com/claytoncasey01/conn/internal/run"
)

// Open work's gates and its pipeline against stand-ins for everything
// outside it (git, gh, the helper runs, cmux), recorded call by call: what must not happen while
// a module is off, a key is not the project's or a repository is not allowed, and the whole run
// for a repository whose only branch is main.

const (
	atlassianOff = `{"enabled":false,"jira":{"projects":["PROJ"],"buildRepo":"acme/widgets"}}`
	atlassianOn  = `{"enabled":true,"jira":{"projects":["PROJ"],"buildRepo":"acme/widgets"}}`
	othersOnly   = `{"allow":["other/*"],"clones":{},"cloneRoot":null}`
	slackOn      = `,"slack":{"enabled":true,"channels":["#code-review"],"dms":false,` +
		`"draftChannel":null}`
	sessionUUIDText = "0f8fad5b-d9cb-469f-a165-70867728950e"
)

// gitEnv is git with no global or system config, so nothing of this machine reaches a test.
func gitEnv() []string {
	return append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@t")
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}

	return string(out)
}

// realSh runs a command over real processes, without a shell, with git's config kept out.
func realSh(ctx context.Context, name string, args []string, dir string,
	timeout time.Duration,
) (string, error) {
	result := command.Cmd(ctx, name, args, command.Opts{
		Dir: dir, Timeout: timeout, MaxOutputBytes: 8 << 20, Stdin: "", Env: gitEnv(),
	})
	if result.Err == nil {
		return result.Stdout, nil
	}

	why := result.Stderr
	if why == "" {
		why = result.Err.Error()
	}

	return result.Stdout, fakeError(js.Slice(js.Trim(why), 400))
}

// gitClone is a folder named widgets that is a clone of acme/widgets as far as Conn can tell
// (origin and its default branch), made with git init.
func gitClone(t *testing.T, base, defaultBranch string) string {
	t.Helper()

	path := filepath.Join(base, "c"+strings.ReplaceAll(t.Name(), "/", "-"), "widgets")

	err := os.MkdirAll(path, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	gitRun(t, path, "init", "-q", "-b", defaultBranch)
	gitRun(t, path, "remote", "add", "origin", "https://github.com/acme/widgets.git")
	gitRun(t, path, "symbolic-ref", "refs/remotes/origin/HEAD",
		"refs/remotes/origin/"+defaultBranch)
	writeJSON(t, filepath.Join(path, "a"), "a")
	gitRun(t, path, "add", "a")
	gitRun(t, path, "commit", "-qm", "a")

	return path
}

func ticket(key, category string) *js.Object {
	status := "Doing"
	if category == "new" {
		status = "Open"
	}

	parsed, _ := js.Parse([]byte(`{"key":` + js.Stringify(key) + `,"title":"Ticket ` + key +
		`","type":"Bug","status":"` + status + `","statusCategory":"` + category +
		`","priority":"High","assignee":"Jane Doe","mine":true,"desc":"","links":0,` +
		`"comments":null}`))

	return objectOf(parsed)
}

func cacheOf(tickets ...any) *js.Object {
	parsed, _ := js.Parse([]byte(`{"fetchedAt":1800000000000,"projects":"PROJ"}`))
	cache := reordered(objectOf(parsed), "fetchedAt")
	cache.Set("tickets", tickets)

	return reordered(cache, "tickets")
}

// runRecord is a run as the server keeps it on disk, building by default.
func runRecord(t *testing.T, key, extra string) *js.Object {
	t.Helper()

	worktree := filepath.Join(t.TempDir(), "wt-"+key)
	text := `{"key":` + js.Stringify(key) + `,"title":"A ticket","base":"main","prBase":"main",` +
		`"model":"haiku","branch":` + js.Stringify(strings.ToLower(key)+"-a-ticket") +
		`,"worktree":` + js.Stringify(worktree) + `,"state":"building","stage":"build",` +
		`"startedAt":1,"updatedAt":1,"jiraStatus":"Doing","agents":[],"answered":[],` +
		`"questions":[],"log":[],"session":null,"pr":null,"error":null,"manualCommand":null,` +
		`"brief":"","desc":"","worktreeReady":true,"ticketStatus":"Doing"}`

	parsed, err := js.Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}

	record := objectOf(parsed)

	if extra != "" {
		over, overErr := js.Parse([]byte(extra))
		if overErr != nil {
			t.Fatal(overErr)
		}

		for _, name := range objectOf(over).Keys() {
			value, _ := objectOf(over).Get(name)
			record.Set(name, value)
		}
	}

	return record
}

// startable is a world whose list holds PROJ-2 in category, with a clone to build in.
func startable(t *testing.T, category string) *world {
	t.Helper()

	base := realTemp(t)
	clone := gitClone(t, base, "main")

	return newWorld(t, worldOpts{
		base: base, settings: workSettings(clone, ""), cache: cacheOf(ticket("PROJ-2", category)),
	})
}

func (w *world) has(prefix string) bool {
	return slices.ContainsFunc(w.lines(), func(line string) bool {
		return strings.HasPrefix(line, prefix)
	})
}

func (w *world) countPrefix(prefix string) int {
	count := 0

	for _, line := range w.lines() {
		if strings.HasPrefix(line, prefix) {
			count++
		}
	}

	return count
}

// latch is a call held until it is let go.
type latch struct{ done chan struct{} }

func newLatch() *latch { return &latch{done: make(chan struct{})} }

func (l *latch) release() { close(l.done) }

func (l *latch) wait(ctx context.Context) {
	select {
	case <-l.done:
	case <-ctx.Done():
	}
}

func body(t *testing.T, text string) *js.Object {
	t.Helper()

	parsed, err := js.Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}

	return objectOf(parsed)
}

func errorOf(reply openwork.Reply) string {
	message, _ := reply.Body.Get("error")

	return text(message)
}

func TestAToDoTicketIsMovedIntoProgressAndNoOtherIs(t *testing.T) {
	t.Parallel()

	for _, category := range []string{"new", "indeterminate", "done", ""} {
		t.Run(category, func(t *testing.T) {
			t.Parallel()

			made := startable(t, category)

			reply := made.work.Start("PROJ-2", body(t, `{"model":"haiku","force":true}`))
			if reply.Code != 200 {
				t.Fatalf("start: %d %s", reply.Code, errorOf(reply))
			}

			until(t, "the questions", func() bool { return made.state("PROJ-2") == "asking" })

			moved := made.has(`transition PROJ-2 {"category":"indeterminate"}`)
			if moved != (category == "new") {
				t.Errorf("moved %v:\n%s", moved, strings.Join(made.lines(), "\n"))
			}

			if !made.has("sh git fetch origin main") {
				t.Error("it did not fetch the clone's default branch")
			}

			if !made.has("sh git worktree add -b proj-2-ticket-proj-2 ") {
				t.Error(strings.Join(made.lines(), "\n"))
			}
		})
	}
}

func TestTheSlackReaderRunsOnlyWhileSlackIsOn(t *testing.T) {
	t.Parallel()

	for _, slackIsOn := range []bool{false, true} {
		t.Run(js.Stringify(slackIsOn), func(t *testing.T) {
			t.Parallel()

			base := realTemp(t)
			clone := gitClone(t, base, "main")

			extra := ""
			if slackIsOn {
				extra = slackOn
			}

			made := newWorld(t, worldOpts{
				base: base, settings: workSettings(clone, extra), cache: cacheOf(ticket("PROJ-2",
					"new")),
			})
			made.work.Start("PROJ-2", body(t, `{"model":"haiku"}`))
			until(t, "the questions", func() bool { return made.state("PROJ-2") == "asking" })

			if !made.has("claude ") {
				t.Fatal("no reader ran")
			}

			slackRan := made.countPrefix(`claude {"name":"slack-PROJ-2"`)
			if (slackRan == 1) != slackIsOn {
				t.Errorf("slack reader ran %d times", slackRan)
			}

			if !slackIsOn {
				agent := made.record("PROJ-2").Agents[2]
				same(t, "slack agent", []any{agent.State, agent.Finding},
					[]any{"done", "Slack is off in Settings: not searched"})
			}
		})
	}
}

func TestTurningAtlassianOffWhileTheMoveIsOutStopsTheRun(t *testing.T) {
	t.Parallel()

	made := startable(t, "new")
	killed := atomic.Bool{}
	made.transition = func(ctx context.Context, _ string, _ jira.Move) (jira.Moved, error) {
		<-ctx.Done()
		killed.Store(true)

		return jira.Moved{}, fakeError("cancelled")
	}

	made.work.Start("PROJ-2", body(t, `{"model":"haiku"}`))
	until(t, "the move", func() bool { return made.has("transition") })
	made.update("atlassian", atlassianOff)
	until(t, "the interruption", func() bool { return made.state("PROJ-2") == "interrupted" })
	made.work.Settle()

	if !killed.Load() {
		t.Error("the move in flight was not stopped")
	}

	if made.has("claude ") {
		t.Error(strings.Join(made.lines(), "\n"))
	}

	if made.state("PROJ-2") != "interrupted" {
		t.Errorf("state %s", made.state("PROJ-2"))
	}

	if !strings.Contains(*made.record("PROJ-2").Error, "Atlassian was turned off in Settings") {
		t.Error(*made.record("PROJ-2").Error)
	}
}

// fetchHeld is a world whose git fetch waits for the latch.
func fetchHeld(t *testing.T) (*world, *latch) {
	t.Helper()

	made := startable(t, "new")
	fetching := newLatch()
	made.sh = func(_ string, args []string, _ string) (string, error) {
		if args[0] == "fetch" {
			fetching.wait(t.Context())
		}

		if slices.Equal(args[:min(2, len(args))], []string{"rev-parse", "--verify"}) {
			return "", fakeError("no such branch")
		}

		return "", nil
	}

	return made, fetching
}

func TestTurningAtlassianOffWhileGitFetchesStopsTheRunBeforeAnythingElse(t *testing.T) {
	t.Parallel()

	made, fetching := fetchHeld(t)
	made.work.Start("PROJ-2", body(t, `{"model":"haiku"}`))
	until(t, "the fetch", func() bool { return made.has("sh git fetch") })
	made.update("atlassian", atlassianOff)

	if made.state("PROJ-2") != "interrupted" {
		t.Fatalf("state %s", made.state("PROJ-2"))
	}

	fetching.release()
	made.work.Settle()

	if made.has("transition") {
		t.Error(strings.Join(made.lines(), "\n"))
	}

	if made.has("claude ") {
		t.Error(strings.Join(made.lines(), "\n"))
	}

	if made.state("PROJ-2") != "interrupted" {
		t.Errorf("state %s", made.state("PROJ-2"))
	}
}

func TestRetryWaitsForTheInterruptedStageToEnd(t *testing.T) {
	t.Parallel()

	made, fetching := fetchHeld(t)
	made.work.Start("PROJ-2", body(t, `{"model":"haiku"}`))
	until(t, "the fetch", func() bool { return made.has("sh git fetch") })
	made.update("atlassian", atlassianOff)
	made.update("atlassian", atlassianOn)

	racing := made.work.Retry("PROJ-2")
	if racing.Code != 409 {
		t.Fatalf("retry %d", racing.Code)
	}

	if !strings.Contains(errorOf(racing), "still ending") {
		t.Error(errorOf(racing))
	}

	if made.work.Start("PROJ-2", body(t, `{"model":"haiku"}`)).Code != 409 {
		t.Error("a second start was not refused")
	}

	fetching.release()
	until(t, "the first pipeline to end", func() bool {
		return !openwork.Running(made.work, "PROJ-2")
	})

	if made.has("transition") {
		t.Error(strings.Join(made.lines(), "\n"))
	}

	if made.work.Retry("PROJ-2").Code != 200 {
		t.Fatal("retry was refused")
	}

	until(t, "the questions", func() bool { return made.state("PROJ-2") == "asking" })
	same(t, "moves", made.countPrefix("transition"), 1)
	same(t, "jira readers", made.countPrefix(`claude {"name":"jira-PROJ-2"`), 1)
	same(t, "syntheses", made.countPrefix(`claude {"name":"synth-`), 1)
}

func TestASettingsReloadNobodyWasToldAboutStopsThePipelineAtItsNextStep(t *testing.T) {
	t.Parallel()

	made, fetching := fetchHeld(t)
	made.work.Start("PROJ-2", body(t, `{"model":"haiku"}`))
	until(t, "the fetch", func() bool { return made.has("sh git fetch") })

	// The store is reloaded from a file that has Atlassian off: no listener hears of it.
	folder := t.TempDir()
	writeJSON(t, filepath.Join(folder, "settings.json"),
		workSettings(filepath.Join(folder, "unused"), `,"atlassian":`+atlassianOff))
	made.store.Reload(folder)

	if made.state("PROJ-2") != "starting" {
		t.Fatalf("state %s", made.state("PROJ-2"))
	}

	fetching.release()
	until(t, "the failure", func() bool { return made.state("PROJ-2") == "failed" })
	same(t, "error", *made.record("PROJ-2").Error, jira.Off)

	if made.has("transition") {
		t.Error(strings.Join(made.lines(), "\n"))
	}
}

func TestResearchStartsNoReaderWhileAtlassianIsOff(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	made := newWorld(t, worldOpts{
		base: base, settings: workSettings(gitClone(t, base, "main"), `,"atlassian":`+atlassianOff),
		runs: []any{runRecord(t, "PROJ-1", `{"state":"starting"}`)},
	})

	err := openwork.Research(made.work, "PROJ-1")
	if err == nil {
		t.Fatal("research ran")
	}

	same(t, "error", err.Error(), jira.Off)
	same(t, "calls", len(made.recorded()), 0)
	same(t, "state", made.record("PROJ-1").State, "starting")
}

func helperOrGit(made *world) []string {
	out := []string{}

	for _, line := range made.lines() {
		for _, prefix := range []string{"claude ", "sh git ", "transition", "cmux"} {
			if strings.HasPrefix(line, prefix) {
				out = append(out, line)
			}
		}
	}

	return out
}

func TestRetryFailsBeforeAnyCallWhenItMayNotRun(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	clone := gitClone(t, base, "main")
	fields := `{"state":"failed","worktreeReady":false,"repo":"acme/widgets","clone":` +
		js.Stringify(clone) + `}`
	ready := `{"state":"failed","stage":"research","worktreeReady":true,"repo":` +
		`"acme/widgets","clone":` + js.Stringify(clone) + `,"ticketCategory":"new"}`
	cases := []struct {
		name, settingsExtra, key, extra, update string
		want                                    *regexp.Regexp
	}{
		{
			"atlassian off", `,"atlassian":` + atlassianOff, "PROJ-1", fields, "",
			regexp.MustCompile(`^` + regexp.QuoteMeta(jira.Off) + `$`),
		},
		{
			"another project", "", "OTHER-1", fields, "",
			regexp.MustCompile(`not in a Jira project listed in Settings`),
		},
		{
			"stale clone", "", "PROJ-1", fields, othersOnly,
			regexp.MustCompile(`acme/widgets is not an allowed repository`),
		},
		{"another repository", "", "PROJ-1", strings.Replace(fields, "acme/widgets",
			"acme/gadgets", 1), "", regexp.MustCompile(`No local clone of acme/gadgets`)},
		{"ready, repository gone", "", "PROJ-1", ready, othersOnly, regexp.MustCompile(
			`acme/widgets is not an allowed repository: add it in Settings`)},
		{"ready, another repository", "", "PROJ-1", strings.Replace(ready, "acme/widgets",
			"acme/gadgets", 1), "", regexp.MustCompile(`No local clone of acme/gadgets`)},
	}

	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			made := newWorld(t, worldOpts{
				settings: workSettings(clone, row.settingsExtra),
				runs:     []any{runRecord(t, row.key, row.extra)},
			})
			if row.update != "" {
				made.update("repos", row.update)
			}

			if made.work.Retry(row.key).Code != 200 {
				t.Fatal("retry was refused")
			}

			until(t, "the failure", func() bool {
				record := made.record(row.key)
				if record.State != "failed" {
					return false
				}

				return row.want.MatchString(*record.Error)
			})
			made.work.Settle()
			same(t, "calls", helperOrGit(made), []string{})
		})
	}
}

// askingRecord is a run waiting for the answer to one question.
func askingRecord(t *testing.T, clone, extra string) *js.Object {
	t.Helper()

	record := runRecord(t, "PROJ-1", `{"state":"asking","stage":"ask","repo":"acme/widgets",`+
		`"clone":`+js.Stringify(clone)+`,"questions":[{"id":"q1","text":"q?","why":"w",`+
		`"choices":[{"id":"a","label":"a"}]}]}`)

	if extra != "" {
		over := body(t, extra)
		for _, name := range over.Keys() {
			value, _ := over.Get(name)
			record.Set(name, value)
		}
	}

	return record
}

func TestAnswersForARunWhoseRepositoryLeftTheListAreRefused(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	clone := gitClone(t, base, "main")
	made := newWorld(t, worldOpts{
		settings: workSettings(clone, ""), runs: []any{askingRecord(t, clone, "")},
	})
	made.update("repos", othersOnly)

	reply := made.work.Answer("PROJ-1", body(t, `{"q1":"a"}`))
	same(t, "code", reply.Code, 409)

	if !strings.Contains(errorOf(reply), "acme/widgets is not an allowed repository") {
		t.Error(errorOf(reply))
	}

	made.work.Settle()
	same(t, "state", made.state("PROJ-1"), "asking")
	same(t, "brief", fileOrNil(filepath.Join(made.record("PROJ-1").Repos[0].Worktree, ".conn",
		"brief.md")), nil)
	same(t, "calls", helperOrGit(made), []string{})
}

func TestAnswersOpenTheSessionOnlyWhereTheRunsRepositoryHasAClone(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	clone := gitClone(t, base, "main")

	moved := newWorld(t, worldOpts{settings: workSettings(clone, ""), runs: []any{
		askingRecord(t, clone, `{"repo":"acme/gadgets"}`),
	}})

	reply := moved.work.Answer("PROJ-1", body(t, `{"q1":"a"}`))
	same(t, "code", reply.Code, 409)

	if !strings.Contains(errorOf(reply), "No local clone of acme/gadgets") {
		t.Error(errorOf(reply))
	}

	same(t, "calls", helperOrGit(moved), []string{})

	worktree := t.TempDir()
	fine := newWorld(t, worldOpts{settings: workSettings(clone, ""), runs: []any{
		askingRecord(t, clone, `{"worktree":`+js.Stringify(worktree)+`}`),
	}})

	same(t, "answer", fine.work.Answer("PROJ-1", body(t, `{"q1":"a"}`)).Code, 200)
	until(t, "the session", func() bool { return fine.has("cmux") })
}

func TestAListChangeWhileGitAnswersForTheBuildSessionOpensNoSession(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	clone := gitClone(t, base, "main")
	worktree := t.TempDir()
	made := newWorld(t, worldOpts{settings: workSettings(clone, ""), runs: []any{
		askingRecord(t, clone, `{"worktree":`+js.Stringify(worktree)+`}`),
	}})
	gitPath := newLatch()
	made.sh = func(_ string, args []string, _ string) (string, error) {
		if args[0] == "rev-parse" {
			gitPath.wait(t.Context())

			return ".git/info/exclude", nil
		}

		return "", nil
	}

	same(t, "answer", made.work.Answer("PROJ-1", body(t, `{"q1":"a"}`)).Code, 200)
	until(t, "the git call", func() bool { return made.has("sh git rev-parse") })
	made.update("repos", othersOnly)
	gitPath.release()
	until(t, "the failure", func() bool { return made.state("PROJ-1") == "failed" })

	if !strings.Contains(*made.record("PROJ-1").Error, "acme/widgets is not an allowed") {
		t.Error(*made.record("PROJ-1").Error)
	}

	if made.has("cmux") {
		t.Error("a session was opened")
	}
}

func TestResumeOpensNoSessionForARepositoryOffTheList(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	clone := gitClone(t, base, "main")
	worktree := t.TempDir()
	runOf := func(repo string) *js.Object {
		return runRecord(t, "PROJ-1", `{"worktree":`+js.Stringify(worktree)+`,"repo":"`+repo+
			`","clone":`+js.Stringify(clone)+`,"claudeSessionId":"`+sessionUUIDText+`"}`)
	}

	moved := newWorld(t, worldOpts{
		settings: workSettings(clone, ""),
		runs:     []any{runOf("acme/gadgets")},
	})
	reply := moved.work.Resume("PROJ-1", "")
	same(t, "moved", reply.Code, 409)

	if !strings.Contains(errorOf(reply), "No local clone of acme/gadgets") {
		t.Error(errorOf(reply))
	}

	fine := newWorld(t, worldOpts{
		settings: workSettings(clone, ""),
		runs:     []any{runOf("acme/widgets")},
	})
	same(t, "fine", fine.work.Resume("PROJ-1", "").Code, 200)

	if !fine.has("cmux PROJ-1 " + worktree + " claude --resume " + sessionUUIDText) {
		t.Error(strings.Join(fine.lines(), "\n"))
	}

	gone := newWorld(t, worldOpts{
		settings: workSettings(clone, ""),
		runs:     []any{runOf("acme/widgets")},
	})
	gone.update("repos", othersOnly)

	reply = gone.work.Resume("PROJ-1", "")
	same(t, "gone", reply.Code, 409)

	if !strings.Contains(errorOf(reply), "acme/widgets is not an allowed repository") {
		t.Error(errorOf(reply))
	}

	same(t, "calls", helperOrGit(moved), []string{})
	same(t, "calls", helperOrGit(gone), []string{})
}
