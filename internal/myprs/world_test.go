package myprs_test

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/sessions"
)

// The My PRs poller's test world, the port of myPrs.test.ts's make(): every dependency is a fake
// (gh, the clock, the clones, a temp state file), so nothing here reaches the real gh or ~/.conn.
// Every field is guarded by the world's mutex: MyPrs calls the fakes from its own goroutines.

// The clock and the fixture times of Bun's tests.
const (
	worldT0 = 1_800_000_000_000
	hourMs  = 3_600_000
	// pollMs is config.reviewPollMs.
	pollMs = 180_000
	// greptileBot is config.greptileBot, and findingBot config.findingBots[0] (Bun's BOT).
	greptileBot = "greptile-apps"
	findingBot  = "greptile-apps"
)

var errWorld = errors.New("world")

// GitCall is one git call the poll made.
type GitCall struct {
	Args    []string
	Dir     string
	Timeout time.Duration
}

// Ask is one Greptile ask.
type Ask struct {
	Repo   string
	Number float64
}

// DraftCall is one Slack draft.
type DraftCall struct {
	Channel, Text string
}

// World is the fakes and what they saw.
type World struct {
	mu sync.Mutex
	t  *testing.T

	Now   float64
	Scope myprs.Scope
	// Source answers the poll's search; Listed is what the last one answered.
	Source  func() (myprs.Search, error)
	Listed  []*myprs.PR
	Logins  int
	Calls   int
	Authors []string
	// Patterns are the patterns of each search.
	Patterns  [][]string
	Clients   bool
	Bumps     int
	Clones    map[string]string
	Launched  []myprs.LaunchOpts
	Sessions  []sessions.LiveSession
	Matched   []string
	OriginOK  bool
	OriginErr error
	// LaunchResult is what launch answers; nil is an ok at /wt/<name> at the PR's listed head.
	LaunchResult func(opts myprs.LaunchOpts) myprs.LaunchResult
	GitCalls     []GitCall
	Present      map[string]bool
	ExistsCalls  []string
	GitAnswer    func(args []string, dir string) (string, error)
	Asks         []Ask
	AskErr       error
	Replies      map[string]float64
	RepliesCalls []string
	Files        map[string]string
	ReadCalls    []string
	Drafts       []DraftCall
	DraftResult  func() myprs.DraftResult
	Records      []string
	// ThreadInfo, Replied and Resolved are for the thread clicks: the poll never reads a thread.
	ThreadInfo  func(id string) (*myprs.ThreadInfo, error)
	ReplyErr    error
	ResolveErr  error
	ReplyCalls  []DraftCall
	ResolveIDs  []string
	ThreadReads []string
}

// newWorld is a world listing prs, with the clone of org/repo at /clone.
func newWorld(t *testing.T, prs ...*myprs.PR) *World {
	t.Helper()

	channel := "#code-review"
	world := &World{
		t: t, Now: worldT0,
		Scope: myprs.Scope{
			Setup: false, Allow: []string{"org/*"}, GitHub: true, Slack: true,
			DraftChannel: &channel,
		},
		Clients: true, Clones: map[string]string{"org/repo": "/clone"}, OriginOK: true,
		Present: map[string]bool{}, Replies: map[string]float64{}, Files: map[string]string{},
	}
	world.SetList(prs...)

	return world
}

// SetList makes the search answer prs, not capped.
func (w *World) SetList(prs ...*myprs.PR) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.Source = func() (myprs.Search, error) {
		return myprs.Search{PRs: prs, Capped: false, Failed: nil, Rate: nil}, nil
	}
}

// Change runs change holding the world's mutex.
func (w *World) Change(change func(w *World)) {
	w.mu.Lock()
	defer w.mu.Unlock()

	change(w)
}

// Advance moves the clock on.
func (w *World) Advance(ms float64) { w.Change(func(w *World) { w.Now += ms }) }

