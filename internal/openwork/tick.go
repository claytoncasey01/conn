package openwork

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/prstatus"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	// gitEveryMs is how often a building run's commits and changed files are counted.
	gitEveryMs = 5000
	// gitLegsAtOnceMax is how many legs of a run are counted at once, two git calls each.
	gitLegsAtOnceMax = 3
	// pollDryMs, pollPendingMs and pollMs are how often a run's PR is read: the dry run's, one
	// whose checks are running, and the rest.
	pollDryMs     = 4000
	pollPendingMs = 30_000
	pollMs        = 60_000
	pollTimeout   = 30 * time.Second
	// pollErrorUnitsMax is how much of a poll's error the log keeps.
	pollErrorUnitsMax = 200
	checksPending     = "pending"
	checksFailing     = "failing"
	checksPassing     = "passing"
)

// Tick follows each building run: the session in the session list, commits and uncommitted
// files from git. A run with an open PR keeps following its session too (a resumed one has a
// new pid), but not git. Open work is not shown while off, so its runs are not followed then.
func (w *Work) Tick() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.on() {
		return
	}

	w.tickPRs()

	for _, key := range w.runs.Keys() {
		record, _ := w.runs.Get(key)
		if !w.followed(record) {
			continue
		}

		w.followSession(record)

		if w.deps.Dry {
			continue
		}

		legs := w.gitDue(record)
		if len(legs) == 0 {
			continue
		}

		w.followGit(record, legs)
	}
}

// followed is whether a run's sessions are followed: while it builds, and while its PR is open
// as long as a slot's worktree is still there.
func (w *Work) followed(record *Record) bool {
	if record.State == stateBuilding {
		return true
	}

	if record.State != statePROpen {
		return false
	}

	return slices.ContainsFunc(w.sessionSlots(record), func(slot *Leg) bool {
		return !slot.cleaned()
	})
}

// followSession keeps each slot's session as the session list has it, by its folder.
func (w *Work) followSession(record *Record) {
	for _, slot := range w.sessionSlots(record) {
		if slot.cleaned() {
			continue
		}

		w.followSlot(record, slot)
	}
}

func (w *Work) followSlot(record *Record, leg *Leg) {
	var found *Found
	if !w.deps.Dry {
		found = w.findSession(record, leg)
	}

	prev := leg.Session
	next := sessionNext(record.Key, found, prev, w.deps.Dry)

	if found != nil {
		if sessionText(leg.ClaudeSessionID) != found.SessionID {
			id := found.SessionID
			leg.ClaudeSessionID = &id

			w.touch(record)
		}
	}

	if string(js.Marshal(next)) != string(js.Marshal(prev)) {
		leg.Session = next

		w.bump()
	}
}

func sessionNext(key string, found *Found, prev *Session, dry bool) *Session {
	if dry {
		return prev
	}

	if found == nil {
		if prev == nil {
			return nil
		}
	}

	next := &Session{Name: key, SessionID: nil, PID: nil, Status: nil}

	if found != nil {
		id, pid, status := found.ID, found.PID, found.Status
		next.SessionID, next.PID, next.Status = &id, &pid, &status
	}

	return next
}

// legCount is what git said of one leg: its commits ahead and its uncommitted files.
type legCount struct {
	ahead, dirty float64
	err          error
}

// gitLegs are the legs whose commits and changed files are counted: every leg still there while
// the run builds, and once a PR is open the legs with none yet (commits may come to them after).
func gitLegs(record *Record) []*Leg {
	switch record.State {
	case stateBuilding:
		return slices.DeleteFunc(slices.Clone(record.Repos), func(leg *Leg) bool {
			return leg.cleaned()
		})
	case statePROpen:
		return slices.DeleteFunc(slices.Clone(record.Repos), func(leg *Leg) bool {
			if leg.cleaned() {
				return true
			}

			return leg.PR != nil
		})
	}

	return nil
}

// gitDue are the legs whose git is counted now, each at most every gitEveryMs; they are marked
// counted.
func (w *Work) gitDue(record *Record) []*Leg {
	now := w.now()
	due := []*Leg{}

	for _, leg := range gitLegs(record) {
		counted := legKey(record.Key, leg.Repo)
		if now-w.lastGit[counted] < gitEveryMs {
			continue
		}

		w.lastGit[counted] = now

		due = append(due, leg)
	}

	return due
}

