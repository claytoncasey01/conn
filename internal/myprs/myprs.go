package myprs

// My PRs, read side: polls GitHub for the signed-in user's open PRs while a dashboard is open and
// keeps what Conn itself remembers in myprs.json. It never reaches into the session list (the
// server hands it the live sessions), and Tick never waits: a slow or failing gh only shows up as
// an error in the view, with the last good rows kept.
//
// Concurrency: one mutex works as a single event loop, as in Reviews. Every stretch of work between
// two calls to the outside (gh, git, cmux, claude) runs holding it, and every call to the outside
// is made without it, so the state moves in whole steps. Nothing that holds the mutex ever waits on
// the session list's goroutine: the live sessions are read from what its last tick published.

import (
	"context"
	"log"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/sessions"
)

// Timings, in milliseconds.
const (
	// refreshGiveUpMs: a REFRESH whose poll has not finished by now is reported as done anyway
	// (the page's own limit is longer).
	refreshGiveUpMs = 150_000
	// seenSaveMs: a listed PR's seenAt is written to the file when the saved one is this old.
	seenSaveMs = 86_400_000
	// restartMs: a second press within this is refused.
	restartMs = 30_000
	// appearMs: a launch whose session has not shown up within this is not coming.
	appearMs = 5 * 60_000
	// threadGapMs: a second click for the same thread within this is refused.
	threadGapMs = 5000
	// gitClean bounds the git calls that clean a worktree up.
	gitClean = 30 * time.Second
	// tickEvery is how often Run ticks.
	tickEvery = 600 * time.Millisecond
	// patternsPerPoll is how many searched patterns one poll interval pays for.
	patternsPerPoll = 2
	// sessionIdle is a live session's status while it waits at its prompt.
	sessionIdle = "idle"
	// budgetName is what My PRs is called in the shared GitHub budget.
	budgetName = "myPrs"
	// messageUnitsMax is where an error's text is cut.
	messageUnitsMax = 300
	// warningUnitsMax is where the list's warning is cut.
	warningUnitsMax = 400
)

// Why nothing is read.
const (
	offSetup  = "setup"
	offGitHub = "github"
)

// What the Settings cards are told.
const (
	moduleGitHub = "github"
	moduleSlack  = "slack"
)

// Scope is what the settings say, answered fresh on every call (never kept: a change must reach a
// poll in flight, a press and a snapshot alike). Setup is true while there is no allowlist; Allow
// is the patterns (empty then); DraftChannel is where the ready-for-review draft goes (the
// setting, else the first Slack channel, else nil: no draft is made).
type Scope struct {
	Setup        bool
	Allow        []string
	GitHub       bool
	Slack        bool
	DraftChannel *string
}

// offOf is why nothing is read: no allowlist (setup, or an empty one), or GitHub is off; "" when
// My PRs may call GitHub.
func offOf(scope Scope) string {
	if scope.Setup {
		return offSetup
	}

	if len(scope.Allow) == 0 {
		return offSetup
	}

	if scope.GitHub {
		return ""
	}

	return offGitHub
}

func offWhy(off string) string {
	if off == offSetup {
		return "No repositories allowed. Add one in Settings."
	}

	return "GitHub is off. Turn it on in Settings."
}

// fingerprint is what a settings change means to My PRs: only these decide what is read, written
// or drafted. A save of another section leaves it as it was, so it forces no poll (a poll is one
// search per pattern).
func fingerprint(scope Scope) string {
	allow := []string{}

	for _, pattern := range scope.Allow {
		lower := strings.ToLower(pattern)
		if !slices.Contains(allow, lower) {
			allow = append(allow, lower)
		}
	}

	slices.SortFunc(allow, js.CompareUTF16)

	var draft *string
	if scope.Slack {
		draft = scope.DraftChannel
	}

	return string(js.Marshal([]any{offOf(scope), allow, scope.Slack, draft}))
}

// PollEvery is the poll interval for n searched patterns: one search costs about 105 GraphQL
// points, so the cadence stretches to keep the spend near that of one or two patterns.
func PollEvery(patterns int, pollMs float64) float64 {
	return pollMs * math.Max(1, math.Ceil(float64(patterns)/patternsPerPoll))
}

// repoOfKey is the repo of a record key `owner/repo#number`.
func repoOfKey(key string) string { return key[:max(0, strings.LastIndex(key, "#"))] }

