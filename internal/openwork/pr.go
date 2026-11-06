package openwork

import (
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/prstatus"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	pushTimeout   = 180 * time.Second
	createTimeout = 60 * time.Second
	gitTimeout    = 15 * time.Second
	removeTimeout = 60 * time.Second
	// dryPRWait is how long the dry run's push takes, dryMoveWait its ticket move.
	dryPRWait   = 1200 * time.Millisecond
	dryMoveWait = 800 * time.Millisecond
	// dryPRFirst and dryPRSpread make the dry run's PR number from the ticket's.
	dryPRFirst  = 1700
	dryPRSpread = 90
	prMerged    = "merged"
	prClosed    = "closed"
	githubOff   = "GitHub is off. Enable it in Settings."
	prBodyFile  = "pr-body.md"
)

var (
	// statusName is a Jira status name the person typed.
	statusName = regexp.MustCompile(`^[A-Za-z][\w .&()/-]{0,39}$`)
	// pullNumber is the number in a pull request URL.
	pullNumber = regexp.MustCompile(`/pull/([0-9]+)`)
)

// wellKnown are branch names that are long-lived by habit: a run's own branch is never one of
// them, and Conn never pushes to one.
func wellKnown() []string {
	return []string{"main", "master", "develop", "development", "staging", "hotfix"}
}

// isProtected is whether Conn never pushes the leg's branch: the leg's base and the PR target,
// the clone's default branch, the one Settings names, and the long-lived names by habit.
func (w *Work) isProtected(leg *Leg) bool {
	names := append(wellKnown(), leg.Base, leg.PRBase)

	if configured := w.settings().Atlassian.Jira.BaseBranch; configured != nil {
		if *configured != "" {
			names = append(names, *configured)
		}
	}

	if leg.Clone != "" {
		if branch, known := DefaultBranch(leg.Clone); known {
			names = append(names, branch)
		}
	}

	return slices.Contains(names, leg.Branch)
}

func plainBranch(name string) bool {
	_, valid := settings.ParseBranch(name)

	return valid
}

// OpenPR pushes the run's branch and opens its PR. The record is read from disk: only a branch
// this tool would have made (the ticket's own key, lowercase, then a slug) is pushed, never a
// protected one, and the branch and the bases must each be one plain branch name.
func (w *Work) OpenPR(key string) Reply {
	w.mu.Lock()
	defer w.mu.Unlock()

	record, found := w.runs.Get(key)
	if !found {
		return notFound("no run for " + key)
	}

	if record.State != stateBuilding {
		return conflict(key + " is " + record.State + ", not building")
	}

	if w.busyPR[key] {
		return conflict("a pull request is already being opened")
	}

	leg := record.lead()
	if refusal, refused := w.pushRefusal(record.Key, leg); refused {
		return refusal
	}

	repo := w.repoOf(leg)
	if refusal, refused := w.repoRefusal(leg, repo); refused {
		return refusal
	}

	w.busyPR[key] = true

	w.launch(func(step *task) {
		defer delete(w.busyPR, key)

		err := w.pr(step, record, leg, repo)
		if err != nil {
			message := err.Error()
			record.Error = &message
			w.log(record, logError, "Pull request failed: "+message)
		}
	})

	return ok()
}

func (w *Work) pushRefusal(key string, leg *Leg) (Reply, bool) {
	refusal := badRequest("refusing to push " + leg.Branch)

	for _, name := range []string{leg.Branch, leg.Base, leg.PRBase} {
		if !plainBranch(name) {
			return refusal, true
		}
	}

	if w.isProtected(leg) {
		return refusal, true
	}

	if !strings.HasPrefix(leg.Branch, strings.ToLower(key)+"-") {
		return refusal, true
	}

	// Opening the PR is a gh call.
	if !w.moduleEnabled(settings.ModuleGitHub) {
		return conflict(githubOff), true
	}

	return Reply{}, false
}

