package openwork

import (
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/settings"
)

// A run's build sessions. Each sits in a slot: a leg that has a session, the lead in the shared
// layout and every leg in the split one. Only a slot has a session, a Claude session id and a
// command to paste. Every session is named after the ticket key; in a run of several legs a
// session is found by its folder alone, since a name finds any of them.

// repoNotName is the refusal of a repo that is not text, in the body of an action on legs.
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
func worktreesOf(record *Record) []string { return legWorktrees(record.Repos) }

// reachArgs are what a slot's claude arguments add so its session reaches the other worktrees:
// the shared session is given each other leg's still there with --add-dir, each folder one
// argument. They sit before another option, since --add-dir takes every argument up to the next
// one.
func reachArgs(record *Record, slot *Leg) []string {
	if record.Layout == layoutSplit {
		return nil
	}

	if slot != record.lead() {
		return nil
	}

	var args []string

	for _, leg := range record.Repos[1:] {
		// A worktree removed (cleaned up, or gone from the disk) is nothing to reach.
		if leg.cleaned() {
			continue
		}

		if !exists(leg.Worktree) {
			continue
		}

		args = append(args, "--add-dir", leg.Worktree)
	}

	return args
}

// findSession is a slot's live session: in a run of one leg, by the ticket key or its folder, as
// always; in a run of several, by its folder alone.
func (w *Work) findSession(record *Record, slot *Leg) *Found {
	name := record.Key
	if len(record.Repos) > 1 {
		name = ""
	}

	// The folder must be the worktree itself, not one inside it: a session's cwd is the folder it
	// was launched in, and Conn launches it there (the Sessions page matches with within).
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

// launchProblem is why no build session of the run may start now, "" when one may: the shared
// session reaches every leg, so any leg's problem stops it; the split sessions start as long as
// one leg may build, and the problems of all are said when none may.
func (w *Work) launchProblem(record *Record) string {
	if record.Layout != layoutSplit {
		return w.runProblem(record)
	}

	for _, leg := range record.Repos {
		if w.buildProblem(leg) == "" {
			return ""
		}
	}

	return w.runProblem(record)
}

// launched is whether a session was ever started in a slot. Only a split run's leg is ever left
// unlaunched, and that is recorded since it can be: a shared run's slot, or one with a Claude id
// or a command to paste, was launched.
func launched(record *Record, slot *Leg) bool {
	if slot.SessionLaunched {
		return true
	}

	if record.Layout != layoutSplit {
		return true
	}

	if slot.ClaudeSessionID != nil {
		return true
	}

	return slot.ManualCommand != nil
}

// markLaunched records that a slot's session was started. Only the split layout can leave a slot
// unlaunched, so only it records the fact: a shared run's file stays as it always was.
func markLaunched(record *Record, slot *Leg) {
	if record.Layout == layoutSplit {
		slot.SessionLaunched = true
	}
}

// slotKey keys what is kept per slot: the run and the slot's repository.
func slotKey(record *Record, slot *Leg) string { return legKey(record.Key, slot.Repo) }

// openWorkspace starts a slot's spec where the settings say sessions open, named after the
// ticket, in the slot's worktree; when it does not start, it leaves the command on the slot for
// the person to paste.
func (w *Work) openWorkspace(step *task, record *Record, slot *Leg, spec pty.Spec, verb string) {
	var opened launch.Opened

	step.outside(func() { opened = w.deps.Open(w.ctx, spec) })

	if opened.OK {
		slot.ManualCommand = nil

		w.log(record, logBuild, w.sessionNamed(record, slot)+" "+verb+" "+opened.Place)

		return
	}

	where := ""
	if len(w.sessionSlots(record)) > 1 {
		where = " for " + slot.Repo
	}

	slot.ManualCommand = &opened.ManualCommand

	if opened.Where == settings.WhereCommand {
		w.log(record, logBuild, "Run the command shown"+where+" to start the session: "+
			"sessions open as a command (Settings).")

		return
	}

	w.log(record, logError, "Could not open "+opened.Tried+where+" ("+opened.Error+"). Run "+
		"the command shown to start the session.")
}

// bodyRepo is the repository a RESUME SESSION, OPEN PR, FIX FAILING CHECKS or REMOVE WORKTREE
// names: "" for every leg it applies to, and false when it is not text.
func bodyRepo(body *js.Object) (string, bool) {
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
		return conflict(strings.Join(said.all, "; "))
	}

	if refusal, refused := w.resumeRefusal(record); refused {
		return refusal
	}

	if w.deps.Dry {
		for _, slot := range slots {
			w.log(record, logBuild, w.sessionNamed(record, slot)+" resumed (dry run)")
			w.openDry(w.held(), record, slot)
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

	if w.runBusy(record) {
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
		return conflict(strings.Join(said.all, "; "))
	}

	// Held across the starts so a cleanup or a second resume cannot interleave with them.
	release := w.hold(key, record.Repos)

	opened := 0
	failures := []string{}

	for _, slot := range slots {
		err := w.reopenSlot(record, slot)
		if err != nil {
			failures = append(failures, w.slotSaid(record, slot, err.Error()))

			continue
		}

		opened++
	}

	release()

	loud := slices.Concat(said.loud, failures)

	if opened == 0 {
		w.touch(record)

		return conflict(strings.Join(loud, "; "))
	}

	for _, message := range loud {
		w.log(record, logError, message)
	}

	w.touch(record)

	return ok()
}

// reopenSlot opens a slot's session again, or for the first time when it was left unlaunched.
// The gap before another is opened starts once cmux is asked: a launch that failed before that
// may be retried at once.
func (w *Work) reopenSlot(record *Record, slot *Leg) error {
	if launched(record, slot) {
		w.resumedAt[slotKey(record, slot)] = w.now()
		spec, verb := ResumeSpec(w.deps.Claude, record, slot)
		w.openWorkspace(w.held(), record, slot, spec, verb)

		return nil
	}

	return w.launchFresh(record, slot)
}

// launchFresh starts a slot left unlaunched when the run's other sessions opened: its brief and
// excluded .conn/ as the launch makes them, then the launch's command. A brief that cannot be
// written is the error, and nothing is started.
func (w *Work) launchFresh(record *Record, slot *Leg) error {
	err := writeBrief(record, slot, briefFor(record, slot))
	if err != nil {
		return err
	}

	worktree := slot.Worktree

	w.held().outside(func() { reviews.ExcludeConn(w.ctx, w.excludeDeps(), worktree) })

	w.resumedAt[slotKey(record, slot)] = w.now()
	markLaunched(record, slot)
	w.openWorkspace(w.held(), record, slot, LaunchSpec(w.deps.Claude, record, slot), "opened")

	return nil
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
			if launched(record, slot) {
				return "the session has not started yet: wait for it to open", false
			}
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

// ResumeSpec is what reopens a slot's session, and what the log says it did. The id comes from a
// file on disk: only a plain UUID is passed to --resume, else the folder's last conversation is
// continued.
func ResumeSpec(claude string, record *Record, slot *Leg) (pty.Spec, string) {
	argv := []string{claude}
	verb := "reopened on its last conversation"

	id := sessionText(slot.ClaudeSessionID)
	if sessionUUID.MatchString(id) {
		argv = append(argv, "--resume", id)
		verb = "resumed"
	} else {
		argv = append(argv, "--continue")
	}

	argv = append(argv, "--name", record.Key, "--model", "sonnet")

	if ownDelegation(slot.Worktree) {
		argv = append(argv, "--append-system-prompt-file", ".conn/delegation.md")
	}

	argv = append(argv, reachArgs(record, slot)...)
	argv = append(argv, "--permission-mode", "acceptEdits")

	return pty.Spec{Argv: argv, Dir: slot.Worktree, Env: nil, Name: record.Key}, verb
}

// ownDelegation is whether the worktree holds a delegation.md claude may be pointed at: a regular
// file, in a .conn folder of the person's own, reached with no link followed, holding exactly
// Conn's Delegation. A repository can commit .conn, or the file, as a link, and claude would read
// wherever it points as its rules; a checkout or a stash can bring back a committed one, and the
// session can write to it, and claude would take that text as its rules too.
func ownDelegation(worktree string) bool {
	file, err := reviews.OpenInOwnFolder(filepath.Join(worktree, connFolder), delegationFile)
	if err != nil {
		return false
	}

	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return false
	}

	if !info.Mode().IsRegular() {
		return false
	}

	// One byte past Conn's text is enough to tell a longer file apart.
	text, err := io.ReadAll(io.LimitReader(file, int64(len(Delegation))+1))
	if err != nil {
		return false
	}

	return string(text) == Delegation
}
