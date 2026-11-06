package openwork

import (
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/settings"
)

// noCommits is why no pull request opens for a branch with nothing on it.
const noCommits = "the branch has no commits yet"

// OpenPR pushes the branch of each leg asked for and opens its PR: the leg in repo, or with no
// repo every leg that has no PR yet. The record is read from disk: only a branch this tool would
// have made (the ticket's own key, lowercase, then a slug) is pushed, never a protected one, and
// the branch and the bases must each be one plain branch name. Each leg is checked on its own,
// and a leg refused does not stop the others.
func (w *Work) OpenPR(key, repo string) Reply {
	w.mu.Lock()
	defer w.mu.Unlock()

	record, found := w.runs.Get(key)
	if !found {
		return notFound("no run for " + key)
	}

	if !openable(record) {
		return conflict(key + " is " + record.State + ", not building")
	}

	asked, refusal, refused := prLegsAsked(record, repo)
	if refused {
		return refusal
	}

	repos := map[*Leg]string{}
	legs, said := siftLegs(asked, func(leg *Leg) (Reply, bool, bool) {
		repos[leg] = w.repoOf(leg)
		reply, isRefused := w.openRefusal(record.Key, leg, repos[leg])

		return reply, isRefused, false
	})

	if len(legs) == 0 {
		return refusedAll(record, said)
	}

	w.logRefused(record, said)

	pushes := make([]legPush, 0, len(legs))
	for _, leg := range legs {
		pushes = append(pushes, legPush{leg: leg, repo: repos[leg], ahead: 0})
	}

	release := w.hold(key, legs)

	w.launch(func(step *task) {
		defer release()

		w.openPRs(step, record, pushes)
	})

	return ok()
}

// openable is whether OPEN PR may run: while the run builds, and while its PR is open as long as
// a leg has none yet.
func openable(record *Record) bool {
	switch record.State {
	case stateBuilding:
		return true
	case statePROpen:
		return slices.ContainsFunc(record.Repos, func(leg *Leg) bool { return leg.PR == nil })
	}

	return false
}

// prLegsAsked are the legs an OPEN PR asks for: the one in repo, or every leg with no PR yet.
func prLegsAsked(record *Record, repo string) ([]*Leg, Reply, bool) {
	if repo == "" {
		return slices.DeleteFunc(slices.Clone(record.Repos), func(leg *Leg) bool {
			return leg.PR != nil
		}), Reply{}, false
	}

	legs, refusal, refused := legsAsked(record, repo)
	if refused {
		return nil, refusal, true
	}

	if legs[0].PR != nil {
		return nil, conflict(legs[0].Repo + " already has a pull request"), true
	}

	return legs, Reply{}, false
}

// openRefusal is why a leg's branch may not be pushed now: its PR is already being opened, the
// branch or a base is not one this tool would push, GitHub is off, or its repository is off the
// list or has no clone.
func (w *Work) openRefusal(key string, leg *Leg, repo string) (Reply, bool) {
	if w.legBusy(key, leg) {
		return conflict("a pull request is already being opened"), true
	}

	if refusal, refused := w.pushRefusal(key, leg); refused {
		return refusal, true
	}

	if refusal, refused := w.repoRefusal(leg, repo); refused {
		return refusal, true
	}

	return w.cloneRefusal(leg)
}

// cloneRefusal refuses a push from a leg whose repository has no clone in Settings now: the
// clone is looked up again on every push, never taken from the record. A run from before
// Settings, which recorded no repository, keeps its worktree's origin as its only check.
func (w *Work) cloneRefusal(leg *Leg) (Reply, bool) {
	if w.deps.Dry {
		return Reply{}, false
	}

	if leg.Repo == "" {
		return Reply{}, false
	}

	if _, problem := w.cloneOf(leg); problem != "" {
		return conflict(problem), true
	}

	return Reply{}, false
}

// legPush is one leg OPEN PR pushes: its repository, checked against the allowlist, and the
// commits git counted on its branch.
type legPush struct {
	leg   *Leg
	repo  string
	ahead float64
}

// openPRs pushes and opens each leg's PR, one after another, the lock never held across a call
// to the outside. A leg with no commits is skipped while another has some; with none anywhere
// the answer is that the branch has none. A failure leaves the PRs that opened in place and is
// said with its repository.
func (w *Work) openPRs(step *task, record *Record, pushes []legPush) {
	record.Error = nil

	if w.deps.Dry {
		for _, push := range pushes {
			w.dryPR(step, record, push.leg)
			w.reviewAfterOpen(step, record, nil)
		}

		return
	}

	counted, failures := w.countPushes(step, record, pushes)
	fresh := map[*Leg]float64{}
	withCommits := make([]legPush, 0, len(counted))

	for _, push := range counted {
		fresh[push.leg] = push.ahead
		if push.ahead >= 1 {
			withCommits = append(withCommits, push)
		}
	}

	failures = w.skipEmpty(record, counted, withCommits, failures)

	for _, push := range withCommits {
		err := w.pushLeg(step, record, push)
		if err != nil {
			failures = append(failures, legSaid(record, push.leg, err.Error()))

			continue
		}

		w.reviewAfterOpen(step, record, fresh)
	}

	if len(failures) > 0 {
		message := strings.Join(failures, "; ")
		record.Error = &message
		w.log(record, logError, "Pull request failed: "+message)
	}
}

// countPushes asks git for each leg's commits, and says which legs it could not.
func (w *Work) countPushes(step *task, record *Record, pushes []legPush) ([]legPush, []string) {
	counted := make([]legPush, 0, len(pushes))
	failures := []string{}

	for _, push := range pushes {
		if push.repo == "" {
			failures = append(failures, legSaid(record, push.leg,
				"Could not tell which repository "+push.leg.Worktree+" belongs to."))

			continue
		}

		ahead, err := w.commitsAhead(step, push.leg)
		if err != nil {
			failures = append(failures, legSaid(record, push.leg, err.Error()))

			continue
		}

		push.ahead = ahead
		counted = append(counted, push)
	}

	return counted, failures
}

