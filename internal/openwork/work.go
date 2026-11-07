// Package openwork is Open work: the open sprint's Jira tickets, and the runs started from them.
// A run makes a worktree beside the clone of each repository it was started in, moves the ticket
// into progress, has four read-only agents research it (Jira, Confluence, Slack and the code),
// asks the person what the sources could not settle, opens a build session where the settings
// say (inside Conn, a terminal app, or a command to paste), and on OPEN PR pushes the branch and
// opens the pull request, then follows it until it merges. A run may build in several
// repositories (legs), each in a worktree of its own, with one session that reaches them all or
// one session per leg.
//
// Concurrency: one mutex guards the state. Every stretch of work between two calls to the outside
// (git, gh, claude, cmux, the session list's reply) runs holding it, and every call to the
// outside is made without it, so the state moves in whole steps. Work started without waiting
// for it (a run's pipeline after START) has its part up to the first call to the outside run at
// once, on the lock its caller holds, and the rest on a goroutine of its own (launch): what a
// request starts has begun before its answer goes out. Nothing that holds the mutex ever waits on
// the session list's goroutine: the live sessions are read from what its last tick published.
package openwork

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	// tickEvery is how often Run ticks.
	tickEvery = 600 * time.Millisecond
	// noClone is why a run cannot start when there is no clone and no reason was given.
	noClone = "there is no clone to build in"
	// noRepo is why a run from before Settings cannot build when it names no repository and
	// Settings names no default one.
	noRepo = "This run does not say which repository it builds in: choose a Default " +
		"repository in Settings, or start the ticket again."
)

// The modules the Settings cards are told about.
const (
	moduleAtlassian = "atlassian"
	moduleSlack     = "slack"
)

// Found is a live session that belongs to a run: by its name or its folder.
type Found struct {
	// ID is the session's id on the Sessions page: its pid, as text, or a hosted session's id.
	ID     string
	PID    float64
	Status string
	// SessionID is Claude's own id for the session.
	SessionID string
}

// Reply is an answer to the page: its HTTP code and its JSON body.
type Reply struct {
	Code int
	Body *js.Object
}

// Hooks are what a sprint read tells as it goes.
type Hooks struct {
	OnProgress func(line string)
	OnAuth     func(needing []string)
}

// Deps are what Work reads and calls. The defaults are the wiring's.
type Deps struct {
	// DataDir holds the runs (runs/) and the cached sprint list (work-cache.json).
	DataDir string
	// Dry is CONN_WORK_DRY: fake tickets and the whole pipeline, nothing outside this process.
	Dry bool
	// Settings are the settings now.
	Settings func() settings.Settings
	// CloneFor is the local clone of a repository per the settings.
	CloneFor CloneFinder
	// SeenRepos are repositories found on this machine (clone folders, live sessions, My PRs
	// rows), offered when the allowlist covers them; nil finds none. Called without the mutex.
	SeenRepos func() []string
	// OriginOf is the GitHub repository of a folder ("" for none).
	OriginOf func(folder string) string
	Sh       Sh
	// ShInput runs a command as Sh does, with input on its standard input.
	ShInput ShInput
	// Helper starts a research agent's or the synthesis's helper run.
	Helper jira.Run
	// FetchSprint reads the sprint; Transition moves a ticket.
	FetchSprint func(ctx context.Context, hooks Hooks) (jira.Sprint, error)
	Transition  func(ctx context.Context, key string, move jira.Move) (jira.Moved, error)
	// Open starts a session where the settings say sessions open.
	Open launch.Opener
	// Claude is the claude program as configured (CONN_CLAUDE): the build session's program.
	Claude string
	// HostedRunning says whether a session Conn hosts, by the id its host issued, still runs:
	// the dry run reuses its running session rather than opening another.
	HostedRunning func(hostedID string) bool
	// Record tells the Settings cards how a Jira, Confluence or Slack run went: one short line
	// of Conn's own, never text from those services.
	Record func(name string, ok bool, detail string)
	// FindSession is the live session named name or in cwd, nil when there is none; a name of ""
	// finds by cwd alone. It must not wait on the session list.
	FindSession func(name, cwd string) *Found
	// Reply types text into a live session.
	Reply func(ctx context.Context, sessionID, text string) Reply
	// Bump tells the session list the snapshot changed.
	Bump func()
	// Now is the time in ms.
	Now func() float64
	// Sleep waits, for the dry run's steps; it ends early when ctx does.
	Sleep  func(ctx context.Context, wait time.Duration)
	Logger *log.Logger
}

// handle stops one helper run.
type handle struct{ cancel context.CancelFunc }

// Work is Open work's state. Safe for use from several goroutines.
type Work struct {
	state

	deps Deps
	// ctx is the context New was given, for the calls work started without a request makes.
	ctx      context.Context
	mu       sync.Mutex
	work     sync.WaitGroup
	runsDir  string
	listFile string
}

