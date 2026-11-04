package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
)

// Summary states.
const (
	SummaryNone    = "none"
	SummaryQueued  = "queued"
	SummaryRunning = "running"
	SummaryReady   = "ready"
	SummaryFailed  = "failed"
)

const glanceUnits = 300

// Summary is an entry's At a Glance summary.
type Summary struct {
	Text  string  `json:"text"`
	Model string  `json:"model"`
	At    float64 `json:"at"`
	State string  `json:"state"`
	Error *string `json:"error"`
}

// Stored is one archived session: its row, its stored detail, and its summary.
type Stored struct {
	Row     Row        `json:"row"`
	Detail  *js.Object `json:"detail"`
	Summary Summary    `json:"summary"`
}

// Get is one entry, or false when there is none. A row archived before ask and sessionId existed
// has neither: its own id is its session id.
func (d *DB) Get(ctx context.Context, id string) (Stored, bool, error) {
	stored, _, _, found, err := d.material(ctx, id)

	return stored, found, err
}

// Material is an entry with the first prompt and final message a summary is written from.
func (d *DB) Material(ctx context.Context, id string) (Stored, string, string, bool, error) {
	return d.material(ctx, id)
}

func (d *DB) material(ctx context.Context, sessionID string) (Stored, string, string, bool, error) {
	var (
		stored             Stored
		detailJSON         string
		summaryError       sql.NullString
		firstPrompt, final string
	)

	row := d.db.QueryRowContext(ctx, "SELECT "+rowColumns+", e.summary_text, e.summary_model, "+
		"e.summary_at, e.summary_error, b.detail_json, b.first_prompt, b.final_message "+
		"FROM entries e JOIN blobs b ON b.session_id = e.id WHERE e.id = ?", sessionID)

	found, err := scanRow(row, &stored.Summary.Text, &stored.Summary.Model, &stored.Summary.At,
		&summaryError, &detailJSON, &firstPrompt, &final)
	if errors.Is(err, sql.ErrNoRows) {
		return Stored{}, "", "", false, nil
	}

	if err != nil {
		return Stored{}, "", "", false, err
	}

	stored.Row = found
	stored.Summary.State = found.SummaryState

	if summaryError.Valid {
		stored.Summary.Error = &summaryError.String
	}

	stored.Detail = storedDetail(found, detailJSON)

	return stored, firstPrompt, final, true, nil
}

// storedDetail is {ask: null, sessionId: id, ...detail, repo: the row's repo}, keys in that order
// with the stored detail's own keeping their place.
func storedDetail(row Row, detailJSON string) *js.Object {
	detail := js.NewObject()
	detail.Set("ask", nil)
	detail.Set("sessionId", row.ID)

	parsed, err := js.Parse([]byte(detailJSON))
	if err == nil {
		if object, isObject := parsed.(*js.Object); isObject {
			for _, key := range object.Keys() {
				value, _ := object.Get(key)
				detail.Set(key, value)
			}
		}
	}

	if row.Repo == nil {
		detail.Set("repo", nil)
	} else {
		detail.Set("repo", *row.Repo)
	}

	return detail
}

// Agent is one stored agent page, as JSON, or false when there is none.
func (d *DB) Agent(ctx context.Context, id, agentID string) (any, bool, error) {
	var value string

	err := d.db.QueryRowContext(ctx, "SELECT j.value FROM blobs b, json_each(b.agents_json) j "+
		"WHERE b.session_id = ? AND j.key = ?", id, agentID).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("reading an agent page: %w", err)
	}

	parsed, err := js.Parse([]byte(value))
	if err != nil {
		return nil, false, fmt.Errorf("reading an agent page: %w", err)
	}

	return parsed, true, nil
}

// RepoOf is the repo a row was captured in ("" for none), and false when there is no such row.
func (d *DB) RepoOf(ctx context.Context, id string) (string, bool, error) {
	var repo sql.NullString

	err := d.db.QueryRowContext(ctx, "SELECT repo FROM entries WHERE id = ?", id).Scan(&repo)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("reading a repo: %w", err)
	}

	return repo.String, true, nil
}

// TranscriptPath is where an entry's transcript was, "" when there is none.
func (d *DB) TranscriptPath(ctx context.Context, id string) (string, error) {
	var path string

	err := d.db.QueryRowContext(ctx, "SELECT transcript_path FROM entries WHERE id = ?", id).
		Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("reading a transcript path: %w", err)
	}

	return path, nil
}

// Resume is what a resume needs: the name and folder the row recorded, whether that name is a
// real one, and where its transcript was.
type Resume struct {
	Name           string
	Cwd            string
	TranscriptPath string
	Named          bool
}

