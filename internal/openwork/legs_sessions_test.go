package openwork_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/openwork"
)

// A run in several repositories from its research to its sessions: the research reads every
// worktree, the shared layout opens one session that reaches every worktree, the split layout
// one session per worktree, each followed and resumed on its own.

// legsRun are the folders of a run in widgets and gadgets: each leg's clone and worktree.
type legsRun struct {
	clones, worktrees []string
}

// legJSON is one leg of a run in a record's file, with extra's fields over it.
func legJSON(t *testing.T, repo, clone, worktree, extra string) *js.Object {
	t.Helper()

	leg := body(t, `{"repo":"acme/`+repo+`","clone":`+js.Stringify(clone)+`,"base":"main",`+
		`"prBase":"main","branch":"proj-1-a-ticket","worktree":`+js.Stringify(worktree)+
		`,"worktreeReady":true,"session":null,"claudeSessionId":null,"manualCommand":null,`+
		`"ahead":0,"dirty":0,"pr":null,"cleaned":null}`)

	if extra != "" {
		over := body(t, extra)
		for _, name := range over.Keys() {
			value, _ := over.Get(name)
			leg.Set(name, value)
		}
	}

	return leg
}

// legsWorld is a world with PROJ-1 in widgets and gadgets, laid out as layout, in the state the
// fields over runRecord's say; legExtras are each leg's own fields. Both worktrees are made.
func legsWorld(t *testing.T, layout, fields string, legExtras ...string) (*world, legsRun) {
	t.Helper()

	base := realTemp(t)
	folders := legsRun{clones: []string{}, worktrees: []string{}}
	legs := make([]any, 0, 2)

	for index, name := range []string{"widgets", "gadgets"} {
		clone := fakeClone(t, base, name)
		worktree := filepath.Join(base, name+"-proj-1")

		err := os.MkdirAll(worktree, 0o755)
		if err != nil {
			t.Fatal(err)
		}

		extra := ""
		if index < len(legExtras) {
			extra = legExtras[index]
		}

		folders.clones = append(folders.clones, clone)
		folders.worktrees = append(folders.worktrees, worktree)
		legs = append(legs, legJSON(t, name, clone, worktree, extra))
	}

	record := runRecord(t, "PROJ-1", fields)

	legacy := []string{
		"base", "prBase", "branch", "worktree", "worktreeReady", "session", "pr", "manualCommand",
	}
	for _, key := range legacy {
		record.Delete(key)
	}

	record.Set("repos", legs)
	record.Set("layout", layout)

	made := newWorld(t, worldOpts{
		base: base, settings: twoClones(folders.clones[0], folders.clones[1]),
		runs: []any{record},
	})

	return made, folders
}

// askingFields are a run waiting for the answer to one question.
const askingFields = `{"state":"asking","stage":"ask","questions":[{"id":"q1","text":"q?",` +
	`"why":"w","choices":[{"id":"a","label":"a"}]}]}`

func TestTheResearchOfARunInTwoRepositoriesReadsBothWorktrees(t *testing.T) {
	t.Parallel()

	agent := func(id string) string {
		return `{"id":"` + id + `","name":"` + id + `","model":"","state":"queued",` +
			`"finding":"","startedAt":null,"endedAt":null}`
	}
	made, folders := legsWorld(t, "shared", `{"state":"starting","stage":"research",`+
		`"agents":[`+agent("jira")+`,`+agent("code")+`]}`)
	seen := map[string]claude.Opts{}
	lock := sync.Mutex{}
	made.helper = func(_ context.Context, opts claude.Opts) (claude.Answer, error) {
		lock.Lock()
		seen[strings.SplitN(opts.Name, "-", 2)[0]] = opts
		lock.Unlock()

		answer := js.NewObject()
		answer.Set("summary", "s")
		answer.Set("brief", "b")

		return claude.Answer{Text: "", Structured: answer}, nil
	}

	same(t, "research", openwork.Research(made.work, "PROJ-1"), nil)

	code := seen["code"]
	same(t, "code cwd", code.Dir, folders.worktrees[0])
	same(t, "code reaches", code.AddDirs, []string{folders.worktrees[1]})

	for _, id := range []string{"code", "jira"} {
		for index, repo := range []string{"acme/widgets", "acme/gadgets"} {
			named := repo + " in " + folders.worktrees[index]
			if !strings.Contains(seen[id].Prompt, named) {
				t.Errorf("the %s prompt does not name %s:\n%s", id, named, seen[id].Prompt)
			}
		}
	}

	same(t, "jira reaches", len(seen["jira"].AddDirs), 0)
}

