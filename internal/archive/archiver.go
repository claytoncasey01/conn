package archive

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/claytoncasey01/conn/internal/history"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/sessions"
)

const (
	checkpointMs = 60_000
	reposKept    = 2000
	endedQueue   = 256
)

// Gate is the allowlist: nothing is captured while it is in setup, and a session in a repo it does
// not allow is not captured.
type Gate struct {
	Setup func() bool
	Allow func() []string
}

// admits is whether a session in repo ("" for none) may be captured now: asked at every capture,
// so a saved list applies at once.
func (g *Gate) admits(repo string) bool {
	if g == nil {
		return true
	}

	if g.Setup() {
		return false
	}

	if repo == "" {
		return true
	}

	return repos.Allowed(repo, g.Allow())
}

type seenAt struct {
	at          float64
	fingerprint string
}

// Archiver archives every session while it is live (a throttled checkpoint) and once more when it
// ends (a full replay). Checkpoint and Claimed run on the session loop; Ended queues the replay
// for its own goroutine, one at a time, in the order sessions ended.
type Archiver struct {
	mutex sync.Mutex
	db    *history.DB
	env   Env
	gate  *Gate
	leads *sessions.Leads
	// captured is told about every session written by a full capture.
	captured func(id, source string)
	seen     map[string]seenAt
	// teammates are the sessions found to be teammates, so the check is made once.
	teammates map[string]bool
	// lastRepo is the last repo each live session was seen in: a folder removed before the
	// session ends reads as no repo, and the replay must not turn a hidden session visible.
	lastRepo  map[string]string
	repoOrder []string
	ended     chan sessions.ArchiveSource
	idle      sync.WaitGroup
}

// NewArchiver archives into db. captured may be nil; gate nil captures every session.
func NewArchiver(db *history.DB, env Env, gate *Gate, leads *sessions.Leads,
	captured func(id, source string),
) *Archiver {
	return &Archiver{
		mutex: sync.Mutex{}, db: db, env: env, gate: gate, leads: leads, captured: captured,
		seen: map[string]seenAt{}, teammates: map[string]bool{}, lastRepo: map[string]string{},
		repoOrder: nil, ended: make(chan sessions.ArchiveSource, endedQueue),
		idle: sync.WaitGroup{},
	}
}

// Run captures ended sessions one at a time until ctx ends.
func (a *Archiver) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case source := <-a.ended:
			a.end(ctx, source)
			a.idle.Done()
		}
	}
}

// Settled waits until every session that has ended so far has been captured (or its failure
// recorded).
func (a *Archiver) Settled() { a.idle.Wait() }

// fingerprint is what changed in a session, cheaply: no detail is built to find out.
func fingerprint(source sessions.ArchiveSource) string {
	lead := source.Parts.Lead
	agentsAt := 0.0

	for _, agent := range source.Parts.Agents {
		agentsAt += agent.LastAt
	}

	prNumber := ""
	if source.PR != nil {
		prNumber = js.Number(source.PR.Number)
	}

	return strings.Join([]string{
		source.Name, js.Number(source.PID), prNumber, source.Kind, lead.Title,
		js.Number(lead.LastAt),
		js.Number(float64(lead.Calls)), js.Number(float64(lead.Errors)),
		js.Number(float64(len(source.Parts.Agents))), js.Number(agentsAt),
	}, "|")
}

// Checkpoint writes the capped live state: at most once a minute per session, and only when it
// changed. It never fails the caller: a failure is recorded.
func (a *Archiver) Checkpoint(source sessions.ArchiveSource, now float64) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	ctx := context.Background()

	delete(a.teammates, source.SessionID)

	repo := folderRepo(source.Cwd, a.env)
	if repo != "" {
		a.remember(source.SessionID, repo)
	}

	// Nothing at all for a session that may not be captured: not even the window is started.
	if !a.gate.admits(firstText(repo, a.lastRepo[source.SessionID])) {
		return
	}

	mark := fingerprint(source)

	if prev, had := a.seen[source.SessionID]; had {
		if now-prev.at < checkpointMs {
			return
		}

		if prev.fingerprint == mark {
			return
		}
	}

	// A final row stands unless the transcript has grown since (a resume).
	if a.settledAndUnchanged(ctx, source) {
		a.seen[source.SessionID] = seenAt{at: now, fingerprint: mark}

		return
	}

	// A failed write waits out the window too, so a broken database is not retried every tick.
	a.seen[source.SessionID] = seenAt{at: now, fingerprint: ""}

	err := a.db.Upsert(ctx, build(source.Parts, facts{
		sessionID: source.SessionID, path: source.TranscriptPath, source: sourceLive,
		kind: source.Kind, pr: source.PR, run: source.Run, name: source.Name, pid: source.PID,
		startedAt: source.StartedAt, cwd: source.Cwd, final: false, transcriptSize: nil,
		transcriptCwd: "", firstAt: 0,
	}, a.env))
	if err != nil {
		a.fail(ctx, source.SessionID, err)

		return
	}

	a.seen[source.SessionID] = seenAt{at: now, fingerprint: mark}
	_ = a.db.ClearFailure(ctx, source.SessionID)
}

