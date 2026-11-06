package openwork

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
)

// A run's build sessions. Each sits in a slot: a leg that has a session, the lead in the shared
// layout and every leg in the split one. Only a slot has a session, a Claude session id and a
// command to paste. Every session is named after the ticket key; in a run of several legs a
// session is found by its folder alone, since a name finds any of them.

// repoNotName is the RESUME SESSION refusal of a repo that is not text.
const repoNotName = "repo must be a repository name"

// sessionSlots are the legs that have a build session: the lead in the shared layout, every leg
// in the split one.
func (w *Work) sessionSlots(record *Record) []*Leg {
	if record.Layout == layoutSplit {
		return record.Repos
	}

	return []*Leg{record.lead()}
}

func (w *Work) isSlot(record *Record, leg *Leg) bool {
	return slices.Contains(w.sessionSlots(record), leg)
}

// worktreesOf are every leg's worktree, in order.
func worktreesOf(record *Record) []string {
	worktrees := make([]string, 0, len(record.Repos))
	for _, leg := range record.Repos {
		worktrees = append(worktrees, leg.Worktree)
	}

	return worktrees
}

// reachFlags are what a slot's claude command adds so its session reaches the other worktrees:
// the shared session is given each other leg's with --add-dir. The paths are single-quoted, one
// word whatever they hold; each flag sits before another flag, since --add-dir takes every word
// up to the next one.
func reachFlags(record *Record, slot *Leg) string {
	if record.Layout == layoutSplit {
		return ""
	}

	if slot != record.lead() {
		return ""
	}

	flags := strings.Builder{}
	for _, worktree := range otherWorktrees(record) {
		flags.WriteString(" --add-dir '" + strings.ReplaceAll(worktree, "'", `'\''`) + "'")
	}

	return flags.String()
}

// findSession is a slot's live session: in a run of one leg, by the ticket key or its folder, as
// always; in a run of several, by its folder alone.
func (w *Work) findSession(record *Record, slot *Leg) *Found {
	name := record.Key
	if len(record.Repos) > 1 {
		name = ""
	}

	return w.deps.FindSession(name, slot.Worktree)
}

// sessionNamed is how the log names a slot's session: by the ticket key, and its repository when
// the run has several sessions.
func (w *Work) sessionNamed(record *Record, slot *Leg) string {
	if len(w.sessionSlots(record)) == 1 {
		return "Session " + record.Key
	}

	return "Session " + record.Key + " in " + slot.Repo
}

// slotSaid is a message about one slot: with its repository first when the run has several
// sessions.
func (w *Work) slotSaid(record *Record, slot *Leg, message string) string {
	if len(w.sessionSlots(record)) == 1 {
		return message
	}

	return slot.Repo + ": " + message
}

// slotProblem is why a slot's session may not start now, "" when it may: the shared session
// reaches every leg, so any leg's problem is its; a split session's is its own leg's.
func (w *Work) slotProblem(record *Record, slot *Leg) string {
	if record.Layout == layoutSplit {
		return w.buildProblem(slot)
	}

	return w.runProblem(record)
}

// slotKey keys what is kept per slot: the run and the slot's repository.
func slotKey(record *Record, slot *Leg) string {
	return record.Key + " " + js.Lower(slot.Repo)
}

// openWorkspace starts command in a new cmux workspace named after the ticket, in the slot's
// worktree; when cmux fails, it leaves the command on the slot for the person to paste.
func (w *Work) openWorkspace(step *task, record *Record, slot *Leg, command, verb string) {
	var opened struct {
		ok             bool
		why, toCommand string
	}

	step.outside(func() {
		result := w.deps.Open(w.ctx, record.Key, slot.Worktree, command)
		opened.ok, opened.why, opened.toCommand = result.OK, result.Error, result.ManualCommand
	})

	if opened.ok {
		slot.ManualCommand = nil

		w.log(record, logBuild, w.sessionNamed(record, slot)+" "+verb+" in a new cmux workspace")

		return
	}

	where := ""
	if len(w.sessionSlots(record)) > 1 {
		where = " for " + slot.Repo
	}

	slot.ManualCommand = &opened.toCommand
	w.log(record, logError, "Could not open a cmux workspace"+where+" ("+opened.why+"). Run "+
		"the command shown to start the session.")
}

// resumeRepo is the repository a RESUME SESSION names: "" for every slot.
func resumeRepo(body *js.Object) (string, bool) {
	value := field(body, "repo")
	if value == nil {
		return "", true
	}

	repo, isText := value.(string)

	return repo, isText
}

