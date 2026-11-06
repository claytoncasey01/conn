package sessions

import (
	"context"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/claytoncasey01/conn/internal/ask"
	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/run"
	"github.com/claytoncasey01/conn/internal/snapshot"
)

const (
	// sessionIDShown is how much of a session id names a session that has no name.
	sessionIDShown = 8
	maxBMP         = 0xffff

	procsEveryMs    = 3000
	registryEveryMs = 2000
	// prTTLMs is how long a PR lookup is kept before it is made again.
	prTTLMs = 5 * 60_000
	// cacheMs is how long a snapshot is reused while nothing changed.
	cacheMs = 5000
	// agentToolCalls is how many of an agent's last tool calls its page shows.
	agentToolCalls = 400

	waitingDefault = "input needed"
	nul            = "\x00"
)

// prHead is a head gh would read as a PR number, not a branch.
var prHead = regexp.MustCompile(`^#?[0-9]+$`)

// noPRBranches are the branches no session's PR is looked up for.
func noPRBranches() []string { return []string{"main", "master", "development", "staging"} }

// shells are the process names counted as a session's shells.
func shells() []string { return []string{"zsh", "bash", "sh", "fish", "dash"} }

// PR is a session's pull request.
type PR = snapshot.PR

// Deps are what the store reads the machine with, and what the rest of the server supplies.
// Every function is called on the loop's goroutine and must be safe to call from it.
type Deps struct {
	// Home is the home folder: the registry is in ~/.claude/sessions and the transcripts in
	// ~/.claude/projects.
	Home string
	// DataDir is conn's data folder, where its review and self-review worktrees are.
	DataDir string
	Kinds   KindRules
	// Procs is `ps -A -o pid=,ppid=,comm=`.
	Procs func() (string, error)
	// Env is a process's environment, entry by entry (see Env).
	Env func(pid float64) ([]string, error)
	// PRView runs `gh pr view` in a folder and gives its stdout.
	PRView func(ctx context.Context, cwd string, args []string) ([]byte, error)
	// RepoOf is the GitHub repo of a folder that exists, "" for none.
	RepoOf func(folder string) string
	// Upstream is the branch on origin a local branch tracks, "" for none.
	Upstream func(cwd, branch string) string
	// Cmux runs cmux with args (10 s at most).
	Cmux func(ctx context.Context, args []string) run.Result
	// Stat is `ps -o stat= -p <pid>` (5 s at most).
	Stat func(ctx context.Context, pid float64) run.Result
	// Kill sends SIGTERM to a process.
	Kill func(pid float64) error
	// Archive keeps sessions in History; nil when History is off.
	Archive Archive
	Hooks   Hooks
	Now     func() time.Time
	// Host runs the sessions Conn hosts on terminals of its own; nil when there are none. A
	// hosted session is told by the host's own record, never by anything a process prints or
	// holds in its environment.
	Host *pty.Host
}

// Hooks are what other parts of the server put in the snapshot and decide about sessions. They
// run on the loop's goroutine, so a hook must never call back into the Loop (Snapshot,
// AgentDetail, LiveSessions wait for that goroutine), and nothing that holds a lock a hook takes
// may wait on the Loop either.
type Hooks struct {
	// Runs are the Open work runs, newest first.
	Runs func() []Run
	// History is the archive's footer state.
	History func() any
	// Reviews is the Reviews tab.
	Reviews func() any
	// MyPrs is the My PRs tab; nil leaves the field out.
	MyPrs func() any
	// Setup is set while there is no repository allowlist.
	Setup func() bool
	// OpenWork is set while Open work is on.
	OpenWork func() bool
	// RepoVisible is whether a repo is on the allowlist.
	RepoVisible func(repo string) bool
	// GithubOn is whether the GitHub module is on.
	GithubOn func() bool
}

// Run is the part of an Open work run the session list reads: its key, its worktrees, and its
// state for the archive.
type Run interface {
	Key() string
	// Worktree is the worktree of the run's first repository, where its shared session works.
	Worktree() string
	// Worktrees are the worktrees of every repository the run builds in, the first's first.
	Worktrees() []string
	Facts() *RunFacts
}