// repoRefusal refuses a push to a repository off the allowlist: the leg's (a run from before
// Settings has none recorded: its worktree's origin says) must still be on it.
func (w *Work) repoRefusal(leg *Leg, repo string) (Reply, bool) {
	if w.deps.Dry {
		return w.dryRepoRefusal(leg)
	}

	if repo == "" {
		return conflict("Could not tell which repository " + leg.Worktree + " belongs to."), true
	}

	if !w.allowed(repo) {
		return conflict(repo + " is not an allowed repository: add it in Settings."), true
	}

	return Reply{}, false
}

// dryRepoRefusal refuses the dry run's push only for a recorded repository off the list.
func (w *Work) dryRepoRefusal(leg *Leg) (Reply, bool) {
	if leg.Repo == "" {
		return Reply{}, false
	}

	if w.allowed(leg.Repo) {
		return Reply{}, false
	}

	return conflict(leg.Repo + " is not an allowed repository: add it in Settings."), true
}

// pr pushes the branch and opens its PR in repo, the repository OpenPR checked against the
// allowlist (named to gh, never left to it).
func (w *Work) pr(step *task, record *Record, leg *Leg, repo string) error {
	record.Error = nil

	if w.deps.Dry {
		w.dryPR(step, record, leg)

		return nil
	}

	if repo == "" {
		return textError("Could not tell which repository " + leg.Worktree + " belongs to.")
	}

	ahead, err := w.commitsAhead(step, leg)
	if err != nil {
		return err
	}

	if ahead < 1 {
		return textError("the branch has no commits yet")
	}

	w.log(record, logPR, "Pushing "+leg.Branch)

	step.outside(func() {
		_, err = w.sh("git", []string{"push", "-u", "origin", leg.Branch}, leg.Worktree,
			pushTimeout)
	})

	if err != nil {
		return err
	}

	bodyFile, err := w.prBody(step, leg)
	if err != nil {
		return err
	}

	// The push can take minutes (and the body's git call more): Settings may have changed
	// meanwhile, and opening the PR is a GitHub write to that repository, so this is checked
	// last, right before it.
	if after := w.prRefusal(repo); after != "" {
		return textError(after + ". " + leg.Branch + " was pushed, and no pull request was " +
			"opened: press OPEN PR again once that is fixed.")
	}

	return w.createPR(step, record, leg, repo, bodyFile)
}

func (w *Work) prRefusal(repo string) string {
	if !w.on() {
		return jira.Off
	}

	if !w.moduleEnabled(settings.ModuleGitHub) {
		return "GitHub is off. Enable it in Settings"
	}

	if !w.allowed(repo) {
		return repo + " is not an allowed repository: add it in Settings"
	}

	return ""
}

// prBody is the PR body file: the session's own, or one made from the commit subjects.
func (w *Work) prBody(step *task, leg *Leg) (string, error) {
	bodyFile := filepath.Join(leg.Worktree, connFolder, prBodyFile)
	if exists(bodyFile) {
		return bodyFile, nil
	}

	var (
		subjects string
		err      error
	)

	step.outside(func() {
		subjects, err = w.sh("git", []string{
			"log", "origin/" + leg.Base + "..HEAD",
			"--format=- %s",
		}, leg.Worktree, 0)
	})

	if err != nil {
		return "", err
	}

	body := "## What changed and why\n\n" + subjects + "\n\n## How it was verified\n\n" +
		"Not recorded: the session did not write .conn/pr-body.md.\n"

	err = os.WriteFile(bodyFile, []byte(body), fileMode)
	if err != nil {
		return "", nodeError{err: err}
	}

	return bodyFile, nil
}

func (w *Work) createPR(step *task, record *Record, leg *Leg, repo, bodyFile string) error {
	var (
		out string
		err error
	)

	step.outside(func() {
		out, err = w.sh("gh", []string{
			"pr", "create", "--repo", repo, "--base", leg.PRBase,
			"--head", leg.Branch, "--title", record.Key + ": " + record.Title,
			"--body-file", bodyFile,
		}, leg.Worktree, createTimeout)
	})

	if err != nil {
		return err
	}

	lines := strings.Split(js.Trim(out), "\n")
	url := lines[len(lines)-1]

	number := 0.0
	if match := pullNumber.FindStringSubmatch(url); match != nil {
		number = js.ToNumber(match[1])
	}

	leg.PR = &PR{URL: url, Number: number, Status: nil}
	record.State = statePROpen
	record.Stage = stagePR
	w.log(record, logPR, "Pull request opened into "+leg.PRBase+": "+url)
	w.moveJira(step, record, jira.MoveReview())

	return nil
}