// Resume reopens the build sessions that exited, each on the same conversation when its id was
// captured: the session of the leg in repo, or with no repo every slot whose session is gone.
// Each slot is refused on its own; a slot that needs nothing (its session runs, its worktree is
// cleaned up) is passed over unless it is the one named, and a slot refused while others open
// is said in the log.
func (w *Work) Resume(key, repo string) Reply {
	w.mu.Lock()
	defer w.mu.Unlock()

	record, found := w.runs.Get(key)
	if !found {
		return notFound("no run for " + key)
	}

	slots, refusal, refused := w.resumeCandidates(record, repo)
	if refused {
		return refusal
	}

	slots, said := w.sift(record, slots, func(slot *Leg) (string, bool) {
		if slot.cleaned() {
			return key + " is cleaned up: nothing to resume", true
		}

		return "", false
	})
	if len(slots) == 0 {
		return conflict(strings.Join(said.all, " "))
	}

	if refusal, refused := w.resumeRefusal(record); refused {
		return refusal
	}

	if w.deps.Dry {
		for _, slot := range slots {
			w.log(record, logBuild, w.sessionNamed(record, slot)+" resumed (dry run)")
		}

		return ok()
	}

	return w.reopen(record, slots)
}

// resumeCandidates are the slots a RESUME SESSION asks for: every slot, or the one of repo.
func (w *Work) resumeCandidates(record *Record, repo string) ([]*Leg, Reply, bool) {
	if repo == "" {
		return w.sessionSlots(record), Reply{}, false
	}

	leg, found := record.legFor(repo)
	if !found {
		return nil, notFound(record.Key + " does not build in " + repo), true
	}

	if !w.isSlot(record, leg) {
		return nil, conflict(leg.Repo + " has no session of its own: the run's session works " +
			"in " + record.lead().Repo), true
	}

	return []*Leg{leg}, Reply{}, false
}

// refusals are what sift said of the slots it left out: every one, and those to tell the person
// even when other slots go on (the ones that needed nothing are not).
type refusals struct{ all, loud []string }

// sift are the slots check passes, and what it said of the others. check is why a slot may not
// go on, "" when it may, and whether that is only that it needs nothing.
func (w *Work) sift(record *Record, slots []*Leg, check func(slot *Leg) (string, bool),
) ([]*Leg, refusals) {
	kept := make([]*Leg, 0, len(slots))
	said := refusals{all: []string{}, loud: []string{}}

	for _, slot := range slots {
		message, needsNothing := check(slot)
		if message == "" {
			kept = append(kept, slot)

			continue
		}

		message = w.slotSaid(record, slot, message)
		said.all = append(said.all, message)

		if !needsNothing {
			said.loud = append(said.loud, message)
		}
	}

	return kept, said
}

func (w *Work) resumeRefusal(record *Record) (Reply, bool) {
	key := record.Key

	resumable := record.State == stateBuilding
	if record.State == statePROpen {
		resumable = true
	}

	if !resumable {
		return conflict(key + " is " + record.State + ": nothing to resume"), true
	}

	if w.busyPR[key] {
		return conflict("another change to this run is in progress"), true
	}

	return Reply{}, false
}

// reopen opens a new session in each slot that may have one now, and says why the others may
// not.
func (w *Work) reopen(record *Record, slots []*Leg) Reply {
	key := record.Key

	slots, said := w.sift(record, slots, func(slot *Leg) (string, bool) {
		return w.slotLiveRefusal(record, slot)
	})
	if len(slots) == 0 {
		return conflict(strings.Join(said.all, " "))
	}

	// Held across the cmux calls so a cleanup or a second resume cannot interleave with them.
	w.busyPR[key] = true

	for _, slot := range slots {
		command, verb := resumeCommand(record, slot)
		w.resumedAt[slotKey(record, slot)] = w.now()

		w.openWorkspace(w.held(), record, slot, command, verb)
	}

	delete(w.busyPR, key)

	for _, message := range said.loud {
		w.log(record, logError, message)
	}

	w.touch(record)

	return ok()
}

// slotLiveRefusal is why a slot's session may not be reopened now, "" when it may, and whether
// that is only that its session runs.
func (w *Work) slotLiveRefusal(record *Record, slot *Leg) (string, bool) {
	if w.findSession(record, slot) != nil {
		return "a session for " + record.Key + " is already running", true
	}

	// While the first session is still opening it is not in the session list yet, and a second
	// one would share its conversation.
	if record.State == stateBuilding {
		if sessionText(slot.ClaudeSessionID) == "" {
			return "the session has not started yet: wait for it to open", false
		}
	}

	if w.now()-w.resumedAt[slotKey(record, slot)] < resumeGapMs {
		return "a session was just opened for this run: give it a few seconds to appear", false
	}

	if !exists(slot.Worktree) {
		return slot.Worktree + " no longer exists", false
	}

	return w.slotProblem(record, slot), false
}

func sessionText(id *string) string {
	if id == nil {
		return ""
	}

	return *id
}

// resumeCommand is the command that reopens a slot's session, and what the log says it did. The
// id comes from a file on disk: only a plain UUID is allowed into a shell command.
func resumeCommand(record *Record, slot *Leg) (string, string) {
	rule := ""
	if exists(filepath.Join(slot.Worktree, connFolder, delegationFile)) {
		rule = " --append-system-prompt-file .conn/delegation.md"
	}

	tail := " --name " + record.Key + " --model sonnet" + rule + reachFlags(record, slot) +
		" --permission-mode acceptEdits"

	id := sessionText(slot.ClaudeSessionID)
	if sessionUUID.MatchString(id) {
		return "claude --resume " + id + tail, "resumed"
	}

	return "claude --continue" + tail, "reopened on its last conversation"
}