// launchLine is the cmux call that starts a build session in worktree; reach are the other
// worktrees it is given.
func launchLine(worktree string, reach ...string) string {
	more := strings.Builder{}
	for _, folder := range reach {
		more.WriteString(" --add-dir '" + folder + "'")
	}

	return "cmux PROJ-1 " + worktree + " claude --name PROJ-1 --model sonnet " +
		"--append-system-prompt-file .conn/delegation.md" + more.String() + " --permission-mode " +
		`acceptEdits "Read .conn/brief.md and follow it exactly."`
}

// cmuxLines are the cmux calls so far.
func (w *world) cmuxLines() []string {
	out := []string{}

	for _, line := range w.lines() {
		if strings.HasPrefix(line, "cmux ") {
			out = append(out, line)
		}
	}

	return out
}

// answered takes PROJ-1's answer and waits for its build sessions to open.
func answered(t *testing.T, made *world) {
	t.Helper()

	same(t, "answer", made.work.Answer("PROJ-1", body(t, `{"q1":"a"}`)).Code, 200)
	until(t, "building", func() bool { return made.state("PROJ-1") == "building" })
	made.work.Settle()
}

func readBrief(t *testing.T, worktree string) any {
	t.Helper()

	return fileOrNil(filepath.Join(worktree, ".conn", "brief.md"))
}

func TestTheSharedLayoutOpensOneSessionThatReachesEveryWorktree(t *testing.T) {
	t.Parallel()

	made, folders := legsWorld(t, "shared", askingFields)
	answered(t, made)

	worktrees := folders.worktrees
	same(t, "cmux", made.cmuxLines(), []string{launchLine(worktrees[0], worktrees[1])})

	record := made.record("PROJ-1")
	same(t, "brief", readBrief(t, worktrees[0]), openwork.BriefText(&record, nil))
	same(t, "no second brief", readBrief(t, worktrees[1]), nil)

	for _, worktree := range worktrees {
		if !made.has("sh git rev-parse --git-path info/exclude " + worktree) {
			t.Errorf(".conn/ is not excluded in %s", worktree)
		}
	}

	same(t, "only the lead has a session", []any{
		record.Repos[1].Session, record.Repos[1].ManualCommand, record.Repos[1].ClaudeSessionID,
	}, []any{nil, nil, nil})
}

func TestTheSplitLayoutOpensOneSessionPerWorktree(t *testing.T) {
	t.Parallel()

	made, folders := legsWorld(t, "split", askingFields)
	answered(t, made)

	worktrees := folders.worktrees
	same(t, "cmux", made.cmuxLines(), []string{launchLine(worktrees[0]), launchLine(worktrees[1])})

	record := made.record("PROJ-1")
	for index, worktree := range worktrees {
		same(t, "brief", readBrief(t, worktree), openwork.BriefText(&record, record.Repos[index]))
		same(t, "log", made.hasLog("Session PROJ-1 in "+record.Repos[index].Repo+
			" opened in a new cmux workspace"), true)
	}
}

func TestACmuxFailureLeavesTheCommandOnThatSessionOnly(t *testing.T) {
	t.Parallel()

	made, folders := legsWorld(t, "split", askingFields)
	made.cmuxFailsIn = folders.worktrees[1]
	answered(t, made)

	record := made.record("PROJ-1")
	same(t, "first", record.Repos[0].ManualCommand, (*string)(nil))
	same(t, "second", *record.Repos[1].ManualCommand, "cd '"+folders.worktrees[1]+"' && "+
		strings.TrimPrefix(launchLine(folders.worktrees[1]), "cmux PROJ-1 "+
			folders.worktrees[1]+" "))
	same(t, "log", made.hasLog("Could not open a cmux workspace for acme/gadgets (cmux is not "+
		"running). Run the command shown to start the session."), true)
}