// Snapshot is everything the page shows, as one value.
type Snapshot struct {
	At       float64           `json:"at"`
	Sessions []snapshot.Detail `json:"sessions"`
	Runs     []Run             `json:"runs"`
	History  any               `json:"history"`
	Reviews  any               `json:"reviews"`
	Setup    bool              `json:"setup"`
	OpenWork bool              `json:"openWork"`
	MyPrs    any               `json:"myPrs,omitempty"`
}

// LiveSession is a live session as plain fields, for the modules that must not reach into the
// store. Its Status is the registry's; Shown is the status every page shows (a busy session
// gone quiet is idle), what Open work's run reports.
type LiveSession struct {
	Name      string
	Cwd       string
	Status    string
	Shown     string
	SessionID string
	PID       float64
	// HostedID is the id its host issued for a session Conn hosts, "" for any other: the only
	// id the routes and actions take for a hosted session.
	HostedID string
}

type prEntry struct {
	at    float64
	value *PR
}

type prResult struct {
	key   string
	value *PR
}

type cached struct {
	version int64
	at      float64
	bytes   []byte
}

// store is the session list's state. Only the loop's goroutine touches it.
type store struct {
	deps     Deps
	version  int64
	order    []string
	sessions map[string]*Session
	procs    map[float64]Proc
	pr       map[string]prEntry
	prBusy   map[string]bool
	// prDone carries finished PR lookups back to the loop; stopped ends a lookup's wait for it.
	prDone  chan prResult
	stopped <-chan struct{}
	cache   *cached
	// sending are the sessions with a reply or an answer in flight, closing those with a
	// workspace close in flight, reading those whose screen is being read for its ask.
	sending map[string]bool
	closing map[string]bool
	reading map[string]bool
	// asks are the last parsed screen of each waiting session, by pid; only the parsed ask is
	// kept, never the screen.
	asks map[string]seen
	// answered is the prompt last answered in each session, by pid. It holds only for that
	// session and status stamp, and is set only once a send went through.
	answered map[string]answeredPrompt
	askDone  chan askRead
	// leads finds a teammate's lead, one spawn index per project folder.
	leads     *Leads
	lastProc  float64
	lastReg   float64
	projects  string
	registry  string
	bumpCount func() int64
	hosts     hostedState
}

func newStore(deps Deps, bumps func() int64, stopped <-chan struct{}) *store {
	claudeDir := filepath.Join(deps.Home, ".claude")

	return &store{
		deps: deps, version: 0, order: nil, sessions: map[string]*Session{},
		procs: map[float64]Proc{}, pr: map[string]prEntry{}, prBusy: map[string]bool{},
		prDone: make(chan prResult, prQueue), stopped: stopped, cache: nil,
		sending: map[string]bool{}, closing: map[string]bool{}, reading: map[string]bool{},
		asks: map[string]seen{}, answered: map[string]answeredPrompt{},
		askDone: make(chan askRead, readsMax), leads: NewLeads(), lastProc: 0, lastReg: 0,
		projects: filepath.Join(claudeDir, "projects"),
		registry: filepath.Join(claudeDir, "sessions"), bumpCount: bumps,
		hosts: newHostedState(),
	}
}

const prQueue = 64

func (s *store) now() float64 { return float64(s.deps.Now().UnixMilli()) }

// currentVersion is the store's own version plus every bump from outside.
func (s *store) currentVersion() int64 { return s.version + s.bumpCount() }

// tick advances everything one step.
func (s *store) tick(ctx context.Context) {
	now := s.now()
	s.refreshProcs(now)

	// The host's sessions are read before the registry, so a hosted session is known as one
	// before anything can look it up as a cmux one.
	changed := s.refreshHosted()

	if s.refreshRegistry(ctx, now) {
		changed = true
	}

	s.markHosted()

	for _, key := range s.order {
		if s.sessions[key].poll(s.projects, now, s.leads) {
			changed = true
		}
	}

	s.checkpoint(ctx, now)

	// Agent status and shell counts move with time, not only with new lines.
	if changed {
		s.version++
	}
}

