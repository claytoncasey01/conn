package hosted

import (
	"context"
	"strings"

	"github.com/claytoncasey01/conn/internal/pty"
)

// OwnerCheck is one owner's check of a record at launch: the session the module would start
// itself to resume it, built from the module's own state with the record only as the key to it,
// and true; or false when the module would not start it now. It never takes a program, an
// argument or an environment from the record.
type OwnerCheck func(ctx context.Context, record Record) (pty.Spec, bool)

// Checks are the owners' checks, by owner. An owner with no check resumes nothing.
type Checks map[string]OwnerCheck

// Start starts a session from spec, the one record's owner rebuilt, as the owner's own starts do,
// and is the id the host issued for it, and true; or false when it did not start inside Conn.
type Start func(ctx context.Context, record Record, spec pty.Spec) (pty.ID, bool)

// Resumed is a session Resume started: the host's id for it and its name, for the page.
type Resumed struct {
	ID   pty.ID `json:"id"`
	Name string `json:"name"`
}

// Outcome is what Resume did: the sessions it started, how many records it did not, and the
// records it did not reach because its context ended first (the quit came during the resume).
type Outcome struct {
	Resumed []Resumed
	Skipped int
	// Unreached are the records not resumed only because the context ended: their checks did
	// not run, or ran or started into the end. The record keeps them for the next launch.
	Unreached []Record
}

// Resume starts again the sessions records name, each only as its owner's check rebuilds it:
// at most MaxRecords records are looked at (and so at most MaxRecords sessions started), a
// record CheckRecord refuses or whose owner has no check or refuses it starts nothing, and two
// records the owners rebuild into the same session, or that name the same Claude session, start
// it once: two programs on one conversation would write over each other. It stops early when ctx
// ends, and says which records it did not reach.
func Resume(ctx context.Context, records []Record, checks Checks, start Start) Outcome {
	considered := records[:min(len(records), MaxRecords())]
	outcome := Outcome{
		Resumed: []Resumed{}, Skipped: len(records) - len(considered), Unreached: []Record{},
	}
	started := map[string]bool{}

	for index, record := range considered {
		if ctx.Err() != nil {
			outcome.Skipped += len(considered) - index
			outcome.Unreached = append(outcome.Unreached, considered[index:]...)

			break
		}

		resumed, done := resumeOne(ctx, record, checks, start, started)
		if !done {
			outcome.Skipped++

			// A check or start the end cut short has not decided: the record is kept.
			if ctx.Err() != nil {
				outcome.Unreached = append(outcome.Unreached, record)
			}

			continue
		}

		outcome.Resumed = append(outcome.Resumed, resumed)
	}

	return outcome
}

// resumeOne starts the session record's owner rebuilds for it, unless one the same, or one on the
// same Claude session, was started already, and is it and true; or false when it started nothing.
func resumeOne(ctx context.Context, record Record, checks Checks, start Start,
	started map[string]bool,
) (Resumed, bool) {
	spec, vouched := vouch(ctx, record, checks)
	if !vouched {
		return Resumed{}, false
	}

	keys := []string{sessionKey(spec)}
	if record.ClaudeSessionID != "" {
		keys = append(keys, claudeKey(record.ClaudeSessionID))
	}

	for _, key := range keys {
		if started[key] {
			return Resumed{}, false
		}
	}

	for _, key := range keys {
		started[key] = true
	}

	hostedID, ok := start(ctx, record, spec)
	if !ok {
		return Resumed{}, false
	}

	return Resumed{ID: hostedID, Name: spec.Name}, true
}

// vouch is the session record's owner rebuilds for it, and whether there is one.
func vouch(ctx context.Context, record Record, checks Checks) (pty.Spec, bool) {
	if CheckRecord(record) != nil {
		return pty.Spec{}, false
	}

	check := checks[record.Owner]
	if check == nil {
		return pty.Spec{}, false
	}

	return check(ctx, record)
}

// claudeKey is the started key of a Claude session: never a sessionKey, which starts with a folder,
// and a folder is absolute.
func claudeKey(sessionID string) string { return "claude\x00" + sessionID }

// sessionKey is what makes two specs the same session: the folder and every argument.
func sessionKey(spec pty.Spec) string {
	return spec.Dir + "\x00" + strings.Join(spec.Argv, "\x00")
}