// hasLog is whether PROJ-1's log has the line.
func (w *world) hasLog(line string) bool {
	return slices.ContainsFunc(w.record("PROJ-1").Log, func(each openwork.LogLine) bool {
		return each.Text == line
	})
}

func found(id string) *openwork.Found {
	return &openwork.Found{ID: id, PID: 7, Status: "busy", SessionID: id + "-claude"}
}

func TestEachSessionOfARunIsFollowedByItsFolder(t *testing.T) {
	t.Parallel()

	rows := []struct {
		layout string
		// sessions are the session ids each leg follows ("" none).
		sessions []string
	}{
		{"shared", []string{"70-claude", ""}},
		{"split", []string{"70-claude", "71-claude"}},
	}

	for _, row := range rows {
		t.Run(row.layout, func(t *testing.T) {
			t.Parallel()

			made, folders := legsWorld(t, row.layout, "")
			made.setSessionIn(folders.worktrees[0], found("70"))
			made.setSessionIn(folders.worktrees[1], found("71"))
			made.sh = func(string, []string, string) (string, error) { return "", nil }
			made.work.Tick()
			made.work.Settle()

			record := made.record("PROJ-1")
			for index, want := range row.sessions {
				same(t, "claude id", sessionText(record.Repos[index].ClaudeSessionID), want)
				same(t, "looked up by folder", made.has("findSession  "+
					folders.worktrees[index]), want != "")
			}

			same(t, "by name", made.has("findSession PROJ-1"), false)
		})
	}
}

func sessionText(id *string) string {
	if id == nil {
		return ""
	}

	return *id
}

func TestGitIsCountedInEveryLeg(t *testing.T) {
	t.Parallel()

	made, folders := legsWorld(t, "shared", "")
	counts := map[string][2]string{
		folders.worktrees[0]: {"2\n", " M a\n"},
		folders.worktrees[1]: {"5\n", " M a\n M b\n?? c\n"},
	}
	made.sh = func(_ string, args []string, dir string) (string, error) {
		if args[0] == "rev-list" {
			return counts[dir][0], nil
		}

		return counts[dir][1], nil
	}
	made.work.Tick()
	made.work.Settle()

	record := made.record("PROJ-1")
	same(t, "counts", [][]float64{
		{record.Repos[0].Ahead, record.Repos[0].Dirty},
		{record.Repos[1].Ahead, record.Repos[1].Dirty},
	}, [][]float64{{2, 1}, {5, 3}})
	same(t, "lead's session", record.Repos[0].Session != nil, true)
	same(t, "no session of its own", record.Repos[1].Session, (*openwork.Session)(nil))
}

func TestAGitFailureInOneLegLeavesOnlyItsCounts(t *testing.T) {
	t.Parallel()

	made, folders := legsWorld(t, "split", "", `{"ahead":1,"dirty":1}`, `{"ahead":4,"dirty":4}`)
	made.sh = func(_ string, _ []string, dir string) (string, error) {
		if dir == folders.worktrees[1] {
			return "", fakeError("fatal: not a git repository")
		}

		return "3\n", nil
	}
	made.work.Tick()
	made.work.Settle()

	record := made.record("PROJ-1")
	same(t, "counts", [][]float64{
		{record.Repos[0].Ahead, record.Repos[0].Dirty},
		{record.Repos[1].Ahead, record.Repos[1].Dirty},
	}, [][]float64{{3, 1}, {4, 4}})
}

// resumeLine is the cmux call that resumes a split run's session in worktree.
func resumeLine(worktree string) string {
	return "cmux PROJ-1 " + worktree + " claude --resume " + sessionUUIDText +
		" --name PROJ-1 --model sonnet --permission-mode acceptEdits"
}

// resumable is a split run of PROJ-1 whose two sessions are known and gone.
func resumable(t *testing.T, layout string) (*world, []string) {
	t.Helper()

	known := `{"claudeSessionId":"` + sessionUUIDText + `"}`
	made, folders := legsWorld(t, layout, "", known, known)

	return made, folders.worktrees
}