// Deps are the fakes as My PRs' dependencies, with myprs.json at file.
func (w *World) Deps(file string) myprs.Deps {
	return myprs.Deps{
		Gh: worldGh{w}, Scope: w.scope, File: file, HasClients: w.hasClients,
		Now: w.now, Bump: w.bump, CloneFor: w.cloneFor, Sessions: w.sessions,
		RepoMatches: w.repoMatches, Launch: w.launch, Git: w.git, DataDir: "/data",
		Exists: w.exists, Writes: worldWrites{w}, RepliesAt: w.repliesAt, ReadFile: w.readFile,
		Draft: w.draft, Record: w.record, Budget: nil, Logger: nil,
		Config: myprs.Config{
			PollMs: pollMs, Me: "", Dry: false,
			FindingBots:     defaults().FindingBots,
			GreptileBot:     defaults().GreptileBot,
			GreptileTrigger: defaults().GreptileTrigger, ProtectedHeads: defaults().ProtectedHeads,
			Self: selfOf(),
		},
	}
}

// New is My PRs over the world, with a fresh myprs.json in a temp folder.
func (w *World) New() (*myprs.MyPrs, string) {
	file := filepath.Join(w.t.TempDir(), "myprs.json")

	return myprs.New(context.Background(), w.Deps(file)), file
}

func (w *World) scope() myprs.Scope {
	w.mu.Lock()
	defer w.mu.Unlock()

	copied := w.Scope
	copied.Allow = append([]string{}, w.Scope.Allow...)

	return copied
}

func (w *World) hasClients() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.Clients
}

func (w *World) now() float64 {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.Now
}

func (w *World) bump() { w.Change(func(w *World) { w.Bumps++ }) }

func (w *World) cloneFor(repo string) string {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.Clones[repo]
}

func (w *World) sessions() []sessions.LiveSession {
	w.mu.Lock()
	defer w.mu.Unlock()

	return append([]sessions.LiveSession{}, w.Sessions...)
}

func (w *World) repoMatches(_ context.Context, repo, path string) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.Matched = append(w.Matched, repo+" "+path)

	return w.OriginOK, w.OriginErr
}

func (w *World) launch(_ context.Context, opts myprs.LaunchOpts) myprs.LaunchResult {
	w.mu.Lock()
	w.Launched = append(w.Launched, opts)
	answer := w.LaunchResult
	head := "abc"

	for _, pull := range w.Listed {
		if pull.Repo == opts.Repo {
			if pull.Number == opts.Number {
				head = pull.HeadSha
			}
		}
	}
	w.mu.Unlock()

	if answer != nil {
		return answer(opts)
	}

	return myprs.LaunchResult{
		OK: true, Worktree: "/wt/" + opts.Name, HeadSha: head, Error: "", ManualCommand: "",
	}
}

func (w *World) git(_ context.Context, args []string, dir string, timeout time.Duration) (
	string, error,
) {
	w.mu.Lock()
	w.GitCalls = append(w.GitCalls, GitCall{Args: args, Dir: dir, Timeout: timeout})
	answer := w.GitAnswer
	w.mu.Unlock()

	if answer == nil {
		return "", nil
	}

	return answer(args, dir)
}

func (w *World) exists(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.ExistsCalls = append(w.ExistsCalls, path)

	return w.Present[path]
}

func (w *World) repliesAt(worktree string) *float64 {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.RepliesCalls = append(w.RepliesCalls, worktree)

	at, found := w.Replies[worktree]
	if !found {
		return nil
	}

	return &at
}

func (w *World) readFile(path string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.ReadCalls = append(w.ReadCalls, path)
	content, found := w.Files[path]

	return content, found
}

func (w *World) draft(_ context.Context, channel, body string) myprs.DraftResult {
	w.mu.Lock()
	w.Drafts = append(w.Drafts, DraftCall{Channel: channel, Text: body})
	answer := w.DraftResult
	w.mu.Unlock()

	if answer == nil {
		return myprs.DraftResult{OK: true, Error: "", Final: false}
	}

	return answer()
}