// noClone is why a round cannot start from a repo with no clone.
func noClone(repo string) string {
	return "No local clone of " + repo + ": set a folder in Settings"
}

// GitHub is what the poll and a thread click read from GitHub (Gh, or a fake).
type GitHub interface {
	Login(ctx context.Context) (string, error)
	PRs(ctx context.Context, me string, patterns []string) (Search, error)
	ThreadInfo(ctx context.Context, threadID string) (*ThreadInfo, error)
}

// GitHubWrites are the only GitHub writes My PRs makes (Writes, or a fake).
type GitHubWrites interface {
	TriggerReview(ctx context.Context, repo string, number float64) error
	ReplyToThread(ctx context.Context, threadID, body string) error
	ResolveThread(ctx context.Context, threadID string) error
}

// Config is My PRs' fixed tuning.
type Config struct {
	// PollMs is the base interval between polls.
	PollMs float64
	// Me is the login when set (CONN_REVIEW_ME); "" asks GitHub.
	Me string
	// Dry runs no poll and writes nothing.
	Dry bool
	// FindingBots are the bots whose open threads are findings.
	FindingBots []string
	// GreptileBot is the login whose review of the head makes a PR ready.
	GreptileBot string
	// GreptileTrigger is the comment that asks Greptile for a review.
	GreptileTrigger string
	// ProtectedHeads are branches a round refuses besides the default branch.
	ProtectedHeads []string
	// Self names self-review sessions.
	Self Self
}

// Deps are what My PRs reads and calls.
type Deps struct {
	Gh GitHub
	// Scope is the settings now.
	Scope func() Scope
	// File is where myprs.json lives.
	File       string
	HasClients func() bool
	Now        func() float64
	Bump       func()
	// CloneFor is the local clone of a repo per the settings (its origin is that repo), ""
	// when there is none.
	CloneFor func(repo string) string
	// Sessions are the live sessions, read without waiting on the session list.
	Sessions func() []sessions.LiveSession
	// Settled is when this launch of Conn finished resuming the sessions of its last quit (ms),
	// and whether it has. Until then, and for appearMs after, a round whose session is not live
	// is not judged gone: its session may be one the launch resumes.
	Settled func() (float64, bool)
	// Recorded is whether the last quit's record named a session in worktree: one this launch
	// resumes, which a press must not open a second round beside.
	Recorded func(worktree string) bool
	// RepoMatches is whether the clone at path has repo as its origin; an error when git
	// cannot say.
	RepoMatches func(ctx context.Context, repo, path string) (bool, error)
	// Launch opens a self-review round (the worktree, the prompt file and the session);
	// the data folder is the wiring's.
	Launch func(ctx context.Context, opts LaunchOpts) LaunchResult
	// Git runs git without a shell, in dir, stopped after timeout.
	Git func(ctx context.Context, args []string, dir string, timeout time.Duration) (string,
		error)
	// DataDir is the data folder: a worktree is only ever swept from under <DataDir>/myprs.
	DataDir string
	Exists  func(path string) bool
	// Writes are the only GitHub writes of the poll and the clicks.
	Writes GitHubWrites
	// RepliesAt is the mtime (ms) of <worktree>/.conn/replies.json, nil when it is not there.
	RepliesAt func(worktree string) *float64
	// ReadFile is the text of a regular file of at most RepliesFileBytesMax plus one byte;
	// false when it is not there or cannot be read.
	ReadFile func(path string) (string, bool)
	// Draft saves one Slack draft in channel with exactly text; it never sends.
	Draft func(ctx context.Context, channel, body string) DraftResult
	// Record tells the Settings cards how a GitHub or Slack call went.
	Record func(name string, ok bool, detail string)
	// Budget is what the GitHub pollers of this process share.
	Budget *ghbudget.Budget
	Logger *log.Logger
	Config Config
}

type askFailure struct {
	sha, text string
}

// MyPrs is the My PRs tab's state. Safe for use from several goroutines.
type MyPrs struct {
	state

	deps Deps
	// ctx is the context New was given, for the calls the work makes: Tick has none to pass on.
	ctx  context.Context
	mu   sync.Mutex
	work sync.WaitGroup
}