func TestResumeWithoutARepositoryReopensEveryGoneSession(t *testing.T) {
	t.Parallel()

	made, worktrees := resumable(t, "split")
	made.setSessionIn(worktrees[0], found("70"))
	same(t, "one live", made.work.Resume("PROJ-1", "").Code, 200)
	same(t, "cmux", made.cmuxLines(), []string{resumeLine(worktrees[1])})

	again, worktrees := resumable(t, "split")
	same(t, "both gone", again.work.Resume("PROJ-1", "").Code, 200)
	same(t, "cmux", again.cmuxLines(), []string{resumeLine(worktrees[0]), resumeLine(worktrees[1])})

	live, worktrees := resumable(t, "split")
	live.setSessionIn(worktrees[0], found("70"))
	live.setSessionIn(worktrees[1], found("71"))

	reply := live.work.Resume("PROJ-1", "")
	same(t, "both live", []any{reply.Code, errorOf(reply)}, []any{
		409, "acme/widgets: a session for PROJ-1 is already running acme/gadgets: a session " +
			"for PROJ-1 is already running",
	})
}

func TestResumeOfOneRepositoryReopensOnlyItsSession(t *testing.T) {
	t.Parallel()

	made, worktrees := resumable(t, "split")
	same(t, "gadgets", made.work.Resume("PROJ-1", "ACME/gadgets").Code, 200)
	same(t, "cmux", made.cmuxLines(), []string{resumeLine(worktrees[1])})

	reply := made.work.Resume("PROJ-1", "acme/gadgets")
	same(t, "the gap is its own", []any{reply.Code, errorOf(reply)}, []any{409, "acme/gadgets: " +
		"a session was just opened for this run: give it a few seconds to appear"})
	same(t, "widgets", made.work.Resume("PROJ-1", "acme/widgets").Code, 200)
	same(t, "both", made.cmuxLines(), []string{resumeLine(worktrees[1]), resumeLine(worktrees[0])})
}

func TestResumeRefusesEachSessionOnItsOwnNamingItsRepository(t *testing.T) {
	t.Parallel()

	gone, worktrees := resumable(t, "split")

	err := os.RemoveAll(worktrees[1])
	if err != nil {
		t.Fatal(err)
	}

	reply := gone.work.Resume("PROJ-1", "acme/gadgets")
	same(t, "gone", []any{reply.Code, errorOf(reply)},
		[]any{409, "acme/gadgets: " + worktrees[1] + " no longer exists"})
	same(t, "the other", gone.work.Resume("PROJ-1", "").Code, 200)
	same(t, "cmux", gone.cmuxLines(), []string{resumeLine(worktrees[0])})
	same(t, "said", gone.hasLog("acme/gadgets: "+worktrees[1]+" no longer exists"), true)

	off, _ := resumable(t, "split")
	off.update("repos", `{"allow":["acme/widgets"],"clones":{},"cloneRoot":null}`)

	reply = off.work.Resume("PROJ-1", "acme/gadgets")
	same(t, "not allowed", []any{reply.Code, errorOf(reply)}, []any{409, "acme/gadgets: " +
		"acme/gadgets is not an allowed repository: add it in Settings."})

	live, worktrees := resumable(t, "split")
	live.setSessionIn(worktrees[1], found("71"))

	reply = live.work.Resume("PROJ-1", "acme/gadgets")
	same(t, "live", []any{reply.Code, errorOf(reply)},
		[]any{409, "acme/gadgets: a session for PROJ-1 is already running"})
}

func TestResumeOfARepositoryWithNoSessionOfItsOwnIsRefused(t *testing.T) {
	t.Parallel()

	made, _ := resumable(t, "shared")

	rows := []struct {
		repo, message string
		code          int
	}{
		{"acme/gadgets", "acme/gadgets has no session of its own: the run's session works in " +
			"acme/widgets", 409},
		{"acme/other", "PROJ-1 does not build in acme/other", 404},
	}

	for _, row := range rows {
		reply := made.work.Resume("PROJ-1", row.repo)
		same(t, row.repo, []any{reply.Code, errorOf(reply)}, []any{row.code, row.message})
	}

	same(t, "cmux", made.cmuxLines(), []string{})
}

func TestTheSharedSessionIsResumedReachingEveryWorktree(t *testing.T) {
	t.Parallel()

	made, worktrees := resumable(t, "shared")
	same(t, "resume", made.work.Resume("PROJ-1", "").Code, 200)
	same(t, "cmux", made.cmuxLines(), []string{"cmux PROJ-1 " + worktrees[0] + " claude --resume " +
		sessionUUIDText + " --name PROJ-1 --model sonnet --add-dir '" + worktrees[1] + "' " +
		"--permission-mode acceptEdits"})
}