// skipEmpty says of each counted leg with no commits that it is skipped, when some leg has
// commits or failed on its own; when none of them has any, that the branch has none is the
// failure, as a run of one says it.
func (w *Work) skipEmpty(record *Record, counted, withCommits []legPush, failures []string,
) []string {
	if len(counted) == len(withCommits) {
		return failures
	}

	if len(withCommits) == 0 {
		if len(failures) == 0 {
			return []string{noCommits}
		}
	}

	for _, push := range counted {
		if push.ahead < 1 {
			w.log(record, logPR, "Skipped "+push.leg.Repo+": "+noCommits)
		}
	}

	return failures
}

// pushNowRefusal is why a leg may not be pushed now, "" when it may: the checks made after a
// push, and its clone in Settings.
func (w *Work) pushNowRefusal(push legPush) string {
	if refusal := w.prRefusal(push.repo); refusal != "" {
		return refusal + "."
	}

	if reply, refused := w.cloneRefusal(push.leg); refused {
		return errorText(reply)
	}

	return ""
}

// pushLeg pushes a leg's branch and opens its PR in repo, the repository OpenPR checked against
// the allowlist (named to gh, never left to it).
func (w *Work) pushLeg(step *task, record *Record, push legPush) error {
	leg := push.leg

	// An earlier leg's push can take minutes: Settings may have changed meanwhile, so the list
	// and the clone are checked again right before this one.
	if refusal := w.pushNowRefusal(push); refusal != "" {
		return textError(refusal + " " + leg.Branch + " was not pushed: press OPEN PR again " +
			"once that is fixed.")
	}

	w.log(record, logPR, legSaid(record, leg, "Pushing "+leg.Branch))

	var err error

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
	if after := w.prRefusal(push.repo); after != "" {
		return textError(after + ". " + leg.Branch + " was pushed, and no pull request was " +
			"opened: press OPEN PR again once that is fixed.")
	}

	return w.createPR(step, record, leg, push.repo, bodyFile)
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
	w.log(record, logPR, legSaid(record, leg, "Pull request opened into "+leg.PRBase+": "+url))

	return nil
}

// reviewDue is whether the ticket moves to review now that a PR opened, as policy (Settings'
// reviewMove) says: "first" once any leg has a PR, "all" once every leg with commits has one,
// "never" never; and only while no move was answered. opened are the legs with a PR,
// withCommits the legs with commits or a PR, withPR those of them with a PR. A move that failed
// is not one answered, so the next PR that opens tries again. Every leg with a PR counts as one
// with commits, so withPR is opened today; both are kept, as the run's contract names them.
func reviewDue(policy string, opened, withCommits, withPR int, moved bool) bool {
	if moved {
		return false
	}

	if opened < 1 {
		return false
	}

	switch policy {
	case settings.ReviewFirst:
		return true
	case settings.ReviewAll:
		return withPR >= withCommits
	}

	return false
}

// reviewCounts are reviewDue's counts for a run: the legs with a PR, the legs with commits or a
// PR, and those of them with a PR. A leg's commits are what git just said (fresh) or else what
// it last counted.
func reviewCounts(record *Record, fresh map[*Leg]float64) (int, int, int) {
	opened, withCommits := 0, 0

	for _, leg := range record.Repos {
		if leg.PR != nil {
			opened++
			withCommits++

			continue
		}

		ahead := leg.Ahead
		if counted, found := fresh[leg]; found {
			ahead = counted
		}

		if ahead >= 1 {
			withCommits++
		}
	}

	return opened, withCommits, opened
}

// reviewAfterOpen moves the ticket to review when Settings says it is due now. Jira's answer
// (moved, already there, or no single review status) records the move as made; a failure does
// not.
func (w *Work) reviewAfterOpen(step *task, record *Record, fresh map[*Leg]float64) {
	opened, withCommits, withPR := reviewCounts(record, fresh)
	policy := w.settings().Atlassian.Jira.ReviewMove

	if !reviewDue(policy, opened, withCommits, withPR, record.ReviewMoved) {
		return
	}

	if !w.deps.Dry {
		w.moveJira(step, record, jira.MoveReview())

		return
	}

	record.JiraStatus = "In Review"
	record.ReviewMoved = true
	w.log(record, logStatus, "Jira "+record.Key+" is now In Review")
}

// dryPR is the dry run's push and PR for a leg. The first leg's PR number comes from the
// ticket's, each later leg's from the ticket's and the leg's place.
func (w *Work) dryPR(step *task, record *Record, leg *Leg) {
	w.log(record, logPR, legSaid(record, leg, "Pushing "+leg.Branch))
	step.outside(func() { w.deps.Sleep(w.ctx, dryPRWait) })

	place := slices.Index(record.Repos, leg)
	if place < 0 {
		panic("openwork: the dry PR of " + record.Key + " is for a leg it does not have")
	}

	repo := "acme/widgets"

	if place > 0 {
		if leg.Repo != "" {
			repo = leg.Repo
		}
	}

	number := dryPRFirst + float64(int(ticketNumber(record.Key))%dryPRSpread+place*dryPRLegStep)
	leg.PR = &PR{
		URL: "https://github.com/" + repo + "/pull/" + js.Number(number), Number: number,
		Status: nil,
	}
	record.State = statePROpen
	record.Stage = stagePR
	w.log(record, logPR, legSaid(record, leg, "Pull request opened into "+leg.PRBase))
}