// state is everything the mutex guards.
type state struct {
	file *File
	me   *string
	// prs are the last good list, by `owner/repo#number`.
	prs *js.Map[*PR]
	// staleKeys are keys of prs carried over from an earlier poll because the search that would
	// refresh them failed.
	staleKeys map[string]bool
	// savedSeen is, by key, the seenAt the file on disk holds.
	savedSeen      map[string]float64
	capped         bool
	err            *string
	failedPatterns []SearchFailure
	rate           *ghbudget.RateInfo
	pausedUntil    *float64
	fp             string
	loaded         bool
	polling        bool
	forcePoll      bool
	lastPollStart  *float64
	refreshSeq     int
	refreshDone    int
	runSeq         int
	refreshAt      float64
	// starting are PRs whose launch is in flight.
	starting map[string]bool
	// seen are launches whose session has been seen live (memory only).
	seen map[*Launch]bool
	// swept are launches whose worktree has been swept: never looked at again (memory only).
	swept map[*Launch]bool
	// sweepQueue are the entries whose PR is off the list that the next sweep looks at.
	sweepQueue *js.Map[*Entry]
	sweeping   bool
	// sweepFailed is, by worktree, the last error its removal failed with.
	sweepFailed map[string]string
	// idleSince is since when a launch's live session has been idle (memory only).
	idleSince map[*Launch]float64
	// lastLive is when a launch's session was last seen live (memory only).
	lastLive map[*Launch]float64
	// lastAsk is, by PR, when the Greptile ask was last tried, a failed try included.
	lastAsk map[string]float64
	// askError is, by PR, why the last ask failed, for the head it failed on.
	askError map[string]askFailure
	// drafting are PRs whose Slack draft is being saved.
	drafting map[string]bool
	// ignored is, by PR, how many entries of the newest round's replies file were not taken.
	ignored map[string]int
	// threadBusy are threads (`key:threadId`) whose click is being written.
	threadBusy map[string]bool
	// threadAt is, by thread, when it was last clicked.
	threadAt map[string]float64
}

// New is My PRs over deps; nothing runs until Run (or Tick) is called.
func New(ctx context.Context, deps Deps) *MyPrs {
	if deps.Budget == nil {
		deps.Budget = ghbudget.New()
	}

	tab := &MyPrs{deps: deps, ctx: ctx, mu: sync.Mutex{}, work: sync.WaitGroup{}}
	tab.state = state{
		file: nil, me: nil, prs: js.NewMap[*PR](), staleKeys: map[string]bool{},
		savedSeen: map[string]float64{}, capped: false, err: nil,
		failedPatterns: []SearchFailure{}, rate: nil, pausedUntil: nil,
		fp: fingerprint(deps.Scope()), loaded: false, polling: false, forcePoll: false,
		lastPollStart: nil, refreshSeq: 0, refreshDone: 0, runSeq: 0, refreshAt: 0,
		starting: map[string]bool{}, seen: map[*Launch]bool{}, swept: map[*Launch]bool{},
		sweepQueue: js.NewMap[*Entry](), sweeping: false, sweepFailed: map[string]string{},
		idleSince: map[*Launch]float64{}, lastLive: map[*Launch]float64{},
		lastAsk: map[string]float64{}, askError: map[string]askFailure{},
		drafting: map[string]bool{}, ignored: map[string]int{}, threadBusy: map[string]bool{},
		threadAt: map[string]float64{},
	}

	return tab
}

// Run ticks every 600 ms until ctx ends, then waits for the work in flight.
func (m *MyPrs) Run(ctx context.Context) {
	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.work.Wait()

			return
		case <-ticker.C:
			m.safeTick()
		}
	}
}

// Settle waits for the work in flight (a poll, a sweep, a draft).
func (m *MyPrs) Settle() { m.work.Wait() }

func seenOf(file *File) map[string]float64 {
	out := map[string]float64{}

	for _, key := range file.PRs.Keys() {
		entry, _ := file.PRs.Get(key)
		out[key] = entry.SeenAt
	}

	return out
}

// message is an error's text, cleaned and cut as the view shows it.
func message(err error) string { return Clean(err.Error(), messageUnitsMax) }