func (w *World) record(name string, ok bool, detail string) {
	w.Change(func(w *World) {
		w.Records = append(w.Records, name+" "+strconv.FormatBool(ok)+" "+detail)
	})
}

// worldGh is the fake GitHub source.
type worldGh struct{ w *World }

func (g worldGh) Login(context.Context) (string, error) {
	g.w.Change(func(w *World) { w.Logins++ })

	return "jane", nil
}

func (g worldGh) PRs(_ context.Context, me string, patterns []string) (myprs.Search, error) {
	g.w.mu.Lock()
	g.w.Calls++
	g.w.Authors = append(g.w.Authors, me)
	g.w.Patterns = append(g.w.Patterns, append([]string{}, patterns...))
	source := g.w.Source
	g.w.mu.Unlock()

	found, err := source()
	if err == nil {
		g.w.Change(func(w *World) { w.Listed = found.PRs })
	}

	return found, err
}

func (g worldGh) ThreadInfo(_ context.Context, threadID string) (*myprs.ThreadInfo, error) {
	g.w.mu.Lock()
	g.w.ThreadReads = append(g.w.ThreadReads, threadID)
	answer := g.w.ThreadInfo
	g.w.mu.Unlock()

	if answer == nil {
		g.w.t.Error("the poll never reads a thread")

		return nil, errWorld
	}

	return answer(threadID)
}

// worldWrites are the fake GitHub writes: the asks, and the thread clicks' writes.
type worldWrites struct{ w *World }

func (x worldWrites) TriggerReview(_ context.Context, repo string, number float64) error {
	x.w.mu.Lock()
	defer x.w.mu.Unlock()

	x.w.Asks = append(x.w.Asks, Ask{Repo: repo, Number: number})

	return x.w.AskErr
}

func (x worldWrites) ReplyToThread(_ context.Context, threadID, body string) error {
	x.w.mu.Lock()
	defer x.w.mu.Unlock()

	x.w.ReplyCalls = append(x.w.ReplyCalls, DraftCall{Channel: threadID, Text: body})

	return x.w.ReplyErr
}

func (x worldWrites) ResolveThread(_ context.Context, threadID string) error {
	x.w.mu.Lock()
	defer x.w.mu.Unlock()

	x.w.ResolveIDs = append(x.w.ResolveIDs, threadID)

	return x.w.ResolveErr
}

// ---- fixtures, as Bun's pr() and thread() ----

// pr is Bun's pr(): org/repo#7, created 5 h before T0, head abc on feature/a; change edits it.
func pr(change ...func(p *myprs.PR)) *myprs.PR {
	branch := "main"
	pull := &myprs.PR{
		Repo: "org/repo", Number: 7, Title: "Add a thing",
		URL: "https://github.com/org/repo/pull/7", Draft: false, CreatedAt: worldT0 - 5*hourMs,
		UpdatedAt: worldT0 - hourMs, LastCommentAt: worldT0 - 2*hourMs, HeadRef: "feature/a",
		HeadSha: "abc", HeadDate: worldT0 - 3*hourMs, BaseRef: "main", DefaultBranch: &branch,
		Fork: false, Threads: []myprs.Thread{}, Reviews: []myprs.Review{},
		Comments: []myprs.Comment{}, Partial: false,
	}

	for _, one := range change {
		one(pull)
	}

	return pull
}

// thread is Bun's thread(): id t by sam on a.ts:1, an hour before T0; change edits it.
func thread(change ...func(t *myprs.Thread)) myprs.Thread {
	path := "a.ts"
	line := 1.0
	out := myprs.Thread{
		ID: "t", Resolved: false, Outdated: false, By: "sam", Path: &path, Line: &line,
		At: worldT0 - hourMs, Text: "",
	}

	for _, one := range change {
		one(&out)
	}

	return out
}

// tick ticks once and waits for the work it started (Bun's tick then flush).
func tick(m *myprs.MyPrs) {
	m.Tick()
	m.Settle()
}
