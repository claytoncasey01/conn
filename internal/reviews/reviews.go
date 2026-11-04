package reviews

// Reviews, read side: polls GitHub for the PRs the chosen review sources list, runs the Slack
// scans when a dashboard is open, and keeps what Conn itself remembers in reviews.json. It never
// reaches into the session list (the server hands it the live sessions), and Tick never waits:
// a slow or failing gh or scan only shows up as an error in the view, with the last good rows
// kept.
//
// The settings are read again on every tick and every poll: nothing is called while Conn is in
// setup or while the module a call needs is off, every PR is checked against the allowlist when
// it comes in and again when it is shown, and a repo that leaves the list keeps its records
// (hidden, never fetched, never pruned) until it is allowed again.
//
// Concurrency: one mutex stands for Bun's event loop. Every stretch of work between two calls
// to the outside (gh, claude, git, cmux, a screen read) runs holding it, and every call to the
// outside is made without it, so the state moves in the same steps Bun's does. Nothing that
// holds the mutex ever waits on the session list's goroutine: the live sessions are read from
// what its last tick published.

import (
	"context"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
)

// Timings and limits, in milliseconds where they are times.
const (
	// restartMs: a second REVIEW on the same PR inside this is a double click.
	restartMs = 30_000
	// appearMs: a launched session that has not shown up in the registry by now is not coming.
	appearMs = 5 * 60_000
	// gitClean bounds the git calls that clean a worktree up.
	gitClean = 30 * time.Second
	// lookAgainMs: after reading a session's screen, it is not read again for this long.
	lookAgainMs = 30_000
	// scanOverlapMs: a scan for PRs that have no post yet reads from this long before the older
	// of the cursor and their first-seen time.
	scanOverlapMs = 15 * 60_000
	// dmOverlapMs: the DM scan reads from this long before its cursor.
	dmOverlapMs = 15 * 60_000
	// refreshGiveUpMs: a REFRESH whose poll has not finished by now is reported as done anyway.
	refreshGiveUpMs = 150_000
	// requestedLimit is how many PRs the "Requests me" search reads per pattern.
	requestedLimit = 100
	// OpenLimit is how many PRs the "every open PR" search reads per pattern; more is a notice
	// that the list is cut off.
	OpenLimit = 200
	// FetchPerScan is the most PRs a scan named that are read from GitHub per scan.
	FetchPerScan = 30
	// unmatchedMax is the most PRs without a post named in one scan prompt.
	unmatchedMax = 40
	// cloneTTLMs is how long a clone lookup's answer is kept.
	cloneTTLMs = 10_000
	// signedOutDelayMaxMs is the longest a signed-out Slack waits before it is asked again.
	signedOutDelayMaxMs = 3_600_000
	// signedOutBackoff is how much longer a signed-out Slack waits each time it is still out.
	signedOutBackoff = 2
	// dayMs is a day.
	dayMs = 86_400_000
	// tickEvery is how often Run ticks.
	tickEvery = 600 * time.Millisecond
	// budgetName is what Reviews is called in the shared GitHub budget.
	budgetName = "reviews"
)

// What the Settings cards say.
const (
	rateDetail      = "Reviews is paused: GitHub's rate limit is used up"
	signedOutDetail = "Slack is signed out: sign in to the claude.ai Slack connector"
	moduleGitHub    = "github"
	moduleSlack     = "slack"
)

// The Slack states the view shows.
const (
	slackUnused    = "unused"
	slackOK        = "ok"
	slackNeedsAuth = "needs-auth"
	slackError     = "error"
)

// GitHub is where the poll reads from (Gh, or a fake).
type GitHub interface {
	Login(ctx context.Context) (string, error)
	Search(ctx context.Context, queries []Query) ([]SearchResult, *ghbudget.RateInfo, error)
	Fetch(ctx context.Context, prs []KeyParts, onRate func(*ghbudget.RateInfo)) (
		*js.Map[*PR], error)
}

// SettingsSource is the settings holder: read again on every tick.
type SettingsSource interface {
	Current() (settings.Settings, uint64)
	Setup() bool
}