func TestTheResumeRouteTakesTheRepository(t *testing.T) {
	t.Parallel()

	made, worktrees := resumable(t, "split")
	route := openwork.Route{Action: openwork.ActionResume, Key: "PROJ-1"}

	reply, err := made.work.Handle(route, body(t, `{"repo":"acme/gadgets"}`))
	same(t, "reply", []any{reply.Code, err}, []any{200, nil})
	same(t, "cmux", made.cmuxLines(), []string{resumeLine(worktrees[1])})

	reply, err = made.work.Handle(route, body(t, `{"repo":3}`))
	same(t, "not a name", []any{reply.Code, errorOf(reply), err},
		[]any{400, "repo must be a repository name", nil})
}

func TestTheDryRunOpensASessionPerSlot(t *testing.T) {
	t.Parallel()

	rows := []struct {
		layout string
		// sessions are whether each leg has a session.
		sessions []bool
		said     []string
	}{
		{"shared", []bool{true, false}, []string{"Session PROJ-1 opened (dry run)"}},
		{"split", []bool{true, true}, []string{
			"Session PROJ-1 in acme/widgets opened (dry run)",
			"Session PROJ-1 in acme/gadgets opened (dry run)",
		}},
	}

	for _, row := range rows {
		t.Run(row.layout, func(t *testing.T) {
			t.Parallel()

			base := realTemp(t)
			made := newWorld(t, worldOpts{
				base: base, dry: true, runs: []any{dryLegsRun(t, base, row.layout)},
				settings: twoClones(fakeClone(t, base, "widgets"), fakeClone(t, base, "gadgets")),
			})
			answered(t, made)

			record := made.record("PROJ-1")
			for index, want := range row.sessions {
				leg := record.Repos[index]
				same(t, "session", leg.Session != nil, want)
				same(t, "counts", []float64{leg.Ahead, leg.Dirty}, []float64{3, 2})
			}

			for _, line := range row.said {
				same(t, line, made.hasLog(line), true)
			}

			same(t, "resume", made.work.Resume("PROJ-1", "").Code, 200)
			same(t, "nothing ran", helperOrGit(made), []string{})
		})
	}
}

// dryLegsRun is PROJ-1 asking, in widgets and gadgets, for the dry run.
func dryLegsRun(t *testing.T, base, layout string) *js.Object {
	t.Helper()

	record := runRecord(t, "PROJ-1", askingFields)
	for _, key := range []string{
		"base", "prBase", "branch", "worktree", "worktreeReady",
		"session", "pr", "manualCommand",
	} {
		record.Delete(key)
	}

	record.Set("repos", []any{
		legJSON(t, "widgets", filepath.Join(base, "clones", "widgets"),
			filepath.Join(base, "widgets-proj-1"), ""),
		legJSON(t, "gadgets", filepath.Join(base, "clones", "gadgets"),
			filepath.Join(base, "gadgets-proj-1"), ""),
	})
	record.Set("layout", layout)

	return record
}

func TestTheDryRunTakesATicketInTwoRepositoriesFromStartToItsSessions(t *testing.T) {
	t.Parallel()

	rows := []struct {
		layout string
		// sessions are whether each leg has a session.
		sessions []bool
	}{
		{"shared", []bool{true, false}},
		{"split", []bool{true, true}},
	}

	for _, row := range rows {
		t.Run(row.layout, func(t *testing.T) {
			t.Parallel()

			base := realTemp(t)
			made := newWorld(t, worldOpts{
				base: base, dry: true,
				settings: twoClones(fakeClone(t, base, "widgets"), fakeClone(t, base, "gadgets")),
			})

			made.work.Tickets()
			until(t, "the dry list", func() bool { return len(made.work.Tickets().Tickets) == 6 })
			same(t, "start", made.work.Start("PROJ-2", body(t, `{"model":"haiku","repos":`+
				`["acme/widgets","acme/gadgets"],"layout":"`+row.layout+`"}`)).Code, 200)
			until(t, "the questions", func() bool { return made.state("PROJ-2") == "asking" })
			same(t, "answers", made.work.Answer("PROJ-2",
				body(t, `{"q1":"c1","q2":"c2"}`)).Code, 200)

			record := made.record("PROJ-2")
			same(t, "layout", record.Layout, row.layout)

			for index, want := range row.sessions {
				leg := record.Repos[index]
				same(t, "ready", leg.WorktreeReady, true)
				same(t, "session", leg.Session != nil, want)
				same(t, "counts", []float64{leg.Ahead, leg.Dirty}, []float64{3, 2})
			}

			same(t, "resume", made.work.Resume("PROJ-2", "").Code, 200)

			if row.layout == "split" {
				same(t, "resume one", made.work.Resume("PROJ-2", "acme/gadgets").Code, 200)
			}

			same(t, "nothing ran", helperOrGit(made), []string{})
		})
	}
}