// state is everything the mutex guards.
type state struct {
	list ticketList
	// listFor are the projects the list was read for, as Settings names them (nil: none); a
	// list for other projects is dropped, so a change in Settings never shows an old
	// project's tickets.
	listFor    *string
	runs       *js.Map[*Record]
	handles    map[string]map[*handle]bool
	listHandle *handle
	// lastGit is when each leg's git was last counted, and busyPR the legs with a change in
	// progress, both by legKey.
	lastGit map[string]float64
	busyPR  map[string]bool
	// running are runs whose pipeline is still going, even if it was interrupted: a second one
	// on the same run would race the first.
	running map[string]bool
	// lastPoll, polling and pollError are the PR polls, by legKey: when each leg's PR was last
	// asked, which are in flight, and the last error logged per leg (a repeating failure is
	// reported once).
	lastPoll  map[string]float64
	polling   map[string]bool
	pollError map[string]string
	// resumedAt is when a session was last opened in a slot, by slotKey: it takes a few seconds
	// to appear.
	resumedAt map[string]float64
	// reviewMoving are the runs whose move to review is out, by run key: one move at a time.
	reviewMoving map[string]bool
	// dryPolls and dryFixed are each leg's dry PR walk, by legKey: its polls so far, and that FIX
	// CHECKS was pressed for it.
	dryPolls map[string]int
	dryFixed map[string]bool
	// dryHosted is the hosted id of the dry session each slot last opened, by slotKey, or
	// dryOpening while one is being opened.
	dryHosted map[string]string
}

// New is Work over deps: it makes the runs folder and reads the runs and the cached list.
// Nothing runs until Run (or Tick) is called.
func New(ctx context.Context, deps Deps) *Work {
	work := &Work{
		deps: deps, ctx: ctx, mu: sync.Mutex{}, work: sync.WaitGroup{},
		runsDir:  filepath.Join(deps.DataDir, "runs"),
		listFile: filepath.Join(deps.DataDir, "work-cache.json"),
	}
	work.state = state{
		list: emptyList(), listFor: nil, runs: js.NewMap[*Record](),
		handles: map[string]map[*handle]bool{}, listHandle: nil, lastGit: map[string]float64{},
		busyPR: map[string]bool{}, running: map[string]bool{}, lastPoll: map[string]float64{},
		polling: map[string]bool{}, pollError: map[string]string{},
		resumedAt: map[string]float64{}, reviewMoving: map[string]bool{},
		dryPolls: map[string]int{}, dryFixed: map[string]bool{},
		dryHosted: map[string]string{},
	}

	_ = os.MkdirAll(work.runsDir, folderMode)
	work.readCache()

	for _, record := range loadRecords(work.runsDir) {
		work.runs.Set(record.Key, record)
	}

	return work
}

// Run ticks every 600 ms until ctx ends, then waits for the work in flight.
func (w *Work) Run(ctx context.Context) {
	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.work.Wait()

			return
		case <-ticker.C:
			w.safeTick()
		}
	}
}

// Settle waits for the work in flight.
func (w *Work) Settle() { w.work.Wait() }

// SettingsChanged takes a settings save. The list belongs to the projects in Settings; turned
// off, nothing Atlassian is running (a stage that was researching is interrupted, and Retry
// starts it again once Atlassian is on); a researching run whose repository left the list, or
// that Settings no longer builds in, stops too: no synthesis run starts for it.
func (w *Work) SettingsChanged() {
	w.mu.Lock()
	defer w.mu.Unlock()
	defer w.recovered("open work settings change failed")

	w.sync()

	if !w.on() {
		w.offNow()
	} else if !w.deps.Dry {
		for _, key := range w.runs.Keys() {
			record, _ := w.runs.Get(key)
			if !researching(record) {
				continue
			}

			if problem := w.runProblem(record); problem != "" {
				w.interrupt(record, problem+" The research stopped; Retry starts it again once "+
					"that is fixed.")
			}
		}
	}

	w.bump()
}

// safeTick is a tick that cannot take the server down: a failure is logged ("tick failed")
// and the next tick runs as usual.
func (w *Work) safeTick() {
	defer w.recovered("tick failed")

	w.Tick()
}

func (w *Work) recovered(what string) {
	failure := recover()
	if failure != nil {
		w.logf("%s %v", what, failure)
	}
}

func (w *Work) logf(format string, args ...any) {
	if w.deps.Logger != nil {
		w.deps.Logger.Printf(format, args...)
	}
}

// task is one piece of work that goes outside between steps: it holds the mutex between its
// calls to the outside.
type task struct {
	work *Work
	// borrowed is that the task runs on the lock its caller holds, which waits on handed until
	// the task first goes outside or ends.
	borrowed bool
	handed   chan struct{}
}

