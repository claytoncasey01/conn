package myprs

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
)

var digits = regexp.MustCompile(`^\d+$`)

// poll is one poll, on its own goroutine holding the mutex. Whatever happens, the poll is over
// at its end: the press it was started for is settled.
func (m *MyPrs) poll() {
	defer func() {
		m.polling = false
		m.settle()
		m.bump()
	}()

	err := m.pollOnce()
	if err == nil {
		return
	}

	if limited, ok := errors.AsType[*ghbudget.RateLimitError](err); ok {
		// Not a failure of the list: it is kept as it was, and nothing more is asked until
		// GitHub says the limit is back.
		now := m.now()
		until := now + ghbudget.RatePauseMs

		if limited.ResetAt != nil {
			if *limited.ResetAt > now {
				until = *limited.ResetAt
			}
		}

		m.pausedUntil = &until
		m.deps.Budget.Pause(until)
		m.record(moduleGitHub, false, "My PRs is paused: GitHub's rate limit is used up")

		return
	}

	why := message(err)
	m.err = &why
	m.record(moduleGitHub, false, "My PRs could not read GitHub")
}

// pollRead is what one poll read, and what it speaks for.
type pollRead struct {
	found    Search
	searched []string
	current  Scope
	// covers is whether a repo was searched by this poll (and not failed) and is allowed now:
	// anything else is left alone, never swept or pruned.
	covers func(repo string) bool
}

func (m *MyPrs) pollOnce() error {
	before := m.scope()
	if offOf(before) != "" {
		m.idle()

		return nil
	}

	// The patterns this poll searches: what it may say about the file's entries is limited to
	// repos they cover.
	searched := SearchPatterns(before.Allow)

	login, err := m.login()
	if err != nil {
		return err
	}

	// The list is searched for the same login the thread and ask checks compare with, so the
	// three cannot disagree.
	var found Search

	m.outside(func() { found, err = m.deps.Gh.PRs(m.ctx, login, searched) })

	if err != nil {
		return fmt.Errorf("%w", err)
	}

	// The settings are read again: a change made while gh ran decides what is kept.
	current := m.scope()
	if offOf(current) != "" {
		m.idle()

		return nil
	}

	if !sameSet(SearchPatterns(current.Allow), searched) {
		m.forcePoll = true
	}

	read := pollRead{found: found, searched: searched, current: current, covers: nil}
	read.covers = coverage(searched, found.Failed, current.Allow)

	m.apply(read)
	m.ingestRounds()
	m.askRounds()
	m.draftRounds()

	return nil
}

// login is my login: CONN_REVIEW_ME, else gh's, asked once.
func (m *MyPrs) login() (string, error) {
	if m.me != nil {
		return *m.me, nil
	}

	if m.deps.Config.Me != "" {
		found := m.deps.Config.Me
		m.me = &found

		return found, nil
	}

	var (
		found string
		err   error
	)

	m.outside(func() { found, err = m.deps.Gh.Login(m.ctx) })

	if err != nil {
		return "", fmt.Errorf("%w", err)
	}

	if m.me == nil {
		m.me = &found
	}

	return *m.me, nil
}

func sameSet(first, second []string) bool {
	left := slices.Clone(first)
	right := slices.Clone(second)

	slices.Sort(left)
	slices.Sort(right)

	return slices.Equal(left, right)
}

// coverage says which repos a poll speaks for: searched by a pattern whose search did not fail,
// and allowed now.
func coverage(searched []string, failed []SearchFailure, allow []string) func(string) bool {
	read := []string{}

	for _, pattern := range searched {
		failedHere := func(failure SearchFailure) bool { return failure.Pattern == pattern }
		if !slices.ContainsFunc(failed, failedHere) {
			read = append(read, pattern)
		}
	}

	return func(repo string) bool {
		if !repos.Allowed(repo, read) {
			return false
		}

		return repos.Allowed(repo, allow)
	}
}

