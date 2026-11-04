package myprs_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// The port of myPrs.test.ts's round() and replying(): a listed PR with recorded launches, the
// newest round 2 at T0 - H from head old, armed, whose worktree is cCloneWorktree.

const (
	cMinuteMs = 60_000
	// cCloneWorktree is Bun's AWT, the worktree of every launch round() records.
	cCloneWorktree = "/data/myprs/org-repo-7/self-repo-7"
	// cRepliesPath is Bun's RFILE, that worktree's replies file.
	cRepliesPath = cCloneWorktree + "/.conn/replies.json"
	cRepo        = "org/repo"
	cNumber      = 7.0
	cWaitMax     = 5 * time.Second
	cWaitStep    = time.Millisecond
)

// cRoundOpts are round()'s options: edits of the PR, of the newest launch and of each extra
// (older) launch, the session live for the newest launch ("" none), its replies file's mtime,
// and an edit of the dependencies before My PRs is made.
type cRoundOpts struct {
	pull    []func(pull *myprs.PR)
	launch  []func(launch *myprs.Launch)
	extra   []func(launch *myprs.Launch)
	session string
	replies *float64
	deps    func(deps *myprs.Deps)
}

// cRound is a round(): the world, My PRs over it, its file and the PR it lists now.
type cRound struct {
	t     *testing.T
	world *World
	myPrs *myprs.MyPrs
	file  string
	cur   *myprs.PR
}

// cAt is value as a *float64 (an untyped constant would make new give an *int).
func cAt(value float64) *float64 { return new(value) }

func cSession(name, cwd, status string) sessions.LiveSession {
	return sessions.LiveSession{Name: name, Cwd: cwd, Status: status, SessionID: "s1", PID: 1}
}

// cHead is a PR edit setting the head sha.
func cHead(sha string) func(pull *myprs.PR) {
	return func(pull *myprs.PR) { pull.HeadSha = sha }
}

// cReviewed is a PR edit: Greptile reviewed sha a minute after T0.
func cReviewed(sha string) func(pull *myprs.PR) {
	return func(pull *myprs.PR) {
		pull.Reviews = []myprs.Review{{By: greptileBot, Sha: sha, At: worldT0 + cMinuteMs}}
	}
}

// cBotThread is a PR edit: one open thread by the finding bot.
func cBotThread(pull *myprs.PR) {
	pull.Threads = []myprs.Thread{thread(func(one *myprs.Thread) { one.By = findingBot })}
}

// cAskedNew is a launch edit: disarmed, it asked for head new at T0.
func cAskedNew(launch *myprs.Launch) {
	launch.Armed = false
	launch.AskedSha = new("new")
	launch.AskedAt = cAt(worldT0)
}

func cNeverAsked(launch *myprs.Launch) {
	launch.AskedSha = nil
	launch.AskedAt = nil
}

func cDisarmed(launch *myprs.Launch) { launch.Armed = false }

func cManual(launch *myprs.Launch) { launch.Manual = true }

func cLaunchHead(sha string) func(launch *myprs.Launch) {
	return func(launch *myprs.Launch) { launch.HeadSha = sha }
}

func cLaunchOf(at float64, name string, round float64) *myprs.Launch {
	return &myprs.Launch{
		At: at, Model: "sonnet", Name: name, Worktree: cCloneWorktree, HeadSha: "old",
		Round: round, Armed: true, AskedSha: nil, AskedAt: nil, ClosedAt: nil, AskTry: nil,
		Manual: false,
	}
}

// cSeed writes the file round() starts from.
func cSeed(t *testing.T, path string, opts cRoundOpts) {
	t.Helper()

	seeded := myprs.LoadFile(path)
	entry := myprs.TrackOf(seeded, myprs.EntryKey(cRepo, cNumber), worldT0-hourMs)

	for _, edit := range opts.extra {
		launch := cLaunchOf(worldT0-3*hourMs, "self-repo-7", 1)
		edit(launch)
		entry.Launches = append(entry.Launches, launch)
	}

	launch := cLaunchOf(worldT0-hourMs, "self-repo-7-2", 2)

	for _, edit := range opts.launch {
		edit(launch)
	}

	entry.Launches = append(entry.Launches, launch)

	cSave(t, path, seeded)
}

func cSave(t *testing.T, path string, file *myprs.File) {
	t.Helper()

	err := myprs.SaveFile(path, file, nil)
	if err != nil {
		t.Fatalf("saving %s: %v", path, err)
	}
}