// allowOnly makes only acme/widgets an allowed repository, its clone kept.
func allowOnly(made *world, folders legsRun) {
	made.update("repos", `{"allow":["acme/widgets"],"clones":{"acme/widgets":`+
		js.Stringify(folders.clones[0])+`},"cloneRoot":null}`)
}

// allowBoth makes acme/* allowed again, with both clones.
func allowBoth(made *world, folders legsRun) {
	made.update("repos", `{"allow":["acme/*"],"clones":{"acme/widgets":`+
		js.Stringify(folders.clones[0])+`,"acme/gadgets":`+js.Stringify(folders.clones[1])+
		`},"cloneRoot":null}`)
}

func TestASplitRunOpensTheSessionsItMayAndOneLeftOutStartsFreshOnResume(t *testing.T) {
	t.Parallel()

	made, folders := legsWorld(t, "split", askingFields)
	worktrees := folders.worktrees
	allowOnly(made, folders)
	answered(t, made)

	record := made.record("PROJ-1")
	same(t, "state", record.State, "building")
	same(t, "cmux", made.cmuxLines(), []string{launchLine(worktrees[0])})
	same(t, "launched", []bool{record.Repos[0].SessionLaunched, record.Repos[1].SessionLaunched},
		[]bool{true, false})
	same(t, "said", made.hasLog("Session PROJ-1 in acme/gadgets not opened: acme/gadgets is "+
		"not an allowed repository: add it in Settings."), true)

	reply := made.work.Resume("PROJ-1", "acme/gadgets")
	same(t, "still off", []any{reply.Code, errorOf(reply)}, []any{409, "acme/gadgets: " +
		"acme/gadgets is not an allowed repository: add it in Settings."})

	allowBoth(made, folders)
	same(t, "resume", made.work.Resume("PROJ-1", "acme/gadgets").Code, 200)
	same(t, "fresh", made.cmuxLines(), []string{launchLine(worktrees[0]), launchLine(worktrees[1])})
	same(t, "brief", readBrief(t, worktrees[1]),
		openwork.BriefText(&record, record.Repos[1]))
	same(t, "now launched", made.record("PROJ-1").Repos[1].SessionLaunched, true)
}

func TestAResumeOfASessionLaunchedWithNoIdYetIsRefused(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name, layout, leg, repo, message string
	}{
		{
			"split, launched", "split", `{"sessionLaunched":true}`, "acme/gadgets",
			"acme/gadgets: the session has not started yet: wait for it to open",
		},
		{
			"shared, from before launches were recorded", "shared", "", "",
			"the session has not started yet: wait for it to open",
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			made, _ := legsWorld(t, row.layout, "", row.leg, row.leg)
			reply := made.work.Resume("PROJ-1", row.repo)
			same(t, "refused", []any{reply.Code, errorOf(reply)}, []any{409, row.message})
			same(t, "cmux", made.cmuxLines(), []string{})
		})
	}
}

func TestASharedRunWithARepositoryOffTheListOpensNothing(t *testing.T) {
	t.Parallel()

	made, folders := legsWorld(t, "shared", askingFields)
	allowOnly(made, folders)

	reply := made.work.Answer("PROJ-1", body(t, `{"q1":"a"}`))
	same(t, "refused", []any{reply.Code, errorOf(reply)}, []any{409, "acme/gadgets is not an " +
		"allowed repository: add it in Settings."})
	made.work.Settle()
	same(t, "cmux", made.cmuxLines(), []string{})
}