// Config is Reviews' fixed tuning.
type Config struct {
	// PollMs is the base interval between polls.
	PollMs float64
	// FirstWindowDays is how far back a first scan reads.
	FirstWindowDays float64
	// DMScanMs and ChannelScanMs are how often at most the DM scan, and a channel scan for new
	// posts alone, run.
	DMScanMs      float64
	ChannelScanMs float64
	// CloseAfterMs and CloseCapMs are how long a finished review session sits idle before it is
	// closed, and the most it may.
	CloseAfterMs float64
	CloseCapMs   float64
	// Me is the reviewer's login when set (CONN_REVIEW_ME); "" asks GitHub.
	Me string
	// Dry shows fixed rows and calls nothing.
	Dry bool
	// NamePattern is the shape a review session's name has.
	NamePattern *regexp.Regexp
}

// Deps are what Reviews reads and calls.
type Deps struct {
	Gh GitHub
	// Scan and DMScan run the Slack scans.
	Scan   func(ctx context.Context, input ScanInput) (ScanResult, error)
	DMScan func(ctx context.Context, allow []string, sinceTS string) ([]DMFind, error)
	// File is where reviews.json lives; review worktrees go under reviews/ beside it.
	File     string
	Settings SettingsSource
	// CloneFor is where the local clone of a repo is ("" for none).
	CloneFor func(repo string) string
	// OriginOf is a folder's GitHub origin ("" for none).
	OriginOf func(folder string) string
	// SeenRepos are repos Conn has seen (live sessions, My PRs), for placing version 1 records.
	SeenRepos  func() []string
	HasClients func() bool
	// Sessions are the live sessions, read without waiting on the session list.
	Sessions func() []sessions.LiveSession
	Bump     func()
	Now      func() float64
	// Launch opens a review (the worktree, the prompt file and the cmux workspace).
	Launch func(ctx context.Context, opts LaunchOpts) LaunchResult
	// Git runs git without a shell, in dir, stopped after timeout.
	Git func(ctx context.Context, args []string, dir string, timeout time.Duration) (string, error)
	// InputState is what a live session's input box shows now ("empty", ...).
	InputState func(ctx context.Context, pid string) string
	// CloseWorkspace closes a session's whole cmux workspace: the reply's code and error.
	CloseWorkspace func(ctx context.Context, pid string) (int, string)
	// Exists is whether a path exists.
	Exists func(path string) bool
	// WorktreeClone is the clone a worktree was made from ("" for none).
	WorktreeClone func(worktree string) string
	// Record tells the Settings cards how a GitHub read or a Slack scan went.
	Record func(name string, ok bool, detail string)
	// Budget is what the GitHub pollers of this process share.
	Budget *ghbudget.Budget
	Logger *log.Logger
	Config Config
}

// failedPattern is a pattern GitHub could not search at the last poll, and why.
type failedPattern struct {
	Pattern string `json:"pattern"`
	Error   string `json:"error"`
}

type cloneHit struct {
	path string
	at   float64
}

// Reviews is the Reviews tab's state. Safe for use from several goroutines.
type Reviews struct {
	state

	deps  Deps
	names Names
	// ctx is the context New was given, for the calls the work makes: Tick has none to pass on.
	ctx  context.Context
	mu   sync.Mutex
	work sync.WaitGroup
}

// state is everything the mutex guards.
type state struct {
	file *File
	// writable is false when reviews.json could not be opened for writing.
	writable bool
	me       *string
	// prs are the open PRs of allowed repos a search or a read found, by key.
	prs *js.Map[*PR]
	// endedPrs are PRs whose own read said they merged or closed in the last 24 h.
	endedPrs *js.Map[*PR]
	// addedDuringPoll are keys a scan's reads added while a poll was running.
	addedDuringPoll *js.Map[bool]
	// reading are leads being read from GitHub now.
	reading map[string]bool
	capped  []string
	failed  []failedPattern
	rate    *ghbudget.RateInfo
	// storeError is why reviews.json could not be opened for writing.
	storeError  *string
	pausedUntil *float64
	// seenKey is the scan- and poll-relevant settings last seen.
	seenKey     *string
	bootRead    bool
	polling     bool
	scanning    bool
	dmScanning  bool
	dmForce     bool
	forcePoll   bool
	refreshSeq  int
	refreshDone int
	runSeq      int
	refreshAt   float64
	// lastPollStart is when the last poll started (nil before the first).
	lastPollStart  *float64
	lastPollAt     *float64
	lastScanAt     *float64
	nextScanAt     float64
	nextDiscovery  float64
	nextDMScanAt   float64
	signedOutDelay float64
	err            *string
	slack          string
	slackError     *string
	dmError        *string
	starting       map[string]bool
	closing        bool
	sweeping       bool
	sweepFailed    map[string]string
	finishing      map[string]bool
	idleSince      map[string]float64
	nextLook       map[string]float64
	seenPid        map[*Launch]float64
	clones         map[string]cloneHit
}

