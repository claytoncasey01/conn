package sessions

import "github.com/claytoncasey01/conn/internal/snapshot"

// RunFacts are what the archive reads of an Open work run: its state and its PR's state.
type RunFacts struct {
	State   string
	PRState string
}

// ArchiveSource is what the archive is told about a live session: plain facts, so the archive
// needs nothing of the session list.
type ArchiveSource struct {
	SessionID string
	PID       float64
	// Name is the registry name; "" when the session has none.
	Name           string
	Cwd            string
	TranscriptPath string
	StartedAt      float64
	Parts          snapshot.Parts
	PR             *PR
	Kind           string
	// Run is the Open work run the session belongs to, when it is a build session.
	Run *RunFacts
}

// Archive keeps sessions in History. Checkpoint and Claimed run on the loop's goroutine; Ended
// queues the full capture and returns.
type Archive interface {
	// Checkpoint writes the capped live state (at most once a minute per session, and only when
	// it changed).
	Checkpoint(source ArchiveSource, now float64)
	// Claimed takes back a session that turned out to be a teammate: its lead's entry holds it.
	Claimed(sessionID string)
	// Ended replays a finished session's transcript in full and replaces its checkpoint.
	Ended(source ArchiveSource)
}