// apply makes what the poll read the list: the records stamped, pruned and saved, the PRs a
// failed search would have listed carried over, and the sweep started for those that left.
func (m *MyPrs) apply(read pollRead) {
	// GitHub's answer is checked once more: a PR of a repo outside the list is never kept,
	// whatever the search was.
	open := js.NewMap[*PR]()

	for _, pull := range read.found.PRs {
		if read.covers(pull.Repo) {
			open.Set(EntryKey(pull.Repo, pull.Number), pull)
		}
	}

	gone := m.track(open, read)
	m.carry(open, read)

	now := m.now()
	failed := read.found.Failed

	m.failedPatterns = append([]SearchFailure{}, failed...)
	m.rate = read.found.Rate
	m.pausedUntil = nil
	m.deps.Budget.Note(budgetName, now)
	m.deps.Budget.Resume()
	m.loaded = true
	m.capped = read.found.Capped
	m.err = nil

	detail := "My PRs read " + js.Number(float64(open.Len())) + " open PR"
	if open.Len() != 1 {
		detail += "s"
	}

	if len(failed) > 0 {
		detail = "My PRs read GitHub, but " + js.Number(float64(len(failed))) + " of " +
			js.Number(float64(len(read.searched))) + " searches failed"
	}

	m.record(moduleGitHub, true, detail)

	// Only a whole list proves a PR has left it: a capped one may have cut it off or missed it.
	if !read.found.Capped {
		m.startSweep(gone)
	}
}

// track records the listed PRs in the file, prunes and saves it, and answers the entries of
// covered repos that are off the list.
func (m *MyPrs) track(open *js.Map[*PR], read pollRead) []keyedEntry {
	now := m.now()

	if m.file == nil {
		m.file = LoadFile(m.deps.File)
		m.savedSeen = seenOf(m.file)
	}

	file := m.file
	changed := false

	for _, key := range open.Keys() {
		if !file.PRs.Has(key) {
			changed = true
		} else if now-m.savedSeen[key] >= seenSaveMs {
			// The stamp is kept in memory every poll but written about once a day: a restart
			// must not read a PR that has stayed open as long gone.
			changed = true
		}

		TrackOf(file, key, now)
	}

	before := file.PRs.Len()

	// An entry is swept before it can be pruned: this poll is the last chance for one that has
	// been off the list for 14 days. Only an entry of a repo this poll covers can have left.
	gone := []keyedEntry{}

	for _, key := range file.PRs.Keys() {
		if open.Has(key) {
			continue
		}

		if read.covers(repoOfKey(key)) {
			entry, _ := file.PRs.Get(key)
			gone = append(gone, keyedEntry{key: key, entry: entry})
		}
	}

	m.stamp(open, read, now)

	if changed {
		m.save()
	} else if file.PRs.Len() != before {
		m.save()
	}

	return gone
}

// stamp prunes, or, when the list is capped (it may have cut a PR off or missed it, and such a
// PR may still be open), only stamps the listed ones.
func (m *MyPrs) stamp(open *js.Map[*PR], read pollRead, now float64) {
	if read.found.Capped {
		for _, key := range open.Keys() {
			entry, _ := m.file.PRs.Get(key)
			entry.SeenAt = now
		}

		return
	}

	listed := map[string]bool{}
	for _, key := range open.Keys() {
		listed[key] = true
	}

	Prune(m.file, listed, now, func(key string) bool { return read.covers(repoOfKey(key)) })
}

// carry makes the list: what was read, then the PRs a failed search would have listed, as the
// last poll had them (stale: nothing is asked, drafted or read for them).
func (m *MyPrs) carry(open *js.Map[*PR], read pollRead) {
	failedOn := make([]string, 0, len(read.found.Failed))

	for _, failure := range read.found.Failed {
		failedOn = append(failedOn, failure.Pattern)
	}

	list := js.NewMap[*PR]()

	for _, key := range open.Keys() {
		pull, _ := open.Get(key)
		list.Set(key, pull)
	}

	stale := map[string]bool{}

	for _, key := range m.prs.Keys() {
		pull, _ := m.prs.Get(key)

		if open.Has(key) {
			continue
		}

		if !repos.Allowed(pull.Repo, failedOn) {
			continue
		}

		if repos.Allowed(pull.Repo, read.current.Allow) {
			stale[key] = true
			list.Set(key, pull)
		}
	}

	m.staleKeys = stale
	m.prs = list
}

type keyedEntry struct {
	key   string
	entry *Entry
}

// listed are the PRs on the list now with their keys, in order: what an iteration over the
// list sees, since the list is only ever replaced, never changed in place.
func (m *MyPrs) listed() []keyedPR {
	out := make([]keyedPR, 0, m.prs.Len())

	for _, key := range m.prs.Keys() {
		pull, _ := m.prs.Get(key)
		out = append(out, keyedPR{key: key, pull: pull})
	}

	return out
}

