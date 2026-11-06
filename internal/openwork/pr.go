package openwork

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
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
	// dryPRLegStep sets the dry PR numbers of a run's later legs apart from the first's.
	dryPRLegStep = 100
	prMerged     = "merged"
	prClosed     = "closed"
	githubOff    = "GitHub is off. Enable it in Settings."
	prBodyFile   = "pr-body.md"
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
// the clone's default branch, the ones Settings names (for every repository and for the leg's),
// and the long-lived names by habit.
func (w *Work) isProtected(leg *Leg) bool {
	names := append(wellKnown(), leg.Base, leg.PRBase)

	if configured := w.settings().Atlassian.Jira.BaseBranch; configured != nil {
		if *configured != "" {
			names = append(names, *configured)
		}
	}

	if own := w.settings().Atlassian.Jira.BaseFor(leg.Repo); own != nil {
		if *own != "" {
			names = append(names, *own)
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

	// A leg with no session of its own (shared layout, not the lead) has no brief, so no .conn/
	// yet; the launch excluded it from git in every worktree.
	err := os.MkdirAll(filepath.Dir(bodyFile), folderMode)
	if err != nil {
		return "", nodeError{err: err}
	}

	var subjects string

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

// legKey keys what is kept per leg (a PR being opened, its polls): the run and the leg's
// repository.
func legKey(key, repo string) string { return key + " " + js.Lower(repo) }

// legBusy is whether a change to a leg is in progress: its PR being opened, a fix asked of its
// session, its worktree being removed, or a change to the whole run.
func (w *Work) legBusy(key string, leg *Leg) bool { return w.busyPR[legKey(key, leg.Repo)] }

// runBusy is whether a change to any leg of a run is in progress.
func (w *Work) runBusy(record *Record) bool {
	return slices.ContainsFunc(record.Repos, func(leg *Leg) bool {
		return w.legBusy(record.Key, leg)
	})
}

// hold marks legs busy until the function it returns is called.
func (w *Work) hold(key string, legs []*Leg) func() {
	for _, leg := range legs {
		w.busyPR[legKey(key, leg.Repo)] = true
	}

	return func() {
		for _, leg := range legs {
			delete(w.busyPR, legKey(key, leg.Repo))
		}
	}
}

// legSaid is a message about one leg: with its repository first when the run has several legs,
// so a run of one says what it always said.
func legSaid(record *Record, leg *Leg, message string) string {
	if len(record.Repos) == 1 {
		return message
	}

	return leg.Repo + ": " + message
}

// errorText is the message of a refusal.
func errorText(reply Reply) string {
	message, _ := reply.Body.Get("error")
	text, _ := message.(string)

	return text
}

// legRefusal is a leg an action left out, the answer that says why, and whether that is only
// that the leg needs nothing (said when it is the only one asked for, never logged).
type legRefusal struct {
	leg          *Leg
	reply        Reply
	needsNothing bool
}

// legCheck is why an action may not go on in a leg, whether it may not, and whether that is only
// that the leg needs nothing.
type legCheck func(leg *Leg) (reply Reply, refused, needsNothing bool)

// siftLegs are the legs check passes, in order, and the refusals of the others.
func siftLegs(legs []*Leg, check legCheck) ([]*Leg, []legRefusal) {
	kept := make([]*Leg, 0, len(legs))
	refused := []legRefusal{}

	for _, leg := range legs {
		reply, isRefused, needsNothing := check(leg)
		if isRefused {
			refused = append(refused, legRefusal{
				leg: leg, reply: reply, needsNothing: needsNothing,
			})

			continue
		}

		kept = append(kept, leg)
	}

	return kept, refused
}

// refusedAll is the answer when every leg asked for was refused: a single leg's own answer, as a
// run of one always gave it, or every leg's reason in one, with the first one's code.
func refusedAll(record *Record, refused []legRefusal) Reply {
	if len(refused) == 0 {
		panic("openwork: " + record.Key + " refused no leg, yet none went on")
	}

	first := refused[0]
	if len(refused) == 1 {
		if len(record.Repos) == 1 {
			return first.reply
		}

		return bad(first.reply.Code, legSaid(record, first.leg, errorText(first.reply)))
	}

	messages := make([]string, 0, len(refused))
	for _, each := range refused {
		messages = append(messages, legSaid(record, each.leg, errorText(each.reply)))
	}

	return bad(first.reply.Code, strings.Join(messages, "; "))
}

// logRefused says in the log why legs were left out while others went on, except the ones that
// needed nothing.
func (w *Work) logRefused(record *Record, refused []legRefusal) {
	for _, each := range refused {
		if each.needsNothing {
			continue
		}

		w.log(record, logError, legSaid(record, each.leg, errorText(each.reply)))
	}
}

// legsAsked are the legs an action names: the one in repo, or with no repo every leg.
func legsAsked(record *Record, repo string) ([]*Leg, Reply, bool) {
	if repo == "" {
		return record.Repos, Reply{}, false
	}

	leg, found := record.legFor(repo)
	if !found {
		return nil, notFound(record.Key + " does not build in " + repo), true
	}

	return []*Leg{leg}, Reply{}, false
}

// workerOf is the slot whose session works in a leg's folder: the leg itself in the split layout,
// the lead (whose session reaches every worktree) in the shared one.
func (w *Work) workerOf(record *Record, leg *Leg) *Leg {
	if record.Layout == layoutSplit {
		return leg
	}

	return record.lead()
}
