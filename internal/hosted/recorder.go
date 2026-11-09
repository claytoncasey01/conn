package hosted

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
)

// conversationFlags are claude's options that name the conversation a session runs on, in the
// order they are looked for: the conversation it resumes, then the id Conn chose for a new one
// (a session started from the Sessions page).
func conversationFlags() []string { return []string{"--resume", "--session-id"} }

// SyncInterval is how often a running Recorder looks for sessions that ended and for Claude's
// ids of the ones that run.
const SyncInterval = time.Second

// RecorderDeps are what a Recorder reads and writes.
type RecorderDeps struct {
	// Dir is the data folder the record is written in.
	Dir string
	// Live is every session the host holds now (pty.Host.Live).
	Live func() []pty.Info
	// Linked is Claude's session id of each hosted session the session list has linked, by
	// the host's id.
	Linked func() map[pty.ID]string
	// Write writes the record (Write).
	Write func(dir string, records []Record) error
	// Failed is told a record that could not be kept, or a write that failed.
	Failed func(err error)
}

// Recorder keeps the record of the hosted sessions up to date: a session is recorded when it
// starts (Started), and dropped once it has ended (Sync). Freeze writes it one last time and
// stops it changing, so the sessions a quit then stops stay recorded. Its methods are safe to
// call from many goroutines.
type Recorder struct {
	deps RecorderDeps

	mu      sync.Mutex
	records []Record
	// carried are the last launch's records this launch has not resumed (yet): kept in the
	// record as they were, beside the sessions that run, so a quit or a crash during the resume
	// loses none of them.
	carried []Record
	// written is what the file holds, once wrote is set.
	written []Record
	wrote   bool
	frozen  bool
	// failure is the write failure said last, until a write works: the same one is not said
	// again.
	failure string
}

// NewRecorder is a Recorder with no sessions recorded yet. Its first Sync writes the record, so
// whatever the file held from an earlier launch is replaced.
func NewRecorder(deps RecorderDeps) *Recorder {
	return &Recorder{
		deps: deps, mu: sync.Mutex{}, records: []Record{}, carried: nil, written: nil,
		wrote: false, frozen: false, failure: "",
	}
}

// Opener is open with each session it starts inside Conn recorded for owner, on the
// conversation it runs on when argv names one (conversationOf).
func (r *Recorder) Opener(owner string, open launch.Opener) launch.Opener {
	return func(ctx context.Context, spec pty.Spec) launch.Opened {
		opened := open(ctx, spec)
		if opened.HostedID != "" {
			r.Started(owner, opened.HostedID, spec, conversationOf(spec.Argv))
		}

		return opened
	}
}

// Carry keeps records, the last launch's that this launch has not resumed, in the record until
// the next Carry: each one Resumed starts leaves it. It writes nothing by itself.
func (r *Recorder) Carry(records []Record) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.frozen {
		return
	}

	r.carried = slices.Clone(records)
}

// Resumed records the session the host issued hostedID for, started from spec to resume from,
// a carried record, in from's place: under its owner, on its conversation.
func (r *Recorder) Resumed(from Record, hostedID pty.ID, spec pty.Spec) {
	r.started(from.Owner, hostedID, spec, from.ClaudeSessionID, from.ID)
}

// Started records the session the host issued hostedID for, started by owner from spec, and
// writes the record. claudeID is Claude's id of the conversation it resumes, "" for a new one
// (the session list links it later). A session that cannot be recorded (see CheckRecord) is said
// to Failed and runs unrecorded: it is not resumed. So is one Write could not write as it is,
// so that one session never stops the record of the others.
func (r *Recorder) Started(owner string, hostedID pty.ID, spec pty.Spec, claudeID string) {
	r.started(owner, hostedID, spec, claudeID, "")
}

// Sync drops the sessions that have ended, takes Claude's id of each one linked since, and
// writes the record if it changed (or was never written).
func (r *Recorder) Sync() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.frozen {
		return
	}

	r.syncLocked()
}

// Freeze syncs one last time and stops the record changing: the sessions that run now stay
// recorded however they end after it.
func (r *Recorder) Freeze() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.frozen {
		return
	}

	r.syncLocked()
	r.frozen = true
}

