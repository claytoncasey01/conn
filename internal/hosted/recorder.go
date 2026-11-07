package hosted

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
)

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
	// written is what the file holds, once wrote is set.
	written []Record
	wrote   bool
	frozen  bool
}

// NewRecorder is a Recorder with no sessions recorded yet. Its first Sync writes the record, so
// whatever the file held from an earlier launch is replaced.
func NewRecorder(deps RecorderDeps) *Recorder {
	return &Recorder{
		deps: deps, mu: sync.Mutex{}, records: []Record{}, written: nil, wrote: false,
		frozen: false,
	}
}

// Opener is open with each session it starts inside Conn recorded for owner.
func (r *Recorder) Opener(owner string, open launch.Opener) launch.Opener {
	return func(ctx context.Context, spec pty.Spec) launch.Opened {
		opened := open(ctx, spec)
		if opened.HostedID != "" {
			r.Started(owner, opened.HostedID, spec, "")
		}

		return opened
	}
}

// Started records the session the host issued hostedID for, started by owner from spec, and
// writes the record. claudeID is Claude's id of the conversation it resumes, "" for a new one
// (the session list links it later). A session that cannot be recorded (see CheckRecord) is said
// to Failed and runs unrecorded: it is not resumed.
func (r *Recorder) Started(owner string, hostedID pty.ID, spec pty.Spec, claudeID string) {
	record := Record{
		ID: string(hostedID), Owner: owner, Name: spec.Name, Dir: spec.Dir,
		ClaudeSessionID: claudeID,
	}

	err := CheckRecord(record)
	if err != nil {
		r.deps.Failed(err)

		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.frozen {
		return
	}

	r.records = append(r.records, record)
	r.syncLocked()
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

// Records are the sessions recorded now.
func (r *Recorder) Records() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.records)
}

// syncLocked is Sync with r.mu held. The host is read with the lock held, so a session Started
// has just recorded is always in what it reads.
func (r *Recorder) syncLocked() {
	r.records = Track(r.records, r.deps.Live(), r.deps.Linked())

	if r.wrote {
		if slices.Equal(r.records, r.written) {
			return
		}
	}

	err := r.deps.Write(r.deps.Dir, r.records)
	if err != nil {
		r.deps.Failed(err)

		return
	}

	r.written = slices.Clone(r.records)
	r.wrote = true
}

// Track is records kept to the sessions that still run in live, each with Claude's id from
// linked once it is a UUID there, in the order they were recorded.
func Track(records []Record, live []pty.Info, linked map[pty.ID]string) []Record {
	running := map[string]bool{}

	for _, info := range live {
		if info.ExitCode == nil {
			running[string(info.ID)] = true
		}
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