type keyedPR struct {
	key  string
	pull *PR
}

// ingestRounds takes the session's proposed replies for the listed PRs, and drops those whose
// thread is closed. One PR's failure must not stop the others or fail the poll.
func (m *MyPrs) ingestRounds() {
	if m.file == nil {
		return
	}

	changed := false

	for _, listed := range m.listed() {
		if m.staleKeys[listed.key] {
			continue
		}

		entry, found := m.file.PRs.Get(listed.key)
		if !found {
			continue
		}

		if m.ingestOne(listed.key, listed.pull, entry) {
			changed = true
		}
	}

	if changed {
		m.save()
		m.bump()
	}
}

// ingestOne reads the proposals of one PR. A proposal whose thread is no longer unresolved
// (resolved, or gone from a list that was not cut off) is dropped, whatever its state. Then the
// newest launch's replies file is read, but only when its worktree is one Conn made and the file
// is newer than the launch. An entry for a thread whose proposal came from this same launch is
// not taken again; one from an earlier launch (or a click, stamped 0) is replaced while it is
// proposed, failed or skipped; a replied or posted one is never touched, since its text is on
// GitHub. It answers whether the proposals changed.
func (m *MyPrs) ingestOne(key string, pull *PR, entry *Entry) bool {
	delete(m.ignored, key)

	listed := map[string]bool{}
	open := map[string]bool{}
	settled := map[string]bool{}

	for _, thread := range pull.Threads {
		listed[thread.ID] = true

		if thread.Resolved {
			settled[thread.ID] = true
		} else {
			open[thread.ID] = true
		}
	}

	// A cut-off thread list says nothing about a thread that is not on it.
	kept := []*Proposal{}

	for _, proposal := range entry.Proposals {
		if open[proposal.ThreadID] {
			kept = append(kept, proposal)
		} else if pull.Partial {
			if !listed[proposal.ThreadID] {
				kept = append(kept, proposal)
			}
		}
	}

	changed := len(kept) != len(entry.Proposals)

	kept, taken := m.takeReplies(key, entry, kept, open, settled)
	if taken {
		changed = true
	}

	if changed {
		entry.Proposals = kept
	}

	return changed
}

// takeReplies adds the replies of the newest launch's file to kept, and answers whether it took
// any.
func (m *MyPrs) takeReplies(key string, entry *Entry, kept []*Proposal,
	open, settled map[string]bool,
) ([]*Proposal, bool) {
	raw, newest := m.repliesOf(entry)
	if newest == nil {
		return kept, false
	}

	replies := AcceptReplies(raw, open, settled)
	if replies.Dropped > 0 {
		m.ignored[key] = replies.Dropped
	}

	changed := false

	for _, accepted := range replies.Accepted {
		fresh := &Proposal{
			ThreadID: accepted.ThreadID, Text: accepted.Text, State: StateProposed,
			Error: nil, LaunchAt: newest.At, TriedAt: nil,
		}

		index := slices.IndexFunc(kept, func(p *Proposal) bool {
			return p.ThreadID == accepted.ThreadID
		})

		switch {
		case index == -1:
			kept = append(kept, fresh)
		case replaceable(kept[index], newest.At):
			// A failed reply's try may be on GitHub, and its text still in the box: the time of
			// the try goes with the thread.
			fresh.TriedAt = kept[index].TriedAt
			kept[index] = fresh
		default:
			continue
		}

		changed = true
	}

	return kept, changed
}

func replaceable(proposal *Proposal, launchAt float64) bool {
	if proposal.LaunchAt >= launchAt {
		return false
	}

	switch proposal.State {
	case StateReplied, StatePosted:
		return false
	}

	return true
}

// repliesOf is the parsed replies file of the newest launch and that launch, when its worktree
// is Conn's and the file is newer than the launch; a nil launch when there is nothing to read.
func (m *MyPrs) repliesOf(entry *Entry) (any, *Launch) {
	if len(entry.Launches) == 0 {
		return nil, nil
	}

	newest := entry.Launches[len(entry.Launches)-1]
	if !m.owned(newest.Worktree) {
		return nil, nil
	}

	at := m.deps.RepliesAt(newest.Worktree)
	if at == nil {
		return nil, nil
	}

	if *at <= newest.At {
		return nil, nil
	}

	content, ok := m.deps.ReadFile(filepath.Join(newest.Worktree, ConnFolder, RepliesFile))
	if !ok {
		return nil, nil
	}

	if len(content) > RepliesFileBytesMax {
		return nil, nil
	}

	parsed, err := js.Parse([]byte(content))
	if err != nil {
		// Not JSON: nothing to take.
		parsed = nil
	}

	return parsed, newest
}