// New is Reviews over deps; nothing runs until Run (or Tick) is called.
func New(ctx context.Context, deps Deps) *Reviews {
	if deps.Budget == nil {
		deps.Budget = ghbudget.New()
	}

	return &Reviews{
		deps: deps, names: Names{Pattern: deps.Config.NamePattern},
		ctx: ctx, mu: sync.Mutex{}, work: sync.WaitGroup{},
		file: nil, writable: true, me: nil, prs: js.NewMap[*PR](),
		endedPrs: js.NewMap[*PR](), addedDuringPoll: js.NewMap[bool](),
		reading: map[string]bool{}, capped: []string{}, failed: []failedPattern{},
		rate: nil, storeError: nil, pausedUntil: nil, seenKey: nil, bootRead: false,
		polling: false, scanning: false, dmScanning: false, dmForce: false,
		forcePoll: false, refreshSeq: 0, refreshDone: 0, runSeq: 0, refreshAt: 0,
		lastPollStart: nil, lastPollAt: nil, lastScanAt: nil, nextScanAt: 0,
		nextDiscovery: 0, nextDMScanAt: 0, signedOutDelay: deps.Config.PollMs, err: nil,
		slack: slackUnused, slackError: nil, dmError: nil, starting: map[string]bool{},
		closing: false, sweeping: false, sweepFailed: map[string]string{},
		finishing: map[string]bool{}, idleSince: map[string]float64{},
		nextLook: map[string]float64{}, seenPid: map[*Launch]float64{},
		clones: map[string]cloneHit{},
	}
}

// Run ticks every 600 ms until ctx ends, then waits for the work in flight.
func (r *Reviews) Run(ctx context.Context) {
	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.work.Wait()

			return
		case <-ticker.C:
			r.safeTick()
		}
	}
}

// Tick is called every 600 ms. It starts at most one poll, one scan and one DM scan; it never
// waits.
func (r *Reviews) Tick() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.deps.Config.Dry {
		return
	}

	now := r.now()

	// A press that has waited this long is let go, so a poll that never answers cannot keep the
	// button disabled for good.
	if r.refreshSeq > r.refreshDone {
		if now-r.refreshAt >= refreshGiveUpMs {
			r.refreshDone = r.refreshSeq
			r.bump()
		}
	}

	current, version := r.settings()

	r.readAtBoot()
	r.startAutoClose()

	if r.seenKey == nil {
		key := scanKey(&current)
		r.seenKey = &key
	}

	if !r.githubOn(&current) {
		// In setup, or GitHub is off: nothing will poll, so nothing is waited for, and Reviews
		// takes no share of the budget.
		r.deps.Budget.Leave(budgetName)

		if r.refreshSeq > r.refreshDone {
			if !r.polling {
				r.refreshDone = r.refreshSeq
				r.bump()
			}
		}

		return
	}

	r.tickPoll(now, current, version)
	r.tickScans(now, &current)
}

// Refresh polls on the next tick. It answers this press's number: the snapshot's refresh.done
// reaches it once the poll (and any scan) it caused has finished.
func (r *Reviews) Refresh() (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.refreshSeq++
	seq := r.refreshSeq

	// The dry view touches no dependency, and has no poll to wait for.
	if r.deps.Config.Dry {
		r.refreshDone = seq

		return seq, nil
	}

	r.refreshAt = r.now()
	r.forcePoll = true
	r.signedOutDelay = r.deps.Config.PollMs
	r.nextScanAt = 0
	r.nextDiscovery = 0
	r.nextDMScanAt = 0
	current, _ := r.settings()

	err := r.reopen(&current)
	if err != nil {
		return 0, err
	}

	r.dmForce = r.dmsOn(&current)

	// A tab that cannot poll has nothing to wait for.
	if !r.githubOn(&current) {
		r.refreshDone = seq
	}

	r.bump()

	return seq, nil
}