// Run syncs every SyncInterval until ctx ends.
func (r *Recorder) Run(ctx context.Context) {
	ticker := time.NewTicker(SyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Sync()
		}
	}
}

// Records are the sessions recorded now, not the carried records.
func (r *Recorder) Records() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.records)
}

// started is Started, with the carried record of id replaced ("" for none).
func (r *Recorder) started(owner string, hostedID pty.ID, spec pty.Spec, claudeID,
	replaced string,
) {
	record := Record{
		ID: string(hostedID), Owner: owner, Name: spec.Name, Dir: spec.Dir,
		ClaudeSessionID: claudeID,
	}

	err := checkWritable(record)
	if err != nil {
		r.deps.Failed(err)

		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.frozen {
		return
	}

	// The host runs at most RecordsMax sessions: one more would make a record Write refuses,
	// and stop every write after it. It runs unrecorded instead.
	r.records = Track(r.records, r.deps.Live(), r.deps.Linked())
	if len(r.records) >= RecordsMax() {
		r.deps.Failed(fmt.Errorf("%w: %d sessions are recorded already, at most %d",
			ErrBadRecord, len(r.records), RecordsMax()))

		return
	}

	r.carried = slices.DeleteFunc(r.carried, func(carried Record) bool {
		return carried.ID == replaced
	})
	r.records = append(r.records, record)
	r.syncLocked()
}

// syncLocked is Sync with r.mu held. The host is read with the lock held, so a session Started
// has just recorded is always in what it reads.
func (r *Recorder) syncLocked() {
	r.records = Track(r.records, r.deps.Live(), r.deps.Linked())
	kept := r.keptLocked()

	if r.wrote {
		if slices.Equal(kept, r.written) {
			return
		}
	}

	err := r.deps.Write(r.deps.Dir, kept)
	if err != nil {
		r.writeFailedLocked(err)

		return
	}

	r.written = kept
	r.wrote = true
	r.failure = ""
}

// writeFailedLocked says err to Failed, unless it is the failure said last: a write that keeps
// failing the same way (a folder at the record's name) is said once, not at every sync.
func (r *Recorder) writeFailedLocked(err error) {
	if err.Error() == r.failure {
		return
	}

	r.failure = err.Error()
	r.deps.Failed(err)
}

// keptLocked is what the record holds: the sessions that run, then as many carried records as
// fit in RecordsMax. The host runs at most RecordsMax sessions, and a resume takes a carried
// record's place, so the carried ones are cut only when modules start sessions of their own
// during the resume.
func (r *Recorder) keptLocked() []Record {
	room := max(RecordsMax()-len(r.records), 0)

	return slices.Concat(r.records, r.carried[:min(len(r.carried), room)])
}

// conversationOf is the conversation argv runs on: the argument after the first of
// conversationFlags that argv holds with a UUID after it, else "" (a new conversation Conn did
// not name, which the session list links later).
func conversationOf(argv []string) string {
	for _, flag := range conversationFlags() {
		flagAt := slices.Index(argv, flag)
		if flagAt < 0 {
			continue
		}

		if flagAt+1 >= len(argv) {
			continue
		}

		if cmux.IsSessionID(argv[flagAt+1]) {
			return argv[flagAt+1]
		}
	}

	return ""
}

// Track is records kept to the sessions that still run in live and are not being stopped (one the
// person is ending must not come back at the next launch), each with Claude's id from linked once
// it is a UUID there, in the order they were recorded.
func Track(records []Record, live []pty.Info, linked map[pty.ID]string) []Record {
	running := map[string]bool{}

	for _, info := range live {
		if info.ExitCode != nil {
			continue
		}

		if info.Stopping {
			continue
		}

		running[string(info.ID)] = true
	}

	kept := make([]Record, 0, len(records))

	for _, record := range records {
		if !running[record.ID] {
			continue
		}

		claudeID := linked[pty.ID(record.ID)]
		if cmux.IsSessionID(claudeID) {
			record.ClaudeSessionID = claudeID
		}

		kept = append(kept, record)
	}

	return kept
}