// ---- the Greptile ask ----

// askRounds asks Greptile for the PRs whose round is over, one at a time. One PR's failure must
// not stop the others or fail the poll.
func (m *MyPrs) askRounds() {
	if m.file == nil {
		return
	}

	for _, listed := range m.listed() {
		if m.staleKeys[listed.key] {
			continue
		}

		entry, _ := m.file.PRs.Get(listed.key)
		m.askOne(listed.key, listed.pull, entry)
	}
}

// askOne is the Greptile ask for the newest launch of one PR (an older one is never looked at:
// the press that recorded the newest disarmed it). The ask is made once per pushed head: on
// success the launch records the head and the time and is disarmed. Each try is saved on the
// launch before the call, since a failure (gh gives up after 30 s) may be a comment GitHub took:
// a later list with a comment of mine made since the try settles the round without posting, a
// failed try is made again no sooner than a minute later, and after AskTriesMax for one head
// nothing more is posted for it.
func (m *MyPrs) askOne(key string, pull *PR, entry *Entry) {
	if entry == nil {
		return
	}

	if len(entry.Launches) == 0 {
		return
	}

	launch := entry.Launches[len(entry.Launches)-1]
	if !launch.Armed {
		return
	}

	// The ask is a GitHub write: never for a repo that is not allowed now, or while GitHub is
	// off (the round stays armed for later).
	if !m.permitted(pull.Repo) {
		return
	}

	now := m.now()
	tried := triedFor(launch, pull.HeadSha)

	if tried != nil {
		if m.askLanded(pull, tried) {
			delete(m.askError, key)
			m.asked(launch, pull.HeadSha, tried.FirstAt)
			m.bump()

			return
		}
	}

	facts, wait := m.askFacts(launch, pull, now)
	if wait {
		return
	}

	m.askIfDue(key, pull, launch, tried, facts, now)
}

func triedFor(launch *Launch, head string) *AskTry {
	if launch.AskTry == nil {
		return nil
	}

	if launch.AskTry.Sha != head {
		return nil
	}

	return launch.AskTry
}

// asked records the ask of head on the launch and disarms it.
func (m *MyPrs) asked(launch *Launch, head string, at float64) {
	sha := head
	when := at
	launch.AskedSha = &sha
	launch.AskedAt = &when
	launch.Armed = false

	m.save()
}

// askFacts are the facts of the launch's round, and whether there is nothing to ask now: a
// round to paste that was never pasted, or a round that ended without a push (it is disarmed
// then).
func (m *MyPrs) askFacts(launch *Launch, pull *PR, now float64) (RoundFacts, bool) {
	live := m.deps.Sessions()
	m.markSeen(live)
	session := launchSession(live, launch)

	// A round to paste has no time by which its session must show up. Until its session has
	// been seen nothing of the round has run, so a head that moves meanwhile is someone else's
	// push: the round is disarmed and that push never asked for.
	if m.notPasted(launch, session) {
		if pull.HeadSha != launch.HeadSha {
			launch.Armed = false

			m.save()
		}

		return RoundFacts{}, true
	}

	facts := m.roundFacts(launch, session, now)

	// A round that ended without a push never asks: it is disarmed. Only a list fetched a minute
	// or more after the round's last step proves nothing was pushed.
	if m.endedUnpushed(facts, pull, now) {
		launch.Armed = false

		m.save()

		return RoundFacts{}, true
	}

	return facts, false
}

// notPasted is whether a round to paste has had no session seen yet: nothing of it has run.
func (m *MyPrs) notPasted(launch *Launch, session *sessions.LiveSession) bool {
	if !launch.Manual {
		return false
	}

	if session != nil {
		return false
	}

	return !m.seen[launch]
}