// SettingsChanged makes the next tick poll at once and the scans due again, so a new allowlist,
// source or channel shows without a restart. The first allowlist (the end of setup) also
// migrates an old reviews.json now, with a list to place its records against.
func (r *Reviews) SettingsChanged() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.deps.Config.Dry {
		return
	}

	current, _ := r.settings()
	clear(r.clones)

	err := r.reopen(&current)
	if err != nil {
		r.logf("reviews: settings change failed %v", err)

		return
	}

	// Only a change that matters to what is read starts work: a save of clone folders alone
	// costs no poll and no metered scan.
	key := scanKey(&current)
	if r.seenKey != nil {
		if key == *r.seenKey {
			r.bump()

			return
		}
	}

	r.seenKey = &key
	r.forcePoll = true
	r.nextScanAt = 0
	r.nextDiscovery = 0
	r.nextDMScanAt = 0
	r.bump()
}

// safeTick is a tick that cannot take the server down: a failure is logged, as Bun's
// "reviews tick failed", and the next tick runs as usual.
func (r *Reviews) safeTick() {
	defer r.recovered("reviews tick failed")

	r.Tick()
}

// recovered logs a panic of the work it guards instead of letting it end the process.
func (r *Reviews) recovered(what string) {
	failure := recover()
	if failure != nil {
		r.logf("%s %v", what, failure)
	}
}

// spawn runs task on a goroutine of its own, holding the mutex; Wait waits for it.
func (r *Reviews) spawn(task func()) {
	r.work.Go(func() {
		defer r.recovered("reviews: background work failed")

		r.mu.Lock()
		defer r.mu.Unlock()

		task()
	})
}

// outside runs call without the mutex: a call to gh, claude, git, cmux or the session list.
// The caller holds the mutex, and holds it again when outside returns.
func (r *Reviews) outside(call func()) {
	r.mu.Unlock()
	defer r.mu.Lock()

	call()
}

func (r *Reviews) settings() (settings.Settings, uint64) {
	return r.deps.Settings.Current()
}

func (r *Reviews) now() float64 { return r.deps.Now() }

func (r *Reviews) logf(format string, args ...any) {
	if r.deps.Logger != nil {
		r.deps.Logger.Printf(format, args...)
	}
}

func (r *Reviews) record(name string, ok bool, detail string) {
	if r.deps.Record != nil {
		r.deps.Record(name, ok, detail)
	}
}

func (r *Reviews) bump() {
	if r.deps.Bump != nil {
		r.deps.Bump()
	}
}

func (r *Reviews) dataDir() string { return nodeDir(r.deps.File) }

func (r *Reviews) exists(path string) bool {
	if r.deps.Exists != nil {
		return r.deps.Exists(path)
	}

	return exists(path)
}

// githubOn is whether GitHub may be called: not in setup, and the module is on.
func (r *Reviews) githubOn(current *settings.Settings) bool {
	if r.deps.Settings.Setup() {
		return false
	}

	return current.GitHub.Enabled
}

// channelsOn is whether Slack may be called for the channel scan: GitHub too (a scan's finds are
// read from it), Slack on, at least one channel.
func (r *Reviews) channelsOn(current *settings.Settings) bool {
	if !r.githubOn(current) {
		return false
	}

	if !current.Slack.Enabled {
		return false
	}

	return len(current.Slack.Channels) > 0
}

func (r *Reviews) dmsOn(current *settings.Settings) bool {
	if !r.githubOn(current) {
		return false
	}

	if !current.Slack.Enabled {
		return false
	}

	return current.Slack.DMs
}

// cloneOf is the clone of repo, looked up at most every few seconds.
func (r *Reviews) cloneOf(repo string) string {
	key := strings.ToLower(repo)
	now := r.now()

	if hit, found := r.clones[key]; found {
		if now-hit.at < cloneTTLMs {
			return hit.path
		}
	}

	path := r.deps.CloneFor(key)
	r.clones[key] = cloneHit{path: path, at: now}

	return path
}

func (r *Reviews) noClone(repo string) *string {
	if r.cloneOf(repo) != "" {
		return nil
	}

	why := "No local clone of " + repo + ": set a folder in Settings"

	return &why
}

// scanKey is the settings a scan or a poll depends on: a save that changes none of them (a clone
// folder, say) starts nothing.
func scanKey(current *settings.Settings) string {
	return string(js.Marshal([]any{
		current.Repos.Allow, current.GitHub.Enabled, current.Slack.Enabled,
		current.Slack.Channels, current.Slack.DMs, current.Reviews.Requested,
		current.Reviews.AllOpen,
	}))
}

