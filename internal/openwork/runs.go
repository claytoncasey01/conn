package openwork

import (
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/sessions"
)

// View is one run as the snapshot shows it, with what the session list reads of it. It is
// written to JSON when it is made, under the mutex, so the snapshot never reads a run that is
// changing.
type View struct {
	key       string
	worktrees []string
	data      []byte
	facts     sessions.RunFacts
}

// Key is the run's ticket key: its build session is named after it.
func (v View) Key() string { return v.key }

// Worktree is the folder the run's first repository builds in, where its shared session works.
func (v View) Worktree() string { return v.worktrees[0] }

// Worktrees are the folders of every repository the run builds in, the first's first.
func (v View) Worktrees() []string { return slices.Clone(v.worktrees) }

// Facts are the run's state and its PR's state, for the archive.
func (v View) Facts() *sessions.RunFacts {
	facts := v.facts

	return &facts
}

// MarshalJSON writes the run as the page reads it.
func (v View) MarshalJSON() ([]byte, error) { return v.data, nil }

// Runs are the runs, newest first; none while Open work is off (and so no ticket sessions).
func (w *Work) Runs() []sessions.Run {
	w.mu.Lock()
	defer w.mu.Unlock()

	out := []sessions.Run{}

	if !w.on() {
		return out
	}

	for _, record := range w.snapshot() {
		prState := historyPRState(record)

		out = append(out, View{
			key: record.Key, worktrees: worktreesOf(record), data: record.viewJSON(),
			facts: sessions.RunFacts{State: record.State, PRState: prState},
		})
	}

	return out
}

// historyPRState is the PR state History shows for a run: merged once the run counts as merged
// (merged), else its lead's, as a run of one always showed.
func historyPRState(record *Record) string {
	if merged(record) {
		return prMerged
	}

	lead := record.lead()
	if lead.PR == nil {
		return ""
	}

	if lead.PR.Status == nil {
		return ""
	}

	return lead.PR.Status.State
}

// snapshot are the runs, newest first.
func (w *Work) snapshot() []*Record {
	records := make([]*Record, 0, len(w.runs.Keys()))

	for _, key := range w.runs.Keys() {
		record, _ := w.runs.Get(key)
		records = append(records, record)
	}

	js.Sort(records, func(left, right *Record) float64 { return right.StartedAt - left.StartedAt })

	return records
}

// touch saves a run and tells the page. Once the server is stopping nothing more is saved: what
// was saved before the stop is what the next boot reads, a stage cut off reading as interrupted.
func (w *Work) touch(record *Record) {
	if w.ctx.Err() != nil {
		return
	}

	record.UpdatedAt = w.now()
	writeRecord(w.runsDir, record)
	w.bump()
}

// log adds a line to a run's log, keeping the last 200, and saves it.
func (w *Work) log(record *Record, kind, text string) {
	record.Log = append(record.Log, LogLine{At: w.now(), Kind: kind, Text: text})
	if len(record.Log) > maxLogLines {
		record.Log = append([]LogLine{}, record.Log[len(record.Log)-maxLogLines:]...)
	}

	w.touch(record)
}

func (w *Work) fail(record *Record, message string) {
	record.State = stateFailed
	record.Error = &message
	w.log(record, logError, message)
}

// live is false once the run was cancelled or interrupted: every stage checks before it writes,
// so whatever was in flight for it ends at its next check.
func live(record *Record) bool {
	switch record.State {
	case stateCancelled, stateInterrupted:
		return false
	}

	return true
}

// repoOf is the repository a leg is in: the one it recorded, else (a run from before Settings)
// its worktree's origin; "" when neither says. Only a worktree that exists says: the origin
// lookup walks up from a missing path, and would name a repository that merely encloses it.
func (w *Work) repoOf(leg *Leg) string {
	if leg.Repo != "" {
		return leg.Repo
	}

	if !exists(leg.Worktree) {
		return ""
	}

	return w.deps.OriginOf(leg.Worktree)
}

func exists(path string) bool {
	if path == "" {
		return false
	}

	_, err := os.Stat(path)

	return err == nil
}

// buildProblem is why this leg may not research or build now, "" when it may: its repository
// has left the allowlist, or Settings finds no clone of it. Asked on every path that starts a
// helper run or a session, whatever was done before: a run made earlier is not a way around a
// list changed since.
func (w *Work) buildProblem(leg *Leg) string {
	_, problem := w.cloneOf(leg)

	return problem
}

// cloneOf is the clone a leg builds from now, looked up again in Settings, never taken from the
// record; or why there is none. A run from before Settings whose worktree is not made says no
// repository: it was made for the default repository.
func (w *Work) cloneOf(leg *Leg) (string, string) {
	current := w.settings()

	repo := w.repoOf(leg)
	if repo == "" {
		if current.Atlassian.Jira.BuildRepo == nil {
			return "", noRepo
		}

		repo = *current.Atlassian.Jira.BuildRepo
	}

	if !w.allowed(repo) {
		return "", notAllowed(repo)
	}

	clone, found := w.deps.CloneFor(repo, current)
	if !found {
		return "", "No local clone of " + repo + ": set a folder in Settings."
	}

	return clone, ""
}

// runProblem is why a run may not research or build now, "" when it may: every leg's problem,
// one sentence each.
func (w *Work) runProblem(record *Record) string {
	problems := []string{}

	for _, leg := range record.Repos {
		if problem := w.buildProblem(leg); problem != "" {
			problems = append(problems, problem)
		}
	}

	return strings.Join(problems, " ")
}

func reasonOr(reason string) string {
	if reason == "" {
		return noClone
	}

	return reason
}

// ok and bad are the answers to the page.
func ok() Reply {
	body := js.NewObject()
	body.Set("ok", true)

	return Reply{Code: http.StatusOK, Body: body}
}

func notFound(message string) Reply { return bad(http.StatusNotFound, message) }

func conflict(message string) Reply { return bad(http.StatusConflict, message) }

func badRequest(message string) Reply { return bad(http.StatusBadRequest, message) }

func bad(code int, message string) Reply {
	body := js.NewObject()
	body.Set("error", message)

	return Reply{Code: code, Body: body}
}