// launch runs body as work started without waiting for it: its part up to the first
// call to the outside runs now, on the lock the caller holds, and the rest on a goroutine of its
// own. The caller holds the mutex, and still holds it when launch returns.
func (w *Work) launch(body func(step *task)) {
	running := &task{work: w, borrowed: true, handed: make(chan struct{})}

	w.work.Go(func() {
		defer running.finish()
		defer w.recovered("open work: background work failed")

		body(running)
	})

	<-running.handed
}

// held is a task for a request that already holds the mutex.
func (w *Work) held() *task { return &task{work: w, borrowed: false, handed: nil} }

// outside runs call without the mutex. The task holds it again when outside returns.
func (t *task) outside(call func()) {
	if t.borrowed {
		t.borrowed = false
		close(t.handed)
	} else {
		t.work.mu.Unlock()
	}

	defer t.work.mu.Lock()

	call()
}

// finish gives the lock back: to the caller still waiting on it, or to the mutex.
func (t *task) finish() {
	if t.borrowed {
		t.borrowed = false
		close(t.handed)

		return
	}

	t.work.mu.Unlock()
}

func (w *Work) settings() settings.Settings { return w.deps.Settings() }

func (w *Work) now() float64 { return w.deps.Now() }

func (w *Work) on() bool { return jira.On(w.settings()) }

func (w *Work) moduleEnabled(name string) bool {
	current := w.settings()
	if len(current.Repos.Allow) == 0 {
		return false
	}

	return current.Switch(name)
}

func (w *Work) bump() {
	defer w.recovered("open work bump failed")

	if w.deps.Bump != nil {
		w.deps.Bump()
	}
}

func (w *Work) record(name string, ok bool, detail string) {
	if w.deps.Record != nil {
		w.deps.Record(name, ok, detail)
	}
}

// targetsNow are the places a run may build now, and any repository named in chosen that the
// list does not offer but the allowlist covers (one found nowhere conn looks can still be chosen
// by name). It reads the disk (clones, default branches) and the session list, so it is called
// before the mutex is taken; the settings are read without it.
func (w *Work) targetsNow(chosen []string) []Target {
	current := w.settings()
	all := BuildTargets(current, w.seen(), w.deps.CloneFor, DefaultBranch)

	for _, name := range chosen[:min(len(chosen), legsMax)] {
		if slices.ContainsFunc(all, func(each Target) bool {
			return js.Lower(each.Repo) == js.Lower(name)
		}) {
			continue
		}

		if _, allowed := patternOf(name, current.Repos.Allow); allowed {
			all = append(all, targetFor(name, current, w.deps.CloneFor, DefaultBranch))
		}
	}

	return all
}

// chooseTargets are the targets a START chose among all (none: the default repository), in the
// order chosen, and every problem with the choice.
func (w *Work) chooseTargets(chosen []string, all []Target) ([]Target, []string) {
	return ChooseTargets(all, chosen, w.settings().Atlassian.Jira.BuildRepo)
}

// seen are the repositories found on this machine. It reads the disk and the session list, so
// it is called before the mutex is taken (by targetsNow).
func (w *Work) seen() []string {
	if w.deps.SeenRepos == nil {
		return nil
	}

	return w.deps.SeenRepos()
}

func (w *Work) allowed(repo string) bool { return repos.Allowed(repo, w.settings().Repos.Allow) }

func (w *Work) offNow() {
	if w.listHandle != nil {
		w.listHandle.cancel()
		w.listHandle = nil
		w.list.Fetching = false
		w.list.FetchStartedAt = nil
		w.list.Step = nil
	}

	for _, key := range w.runs.Keys() {
		record, _ := w.runs.Get(key)
		if researching(record) {
			w.interrupt(record, "Atlassian was turned off in Settings while this stage was "+
				"running. Retry starts it again once it is on.")
		}
	}
}

func researching(record *Record) bool {
	switch record.State {
	case stateStarting, stateResearching:
		return true
	}

	return false
}

func (w *Work) interrupt(record *Record, why string) {
	record.State = stateInterrupted
	record.Error = &why

	w.kill(record.Key)

	for _, agent := range record.Agents {
		stopAgent(agent)
	}

	w.touch(record)
}

// kill stops every helper run of a run.
func (w *Work) kill(key string) {
	for running := range w.handles[key] {
		running.cancel()
	}
}

// track starts a helper run's context: cancelled when the run is interrupted or cancelled.
// Done must be called once the call has ended.
func (w *Work) track(key string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(w.ctx)
	running := &handle{cancel: cancel}

	set := w.handles[key]
	if set == nil {
		set = map[*handle]bool{}
		w.handles[key] = set
	}

	set[running] = true

	return ctx, func() {
		delete(set, running)
		cancel()
	}
}