// Refresh polls on the next tick. It answers this press's number: the snapshot's refresh.done
// reaches it once the poll it caused has finished.
func (m *MyPrs) Refresh() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.refreshSeq++
	seq := m.refreshSeq

	// Nothing polls, so there is nothing to wait for.
	if m.dry() {
		m.refreshDone = seq

		return seq
	}

	// Nor while no repository is allowed or GitHub is off: no call is made, and a press must
	// not leave the button waiting.
	if offOf(m.scope()) != "" {
		m.refreshDone = seq
		m.bump()

		return seq
	}

	m.refreshAt = m.now()
	m.forcePoll = true
	m.bump()

	return seq
}

// Tick is called every 600 ms. It starts at most one poll, only while a dashboard is open; it
// never waits.
func (m *MyPrs) Tick() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.dry() {
		return
	}

	m.markSeen(m.deps.Sessions())

	// No allowlist, or GitHub off: no poll, no gh call, nothing listed.
	if offOf(m.scope()) != "" {
		m.idle()

		return
	}

	current := m.scope()
	now := m.now()

	m.updatePause(now)

	// A press that has waited this long is let go, so a poll that never answers cannot keep
	// the button disabled for good.
	if m.refreshSeq > m.refreshDone {
		if now-m.refreshAt >= refreshGiveUpMs {
			m.refreshDone = m.refreshSeq
			m.bump()
		}
	}

	if !m.deps.HasClients() {
		m.noDashboard()

		return
	}

	// GitHub's rate limit is used up: no poll until it resets, and a press has nothing to wait
	// for.
	if m.pausedUntil != nil {
		m.letPressGo()

		return
	}

	if m.pollDue(current, now) {
		m.forcePoll = false
		m.runSeq = m.refreshSeq
		started := now
		m.lastPollStart = &started
		m.polling = true

		m.spawn(m.poll)
	}
}

// SettingsChanged takes a settings save: a PR of a repo that is no longer allowed leaves the
// list now (nothing is deleted: its record, worktrees and proposals stay in the file, and show
// again when the repo is allowed), and a poll runs on the next tick so a repo that was added
// shows without waiting for the interval.
func (m *MyPrs) SettingsChanged() {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer m.recovered("myprs settings change failed")

	current := m.scope()
	mark := fingerprint(current)

	// Another section saved: nothing My PRs reads has changed.
	if mark == m.fp {
		return
	}

	m.fp = mark

	if offOf(current) != "" {
		m.idle()

		return
	}

	kept := js.NewMap[*PR]()

	for _, key := range m.prs.Keys() {
		pull, _ := m.prs.Get(key)
		if repos.Allowed(pull.Repo, current.Allow) {
			kept.Set(key, pull)
		}
	}

	m.prs = kept
	m.forcePoll = true
	m.bump()
}

// safeTick is a tick that cannot take the server down: a failure is logged as "myprs tick
// failed", and the next tick runs as usual.
func (m *MyPrs) safeTick() {
	defer m.recovered("myprs tick failed")

	m.Tick()
}

func (m *MyPrs) recovered(what string) {
	failure := recover()
	if failure != nil {
		m.logf("%s %v", what, failure)
	}
}

// spawn runs task on a goroutine of its own, holding the mutex; Settle waits for it.
func (m *MyPrs) spawn(task func()) {
	m.work.Go(func() {
		defer m.recovered("myprs: background work failed")

		m.mu.Lock()
		defer m.mu.Unlock()

		task()
	})
}

// outside runs call without the mutex: a call to gh, git, cmux or claude. The caller holds the
// mutex, and holds it again when outside returns.
func (m *MyPrs) outside(call func()) {
	m.mu.Unlock()
	defer m.mu.Lock()

	call()
}

func (m *MyPrs) scope() Scope { return m.deps.Scope() }

func (m *MyPrs) now() float64 { return m.deps.Now() }

func (m *MyPrs) dry() bool { return m.deps.Config.Dry }

func (m *MyPrs) pollMs() float64 { return m.deps.Config.PollMs }

func (m *MyPrs) logf(format string, args ...any) {
	if m.deps.Logger != nil {
		m.deps.Logger.Printf(format, args...)
	}
}

func (m *MyPrs) record(name string, ok bool, detail string) {
	if m.deps.Record != nil {
		m.deps.Record(name, ok, detail)
	}
}

func (m *MyPrs) bump() {
	// A failing listener must not stop the work that told it: it is logged as "myprs bump failed".
	defer m.recovered("myprs bump failed")

	if m.deps.Bump != nil {
		m.deps.Bump()
	}
}