// followGit counts the legs' commits and changed files, gitLegsAtOnceMax legs at a time; a leg
// whose git fails keeps its counts.
func (w *Work) followGit(record *Record, legs []*Leg) {
	slots := make([]bool, 0, len(legs))
	for _, leg := range legs {
		slots = append(slots, w.isSlot(record, leg))
	}

	w.launch(func(step *task) {
		counts := make([]legCount, len(legs))

		step.outside(func() {
			turns := make(chan struct{}, gitLegsAtOnceMax)
			calls := sync.WaitGroup{}

			for index, leg := range legs {
				calls.Go(func() {
					turns <- struct{}{}
					defer func() { <-turns }()

					counts[index] = w.countLeg(leg)
				})
			}

			calls.Wait()
		})

		for index, leg := range legs {
			if counts[index].err != nil {
				continue
			}

			w.counted(record.Key, leg, slots[index], counts[index].ahead, counts[index].dirty)
		}
	})
}

// countLeg asks git for a leg's commits ahead and uncommitted files, both at once.
func (w *Work) countLeg(leg *Leg) legCount {
	var (
		ahead, dirty       float64
		aheadErr, dirtyErr error
	)

	calls := sync.WaitGroup{}
	calls.Go(func() { ahead, aheadErr = w.countAhead(leg) })
	calls.Go(func() {
		var out string

		out, dirtyErr = w.sh("git", []string{"status", argPorcelain}, leg.Worktree, gitTimeout)
		dirty = float64(countLines(out))
	})
	calls.Wait()

	if aheadErr != nil {
		return legCount{ahead: 0, dirty: 0, err: aheadErr}
	}

	return legCount{ahead: ahead, dirty: dirty, err: dirtyErr}
}

// counted keeps a leg's counts. The page reads a slot's session once anything was counted, so
// one is made for a slot that has none yet; a leg with no session of its own gets none.
func (w *Work) counted(key string, leg *Leg, slot bool, ahead, dirty float64) {
	changed := false
	if slot {
		changed = leg.Session == nil
	}

	if leg.Ahead != ahead {
		changed = true
	}

	if leg.Dirty != dirty {
		changed = true
	}

	if !changed {
		return
	}

	if slot {
		if leg.Session == nil {
			leg.Session = &Session{Name: key, SessionID: nil, PID: nil, Status: nil}
		}
	}

	leg.Ahead, leg.Dirty = ahead, dirty

	w.bump()
}

// tickPRs asks GitHub about each leg's open PR, each on its own schedule, until it merges or
// closes. Reading a PR is a gh call: with GitHub off the status stays as it was.
func (w *Work) tickPRs() {
	if !w.moduleEnabled(settings.ModuleGitHub) {
		return
	}

	now := w.now()

	for _, key := range w.runs.Keys() {
		record, _ := w.runs.Get(key)

		for _, leg := range record.Repos {
			if !w.pollDue(record, leg, now) {
				continue
			}

			polled := legKey(key, leg.Repo)
			w.lastPoll[polled] = now
			w.polling[polled] = true
			number := leg.PR.Number

			w.launch(func(step *task) {
				defer delete(w.polling, polled)

				err := w.pollPR(step, record, leg)
				if err != nil {
					w.pollFailed(record, leg, number, err)
				}
			})
		}
	}
}

// pollDue is whether a leg's PR is read now. A repository that left the list is not read: the
// leg is kept as it was and followed again once it is allowed. The repository checked is the
// one the read goes to, the PR's own URL (and the leg's, when it recorded one).
func (w *Work) pollDue(record *Record, leg *Leg, now float64) bool {
	if record.State != statePROpen {
		return false
	}

	pull := leg.PR
	if pull == nil {
		return false
	}

	if pull.Status != nil {
		if finished(pull.Status.State) {
			return false
		}
	}

	polled := legKey(record.Key, leg.Repo)
	if w.polling[polled] {
		return false
	}

	if !w.prRepoAllowed(leg, PRURLRepo(pull.URL)) {
		return false
	}

	return now-w.lastPoll[polled] >= w.pollEvery(pull.Status)
}

