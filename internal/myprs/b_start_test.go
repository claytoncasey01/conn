package myprs_test

// SELF-REVIEW and RE-REVIEW presses: names that read alike, the same number in two repos, a
// launch that failed, a session of the PR's folder, and CONN_MYPRS_DRY. Bun's myPrs.test.ts
// "---- start: Self-Review and Re-Review ----", second half.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
)

const bPaste = "cd /wt && claude"

// Bun: 'session names that read the same for two PRs never block each other'.
func TestBNamesThatReadAlikeNeverBlock(t *testing.T) {
	t.Parallel()

	// repo web PR 12 round 3 and repo web-12 PR 3 round 1 are both self-web-12-3.
	world, tab, _ := bPolled(t,
		map[string]string{"org/web": "/clone/web", "org/web-12": "/clone/web-12"},
		pr(func(p *myprs.PR) { p.Repo, p.Number = "org/web", 12 }),
		pr(func(p *myprs.PR) {
			p.Repo, p.Number, p.URL = "org/web-12", 3, "https://github.com/org/web-12/pull/3"
		}),
	)
	bEqual(t, "web-12", bStart(t, tab, "org/web-12", 3, bSonnet).Code, 200)
	bEqual(t, "name", bLaunched(world)[0].Name, "self-web-12-3")
	bSessions(world, bLive("self-web-12-3", "/wt/self-web-12-3", "busy"))
	// org/web#12 has no launch of its own: the other repo's live session is not its session.
	bEqual(t, "web", bStart(t, tab, "org/web", 12, bSonnet).Code, 200)
	bEqual(t, "launched", len(bLaunched(world)), 2)
	// And the live session shows as running on its own row only.
	bEqual(t, "web-12 status", bRowOf(t, tab, "org/web-12").Status, "running")
	bEqual(t, "web status", bRowOf(t, tab, "org/web").Status, "idle")
}

// Bun: 'the same number in two repos launches separately, within the same 30 s'.
func TestBSameNumberInTwoReposLaunchesSeparately(t *testing.T) {
	t.Parallel()

	world, tab, file := bPolled(t, map[string]string{"org/a": "/clone/a", "org/b": "/clone/b"},
		pr(func(p *myprs.PR) {
			p.Repo, p.Number, p.URL = "org/a", 5, "https://github.com/org/a/pull/5"
		}),
		pr(func(p *myprs.PR) {
			p.Repo, p.Number, p.URL = "org/b", 5, "https://github.com/org/b/pull/5"
		}),
	)
	bEqual(t, "a", bStart(t, tab, "org/a", 5, bSonnet).Code, 200)
	bEqual(t, "b", bStart(t, tab, "org/b", 5, bSonnet).Code, 200)

	launched := bLaunched(world)
	got := make([][]any, 0, len(launched))

	for _, launch := range launched {
		got = append(got, []any{launch.Repo, launch.Name, launch.RepoPath, launch.Round})
	}

	bEqual(t, "launched", got, [][]any{
		{"org/a", "self-a-5", "/clone/a", 1.0}, {"org/b", "self-b-5", "/clone/b", 1.0},
	})
	bEqual(t, "a saved", len(bLaunches(file, "org/a", 5)), 1)
	bEqual(t, "b saved", len(bLaunches(file, "org/b", 5)), 1)
}

// Bun: 'a cmux failure records the round for the session pasted by hand (armed, at the
// worktree's commit, marked manual) and still answers the command'.
func TestBCmuxFailureRecordsAManualRound(t *testing.T) {
	t.Parallel()

	world, tab, file := bPolled(t, nil, pr(func(p *myprs.PR) { p.HeadSha = "aaa111" }))
	bAnswer(world, myprs.LaunchResult{
		OK: false, Error: "cmux is not running", ManualCommand: bPaste,
		Worktree: "/wt/self-repo-7", HeadSha: "bbb222",
	})

	reply := bStart(t, tab, bRepo, 7, bSonnet)
	bEqual(t, "code", reply.Code, 200)
	bEqual(t, "body", bBody(reply),
		`{"ok":false,"error":"cmux is not running","manualCommand":"cd /wt && claude"}`)
	bEqual(t, "launches", string(js.Marshal(bLaunches(file, bRepo, 7))),
		`[{"at":1800000000000,"model":"sonnet","name":"self-repo-7",`+
			`"worktree":"/wt/self-repo-7","headSha":"bbb222","round":1,"armed":true,`+
			`"askedSha":null,"askedAt":null,"closedAt":null,"askTry":null,"manual":true}]`)

	row := bRow(t, tab)
	bEqual(t, "label", []any{row.Label, row.Round}, []any{"Re-review", 1.0})
	// The pasted session is this round's: the row shows it running, and a press is refused
	// while it is live.
	world.Advance(10 * bMin)
	bSessions(world, bLive("self-repo-7", "/wt/self-repo-7", "busy"))
	bEqual(t, "status", bRow(t, tab).Status, "running")

	again := bStart(t, tab, bRepo, 7, bSonnet)
	bEqual(t, "again", again.Code, 409)
	bContains(t, "again error", bError(again), "self-repo-7")
	bEqual(t, "launched", len(bLaunched(world)), 1)
}