func (m *MyPrs) save() {
	if m.file == nil {
		return
	}

	// The file can be rebuilt from GitHub: a failed write must not stop the view.
	err := SaveFile(m.deps.File, m.file, nil)
	if err != nil {
		m.logf("myprs: could not save %s", message(err))

		return
	}

	m.savedSeen = seenOf(m.file)
}

// settle says a press is over once no poll is running or waiting to start: the press the poll
// was started for, and every one before it, are done.
func (m *MyPrs) settle() {
	if m.polling {
		return
	}

	if m.forcePoll {
		return
	}

	m.refreshDone = max(m.refreshDone, m.runSeq)
}

// updatePause ends a pause whose reset has come, and takes on the shared budget's: a rate limit
// that Reviews ran into is GitHub's for both.
func (m *MyPrs) updatePause(now float64) {
	if m.pausedUntil != nil {
		if now >= *m.pausedUntil {
			m.pausedUntil = nil
			m.forcePoll = true
			m.bump()
		}
	}

	shared := m.deps.Budget.PausedUntil(now)
	if shared == nil {
		return
	}

	if m.pausedUntil == nil {
		m.pausedUntil = shared
		m.bump()
	} else if *shared > *m.pausedUntil {
		m.pausedUntil = shared
		m.bump()
	}
}

// noDashboard: nothing is asked of GitHub, so My PRs takes no share of the budget, and a press
// with no dashboard to show it has nothing to wait for.
func (m *MyPrs) noDashboard() {
	m.deps.Budget.Leave(budgetName)

	if m.refreshSeq <= m.refreshDone {
		return
	}

	if m.polling {
		return
	}

	m.forcePoll = false
	m.refreshDone = m.refreshSeq
	m.bump()
}

func (m *MyPrs) letPressGo() {
	if m.refreshSeq <= m.refreshDone {
		return
	}

	if m.polling {
		return
	}

	m.refreshDone = m.refreshSeq
	m.bump()
}

func (m *MyPrs) pollDue(current Scope, now float64) bool {
	if m.polling {
		return false
	}

	if m.forcePoll {
		return true
	}

	if m.lastPollStart == nil {
		return true
	}

	return now-*m.lastPollStart >= m.every(len(SearchPatterns(current.Allow)), now)
}

// every is how long to wait between polls: the pattern count's interval, and, once a poll has
// measured what it cost, no less than what that cost allows while sharing the budget with
// Reviews. The measured figure only ever lengthens it.
func (m *MyPrs) every(patterns int, now float64) float64 {
	byCount := PollEvery(patterns, m.pollMs())
	if m.rate == nil {
		return byCount
	}

	return math.Max(byCount, m.deps.Budget.Every(budgetName, m.rate, now, m.pollMs()))
}

// idle: nothing is read; the list is empty and a press is settled. Quiet when there is nothing
// to change (the ticker calls it every 600 ms).
func (m *MyPrs) idle() {
	changed := slices.Contains([]bool{
		m.prs.Len() > 0, m.loaded, m.err != nil, m.capped, m.forcePoll,
		m.refreshSeq > m.refreshDone, len(m.failedPatterns) > 0, m.pausedUntil != nil,
	}, true)

	m.failedPatterns = []SearchFailure{}
	m.pausedUntil = nil
	m.rate = nil
	m.deps.Budget.Leave(budgetName)
	m.staleKeys = map[string]bool{}
	m.prs = js.NewMap[*PR]()
	m.loaded = false
	m.err = nil
	m.capped = false
	m.forcePoll = false
	m.refreshDone = max(m.refreshDone, m.refreshSeq)

	if changed {
		m.bump()
	}
}

// permitted is whether GitHub may be read and written for repo right now: an allowlist is
// saved, GitHub is on, and repo is on the list. Asked fresh each time.
func (m *MyPrs) permitted(repo string) bool {
	current := m.scope()
	if offOf(current) != "" {
		return false
	}

	return repos.Allowed(repo, current.Allow)
}

// gate is why a press for repo is refused before anything is read or written (no allowlist,
// GitHub off, repo not on the list); "" when it may go on.
func (m *MyPrs) gate(repo string) string {
	current := m.scope()
	if off := offOf(current); off != "" {
		return offWhy(off)
	}

	if repos.Allowed(repo, current.Allow) {
		return ""
	}

	return repo + " is not an allowed repository"
}