// cNewRound is round(o): nothing is polled yet.
func cNewRound(t *testing.T, opts cRoundOpts) *cRound {
	t.Helper()

	file := filepath.Join(t.TempDir(), "myprs.json")
	cSeed(t, file, opts)

	edits := append([]func(pull *myprs.PR){cHead("new")}, opts.pull...)
	cur := pr(edits...)
	world := newWorld(t, cur)

	if opts.session != "" {
		world.Sessions = append(world.Sessions,
			cSession("self-repo-7-2", cCloneWorktree, opts.session))
	}

	if opts.replies != nil {
		world.Replies[cCloneWorktree] = *opts.replies
	}

	deps := world.Deps(file)
	if opts.deps != nil {
		opts.deps(&deps)
	}

	return &cRound{
		t: t, world: world, myPrs: myprs.New(context.Background(), deps), file: file, cur: cur,
	}
}

// cReadyRound is readyRound(o): the head new was asked for at T0 and Greptile reviewed it.
func cReadyRound(t *testing.T, opts cRoundOpts) *cRound {
	t.Helper()

	opts.launch = append([]func(launch *myprs.Launch){cAskedNew}, opts.launch...)
	opts.pull = append([]func(pull *myprs.PR){cReviewed("new")}, opts.pull...)

	return cNewRound(t, opts)
}

// poll moves the clock on and runs a poll, waiting for all it started.
func (r *cRound) poll(advanceMs float64) {
	r.world.Advance(advanceMs)
	r.myPrs.Refresh()
	tick(r.myPrs)
}

// setPR makes the list answer pull from now on.
func (r *cRound) setPR(pull *myprs.PR) {
	r.cur = pull
	r.world.SetList(pull)
}

func (r *cRound) now() float64 {
	var now float64

	r.world.Change(func(w *World) { now = w.Now })

	return now
}

func (r *cRound) setSessions(live ...sessions.LiveSession) {
	r.world.Change(func(w *World) { w.Sessions = live })
}

func (r *cRound) setDraft(result myprs.DraftResult) { cSetDraft(r.world, result) }

// cSetDraft makes every Slack draft of world answer result.
func cSetDraft(world *World, result myprs.DraftResult) {
	world.Change(func(w *World) {
		w.DraftResult = func() myprs.DraftResult { return result }
	})
}

func (r *cRound) asks() []Ask {
	var asks []Ask

	r.world.Change(func(w *World) { asks = append([]Ask{}, w.Asks...) })

	return asks
}

func (r *cRound) drafts() []DraftCall { return cDrafts(r.world) }

// cDrafts are the Slack drafts world was asked for.
func cDrafts(world *World) []DraftCall {
	var drafts []DraftCall

	world.Change(func(w *World) { drafts = append([]DraftCall{}, w.Drafts...) })

	return drafts
}

func (r *cRound) readCalls() []string {
	var calls []string

	r.world.Change(func(w *World) { calls = append([]string{}, w.ReadCalls...) })

	return calls
}

// entry is the saved record of org/repo#7.
func (r *cRound) entry() *myprs.Entry { return cEntryOf(r.t, r.file) }

func cEntryOf(t *testing.T, path string) *myprs.Entry {
	t.Helper()

	entry, found := myprs.LoadFile(path).PRs.Get(myprs.EntryKey(cRepo, cNumber))
	if !found {
		t.Fatalf("%s holds no entry for org/repo#7", path)
	}

	return entry
}

func (r *cRound) saved() []*myprs.Launch { return r.entry().Launches }

func (r *cRound) savedDraft() *myprs.SlackDraft { return r.entry().Draft }

func (r *cRound) proposals() []*myprs.Proposal { return r.entry().Proposals }

// editFile changes the saved file (My PRs must have settled).
func (r *cRound) editFile(edit func(entry *myprs.Entry)) {
	file := myprs.LoadFile(r.file)
	entry, _ := file.PRs.Get(myprs.EntryKey(cRepo, cNumber))
	edit(entry)
	cSave(r.t, r.file, file)
}

func (r *cRound) row() *myprs.Row {
	rows := r.myPrs.Snapshot().Rows
	if len(rows) == 0 {
		r.t.Fatal("the snapshot has no row")
	}

	return rows[0]
}

func (r *cRound) status() string { return r.row().Status }

func (r *cRound) start() *js.Object {
	return r.myPrs.Start(context.Background(), cRepo, cNumber, "sonnet").Body
}

// cRestart is a new poller (a new world listing prs) on path, as Bun's make(..., r.file).
func cRestart(t *testing.T, path string, prs ...*myprs.PR) (*World, *myprs.MyPrs) {
	t.Helper()

	world := newWorld(t, prs...)

	return world, myprs.New(context.Background(), world.Deps(path))
}

// cSame fails when got and want differ, showing both as JSON.
func cSame(t *testing.T, what string, got, want any) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %s, want %s", what, js.Marshal(got), js.Marshal(want))
	}
}