func (w *Work) commitsAhead(step *task, leg *Leg) (float64, error) {
	var (
		count float64
		err   error
	)

	step.outside(func() { count, err = w.countAhead(leg) })

	return count, err
}

// countAhead is the number of commits on the leg's branch past its base; 0 when git's answer
// is not a number.
func (w *Work) countAhead(leg *Leg) (float64, error) {
	out, err := w.sh("git", []string{"rev-list", "--count", "origin/" + leg.Base + "..HEAD"},
		leg.Worktree, gitTimeout)
	if err != nil {
		return 0, err
	}

	count := js.ToNumber(js.Trim(out))
	if math.IsNaN(count) {
		return 0, nil
	}

	return count, nil
}

func (w *Work) dryPR(step *task, record *Record, leg *Leg) {
	w.log(record, logPR, "Pushing "+leg.Branch)
	step.outside(func() { w.deps.Sleep(w.ctx, dryPRWait) })

	number := dryPRFirst + float64(int(ticketNumber(record.Key))%dryPRSpread)
	leg.PR = &PR{
		URL: "https://github.com/acme/widgets/pull/" + js.Number(number), Number: number,
		Status: nil,
	}
	record.State = statePROpen
	record.Stage = stagePR
	record.JiraStatus = "In Review"
	w.log(record, logPR, "Pull request opened into "+leg.PRBase)
	w.log(record, logStatus, "Jira "+record.Key+" is now In Review")
}

// FixChecks asks the build session to fix red checks. It is told which checks and where to read
// them; CI output is not pasted in.
func (w *Work) FixChecks(key string) (Reply, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	record, found := w.runs.Get(key)
	if !found {
		return notFound("no run for " + key), nil
	}

	leg := record.lead()

	refusal, repo, refused := w.fixRefusal(record.Key, record.State, leg)
	if refused {
		return refusal, nil
	}

	if w.deps.Dry {
		w.dryFixed[key] = true
		w.dryPolls[key] = 0
		delete(w.lastPoll, key)
		w.log(record, logBuild, "Asked the session to fix the failing checks (dry run)")

		return ok(), nil
	}

	if w.busyPR[key] {
		return conflict("another change to this run is in progress"), nil
	}

	session := w.deps.FindSession(record.Key, leg.Worktree)
	if session == nil {
		return conflict("no session for " + key + " is running; open one in " + leg.Worktree +
			" first"), nil
	}

	return w.askToFix(record, leg, repo, session)
}

func (w *Work) askToFix(record *Record, leg *Leg, repo string, session *Found) (Reply, error) {
	status := leg.PR.Status

	message, err := prstatus.FixChecksMessage(leg.PR.Number, status.Checks.Failed, repo)
	if err != nil {
		return Reply{}, fmt.Errorf("asking the session to fix the checks: %w", err)
	}

	// Held so a second click cannot queue the same instruction twice.
	w.busyPR[record.Key] = true

	var reply Reply

	w.held().outside(func() { reply = w.deps.Reply(w.ctx, session.ID, message) })
	delete(w.busyPR, record.Key)

	if reply.Code != http.StatusOK {
		return reply, nil
	}

	w.log(record, logBuild, "Asked the session to fix the failing checks: "+
		failedOr(status.Checks.Failed))

	return ok(), nil
}

func failedOr(failed []string) string {
	if len(failed) == 0 {
		return "see GitHub"
	}

	return strings.Join(failed, ", ")
}