// checkpoint offers every live session to the archive; it decides what is due. A teammate is
// claimed instead: its lead's entry holds it.
func (s *store) checkpoint(ctx context.Context, now float64) {
	archive := s.deps.Archive
	if archive == nil {
		return
	}

	if len(s.order) == 0 {
		return
	}

	keys, runs := s.runKeys()

	for _, key := range s.order {
		session := s.sessions[key]
		if session.leadPath == "" {
			continue
		}

		if session.team(now, s.leads, false) != nil {
			archive.Claimed(session.reg.SessionID)

			continue
		}

		archive.Checkpoint(s.sourceOf(ctx, session, keys, runs, true), now)
	}
}

// archiveEnded queues the full archive of a session that has ended; a teammate is archived as
// part of its lead.
func (s *store) archiveEnded(ctx context.Context, session *Session) {
	archive := s.deps.Archive
	if archive == nil {
		return
	}

	if session.team(s.now(), s.leads, true) != nil {
		archive.Claimed(session.reg.SessionID)

		return
	}

	keys, runs := s.runKeys()
	archive.Ended(s.sourceOf(ctx, session, keys, runs, false))
}

// sourceOf is what the archive is told about a session. A dead session never starts a gh lookup.
func (s *store) sourceOf(ctx context.Context, session *Session, keys map[string]bool,
	runs []Run, alive bool,
) ArchiveSource {
	reg := session.reg
	name := reg.name()

	var run *RunFacts

	for _, candidate := range runs {
		if candidate.Key() == name {
			run = candidate.Facts()

			break
		}
	}

	prNow := s.cachedPR(reg.Cwd, session.lead.Branch)
	if alive {
		prNow = s.prFor(ctx, reg.Cwd, session.lead.Branch)
	}

	registryName := ""
	if reg.Name != nil {
		registryName = *reg.Name
	}

	cwd := reg.Cwd
	rules := s.deps.Kinds
	rules.ReviewsRoot = filepath.Join(s.deps.DataDir, "reviews")

	return ArchiveSource{
		SessionID: reg.SessionID, PID: reg.PID, Name: registryName, Cwd: reg.Cwd,
		TranscriptPath: session.leadPath, StartedAt: valueOr(reg.StartedAt, session.firstAt),
		Parts: session.parts(), PR: prNow, Kind: KindOf(name, &cwd, keys, rules), Run: run,
	}
}

// cachedPR is the PR last found for a branch, with no lookup: for a session that is gone.
func (s *store) cachedPR(cwd, branch string) *PR {
	if branch == "" {
		return nil
	}

	if slices.Contains(noPRBranches(), branch) {
		return nil
	}

	return s.pr[cwd+nul+branch].value
}

// refreshProcs reads the whole process table with one ps call, at most every 3 s.
func (s *store) refreshProcs(now float64) {
	if now-s.lastProc < procsEveryMs {
		return
	}

	s.lastProc = now

	out, err := s.deps.Procs()
	if err != nil {
		return
	}

	s.procs = parseProcs(out)
}

func (s *store) shellsOf(pid float64) float64 {
	count := 0.0

	for _, proc := range s.procs {
		if proc.PPID != pid {
			continue
		}

		if slices.Contains(shells(), strings.TrimPrefix(proc.Comm, "-")) {
			count++
		}
	}

	return count
}

// refreshRegistry reads the registry at most every 2 s, and is true when a session came, went
// or changed.
func (s *store) refreshRegistry(ctx context.Context, now float64) bool {
	if now-s.lastReg < registryEveryMs {
		return false
	}

	s.lastReg = now
	changed := false
	live := map[string]bool{}

	for _, file := range listDir(s.registry) {
		if !strings.HasSuffix(file, ".json") {
			continue
		}

		reg, ok := s.readReg(file)
		if !ok {
			continue
		}

		live[reg.key()] = true

		if s.admit(ctx, reg) {
			changed = true
		}
	}

	for _, key := range slices.Clone(s.order) {
		if live[key] {
			continue
		}

		s.archiveEnded(ctx, s.sessions[key])
		s.order = slices.DeleteFunc(s.order, func(other string) bool { return other == key })
		delete(s.sessions, key)

		changed = true
	}

	return changed
}

