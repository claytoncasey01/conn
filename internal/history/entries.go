package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/claytoncasey01/conn/internal/js"
)

// Entry is one session to write: its row, its stored detail and agent pages, and its search text.
type Entry struct {
	SessionID, Name, Cwd, Project, Branch, Kind, Model string
	StartedAt, EndedAt, CostUSD                        float64
	Agents, AdvisorCalls, Errors                       float64
	Outcome                                            string
	// Repo is the lowercase owner/repo of the session's folder when it was captured; "" when it
	// is in no repo or the folder is gone.
	Repo           string
	PRNumber       *float64
	TicketKey      *string
	Source         string
	ResumedFrom    *string
	TranscriptPath string
	// Detail and AgentDetails are stored as JSON, as JSON.stringify writes them.
	Detail       any
	AgentDetails *js.Object
	FirstPrompt  string
	FinalMessage string
	SearchText   string
	// Final is set for a full replay, not a capped live checkpoint; a final row is never
	// replaced by a checkpoint of the same transcript.
	Final bool
	// TranscriptSize is the size the final replay ended at; nil for a checkpoint.
	TranscriptSize *float64
	// Named is set when the name is one a person or the transcript gave the session, not a
	// snippet of its first prompt: a resume passes --name only for those.
	Named bool
}

const upsertEntry = `INSERT INTO entries (id, name, cwd, project, branch, kind, model, started_at,
  ended_at, cost_usd, agents, advisor_calls, errors, outcome, pr_number, ticket_key, source,
  resumed_from, transcript_path, final, transcript_size, named, repo)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET name = excluded.name, cwd = excluded.cwd,
  project = excluded.project, branch = excluded.branch,
  kind = CASE WHEN excluded.kind = 'adhoc' AND entries.kind = 'build' THEN entries.kind
              ELSE excluded.kind END,
  model = excluded.model, started_at = excluded.started_at, ended_at = excluded.ended_at,
  cost_usd = excluded.cost_usd, agents = excluded.agents,
  advisor_calls = excluded.advisor_calls, errors = excluded.errors,
  outcome = CASE WHEN excluded.outcome = 'ended' AND entries.outcome IN ('PR opened', 'merged')
                   THEN entries.outcome
                 WHEN excluded.outcome = 'PR opened' AND entries.outcome = 'merged'
                   THEN entries.outcome
                 ELSE excluded.outcome END,
  pr_number = COALESCE(excluded.pr_number, entries.pr_number),
  ticket_key = COALESCE(excluded.ticket_key, entries.ticket_key), source = excluded.source,
  resumed_from = excluded.resumed_from, transcript_path = excluded.transcript_path,
  final = excluded.final, transcript_size = excluded.transcript_size, named = excluded.named,
  repo = COALESCE(excluded.repo, entries.repo)`

const upsertBlobs = `INSERT INTO blobs (session_id, detail_json, agents_json, first_prompt,
  final_message)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (session_id) DO UPDATE SET detail_json = excluded.detail_json,
  agents_json = excluded.agents_json, first_prompt = excluded.first_prompt,
  final_message = excluded.final_message`

// number is a JavaScript number as SQLite gets it from Bun: an integer when it is one.
func number(value float64) any {
	if value == float64(int64(value)) {
		return int64(value)
	}

	return value
}

func optionalNumber(value *float64) any {
	if value == nil {
		return nil
	}

	return number(*value)
}

func optionalText(value *string) any {
	if value == nil {
		return nil
	}

	return *value
}

func bit(value bool) int {
	if value {
		return 1
	}

	return 0
}

