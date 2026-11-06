package openwork

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
)

// prStillOpen and alreadyRemoved are why REMOVE WORKTREE has nothing to do in a leg.
const (
	prStillOpen    = "the PR is still open"
	alreadyRemoved = "the worktree is already removed"
)

// MoveTicket moves the ticket to the status the person picks, after the PRs merge. Never
// automatic, and never "Done" by assumption.
func (w *Work) MoveTicket(key string, status any) Reply {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.on() {
		return notFound(jira.Off)
	}

	record, found := w.runs.Get(key)
	if !found {
		return notFound("no run for " + key)
	}

	if !merged(record) {
		return conflict("the PR has not merged")
	}

	name, isText := status.(string)
	if !isText {
		return badRequest("give the Jira status name to move to")
	}

	target := js.Trim(name)
	if !statusName.MatchString(target) {
		return badRequest("give the Jira status name to move to")
	}

	if w.runBusy(record) {
		return conflict("another change to this run is in progress")
	}

	release := w.hold(key, record.Repos)

	w.launch(func(step *task) {
		defer release()

		if !w.deps.Dry {
			w.moveJira(step, record, jira.MoveTo(target))

			return
		}

		step.outside(func() { w.deps.Sleep(w.ctx, dryMoveWait) })

		record.JiraStatus = target
		record.TicketStatus = target
		w.log(record, logStatus, "Jira "+record.Key+" is now "+target+" (dry run)")
	})

	return ok()
}

// merged is whether a run counts as merged: every leg's PR is settled (merged, or closed without
// merging) and at least one merged. A leg with no PR does not hold it back.
func merged(record *Record) bool {
	if record.State != statePROpen {
		return false
	}

	mergedAny := false

	for _, leg := range record.Repos {
		if leg.PR == nil {
			continue
		}

		if leg.PR.Status == nil {
			return false
		}

		switch leg.PR.Status.State {
		case prMerged:
			mergedAny = true
		case prClosed:
		default:
			return false
		}
	}

	return mergedAny
}

// Cleanup removes worktrees once their PRs are done: the leg's in repo, or with no repo every
// leg whose PR merged or closed and whose worktree is still there. Each is refused while
// anything could be lost (a live session, uncommitted files), and on its own.
func (w *Work) Cleanup(key, repo string) Reply {
	w.mu.Lock()
	defer w.mu.Unlock()

	record, found := w.runs.Get(key)
	if !found {
		return notFound("no run for " + key)
	}

	asked, refusal, refused := legsAsked(record, repo)
	if refused {
		return refusal
	}

	legs, said := siftLegs(asked, func(leg *Leg) (Reply, bool, bool) {
		return w.cleanupLegRefusal(record, leg)
	})
	if len(legs) == 0 {
		return refusedAll(record, said)
	}

	w.logRefused(record, said)

	if w.deps.Dry {
		for _, leg := range legs {
			cleaned := true
			leg.Cleaned = &cleaned
			w.log(record, logStatus, legSaid(record, leg, "Worktree "+leg.Worktree+
				" removed (dry run). The branch "+leg.Branch+" stays."))
		}

		return ok()
	}

	return w.removeWorktrees(record, legs)
}

// cleanupLegRefusal is why a leg's worktree may not be removed now, and whether that is only
// that its PR is open or it is removed already.
func (w *Work) cleanupLegRefusal(record *Record, leg *Leg) (Reply, bool, bool) {
	if reply, quiet := cleanupQuiet(record.State, leg); quiet {
		return reply, true, true
	}

	if reply, refused := cleanupRefusal(leg); refused {
		return reply, true, false
	}

	// A repository that left the list is not touched, as no push or session is.
	if repo := w.repoOf(leg); repo != "" {
		if !w.allowed(repo) {
			return conflict(notAllowed(repo)), true, false
		}
	}

	if w.legBusy(record.Key, leg) {
		return conflict("another change to this run is in progress"), true, false
	}

	if w.deps.Dry {
		return Reply{}, false, false
	}

	if w.sessionIn(record, leg) {
		return conflict("the session is still running: close it first"), true, false
	}

	return Reply{}, false, false
}