// readReg reads one registry file, and is false for one that is not a live session: a helper run
// the dashboard started, or one whose process is not a running claude.
func (s *store) readReg(file string) (Reg, bool) {
	data, err := readFile(filepath.Join(s.registry, file))
	if err != nil {
		return Reg{}, false
	}

	reg, valid := parseReg(data)
	if !valid {
		return Reg{}, false
	}

	// Research and Jira helper runs started by the dashboard are not sessions to show.
	if reg.Name != nil {
		if strings.HasPrefix(*reg.Name, claude.HelperPrefix) {
			return Reg{}, false
		}
	}

	proc, alive := s.procs[reg.PID]
	if !alive {
		return Reg{}, false
	}

	// The host's own record says this process is the hosted program, whatever it is named.
	if _, hosted := s.hosts.byPID[reg.PID]; hosted {
		return reg, true
	}

	return reg, strings.Contains(strings.ToLower(proc.Comm), "claude")
}

// admit adds or updates a session from its registry entry, and is true when that changed it.
func (s *store) admit(ctx context.Context, reg Reg) bool {
	key := reg.key()

	had, found := s.sessions[key]
	if !found {
		s.order = append(s.order, key)
		s.sessions[key] = newSession(reg)

		return true
	}

	// The same process moved to another session (a /clear, say): the one it left is over. A Map
	// keeps the key where it was.
	if had.reg.SessionID != reg.SessionID {
		s.archiveEnded(ctx, had)
		s.sessions[key] = newSession(reg)

		return true
	}

	if had.reg.changed(reg) {
		had.reg = reg

		return true
	}

	return false
}

// liveSessions are the live sessions as plain fields.
func (s *store) liveSessions() []LiveSession {
	out := make([]LiveSession, 0, len(s.order))
	now := s.now()

	for _, key := range s.order {
		session := s.sessions[key]
		reg := session.reg
		name := ""

		if reg.Name != nil {
			name = *reg.Name
		}

		out = append(out, LiveSession{
			Name: name, Cwd: reg.Cwd, Status: reg.status(),
			Shown:     snapshot.ShownStatus(reg.status(), session.parts(), now),
			SessionID: reg.SessionID, PID: reg.PID, HostedID: string(session.hosted),
		})
	}

	return out
}

// folderRepo is the repo of a folder that exists, else "": a folder since deleted would
// otherwise take the repo of whatever encloses it.
func (s *store) folderRepo(folder string) string {
	if !isDir(folder) {
		return ""
	}

	return s.deps.RepoOf(folder)
}

// prFor is the PR of a folder's branch: gh pr view, cached, looked up in the background and
// never on the request path: the lookup outlives the request that started it.
func (s *store) prFor(ctx context.Context, cwd, branch string) *PR {
	if branch == "" {
		return nil
	}

	if slices.Contains(noPRBranches(), branch) {
		return nil
	}

	if !s.deps.Hooks.GithubOn() {
		return nil
	}

	key := cwd + nul + branch
	hit, cachedHit := s.pr[key]
	now := s.now()

	if s.lookupDue(key, cwd, hit, cachedHit, now) {
		s.lookup(ctx, key, cwd, branch, now)
	}

	return hit.value
}

// lookupDue is whether a PR lookup should start now. gh is run only for a folder in an allowed
// repo: a repo off the list (every repo while setting up) is never looked up, and a folder in no
// repo has no GitHub PR to find.
func (s *store) lookupDue(key, cwd string, hit prEntry, cachedHit bool, now float64) bool {
	repo := s.folderRepo(cwd)
	if repo == "" {
		return false
	}

	if !s.deps.Hooks.RepoVisible(repo) {
		return false
	}

	if s.prBusy[key] {
		return false
	}

	if !cachedHit {
		return true
	}

	return now-hit.at > prTTLMs
}

// lookup starts `gh pr view` for a branch. The PR's head is the branch on origin the local one
// tracks, else the local name; a head of digits alone would be read by gh as a PR number, so it
// is not looked up, and that is kept like an answer until the TTL. The repository is named,
// never left to gh, and `--` ends the options so a branch can never be read as one.
func (s *store) lookup(ctx context.Context, key, cwd, branch string, now float64) {
	head := s.deps.Upstream(cwd, branch)
	if head == "" {
		head = branch
	}

	if prHead.MatchString(head) {
		s.pr[key] = prEntry{at: now, value: nil}

		return
	}

	repo := s.folderRepo(cwd)
	s.prBusy[key] = true
	args := []string{"pr", "view", "--repo", repo, "--json", "number,url", "--", head}

	detached := context.WithoutCancel(ctx)

	go func() {
		limited, cancel := context.WithTimeout(detached, prTimeout)
		defer cancel()

		out, err := s.deps.PRView(limited, cwd, args)

		select {
		case s.prDone <- prResult{key: key, value: parsePR(out, err)}:
		case <-s.stopped:
		}
	}()
}