// write inserts or replaces the entry's columns, blobs and search text. The summary columns are
// never touched. What a later write cannot say does not erase what an earlier one did: a build
// row keeps its kind, its ticket key, its PR number and a better outcome.
func write(ctx context.Context, txn *sql.Tx, entry Entry) error {
	var repo any
	if entry.Repo != "" {
		repo = entry.Repo
	}

	_, err := txn.ExecContext(ctx, upsertEntry,
		entry.SessionID, entry.Name, entry.Cwd, entry.Project, entry.Branch, entry.Kind,
		entry.Model, number(entry.StartedAt), number(entry.EndedAt), entry.CostUSD,
		number(entry.Agents), number(entry.AdvisorCalls), number(entry.Errors), entry.Outcome,
		optionalNumber(entry.PRNumber), optionalText(entry.TicketKey), entry.Source,
		optionalText(entry.ResumedFrom), entry.TranscriptPath, bit(entry.Final),
		optionalNumber(entry.TranscriptSize), bit(entry.Named), repo)
	if err != nil {
		return fmt.Errorf("writing the entry: %w", err)
	}

	_, err = txn.ExecContext(ctx, upsertBlobs, entry.SessionID, string(js.Marshal(entry.Detail)),
		string(js.Marshal(entry.AgentDetails)), entry.FirstPrompt, entry.FinalMessage)
	if err != nil {
		return fmt.Errorf("writing the blobs: %w", err)
	}

	var rowid int64

	err = txn.QueryRowContext(ctx, "SELECT rowid FROM entries WHERE id = ?", entry.SessionID).
		Scan(&rowid)
	if err != nil {
		return fmt.Errorf("reading the rowid: %w", err)
	}

	_, err = txn.ExecContext(ctx, "DELETE FROM entries_fts WHERE rowid = ?", rowid)
	if err != nil {
		return fmt.Errorf("clearing the search text: %w", err)
	}

	// The summary is written later and by someone else, so a re-write carries it over.
	_, err = txn.ExecContext(ctx, "INSERT INTO entries_fts (rowid, search_text, summary) "+
		"SELECT rowid, ?, summary_text FROM entries WHERE id = ?", entry.SearchText,
		entry.SessionID)
	if err != nil {
		return fmt.Errorf("writing the search text: %w", err)
	}

	return nil
}

// Upsert writes an entry, replacing the one with its id.
func (d *DB) Upsert(ctx context.Context, entry Entry) error {
	return d.transaction(ctx, func(txn *sql.Tx) error { return write(ctx, txn, entry) })
}

// InsertIfAbsent writes an entry only when none has its id, and says whether it did.
func (d *DB) InsertIfAbsent(ctx context.Context, entry Entry) (bool, error) {
	added := false

	err := d.transaction(ctx, func(txn *sql.Tx) error {
		var one int

		err := txn.QueryRowContext(ctx, "SELECT 1 FROM entries WHERE id = ?", entry.SessionID).
			Scan(&one)
		if err == nil {
			return nil
		}

		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("looking for the entry: %w", err)
		}

		added = true

		return write(ctx, txn, entry)
	})

	return added, err
}

// Has is whether an entry with this id is archived.
func (d *DB) Has(ctx context.Context, id string) (bool, error) {
	var one int

	err := d.db.QueryRowContext(ctx, "SELECT 1 FROM entries WHERE id = ?", id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("looking for an entry: %w", err)
	}

	return true, nil
}

// FinalSize is the transcript size a final entry was replayed to, and false when it is not
// final, has no recorded size, or there is no such entry.
func (d *DB) FinalSize(ctx context.Context, sessionID string) (float64, bool, error) {
	var (
		final int
		size  sql.NullFloat64
	)

	err := d.db.QueryRowContext(ctx,
		"SELECT final, transcript_size FROM entries WHERE id = ?", sessionID).Scan(&final, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}

	if err != nil {
		return 0, false, fmt.Errorf("reading the final size: %w", err)
	}

	if final != 1 {
		return 0, false, nil
	}

	return size.Float64, size.Valid, nil
}

// DeleteIfNotFinal removes an entry that is only a live checkpoint (its blobs and search text
// with it); a final entry is never touched. It says whether one was removed.
func (d *DB) DeleteIfNotFinal(ctx context.Context, sessionID string) (bool, error) {
	removed := false

	err := d.transaction(ctx, func(txn *sql.Tx) error {
		var rowid int64

		err := txn.QueryRowContext(ctx,
			"SELECT rowid FROM entries WHERE id = ? AND final = 0", sessionID).Scan(&rowid)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("looking for a checkpoint: %w", err)
		}

		_, err = txn.ExecContext(ctx, "DELETE FROM entries_fts WHERE rowid = ?", rowid)
		if err != nil {
			return fmt.Errorf("clearing its search text: %w", err)
		}

		// The blobs go with it (ON DELETE CASCADE).
		_, err = txn.ExecContext(ctx, "DELETE FROM entries WHERE id = ?", sessionID)
		if err != nil {
			return fmt.Errorf("removing the checkpoint: %w", err)
		}

		removed = true

		return nil
	})

	return removed, err
}