// fixRefusal refuses FIX FAILING CHECKS for a run whose checks are not failing. The state shown
// is only as fresh as the poll, which stops for a repository off the list or with GitHub off:
// the session is sent to read that PR on GitHub, so both must hold now. The repository named to
// it is the PR's own (its URL), checked like the poll's.
func (w *Work) fixRefusal(key, state string, leg *Leg) (Reply, string, bool) {
	if state != statePROpen {
		return conflict(key + " has no open PR being watched"), "", true
	}

	if leg.PR == nil {
		return conflict(key + " has no open PR being watched"), "", true
	}

	status := leg.PR.Status
	if status == nil {
		return conflict(key + " has no open PR being watched"), "", true
	}

	if status.Checks.State != "failing" {
		return conflict("the checks are not failing"), "", true
	}

	if finished(status.State) {
		return conflict("the checks are not failing"), "", true
	}

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

// prRepoAllowed is whether a leg's PR may be read: the PR's own repository (its URL) and the
// leg's, when it recorded one, are both on the allowlist.
func (w *Work) prRepoAllowed(leg *Leg, repo string) bool {
	if repo == "" {
		return false
	}

	if !w.allowed(repo) {
		return false
	}

	if leg.Repo != "" {
		return w.allowed(leg.Repo)
	}

	return true
}

func finished(state string) bool {
	switch state {
	case prMerged, prClosed:
		return true
	}

	return false
}

// MoveTicket moves the ticket to the status the person picks, after the PR merges. Never
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

	if w.busyPR[key] {
		return conflict("another change to this run is in progress")
	}

	w.busyPR[key] = true

	w.launch(func(step *task) {
		defer delete(w.busyPR, key)

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

func merged(record *Record) bool {
	if record.State != statePROpen {
		return false
	}

	pull := record.lead().PR
	if pull == nil {
		return false
	}

	if pull.Status == nil {
		return false
	}

	return pull.Status.State == prMerged
}

// Cleanup removes the run's worktree once its PR is done. It refuses while anything could be
// lost: a live session, or uncommitted files.
func (w *Work) Cleanup(key string) Reply {
	w.mu.Lock()
	defer w.mu.Unlock()

	record, found := w.runs.Get(key)
	if !found {
		return notFound("no run for " + key)
	}

	leg := record.lead()
	if refusal, refused := cleanupRefusal(record.State, leg); refused {
		return refusal
	}

	if w.busyPR[key] {
		return conflict("another change to this run is in progress")
	}

	if w.deps.Dry {
		cleaned := true
		leg.Cleaned = &cleaned
		w.log(record, logStatus, "Worktree "+leg.Worktree+" removed (dry run). The branch "+
			leg.Branch+" stays.")

		return ok()
	}

	if w.deps.FindSession(record.Key, leg.Worktree) != nil {
		return conflict("the session is still running: close it first")
	}

	w.busyPR[key] = true
	defer delete(w.busyPR, key)

	return w.removeWorktree(w.held(), record, leg)
}

// cleanupRefusal refuses a run whose PR is open or whose worktree is gone. The record is read
// from disk and its path is not recomputed (a run made under another naming, or another clone,
// must still clean up), so the path is only ever removed when git itself lists it as a linked
// worktree of a repository.
func cleanupRefusal(runState string, leg *Leg) (Reply, bool) {
	if runState != statePROpen {
		return conflict("the PR is still open"), true
	}

	state := ""

	if leg.PR != nil {
		if leg.PR.Status != nil {
			state = leg.PR.Status.State
		}
	}

	if !finished(state) {
		return conflict("the PR is still open"), true
	}

	if leg.cleaned() {
		return conflict("the worktree is already removed"), true
	}

	if !strings.HasPrefix(leg.Worktree, "/") {
		return badRequest("refusing to remove " + leg.Worktree), true
	}

	if strings.Contains(leg.Worktree, "\x00") {
		return badRequest("refusing to remove " + leg.Worktree), true
	}

	return Reply{}, false
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
	w.log(record, logStatus, "Worktree "+leg.Worktree+" removed. The branch "+leg.Branch+
		" stays.")

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

// countLines is the number of lines that are not empty.
func countLines(text string) int {
	count := 0

	for line := range strings.SplitSeq(text, "\n") {
		if line != "" {
			count++
		}
	}

	return count
}