const prTimeout = 10 * time.Second

// parsePR reads gh's answer: a PR when it names one by number.
func parsePR(out []byte, err error) *PR {
	if err != nil {
		return nil
	}

	parsed, parseErr := js.Parse([]byte(js.DecodeUTF8(out, false)))
	if parseErr != nil {
		return nil
	}

	object, _ := parsed.(*js.Object)
	number, _ := object.Get("number")

	if !js.Truthy(number) {
		return nil
	}

	value, _ := number.(float64)
	url, _ := object.Get("url")
	text, _ := url.(string)

	return &PR{Number: value, URL: text}
}

// settlePR keeps a finished lookup; the version moves when the PR number changed.
func (s *store) settlePR(result prResult) {
	delete(s.prBusy, result.key)

	was := s.pr[result.key].value
	s.pr[result.key] = prEntry{at: s.now(), value: result.value}

	if prNumber(was) != prNumber(result.value) {
		s.version++
	}
}

func prNumber(pr *PR) float64 {
	if pr == nil {
		return -1
	}

	return pr.Number
}

func (s *store) runKeys() (map[string]bool, []Run) {
	runs := s.deps.Hooks.Runs()
	keys := map[string]bool{}

	for _, run := range runs {
		keys[run.Key()] = true
	}

	return keys, runs
}

// hidden is whether a session is left out of every view: one in a repo off the allowlist,
// unless conn started it and it is still alive. One in no repo always shows.
func (s *store) hidden(session *Session, runKeys map[string]bool, runs []Run) bool {
	repo := s.folderRepo(session.reg.Cwd)
	if repo == "" {
		return false
	}

	if s.deps.Hooks.RepoVisible(repo) {
		return false
	}

	name := session.reg.name()
	worktrees := []string{}

	for _, run := range runs {
		if run.Key() == name {
			worktrees = run.Worktrees()

			break
		}
	}

	rules := s.deps.Kinds
	rules.ReviewsRoot = filepath.Join(s.deps.DataDir, "reviews")
	cwd := session.reg.Cwd
	kind := KindOf(name, &cwd, runKeys, rules)

	// A run's build session may work in any of its worktrees.
	if kind == KindBuild {
		return !slices.ContainsFunc(worktrees, func(worktree string) bool {
			return StartedByConn(kind, cwd, s.deps.DataDir, worktree)
		})
	}

	return !StartedByConn(kind, cwd, s.deps.DataDir, "")
}

// shown is the live session for a pid the page sent, unless it is hidden. A hosted session is
// named by the id its host issued, never by its pid.
func (s *store) shown(pid string) (*Session, bool) {
	session, found := s.sessions[pid]
	if !found {
		return nil, false
	}

	if session.hosted != "" {
		return nil, false
	}

	keys, runs := s.runKeys()

	return session, !s.hidden(session, keys, runs)
}

// snapshotBytes is the snapshot as JSON, reused while nothing changed and it is under 5 s old.
func (s *store) snapshotBytes(ctx context.Context) []byte {
	now := s.now()
	version := s.currentVersion()

	if s.cache != nil {
		if s.cache.version == version {
			if now-s.cache.at < cacheMs {
				return s.cache.bytes
			}
		}
	}

	snap := s.build(ctx, now)
	s.cache = &cached{version: version, at: now, bytes: marshal(snap)}

	return s.cache.bytes
}