// Bun: 'a round to paste that is not live yet holds a press for the usual 5 minutes, and the
// refusal says to paste the command'.
func TestBRoundToPasteHoldsAPressFiveMinutes(t *testing.T) {
	t.Parallel()

	world, tab, _ := bPolled(t, nil, pr())
	bAnswer(world, myprs.LaunchResult{
		OK: false, Error: "cmux is not running", ManualCommand: bPaste,
		Worktree: "/wt/self-repo-7", HeadSha: "abc",
	})
	bStart(t, tab, bRepo, 7, bSonnet)
	world.Advance(5*bMin - 1)

	reply := bStart(t, tab, bRepo, 7, bSonnet)
	bEqual(t, "code", reply.Code, 409)
	bContains(t, "error", bError(reply), "paste")
	world.Advance(1)
	world.Change(func(w *World) { w.LaunchResult = nil })

	reply = bStart(t, tab, bRepo, 7, bSonnet)
	bEqual(t, "after", []any{reply.Code, bBody(reply)},
		[]any{200, `{"ok":true,"name":"self-repo-7-2"}`})
}

// Bun: 'a launch failure with no command to paste (git, the prompt write) records nothing, and
// the next press launches at once'.
func TestBLaunchFailureWithoutACommandRecordsNothing(t *testing.T) {
	t.Parallel()

	world, tab, file := bPolled(t, nil, pr())
	bAnswer(world, myprs.LaunchResult{
		OK: false, Error: "git said no", ManualCommand: "", Worktree: "", HeadSha: "",
	})
	bEqual(t, "body", bBody(bStart(t, tab, bRepo, 7, bSonnet)),
		`{"ok":false,"error":"git said no"}`)
	bEqual(t, "launches", len(bLaunches(file, bRepo, 7)), 0)
	bEqual(t, "label", bRow(t, tab).Label, "Self-review")
	// A command without the worktree it runs in is not a round conn can follow either.
	bAnswer(world, myprs.LaunchResult{
		OK: false, Error: "cmux is not running", ManualCommand: bPaste, Worktree: "",
		HeadSha: "",
	})

	command, _ := bStart(t, tab, bRepo, 7, bSonnet).Body.Get("manualCommand")
	bEqual(t, "manualCommand", command, bPaste)
	bEqual(t, "launches after", len(bLaunches(file, bRepo, 7)), 0)
	world.Change(func(w *World) { w.LaunchResult = nil })
	bEqual(t, "next", bStart(t, tab, bRepo, 7, bSonnet).Code, 200)

	launched := bLaunched(world)
	bEqual(t, "launched", len(launched), 3)
	bEqual(t, "round", launched[2].Round, 1.0)
}

// Bun: 'a live session named like one of this PR's rounds, working in its folder (links
// resolved), refuses a press whatever the records say'.
func TestBSessionOfARoundInItsFolderRefusesAPress(t *testing.T) {
	t.Parallel()

	world, tab, _ := bPolled(t, nil, pr())

	folder, err := myprs.Folder("/data", bRepo, 7)
	if err != nil {
		t.Fatal(err)
	}

	// No launch recorded at all (a record lost, or a round started by hand).
	bSessions(world, bLive("self-repo-7-3", folder+"/clone", "busy"))

	reply := bStart(t, tab, bRepo, 7, bSonnet)
	bEqual(t, "code", reply.Code, 409)
	bContains(t, "error", bError(reply), "self-repo-7-3")
	bEqual(t, "calls", bCounts(world), [2]int{0, 0})

	row := bRow(t, tab)
	bEqual(t, "row", []any{row.Status, row.CanStart}, []any{"running", false})

	// A name that is not one of this PR's rounds, or this PR's name in another folder, is not
	// this PR's session.
	others := []struct{ name, cwd string }{
		{"self-repo-70", folder + "/clone"},
		{"self-repo-7-x", folder + "/clone"},
		{"self-repo-7", folder + "-2/clone"},
		{"self-repo-7", "/elsewhere"},
	}
	for _, other := range others {
		bSessions(world, bLive(other.name, other.cwd, "busy"))
		bEqual(t, other.name+" in "+other.cwd, bRow(t, tab).Status, "idle")
	}

	bSessions(world)
	bEqual(t, "free", bStart(t, tab, bRepo, 7, bSonnet).Code, 200)
}