// sessionIn is whether a live session works in a leg's worktree: one started in it, or the
// session of the slot that reaches it (in the shared layout, the lead's).
func (w *Work) sessionIn(record *Record, leg *Leg) bool {
	if w.findSession(record, leg) != nil {
		return true
	}

	worker := w.workerOf(record, leg)
	if worker == leg {
		return false
	}

	return w.findSession(record, worker) != nil
}

// cleanupQuiet refuses a leg whose PR is open, or whose worktree is removed already.
func cleanupQuiet(runState string, leg *Leg) (Reply, bool) {
	if runState != statePROpen {
		return conflict(prStillOpen), true
	}

	state := ""

	if leg.PR != nil {
		if leg.PR.Status != nil {
			state = leg.PR.Status.State
		}
	}

	if !finished(state) {
		return conflict(prStillOpen), true
	}

	if leg.cleaned() {
		return conflict(alreadyRemoved), true
	}

	return Reply{}, false
}

// cleanupRefusal refuses a path that is not one. The record is read from disk and its path is
// not recomputed (a run made under another naming, or another clone, must still clean up), so
// the path is only ever removed when git itself lists it as a linked worktree of a repository.
func cleanupRefusal(leg *Leg) (Reply, bool) {
	if !strings.HasPrefix(leg.Worktree, "/") {
		return badRequest("refusing to remove " + leg.Worktree), true
	}

	if strings.Contains(leg.Worktree, "\x00") {
		return badRequest("refusing to remove " + leg.Worktree), true
	}

	return Reply{}, false
}

// removeWorktrees removes each leg's worktree, one after another, the lock let go for each git
// call. A leg that could not be removed is said in the log while others were; when none was, the
// answer says why.
func (w *Work) removeWorktrees(record *Record, legs []*Leg) Reply {
	removed := 0
	failed := []legRefusal{}

	for _, leg := range legs {
		release := w.hold(record.Key, []*Leg{leg})
		reply := w.removeWorktree(w.held(), record, leg)

		release()

		if reply.Code != http.StatusOK {
			failed = append(failed, legRefusal{leg: leg, reply: reply, needsNothing: false})

			continue
		}

		removed++
	}

	if removed == 0 {
		return refusedAll(record, failed)
	}

	w.logRefused(record, failed)

	return ok()
}

func (w *Work) removeWorktree(step *task, record *Record, leg *Leg) Reply {
	if exists(leg.Worktree) {
		reply, refused := w.removeLinked(step, leg)
		if refused {
			return reply
		}
	}

	cleaned := true
	leg.Cleaned = &cleaned
	w.log(record, logStatus, legSaid(record, leg, "Worktree "+leg.Worktree+" removed. The "+
		"branch "+leg.Branch+" stays."))

	return ok()
}

func (w *Work) removeLinked(step *task, leg *Leg) (Reply, bool) {
	var (
		repo   string
		linked bool
	)

	step.outside(func() { repo, linked = LinkedWorktreeRepo(w.ctx, leg.Worktree, w.deps.Sh) })

	if !linked {
		return badRequest("refusing to remove " + leg.Worktree + ": it is not a linked " +
			"worktree of a repository"), true
	}

	// It must belong to the repository the leg was made in (a record from before Settings has
	// no clone recorded, and keeps the rule above only).
	if leg.Clone != "" {
		if realPath(repo) != realPath(leg.Clone) {
			return badRequest("refusing to remove " + leg.Worktree + ": it belongs to " +
				"another repository than " + leg.Clone), true
		}
	}

	var (
		out string
		err error
	)

	step.outside(func() {
		out, err = w.sh("git", []string{"status", argPorcelain}, leg.Worktree, gitTimeout)
	})

	if err != nil {
		return bad(http.StatusInternalServerError, err.Error()), true
	}

	if dirty := countLines(out); dirty > 0 {
		plural := "s"
		if dirty == 1 {
			plural = ""
		}

		return conflict(strconv.Itoa(dirty) + " uncommitted file" + plural + " in " +
			leg.Worktree + ": commit or discard them first"), true
	}

	step.outside(func() {
		_, err = w.sh("git", []string{argWorktree, "remove", leg.Worktree}, repo, removeTimeout)
	})

	if err != nil {
		return bad(http.StatusInternalServerError, err.Error()), true
	}

	return Reply{}, false
}