func (s *store) build(ctx context.Context, now float64) Snapshot {
	keys, runs := s.runKeys()
	details := []snapshot.Detail{}

	for _, key := range s.order {
		session := s.sessions[key]
		// A hosted session is a row of the host's, below.
		if session.hosted != "" {
			continue
		}

		if s.hidden(session, keys, runs) {
			continue
		}

		details = append(details, s.detail(ctx, session, now, keys, rowFacts{
			id: key, canReply: s.cmuxOf(session) != nil,
			ask: s.askOf(key, session), hosted: false, exitCode: nil,
		}))
	}

	for _, info := range s.hosts.live {
		details = append(details, s.hostedDetail(ctx, info, now, keys))
	}

	js.Sort(details, func(left, right snapshot.Detail) float64 {
		return float64(right.UpdatedAt) - float64(left.UpdatedAt)
	})

	hooks := s.deps.Hooks
	snap := Snapshot{
		At: now, Sessions: details, Runs: runs, History: hooks.History(),
		Reviews: hooks.Reviews(), Setup: hooks.Setup(), OpenWork: hooks.OpenWork(), MyPrs: nil,
	}

	if hooks.MyPrs != nil {
		snap.MyPrs = hooks.MyPrs()
	}

	return snap
}

// rowFacts are what a row says that depends on where the session runs: the id the page names it
// by, whether a reply can be typed into it, what its screen asks, and, for a hosted one, how it
// ended.
type rowFacts struct {
	id       string
	canReply bool
	ask      *ask.Ask
	hosted   bool
	exitCode *int
}

// detail is one session with the live fields only this machine can supply; the rest is built
// from the transcripts.
func (s *store) detail(
	ctx context.Context, session *Session, now float64, runKeys map[string]bool, row rowFacts,
) snapshot.Detail {
	reg := session.reg
	name := reg.name()
	status := reg.status()

	var repo *string
	if found := s.folderRepo(reg.Cwd); found != "" {
		repo = &found
	}

	var waitingFor *string

	if status == regWaiting {
		value := waitingDefault
		if reg.WaitingFor != nil {
			value = *reg.WaitingFor
		}

		waitingFor = &value
	}

	live := snapshot.Live{
		ID: row.id, PID: reg.PID, SessionID: reg.SessionID, Name: name, Cwd: reg.Cwd,
		Repo: repo, Status: status, WaitingFor: waitingFor, CanReply: row.canReply, Ask: row.ask,
		Shells: s.shellsOf(reg.PID), PR: s.prFor(ctx, reg.Cwd, session.lead.Branch),
		StartedAt: valueOr(reg.StartedAt, session.firstAt), UpdatedAt: valueOr(reg.UpdatedAt, 0),
		Team: s.teamOf(session, now), Hosted: row.hosted, ExitCode: row.exitCode,
	}

	cwd := reg.Cwd
	rules := s.deps.Kinds
	rules.ReviewsRoot = filepath.Join(s.deps.DataDir, "reviews")

	return snapshot.Build(session.parts(), live, now, snapshot.Opts{
		Trim: true, Kind: KindOf(name, &cwd, runKeys, rules),
		AdvisorModel: snapshot.AdvisorModel, Ended: false, EndedAt: nil,
	})
}

// teamOf is a session's team, with the id of the lead's row when the lead is live too.
func (s *store) teamOf(session *Session, now float64) *snapshot.Team {
	team := session.team(now, s.leads, false)
	if team == nil {
		return nil
	}

	linked := *team

	for _, key := range s.order {
		if s.sessions[key].reg.SessionID == team.LeadSessionID {
			id := s.rowID(key)
			linked.LeadID = &id

			break
		}
	}

	return &linked
}

func valueOr(value *float64, fallback float64) float64 {
	if value == nil {
		return fallback
	}

	return *value
}

// agentDetail is one agent page of a live session that is shown.
func (s *store) agentDetail(pid, agentID string) ([]byte, bool) {
	session, shown := s.shown(pid)
	if !shown {
		return nil, false
	}

	return s.agentPage(session, agentID)
}

// agentPage is one agent page of a live session.
func (s *store) agentPage(session *Session, agentID string) ([]byte, bool) {
	name := session.reg.name()
	status := session.reg.status()

	page, found := snapshot.BuildAgent(session.parts(), agentID, s.now(), snapshot.AgentOpts{
		ToolCallLimit: agentToolCalls, Ended: false, EndedAt: nil, Name: &name, Status: &status,
	})
	if !found {
		return nil, false
	}

	return marshal(page), true
}

// marshal writes a value as JSON.stringify would.
func marshal(value any) []byte { return js.Marshal(value) }