// ResumeInfo is a row's resume facts, or false when there is no such row.
func (d *DB) ResumeInfo(ctx context.Context, sessionID string) (Resume, bool, error) {
	var (
		info  Resume
		named int
	)

	err := d.db.QueryRowContext(ctx,
		"SELECT name, cwd, transcript_path, named FROM entries WHERE id = ?", sessionID).
		Scan(&info.Name, &info.Cwd, &info.TranscriptPath, &named)
	if errors.Is(err, sql.ErrNoRows) {
		return Resume{}, false, nil
	}

	if err != nil {
		return Resume{}, false, fmt.Errorf("reading resume facts: %w", err)
	}

	info.Named = named == 1

	return info, true, nil
}

// SummaryUpdate moves a summary to a state; a nil field is left as it is, so queued, running and
// failed keep the previous text.
type SummaryUpdate struct {
	State string
	Text  *string
	Model *string
	// Error is set when SetError is (nil writes NULL).
	Error    *string
	SetError bool
}

// SetSummary moves an entry's summary to a state. Writing text stamps the time and refreshes the
// glance line and the search text.
func (d *DB) SetSummary(ctx context.Context, sessionID string, update SummaryUpdate) error {
	sets := []string{"summary_state = ?"}
	params := []any{update.State}

	if update.Text != nil {
		sets = append(sets, "summary_text = ?", "summary_at = ?", "glance = ?")
		params = append(params, *update.Text, number(d.now()), glance(*update.Text))
	}

	if update.Model != nil {
		sets = append(sets, "summary_model = ?")
		params = append(params, *update.Model)
	}

	if update.SetError {
		sets = append(sets, "summary_error = ?")
		params = append(params, optionalText(update.Error))
	}

	return d.transaction(ctx, func(txn *sql.Tx) error {
		_, err := txn.ExecContext(ctx,
			statement("UPDATE entries SET ", strings.Join(sets, ", "), " WHERE id = ?"),
			append(params, sessionID)...)
		if err != nil {
			return fmt.Errorf("setting a summary: %w", err)
		}

		if update.Text == nil {
			return nil
		}

		_, err = txn.ExecContext(ctx, "UPDATE entries_fts SET summary = ? WHERE rowid = "+
			"(SELECT rowid FROM entries WHERE id = ?)", *update.Text, sessionID)
		if err != nil {
			return fmt.Errorf("indexing a summary: %w", err)
		}

		return nil
	})
}

// glance is the first non-blank line of a summary, cut to 300 UTF-16 units.
func glance(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		if trimmed := js.Trim(line); trimmed != "" {
			return js.Cut(trimmed, glanceUnits)
		}
	}

	return ""
}

// MarkSummaryStale returns a ready or failed summary to none (the transcript grew after it was
// written), so opening the entry writes a new one.
func (d *DB) MarkSummaryStale(ctx context.Context, id string) error {
	_, err := d.db.ExecContext(ctx, "UPDATE entries SET summary_state = 'none' "+
		"WHERE id = ? AND summary_state IN ('ready', 'failed')", id)
	if err != nil {
		return fmt.Errorf("marking a summary stale: %w", err)
	}

	return nil
}

// ResetInterruptedSummaries marks every queued or running summary failed (its queue died with the
// process that had it), so it can be retried, and says how many there were.
func (d *DB) ResetInterruptedSummaries(ctx context.Context) (int64, error) {
	result, err := d.db.ExecContext(ctx, "UPDATE entries SET summary_state = 'failed', "+
		"summary_error = 'interrupted by a restart' WHERE summary_state IN ('queued', 'running')")
	if err != nil {
		return 0, fmt.Errorf("resetting summaries: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting reset summaries: %w", err)
	}

	return affected, nil
}

// RecordFailure keeps why an id could not be archived.
func (d *DB) RecordFailure(ctx context.Context, id, message string) error {
	_, err := d.db.ExecContext(ctx, "INSERT INTO failures (id, error, at) VALUES (?, ?, ?) "+
		"ON CONFLICT (id) DO UPDATE SET error = excluded.error, at = excluded.at", id, message,
		number(d.now()))
	if err != nil {
		return fmt.Errorf("recording a failure: %w", err)
	}

	return nil
}

// ClearFailure forgets an id's failure.
func (d *DB) ClearFailure(ctx context.Context, id string) error {
	_, err := d.db.ExecContext(ctx, "DELETE FROM failures WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("clearing a failure: %w", err)
	}

	return nil
}

// ClearFailuresExcept forgets the failures of every id not in keep recorded before before: the
// transcript they were about is gone. It says how many were cleared.
func (d *DB) ClearFailuresExcept(ctx context.Context, keep []string, before float64) (int64,
	error,
) {
	result, err := d.db.ExecContext(ctx, "DELETE FROM failures WHERE at < ? AND id NOT IN "+
		"(SELECT value FROM json_each(?))", number(before), string(js.Marshal(keep)))
	if err != nil {
		return 0, fmt.Errorf("clearing failures: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting cleared failures: %w", err)
	}

	return affected, nil
}

// FailureCount is how many ids could not be archived.
func (d *DB) FailureCount(ctx context.Context) (float64, error) {
	var count float64

	err := d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM failures").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting failures: %w", err)
	}

	return count, nil
}