// SourcesOf are the review sources the settings turn on.
func SourcesOf(current *settings.Settings) Sources {
	sources := Sources{
		Requested: current.Reviews.Requested, Channels: nil, DMs: false,
		AllOpen: current.Reviews.AllOpen,
	}

	if !current.Slack.Enabled {
		return sources
	}

	if len(current.Slack.Channels) > 0 {
		sources.Channels = current.Slack.Channels
	}

	sources.DMs = current.Slack.DMs

	return sources
}

// SearchPatterns are the patterns that need a search of their own: an exact pattern an owner
// wildcard of the list already covers is searched by that.
func SearchPatterns(allow []string) []string {
	owners := map[string]bool{}

	for _, pattern := range allow {
		if strings.HasSuffix(pattern, "/*") {
			owners[pattern] = true
		}
	}

	out := []string{}

	for _, pattern := range allow {
		if strings.HasSuffix(pattern, "/*") {
			out = append(out, pattern)

			continue
		}

		owner := pattern[:max(0, strings.Index(pattern, "/"))]
		if !owners[owner+"/*"] {
			out = append(out, pattern)
		}
	}

	return out
}

// PollEvery is how long Reviews waits between polls when it is the only one spending the budget
// (see ghbudget.PollInterval).
func PollEvery(rate *ghbudget.RateInfo, now, base float64) float64 {
	return ghbudget.PollInterval(rate, now, base, 1)
}

// readAtBoot reads reviews.json once: launches Conn made are still closed when they finish,
// whatever the settings, and with GitHub off nothing else opens the file (a version 2 file only:
// never migrated, copied or replaced).
func (r *Reviews) readAtBoot() {
	if r.bootRead {
		return
	}

	if r.file != nil {
		return
	}

	if r.deps.Settings.Setup() {
		return
	}

	r.bootRead = true

	read := ReadFile(r.deps.File)
	if read.Kind == ReadV2 {
		r.file = read.V2
	}
}

func (r *Reviews) startAutoClose() {
	if r.closing {
		return
	}

	if r.file == nil {
		return
	}

	if !r.anyOpenLaunch() {
		return
	}

	r.closing = true
	r.autoClose(r.entries(), 0, 0, true)
}

func (r *Reviews) anyOpenLaunch() bool {
	for _, key := range r.file.PRs.Keys() {
		entry, _ := r.file.PRs.Get(key)
		for _, launch := range entry.Launches {
			if launch.ClosedAt == nil {
				return true
			}
		}
	}

	return false
}

// tickPoll starts a poll when one is due and GitHub's rate limit allows it.
func (r *Reviews) tickPoll(now float64, current settings.Settings, version uint64) {
	r.updatePause(now)

	// GitHub's rate limit is used up: no poll and no read until it resets, and a press has
	// nothing to wait for (Slack scans go on; what they find waits).
	if r.pausedUntil != nil {
		if r.refreshSeq > r.refreshDone {
			if !r.polling {
				r.forcePoll = false
				r.refreshDone = r.refreshSeq
				r.bump()
			}
		}

		return
	}

	if r.polling {
		return
	}

	if !r.pollDue(now) {
		return
	}

	r.forcePoll = false
	r.runSeq = r.refreshSeq
	started := now
	r.lastPollStart = &started
	r.polling = true
	r.startPoll(current, version)
}

// updatePause ends a pause whose reset has come, and takes on the shared budget's.
func (r *Reviews) updatePause(now float64) {
	if r.pausedUntil != nil {
		if now >= *r.pausedUntil {
			r.pausedUntil = nil
			r.forcePoll = true
			r.bump()
		}
	}

	// A rate limit that My PRs ran into is GitHub's for both: Reviews waits for the same reset.
	if shared := r.deps.Budget.PausedUntil(now); shared != nil {
		if r.pausedUntil == nil {
			r.pausedUntil = shared
			r.bump()
		} else if *shared > *r.pausedUntil {
			r.pausedUntil = shared
			r.bump()
		}
	}
}

func (r *Reviews) pollDue(now float64) bool {
	if r.forcePoll {
		return true
	}

	if r.lastPollStart == nil {
		return true
	}

	every := r.deps.Budget.Every(budgetName, r.rate, now, r.deps.Config.PollMs)

	return now-*r.lastPollStart >= every
}