func cGone(t *testing.T, path string) {
	t.Helper()

	err := os.Remove(path)
	if err != nil {
		t.Fatalf("removing %s: %v", path, err)
	}
}

func cExists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

// cWaitFor waits, bounded, until done is true.
func cWaitFor(t *testing.T, what string, done func() bool) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(cWaitMax))
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}

		time.Sleep(cWaitStep)
	}
}

// ---- replying() ----

// cReplyEntry is one entry of a replies file.
type cReplyEntry struct {
	ThreadID string `json:"threadId"`
	Text     string `json:"text"`
}

// cBody is Bun's body(): a replies file listing entries.
func cBody(entries ...cReplyEntry) string {
	data, err := json.Marshal(struct {
		Threads []cReplyEntry `json:"threads"`
	}{Threads: entries})
	if err != nil {
		panic("marshalling a replies file: " + err.Error())
	}

	return string(data)
}

// cProp is Bun's prop(): a stored proposal.
func cProp(threadID, text, state string, launchAt float64) *myprs.Proposal {
	return &myprs.Proposal{
		ThreadID: threadID, Text: text, State: state, Error: nil, LaunchAt: launchAt,
		TriedAt: nil,
	}
}

// cProposed is cProp of the newest launch of round(), proposed.
func cProposed(threadID, text string) *myprs.Proposal {
	return cProp(threadID, text, "proposed", worldT0-hourMs)
}

// cThreads is a PR edit: open threads with these ids.
func cThreads(ids ...string) func(pull *myprs.PR) {
	return func(pull *myprs.PR) {
		pull.Threads = []myprs.Thread{}
		for _, id := range ids {
			pull.Threads = append(pull.Threads, thread(func(one *myprs.Thread) { one.ID = id }))
		}
	}
}

// cReplyOpts are replying()'s options. file nil is the default body (t1 fixed); noFile is
// Bun's file: null; at nil is T0.
type cReplyOpts struct {
	threads   []string
	file      *string
	noFile    bool
	at        *float64
	proposals []*myprs.Proposal
	pull      []func(pull *myprs.PR)
	launch    []func(launch *myprs.Launch)
	extra     []func(launch *myprs.Launch)
}

// cReplying is replying(o).
func cReplying(t *testing.T, opts cReplyOpts) *cRound {
	t.Helper()

	ids := opts.threads
	if ids == nil {
		ids = []string{"t1", "t2"}
	}

	round := cNewRound(t, cRoundOpts{
		pull:   append([]func(pull *myprs.PR){cThreads(ids...)}, opts.pull...),
		launch: opts.launch, extra: opts.extra, session: "", replies: nil, deps: nil,
	})

	if opts.proposals != nil {
		round.editFile(func(entry *myprs.Entry) { entry.Proposals = opts.proposals })
	}

	if !opts.noFile {
		body := cBody(cReplyEntry{ThreadID: "t1", Text: "Fixed in the last commit."})
		if opts.file != nil {
			body = *opts.file
		}

		round.world.Change(func(w *World) { w.Files[cRepliesPath] = body })
	}

	at := worldT0 + 0.0
	if opts.at != nil {
		at = *opts.at
	}

	round.world.Change(func(w *World) { w.Replies[cCloneWorktree] = at })

	return round
}

// cOrderedWrites are GitHub writes that record replies and resolves in one ordered list.
type cOrderedWrites struct {
	mu     *sync.Mutex
	writes *[]string
}

func (x cOrderedWrites) TriggerReview(context.Context, string, float64) error { return nil }

func (x cOrderedWrites) ReplyToThread(_ context.Context, threadID, body string) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	*x.writes = append(*x.writes, "reply "+threadID+" "+body)

	return nil
}

func (x cOrderedWrites) ResolveThread(_ context.Context, threadID string) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	*x.writes = append(*x.writes, "resolve "+threadID)

	return nil
}

// threadSide is Bun's threadSide(r): the fresh read answers an open thread of PR 7, and the
// writes are recorded in order; read them with the answer.
func (r *cRound) threadSide() func() []string {
	guard := &sync.Mutex{}
	writes := &[]string{}

	r.world.Change(func(w *World) {
		w.ThreadInfo = func(string) (*myprs.ThreadInfo, error) {
			return &myprs.ThreadInfo{
				Resolved: false, Number: cNumber, State: "OPEN", Author: "jane", Repo: cRepo,
				Last: nil, Recent: []myprs.RecentComment{},
			}, nil
		}
	})
	r.myPrs.SetDeps(func(deps *myprs.Deps) {
		deps.Writes = cOrderedWrites{mu: guard, writes: writes}
	})

	return func() []string {
		guard.Lock()
		defer guard.Unlock()

		return append([]string{}, *writes...)
	}
}
