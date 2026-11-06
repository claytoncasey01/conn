package openwork

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/claytoncasey01/conn/internal/prstatus"
	"github.com/claytoncasey01/conn/internal/settings"
)

// noPRWatched and notFailing are why FIX FAILING CHECKS has nothing to do in a leg.
const (
	noPRWatched = " has no open PR being watched"
	notFailing  = "the checks are not failing"
)

// FixChecks asks the build sessions to fix red checks: of the leg in repo, or with no repo of
// every leg whose checks are failing. Each instruction names its leg's PR, repository and failed
// checks, and is typed into the session that works in that leg's folder, never another's. The
// session is told where to read them; CI output is not pasted in.
func (w *Work) FixChecks(key, repo string) (Reply, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	record, found := w.runs.Get(key)
	if !found {
		return notFound("no run for " + key), nil
	}

	asked, refusal, refused := legsAsked(record, repo)
	if refused {
		return refusal, nil
	}

	sessions := map[*Leg]*Found{}

	legs, said := siftLegs(asked, func(leg *Leg) (Reply, bool, bool) {
		return w.fixLegRefusal(record, leg, sessions)
	})
	if len(legs) == 0 {
		return refusedAll(record, said), nil
	}

	w.logRefused(record, said)

	if w.deps.Dry {
		for _, leg := range legs {
			polled := legKey(key, leg.Repo)
			w.dryFixed[polled] = true
			w.dryPolls[polled] = 0
			delete(w.lastPoll, polled)
			w.log(record, logBuild, legSaid(record, leg,
				"Asked the session to fix the failing checks (dry run)"))
		}

		return ok(), nil
	}

	return w.askToFix(record, legs, sessions)
}

// fixLegRefusal is why a leg's session may not be asked to fix its checks now, and whether that
// is only that its checks are not failing. The session found working in the leg's folder is put
// in sessions.
func (w *Work) fixLegRefusal(record *Record, leg *Leg, sessions map[*Leg]*Found,
) (Reply, bool, bool) {
	if reply, quiet := fixQuiet(record.Key, record.State, leg); quiet {
		return reply, true, true
	}

	if reply, _, refused := w.fixRefusal(leg); refused {
		return reply, true, false
	}

	if w.deps.Dry {
		return Reply{}, false, false
	}

	if w.legBusy(record.Key, leg) {
		return conflict("another change to this run is in progress"), true, false
	}

	worker := w.workerOf(record, leg)

	session := w.findSession(record, worker)
	if session == nil {
		return conflict("no session for " + record.Key + " is running; open one in " +
			worker.Worktree + " first"), true, false
	}

	sessions[leg] = session

	return Reply{}, false, false
}

// fixQuiet refuses FIX FAILING CHECKS for a leg whose checks are not failing.
func fixQuiet(key, state string, leg *Leg) (Reply, bool) {
	if state != statePROpen {
		return conflict(key + noPRWatched), true
	}

	if leg.PR == nil {
		return conflict(key + noPRWatched), true
	}

	status := leg.PR.Status
	if status == nil {
		return conflict(key + noPRWatched), true
	}

	if status.Checks.State != checksFailing {
		return conflict(notFailing), true
	}

	if finished(status.State) {
		return conflict(notFailing), true
	}

	return Reply{}, false
}

// fixRefusal refuses FIX FAILING CHECKS where its PR may not be read now. The state shown is
// only as fresh as the poll, which stops for a repository off the list or with GitHub off: the
// session is sent to read that PR on GitHub, so both must hold now. The repository named to it
// is the PR's own (its URL), checked like the poll's.
func (w *Work) fixRefusal(leg *Leg) (Reply, string, bool) {
	if !w.moduleEnabled(settings.ModuleGitHub) {
		return conflict(githubOff), "", true
	}

	repo := PRURLRepo(leg.PR.URL)
	if !w.prRepoAllowed(leg, repo) {
		named := repo
		if named == "" {
			named = "this repository"
			if leg.Repo != "" {
				named = leg.Repo
			}
		}

		return conflict(named + " is not an allowed repository: add it in Settings."), "", true
	}

	return Reply{}, repo, false
}

// askToFix types each leg's instruction into the session working in its folder, one after
// another, the lock let go for each. A leg whose session did not take it is said in the log
// while others did; when none did, the answer says why.
func (w *Work) askToFix(record *Record, legs []*Leg, sessions map[*Leg]*Found,
) (Reply, error) {
	asked := 0
	failed := []legRefusal{}

	for _, leg := range legs {
		reply, err := w.askLegToFix(record, leg, sessions[leg])
		if err != nil {
			return Reply{}, err
		}

		if reply.Code != http.StatusOK {
			failed = append(failed, legRefusal{leg: leg, reply: reply, needsNothing: false})

			continue
		}

		asked++
	}

	if asked == 0 {
		return refusedAll(record, failed), nil
	}

	w.logRefused(record, failed)

	return ok(), nil
}

// askLegToFix types a leg's instruction into session. Settings may have changed while an
// earlier leg's was typed, so its PR is checked again.
func (w *Work) askLegToFix(record *Record, leg *Leg, session *Found) (Reply, error) {
	refusal, repo, refused := w.fixRefusal(leg)
	if refused {
		return refusal, nil
	}

	status := leg.PR.Status

	message, err := prstatus.FixChecksMessage(leg.PR.Number, status.Checks.Failed, repo)
	if err != nil {
		return Reply{}, fmt.Errorf("asking the session to fix the checks: %w", err)
	}

	// Held so a second click cannot queue the same instruction twice.
	release := w.hold(record.Key, []*Leg{leg})

	var reply Reply

	w.held().outside(func() { reply = w.deps.Reply(w.ctx, session.ID, message) })
	release()

	if reply.Code != http.StatusOK {
		return reply, nil
	}

	w.log(record, logBuild, legSaid(record, leg, "Asked the session to fix the failing checks: "+
		failedOr(status.Checks.Failed)))

	return ok(), nil
}

func failedOr(failed []string) string {
	if len(failed) == 0 {
		return "see GitHub"
	}

	return strings.Join(failed, ", ")
}
