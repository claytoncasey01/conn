package myprs

import (
	"context"
	"net/http"
	"slices"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// askOwedReason is why no round starts while the session of a round that is over stays open
// (nothing closes it: I do).
const askOwedReason = "the last round of this PR has not been settled yet: conn asks Greptile " +
	"for its push (a failed ask is tried again, up to 3 tries) or sees it made none, on its " +
	"polls. REFRESH starts one (a round that pushed nothing needs a minute after its session " +
	"closed)"

const forkStart = "fork PR: Self-Review only works on PRs from branches of the repo itself"

func stillOpen(name string) string {
	return "its session (" + name + ") is still open: close it to start the next round"
}

func bad(code int, why string) reviews.Reply {
	body := js.NewObject()
	body.Set("error", why)

	return reviews.Reply{Code: code, Body: body}
}

func reply(fields ...any) reviews.Reply {
	body := js.NewObject()

	for index := 0; index+1 < len(fields); index += 2 {
		name, _ := fields[index].(string)
		body.Set(name, fields[index+1])
	}

	return reviews.Reply{Code: http.StatusOK, Body: body}
}

// prOf is a repo and number as a request sent them: a repo by the grammar and a positive safe
// integer, or the refusal.
func prOf(repo, number any) (string, float64, *reviews.Reply) {
	name, isText := repo.(string)
	if !isText {
		refusal := bad(http.StatusBadRequest, "not a repo name")

		return "", 0, &refusal
	}

	if !repos.IsName(name) {
		refusal := bad(http.StatusBadRequest, "not a repo name")

		return "", 0, &refusal
	}

	count, isNumber := number.(float64)
	if !isNumber {
		refusal := bad(http.StatusBadRequest, "not a PR number")

		return "", 0, &refusal
	}

	if !isCount(count) {
		refusal := bad(http.StatusBadRequest, "not a PR number")

		return "", 0, &refusal
	}

	return name, count, nil
}

// Start is SELF-REVIEW or RE-REVIEW pressed for a PR: it refuses before any git or cmux call,
// else launches the next round. The origin of the clone must be the PR's repo, so a wrong clone
// entry never pushes another repo's branch. Nothing is recorded unless the launch opened, or cmux
// alone failed and the command to paste was given (that round is recorded, marked manual).
func (m *MyPrs) Start(ctx context.Context, repo, number, model any) reviews.Reply {
	name, count, refusal := prOf(repo, number)
	if refusal != nil {
		return *refusal
	}

	chosen, _ := model.(string)
	if !isModel(chosen) {
		return bad(http.StatusBadRequest, "model must be sonnet or opus")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// A press with no poll behind it has nothing real to start.
	if m.dry() {
		return reply("ok", true, "dry", true)
	}

	target, refused := m.startTarget(name, count)
	if refused != nil {
		return *refused
	}

	if refused := m.refuseBeside(&target); refused != nil {
		return *refused
	}

	return m.launch(ctx, target, chosen)
}

// startTarget is the PR a press names when a round may start on it: listed, read at the last
// poll, not a fork, not a protected branch, with a clone.
type startTarget struct {
	key, clone string
	pull       *PR
	entry      *Entry
	round      float64
	name       string
}

func (m *MyPrs) startTarget(repo string, number float64) (startTarget, *reviews.Reply) {
	if refused := m.listRefusal(repo); refused != nil {
		return startTarget{}, refused
	}

	key := EntryKey(repo, number)
	pull, found := m.prs.Get(key)

	if !found {
		refusal := bad(http.StatusNotFound, "that PR is not in the list, or is not open")

		return startTarget{}, &refusal
	}

	if m.file == nil {
		refusal := bad(http.StatusNotFound, "that PR is not in the list, or is not open")

		return startTarget{}, &refusal
	}

	if pull.Fork {
		refusal := bad(http.StatusConflict, forkStart)

		return startTarget{}, &refusal
	}

	if head := m.headRefusal(pull); head != "" {
		refusal := bad(http.StatusConflict, head)

		return startTarget{}, &refusal
	}

	clone := m.deps.CloneFor(pull.Repo)
	if clone == "" {
		refusal := bad(http.StatusConflict, noClone(pull.Repo))

		return startTarget{}, &refusal
	}

	return startTarget{
		key: key, clone: clone, pull: pull, entry: TrackOf(m.file, key, m.now()), round: 0,
		name: "",
	}, nil
}

// listRefusal is why a press is refused before the PR is looked up: the gate, a list that could
// not be refreshed, or a repo whose search failed (what is listed for it may be a poll or more
// old).
func (m *MyPrs) listRefusal(repo string) *reviews.Reply {
	if refused := m.gate(repo); refused != "" {
		refusal := bad(http.StatusConflict, refused)

		return &refusal
	}

	// While the poll fails the list is the last good one, and a PR on it may have merged since:
	// a paid round must not start on it.
	if m.err != nil {
		refusal := bad(http.StatusConflict, "the PR list could not be refreshed: "+*m.err)

		return &refusal
	}

	for _, failure := range m.failedPatterns {
		if repos.Allowed(repo, []string{failure.Pattern}) {
			refusal := bad(http.StatusConflict, failure.Pattern+" could not be searched ("+
				failure.Error+"): its PRs may be out of date")

			return &refusal
		}
	}

	return nil
}

// refuseBeside refuses a round beside one that is running, just started, coming up or not yet
// settled, and names the round.
func (m *MyPrs) refuseBeside(target *startTarget) *reviews.Reply {
	now := m.now()
	live := m.deps.Sessions()
	m.markSeen(live)

	if open := m.openSession(target.pull, target.entry, live, now); open != nil {
		why := "a session named " + open.name + " is already running"
		if open.over {
			why = stillOpen(open.name)
		}

		refusal := bad(http.StatusConflict, why)

		return &refusal
	}

	if why := m.tooSoon(target, now); why != "" {
		refusal := bad(http.StatusConflict, why)

		return &refusal
	}

	// The last round's session is gone but Conn has not settled it: a push it made is still to
	// be asked for, and a press now would disarm it.
	if m.askOwed(target.pull, target.entry) {
		refusal := bad(http.StatusConflict, askOwedReason)

		return &refusal
	}

	target.round = float64(len(target.entry.Launches) + 1)

	name, err := m.deps.Config.Self.Name(target.pull.Repo, target.pull.Number, target.round)
	if err != nil {
		refusal := bad(http.StatusConflict, message(err))

		return &refusal
	}

	target.name = name

	return nil
}

// tooSoon is why a press comes too soon after the last: a round just started, or one whose
// session has not appeared yet (claude can take longer than restartMs to register it).
func (m *MyPrs) tooSoon(target *startTarget, now float64) string {
	const justStarted = "a self-review of this PR was just started: give it a few seconds"

	if m.starting[target.key] {
		return justStarted
	}

	for _, launch := range target.entry.Launches {
		if now-launch.At < restartMs {
			return justStarted
		}
	}

	launches := target.entry.Launches
	if len(launches) == 0 {
		return ""
	}

	newest := launches[len(launches)-1]
	if m.resumingRound(newest, now) {
		return "a self-review of this PR may be resuming after Conn's launch: give it a few " +
			"minutes"
	}

	for _, settled := range []bool{
		m.seen[newest], newest.ClosedAt != nil, now-newest.At >= appearMs,
	} {
		if settled {
			return ""
		}
	}

	if newest.Manual {
		return "the last round of this PR is waiting for its command to be pasted into a " +
			"terminal: paste it, or press again 5 minutes after it was given"
	}

	return "a self-review of this PR was started a moment ago and has not appeared yet"
}

// resumingRound is whether launch's session may be one this launch of Conn resumes and has not
// shown yet: the last quit's record named a session in its worktree, it was started inside Conn
// and not closed, and the launch has not settled, or did less than appearMs ago.
func (m *MyPrs) resumingRound(launch *Launch, now float64) bool {
	if !m.resumeHeld(now) {
		return false
	}

	if launch.Manual {
		return false
	}

	if launch.ClosedAt != nil {
		return false
	}

	return m.deps.Recorded(launch.Worktree)
}

// askOwed is whether the newest round is still armed with its ask possibly owed: it is armed,
// the PR is not a draft, its session has been seen (seen is memory only), and the tries for the
// head are not used up.
func (m *MyPrs) askOwed(pull *PR, entry *Entry) bool {
	if entry == nil {
		return false
	}

	if len(entry.Launches) == 0 {
		return false
	}

	launch := entry.Launches[len(entry.Launches)-1]
	if !launch.Armed {
		return false
	}

	if pull.Draft {
		return false
	}

	if !m.seen[launch] {
		return false
	}

	tried := triedFor(launch, pull.HeadSha)
	if tried == nil {
		return true
	}

	return tried.Tries < AskTriesMax
}

// headRefusal is why the PR's branch is one a round must not push to, cleaned to show; "" when
// it is not.
func (m *MyPrs) headRefusal(pull *PR) string {
	why := ProtectedHead(pull, m.deps.Config.ProtectedHeads)
	if why == "" {
		return ""
	}

	return Clean(why, messageUnitsMax)
}

func (m *MyPrs) launch(ctx context.Context, target startTarget, model string) reviews.Reply {
	m.starting[target.key] = true
	defer delete(m.starting, target.key)

	pull := target.pull

	var (
		matches bool
		err     error
	)

	m.outside(func() { matches, err = m.deps.RepoMatches(ctx, pull.Repo, target.clone) })

	if err != nil {
		return bad(http.StatusConflict, "could not read the origin of the clone at "+
			target.clone+": "+message(err))
	}

	if !matches {
		return bad(http.StatusConflict, "the clone at "+target.clone+
			" has an origin that is not "+pull.Repo)
	}

	var result LaunchResult

	m.outside(func() {
		result = m.deps.Launch(ctx, LaunchOpts{
			Repo: pull.Repo, Number: pull.Number, HeadRef: pull.HeadRef, Model: model,
			Name: target.name, RepoPath: target.clone, DataDir: "", Round: target.round,
		})
	})

	if !result.OK {
		// Only cmux failed: the worktree and the prompt are there, and the session the command
		// opens is this round's, tracked like any other.
		if launchedByHand(result) {
			m.recordLaunch(target, model, result, true)
		}

		out := reply("ok", false, "error", Clean(result.Error, messageUnitsMax))
		if result.ManualCommand != "" {
			out.Body.Set("manualCommand", result.ManualCommand)
		}

		return out
	}

	m.recordLaunch(target, model, result, false)

	return reply("ok", true, "name", target.name)
}

func launchedByHand(result LaunchResult) bool {
	return !slices.Contains([]string{result.ManualCommand, result.Worktree, result.HeadSha}, "")
}

// recordLaunch records the round at the head the worktree was put at, not the last poll's.
// Every older round still armed is disarmed: only the newest is ever asked for.
func (m *MyPrs) recordLaunch(target startTarget, model string, result LaunchResult,
	manual bool,
) {
	for _, launch := range target.entry.Launches {
		launch.Armed = false
	}

	target.entry.Launches = append(target.entry.Launches, &Launch{
		At: m.now(), Model: model, Name: target.name, Worktree: result.Worktree,
		HeadSha: result.HeadSha, Round: target.round, Armed: true, AskedSha: nil,
		AskedAt: nil, ClosedAt: nil, AskTry: nil, Manual: manual,
	})
	m.save()
	m.bump()
}