// endedUnpushed is whether the round is over, its head is still the one it started from, and
// the list was fetched a minute or more after its last step: after the session was last live
// when it is gone (a restart forgets when it was, which reads as long ago), or after the replies
// file was written when it stays open (the push comes first).
func (m *MyPrs) endedUnpushed(facts RoundFacts, pull *PR, now float64) bool {
	if !RoundOver(facts) {
		return false
	}

	if pull.HeadSha != facts.Launch.HeadSha {
		return false
	}

	last := math.Inf(1)

	if facts.SessionGone {
		last = math.Inf(-1)
		if at, found := m.lastLive[facts.Launch]; found {
			last = at
		}
	} else if facts.RepliesAt != nil {
		last = *facts.RepliesAt
	}

	start := now
	if m.lastPollStart != nil {
		start = *m.lastPollStart
	}

	return start-last >= AskGapMs
}

func (m *MyPrs) askIfDue(key string, pull *PR, launch *Launch, tried *AskTry,
	facts RoundFacts, now float64,
) {
	lastAsk := math.Inf(-1)
	if at, found := m.lastAsk[key]; found {
		lastAsk = at
	}

	if tried != nil {
		lastAsk = math.Max(lastAsk, tried.At)
	}

	if !AskDue(AskFacts{RoundFacts: facts, PR: pull, LastAskAt: lastAsk, Now: now}) {
		return
	}

	if tried != nil {
		if tried.Tries >= AskTriesMax {
			return
		}
	}

	m.lastAsk[key] = now
	next := &AskTry{Sha: pull.HeadSha, At: now, FirstAt: now, Tries: 1}

	if tried != nil {
		next.FirstAt = tried.FirstAt
		next.Tries = tried.Tries + 1
	}

	launch.AskTry = next

	m.save()

	var err error

	m.outside(func() { err = m.deps.Writes.TriggerReview(m.ctx, pull.Repo, pull.Number) })

	if err != nil {
		m.askError[key] = askFailure{sha: pull.HeadSha, text: message(err)}
		m.bump()

		return
	}

	m.bump()

	delete(m.askError, key)
	// The first try for this head may be the one that reached Greptile (its answer was lost):
	// its review is judged from then.
	m.asked(launch, pull.HeadSha, launch.AskTry.FirstAt)
}

// roundFacts are what decides whether a launch's round is over, as the ask and the row both
// read it; session is the launch's live session, if any.
func (m *MyPrs) roundFacts(launch *Launch, session *sessions.LiveSession, now float64,
) RoundFacts {
	facts := RoundFacts{
		Launch: launch, SessionGone: false, SessionIdle: false, SessionWaiting: false,
		QuietMs: 0, RepliesAt: nil,
	}

	// A session that has not shown up yet is not gone: a push by anyone else in that wait must
	// not read as the end of the round.
	if session == nil {
		facts.SessionGone = m.sessionGone(launch, now)
	} else {
		facts.SessionIdle = session.Status == sessionIdle
		facts.SessionWaiting = session.Status == "waiting"

		if since, found := m.idleSince[launch]; found {
			facts.QuietMs = now - since
		}
	}

	// Never a path outside <dataDir>/myprs: the file only records where the worktree was.
	if m.owned(launch.Worktree) {
		facts.RepliesAt = m.deps.RepliesAt(launch.Worktree)
	}

	return facts
}

// sessionGone is whether launch's session, not live now, is gone: seen before, or not shown up
// within appearMs. While this launch of Conn resumes its last quit's sessions, and for appearMs
// after, it may be one the launch resumes: it is not gone yet.
func (m *MyPrs) sessionGone(launch *Launch, now float64) bool {
	if m.resumeHeld(now) {
		return false
	}

	if m.seen[launch] {
		return true
	}

	return now-launch.At >= appearMs
}

// resumeHeld is whether this launch of Conn has not finished resuming its last quit's sessions,
// or did less than appearMs ago: a session not live may be one it resumes.
func (m *MyPrs) resumeHeld(now float64) bool {
	settledAt, settled := m.deps.Settled()
	if !settled {
		return true
	}

	return now-settledAt < appearMs
}

// askLanded is whether a try of the ask shows on the PR: my own ask made since the first try for
// this head, less the skew of the clocks.
func (m *MyPrs) askLanded(pull *PR, tried *AskTry) bool {
	if m.me == nil {
		return false
	}

	for _, comment := range pull.Comments {
		if !IsOwnAsk(comment, *m.me, m.deps.Config.GreptileTrigger) {
			continue
		}

		if comment.At >= tried.FirstAt-AskSkewMs {
			return true
		}
	}

	return false
}