func (w *Work) pollEvery(status *prstatus.Status) float64 {
	if w.deps.Dry {
		return pollDryMs
	}

	if status != nil {
		if status.Checks.State == checksPending {
			return pollPendingMs
		}
	}

	return pollMs
}

// pollFailed says so once per distinct error of each leg: an expired gh login would otherwise
// leave "checked N ago" frozen with no reason.
func (w *Work) pollFailed(record *Record, leg *Leg, number float64, err error) {
	message := err.Error()
	if message == "" {
		message = "failed"
	}

	why := js.Cut(strings.Split(message, "\n")[0], pollErrorUnitsMax)

	polled := legKey(record.Key, leg.Repo)
	if said, found := w.pollError[polled]; found {
		if said == why {
			return
		}
	}

	w.pollError[polled] = why
	w.log(record, logError, legSaid(record, leg, "Could not read PR #"+js.Number(number)+
		" from GitHub: "+why))
}

func (w *Work) pollPR(step *task, record *Record, leg *Leg) error {
	pull := leg.PR

	next, err := w.readPR(step, record, leg)
	if err != nil {
		return err
	}

	delete(w.pollError, legKey(record.Key, leg.Repo))

	prev := pull.Status
	pull.Status = &next

	said := prSaid(pull.Number, prev, next)

	switch {
	case len(said) > 0:
		for _, line := range said {
			w.log(record, logPR, legSaid(record, leg, line))
		}
	case atZero(prev) != atZero(&next):
		w.touch(record)
	default:
		w.bump()
	}

	return nil
}

func (w *Work) readPR(step *task, record *Record, leg *Leg) (prstatus.Status, error) {
	if w.deps.Dry {
		return w.dryStatus(record, leg), nil
	}

	pull := leg.PR

	var (
		out string
		err error
	)

	step.outside(func() {
		out, err = w.sh("gh", []string{"pr", "view", pull.URL, "--json", prstatus.Fields},
			"", pollTimeout)
	})

	if err != nil {
		return prstatus.Status{}, err
	}

	parsed, err := js.Parse([]byte(out))
	if err != nil {
		return prstatus.Status{}, parseError(out)
	}

	if parsed == nil {
		return prstatus.Status{}, textError("null is not an object (evaluating 'v.state')")
	}

	view, isObject := parsed.(*js.Object)
	if !isObject {
		view = js.NewObject()
	}

	return prstatus.From(view, w.now()), nil
}

// parseError is the error for gh output that is not JSON, in the words of a JavaScript engine's
// JSON.parse for the common cases.
func parseError(out string) error {
	if js.Trim(out) == "" {
		return textError("JSON Parse error: Unexpected EOF")
	}

	return textError("JSON Parse error: Unexpected identifier \"" + firstWord(out) + "\"")
}

func firstWord(out string) string {
	trimmed := js.Trim(out)
	end := strings.IndexFunc(trimmed, func(char rune) bool { return !asciiLetter(char) })

	if end < 0 {
		return trimmed
	}

	return trimmed[:end]
}

// atZero is the status as JSON with its poll time left out, so two polls that found the same
// compare equal.
func atZero(status *prstatus.Status) string {
	if status == nil {
		return `{"at":0}`
	}

	copied := *status
	copied.At = 0

	return string(js.Marshal(copied))
}

// prSaid are the log lines a poll's news makes: the PR merged or closed, or its checks failed or
// passed.
func prSaid(number float64, prev *prstatus.Status, next prstatus.Status) []string {
	said := []string{}
	named := "PR #" + js.Number(number)

	prevState, prevChecks := "", ""
	if prev != nil {
		prevState, prevChecks = prev.State, prev.Checks.State
	}

	if next.State != prevState {
		switch next.State {
		case prMerged:
			said = append(said, named+" merged")
		case prClosed:
			said = append(said, named+" closed without merging")
		}
	}

	if finished(next.State) {
		return said
	}

	if next.Checks.State == prevChecks {
		return said
	}

	switch next.Checks.State {
	case checksFailing:
		said = append(said, "Checks failed on "+named+": "+failedOr(next.Checks.Failed))
	case checksPassing:
		said = append(said, "Checks passed on "+named)
	}

	return said
}

func asciiLetter(char rune) bool {
	if char >= 'a' {
		return char <= 'z'
	}

	if char >= 'A' {
		return char <= 'Z'
	}

	return false
}