const (
	sourceLive     = "live"
	sourceBackfill = "backfill"
)

// Claimed takes back a checkpoint of a session that turned out to be a teammate (its lead's
// entry holds it), and writes no more for it.
func (a *Archiver) Claimed(sessionID string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	if a.teammates[sessionID] {
		return
	}

	ctx := context.Background()
	a.teammates[sessionID] = true
	delete(a.seen, sessionID)
	a.forget(sessionID)

	removed, err := a.db.DeleteIfNotFinal(ctx, sessionID)
	if err != nil {
		a.fail(ctx, sessionID, err)

		return
	}

	if removed {
		_ = a.db.ClearFailure(ctx, sessionID)
	}
}

// Ended queues the full capture of a session that is over.
func (a *Archiver) Ended(source sessions.ArchiveSource) {
	a.idle.Add(1)

	a.ended <- source
}

func (a *Archiver) remember(sessionID, repo string) {
	for index, other := range a.repoOrder {
		if other == sessionID {
			a.repoOrder = append(a.repoOrder[:index], a.repoOrder[index+1:]...)

			break
		}
	}

	a.lastRepo[sessionID] = repo
	a.repoOrder = append(a.repoOrder, sessionID)

	if len(a.repoOrder) > reposKept {
		delete(a.lastRepo, a.repoOrder[0])
		a.repoOrder = a.repoOrder[1:]
	}
}

func (a *Archiver) forget(sessionID string) {
	delete(a.lastRepo, sessionID)

	for index, other := range a.repoOrder {
		if other == sessionID {
			a.repoOrder = append(a.repoOrder[:index], a.repoOrder[index+1:]...)

			return
		}
	}
}

// end replays the transcript to the end, uncapped, and replaces the checkpoint with it. The end
// time is the transcript's last line, not the clock. A failure is recorded and leaves the last
// checkpoint as it was.
func (a *Archiver) end(ctx context.Context, source sessions.ArchiveSource) {
	a.mutex.Lock()
	delete(a.seen, source.SessionID)
	known := a.lastRepo[source.SessionID]
	a.forget(source.SessionID)
	a.mutex.Unlock()

	// No transcript was ever found: nothing happened that could be archived.
	if source.TranscriptPath == "" {
		return
	}

	if a.gate != nil {
		if a.gate.Setup() {
			return
		}
	}

	replayed, found := Replay(source.TranscriptPath, a.leads)
	if !found {
		a.fail(ctx, source.SessionID, errGone)

		return
	}

	entry := FromReplay(replayed, ReplayOpts{
		Source: sourceLive, Kind: source.Kind, Name: source.Name, PR: source.PR, Run: source.Run,
		PID: source.PID, StartedAt: source.StartedAt, Cwd: source.Cwd, NotFinal: false,
	}, a.env)

	// The folder the transcript names may differ from the registry's: the entry's repo counts,
	// else the one the session was last seen in.
	if entry.Repo == "" {
		entry.Repo = known
	}

	if !a.gate.admits(entry.Repo) {
		return
	}

	err := a.db.Upsert(ctx, entry)
	if err != nil {
		a.fail(ctx, source.SessionID, err)

		return
	}

	_ = a.db.ClearFailure(ctx, source.SessionID)

	if a.captured != nil {
		a.captured(source.SessionID, sourceLive)
	}
}

// settledAndUnchanged is whether the stored entry is final and the transcript is no bigger than
// when it was replayed (or cannot be read).
func (a *Archiver) settledAndUnchanged(ctx context.Context, source sessions.ArchiveSource) bool {
	size, final, err := a.db.FinalSize(ctx, source.SessionID)
	if err != nil {
		return false
	}

	if !final {
		return false
	}

	info, err := os.Stat(source.TranscriptPath)
	if err != nil {
		return true
	}

	return float64(info.Size()) <= size
}

func (a *Archiver) fail(ctx context.Context, id string, err error) {
	_ = a.db.RecordFailure(ctx, id, err.Error())
}

// admitsRepo is whether a repo is on the list (the list alone, not setup): a summary is written
// for a row in no repo or an allowed one.
func (g *Gate) admitsRepo(repo string) bool {
	if g == nil {
		return true
	}

	return repos.Allowed(repo, g.Allow())
}