// tickScans starts a channel scan and a DM scan when they are due.
func (r *Reviews) tickScans(now float64, current *settings.Settings) {
	r.tickChannelScan(now, current)

	if !r.dmScanning {
		if r.dmDue(now, current) {
			r.dmScanning = true
			r.dmForce = false
			r.nextDMScanAt = now + r.deps.Config.DMScanMs
			r.startDMScan(now, *current)

			return
		}
	}

	r.dropDMPress()
}

// tickChannelScan starts a channel scan when one is due, the channels are on and a dashboard
// is open.
func (r *Reviews) tickChannelScan(now float64, current *settings.Settings) {
	if r.scanning {
		return
	}

	if now >= r.nextScanAt {
		if !r.channelsOn(current) {
			return
		}

		if r.deps.HasClients() {
			r.startScan(now, current)
		}
	}
}

// dropDMPress lets a press go that asked for a DM scan that cannot run now (no dashboard,
// switched off): nothing is left to wait for.
func (r *Reviews) dropDMPress() {
	if !r.dmForce {
		return
	}

	if r.dmScanning {
		return
	}

	if r.polling {
		return
	}

	if r.forcePoll {
		return
	}

	r.dmForce = false
	done := r.refreshDone

	r.settle()

	if r.refreshDone != done {
		r.bump()
	}
}

func (r *Reviews) startScan(now float64, current *settings.Settings) {
	plan := r.plan(now, *current)
	if plan == nil {
		return
	}

	r.scanning = true
	input := r.scanInput(plan)

	r.spawn(func() { r.scanOnce(plan, input) })
}

// settle says the work a press asked for is over when nothing is running or waiting to start:
// the press the poll was started for, and every one before it, are done.
func (r *Reviews) settle() {
	if r.polling {
		return
	}

	if r.scanning {
		return
	}

	if r.dmScanning {
		return
	}

	if r.forcePoll {
		return
	}

	if r.dmForce {
		current, _ := r.settings()
		if r.dmReady(&current) {
			return
		}
	}

	r.refreshDone = max(r.refreshDone, r.runSeq)
}

func (r *Reviews) save() {
	if r.file == nil {
		return
	}

	if !r.writable {
		return
	}

	// The file can be rebuilt from GitHub and Slack: a failed write must not stop the view.
	err := SaveFile(r.deps.File, r.file, nil)
	if err != nil {
		r.logf("reviews: could not save %s", message(nodeError(err)))
	}
}

// reopen opens reviews.json when it is not open yet, or again when it could not be opened for
// writing. Never in setup: a version 1 file needs an allowlist to be placed.
func (r *Reviews) reopen(current *settings.Settings) error {
	if r.deps.Settings.Setup() {
		return nil
	}

	if r.file != nil {
		if r.writable {
			return nil
		}
	}

	file, err := r.open(current)
	if err != nil {
		return err
	}

	r.file = file

	return nil
}

func (r *Reviews) notSaving() string {
	why := "it could not be opened"
	if r.storeError != nil {
		why = *r.storeError
	}

	return "reviews.json cannot be saved, so this would be lost at the next restart: " + why
}

// open is reviews.json, migrated first when it is version 1 (only ever with an allowlist to
// place its records against).
func (r *Reviews) open(current *settings.Settings) (*File, error) {
	seen := r.deps.SeenRepos()
	allow := current.Repos.Allow

	if allow == nil {
		allow = []string{}
	}

	opened, err := OpenFile(r.deps.File, MigrateContext{
		Allow: allow, Seen: seen, OriginOf: r.deps.OriginOf, Exists: r.deps.Exists,
	})
	if err != nil {
		return nil, err
	}

	// The launches are new values: what was remembered about the old ones goes with them.
	clear(r.seenPid)

	r.writable = opened.Writable
	r.storeError = nil

	switch {
	case !opened.Writable:
		why := opened.Error
		r.storeError = &why
		r.logf("reviews: %s", opened.Error)
	case opened.Migrated != nil:
		r.logf("reviews: reviews.json moved to version 2 (%d records; the old file is %s)",
			*opened.Migrated, opened.KeptAs)
	case opened.KeptAs != "":
		r.logf("reviews: reviews.json could not be used; it was kept as %s and a new one is "+
			"started", opened.KeptAs)
	}

	return opened.File, nil
}

// message is an error's text, cut as Bun cuts it.
func message(err error) string { return js.Slice(err.Error(), failureUnits) }