// ---- sessions ----

// isLaunchSession is whether a live session is this launch's: its exact name, working in the
// launch's worktree or a folder under it, links resolved. The name is compared first, so only a
// likely match costs a path lookup.
func isLaunchSession(session sessions.LiveSession, launch *Launch) bool {
	if session.Name != launch.Name {
		return false
	}

	return reviews.Within(reviews.Real(session.Cwd), reviews.Real(launch.Worktree))
}

func launchSession(live []sessions.LiveSession, launch *Launch) *sessions.LiveSession {
	for index := range live {
		if isLaunchSession(live[index], launch) {
			return &live[index]
		}
	}

	return nil
}

// markSeen remembers each launch whose session is up now, so one that came and went is not
// waited for as if it never appeared, and since when it has been idle.
func (m *MyPrs) markSeen(live []sessions.LiveSession) {
	if m.file == nil {
		return
	}

	now := m.now()

	for _, key := range m.file.PRs.Keys() {
		entry, _ := m.file.PRs.Get(key)

		for _, launch := range entry.Launches {
			session := launchSession(live, launch)
			if session != nil {
				m.seen[launch] = true
				m.lastLive[launch] = now
			}

			if session == nil {
				delete(m.idleSince, launch)
			} else if session.Status != sessionIdle {
				delete(m.idleSince, launch)
			} else if _, found := m.idleSince[launch]; !found {
				m.idleSince[launch] = now
			}
		}
	}
}

// owned is whether path is strictly inside <dataDir>/myprs, links and `..` resolved: the file is
// only a record, so it never decides what git is pointed at.
func (m *MyPrs) owned(path string) bool {
	if !strings.HasPrefix(path, "/") {
		return false
	}

	root := reviews.Real(absolute(filepath.Join(m.deps.DataDir, "myprs")))
	resolved := reviews.Real(absolute(path))

	if resolved == root {
		return false
	}

	return reviews.Within(resolved, root)
}

// absolute is path.resolve(path): against the working folder, cleaned.
func absolute(path string) string {
	out, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}

	return out
}

// openRound is the live session of a PR and whether its round is over.
type openRound struct {
	name string
	over bool
}

// openSession is the live session of this PR, and whether its round is over: a session of one
// of its launches is over by RoundOver, or once the launch is disarmed and the session sits
// idle. One of its rounds that Conn has no record of says nothing of its end, so it never is.
func (m *MyPrs) openSession(pull *PR, entry *Entry, live []sessions.LiveSession, now float64,
) *openRound {
	if launch := liveLaunch(entry, live); launch != nil {
		session := launchSession(live, launch)

		over := false
		if launch.Armed {
			over = RoundOver(m.roundFacts(launch, session, now))
		} else if session != nil {
			over = session.Status == sessionIdle
		}

		return &openRound{name: launch.Name, over: over}
	}

	if session := m.liveRound(pull, live); session != nil {
		return &openRound{name: session.Name, over: false}
	}

	return nil
}

// liveLaunch is the live session's launch among this PR's own: its name exactly, and its
// worktree. A name alone says nothing (self-web-12-3 is two PRs).
func liveLaunch(entry *Entry, live []sessions.LiveSession) *Launch {
	if entry == nil {
		return nil
	}

	for _, launch := range entry.Launches {
		if launchSession(live, launch) != nil {
			return launch
		}
	}

	return nil
}

// liveRound is a live session of this PR whatever the file records (a round pasted by hand, a
// record lost): named as one of its rounds and working in this PR's own worktree folder or
// under it, links resolved.
func (m *MyPrs) liveRound(pull *PR, live []sessions.LiveSession) *sessions.LiveSession {
	base, err := m.deps.Config.Self.Name(pull.Repo, pull.Number, 1)
	if err != nil {
		return nil
	}

	folder, err := Folder(m.deps.DataDir, pull.Repo, pull.Number)
	if err != nil {
		return nil
	}

	folder = reviews.Real(folder)

	for index := range live {
		session := &live[index]
		if !roundNamed(session.Name, base) {
			continue
		}

		if reviews.Within(reviews.Real(session.Cwd), folder) {
			return session
		}
	}

	return nil
}

func roundNamed(name, base string) bool {
	if name == base {
		return true
	}

	rest, found := strings.CutPrefix(name, base+"-")
	if !found {
		return false
	}

	return digits.MatchString(rest)
}