// Bun: 'a session in the PR's folder is found when the data folder is reached through a link'.
func TestBSessionInTheFolderFoundThroughALinkedDataFolder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	realData := filepath.Join(dir, "rounds-real-data")
	linked := filepath.Join(dir, "rounds-linked-data")

	folder, err := myprs.Folder(realData, bRepo, 7)
	if err != nil {
		t.Fatal(err)
	}

	err = os.MkdirAll(filepath.Join(folder, "clone"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(realData, linked)
	if err != nil {
		t.Fatal(err)
	}

	world := newWorld(t, pr())
	deps := world.Deps(bFile(t))
	deps.DataDir = linked
	tab := bNew(deps)
	tick(tab)
	// The registry reports the folder as the session sees it: resolved.
	bSessions(world, bLive("self-repo-7", filepath.Join(folder, "clone"), "busy"))
	bEqual(t, "code", bStart(t, tab, bRepo, 7, bSonnet).Code, 409)
	bEqual(t, "launched", len(bLaunched(world)), 0)
}

// Bun: 'a launch that throws is an error reply, records nothing and frees the PR'. Go's launch
// has no error return: what Bun's throw becomes is a failed result with no command to paste.
func TestBLaunchErrorRepliesAndFreesThePR(t *testing.T) {
	t.Parallel()

	world, tab, file := bPolled(t, nil, pr())
	bAnswer(world, myprs.LaunchResult{
		OK: false, Error: "boom", ManualCommand: "", Worktree: "", HeadSha: "",
	})

	reply := bStart(t, tab, bRepo, 7, bSonnet)
	bEqual(t, "code", reply.Code, 200)

	ok, _ := reply.Body.Get("ok")
	bEqual(t, "ok", ok, false)
	bContains(t, "error", bError(reply), "boom")
	bEqual(t, "launches", len(bLaunches(file, bRepo, 7)), 0)
	world.Change(func(w *World) { w.LaunchResult = nil })
	bEqual(t, "next", bStart(t, tab, bRepo, 7, bSonnet).Code, 200)
}

// Bun: 'the label flips to Re-review after a launch, and the row is running while its session
// is live'.
func TestBLabelFlipsAndRowRunsWhileItsSessionIsLive(t *testing.T) {
	t.Parallel()

	world, tab, _ := bPolled(t, nil, pr())
	bEqual(t, "label", bRow(t, tab).Label, "Self-review")
	bStart(t, tab, bRepo, 7, bSonnet)

	row := bRow(t, tab)
	bEqual(t, "after", []any{row.Label, row.Round, row.Status}, []any{"Re-review", 1.0, "idle"})
	bSessions(world, bLive("self-repo-7", "/wt/self-repo-7", "busy"))

	row = bRow(t, tab)
	bEqual(t, "live", []any{row.Status, row.CanStart}, []any{"running", false})
	reason, _ := bOpt(row.Reason).(string)
	bContains(t, "reason", reason, "running")
}

// Bun: 'a press that is a no-op under CONN_MYPRS_DRY still checks its input and never
// launches'.
func TestBDryPressChecksItsInputAndNeverLaunches(t *testing.T) {
	t.Parallel()

	world, tab, _ := bPolled(t, nil, pr())
	tab.SetDeps(func(deps *myprs.Deps) { deps.Config.Dry = true })
	bEqual(t, "gold", bStart(t, tab, bRepo, 7, "gold").Code, 400)

	reply := bStart(t, tab, bRepo, 7, bSonnet)
	bEqual(t, "dry", []any{reply.Code, bBody(reply)}, []any{200, `{"ok":true,"dry":true}`})
	tab.SetDeps(func(deps *myprs.Deps) { deps.Config.Dry = false })
	bEqual(t, "calls", bCounts(world), [2]int{0, 0})
	bEqual(t, "label", bRow(t, tab).Label, "Self-review")
}
