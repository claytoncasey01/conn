package reviews

import (
	"errors"
	"strconv"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/settings"
)

// One poll. The universe is the union of the "Requests me" search, the "every open PR"
// searches, and every tracked record of an allowed repo read by number (that is how a PR I
// reviewed, which GitHub no longer reports as requesting me, keeps its state); the scans add the
// PRs they find. Whether a tracked PR is open, merged, closed or gone comes from its own read,
// never from being absent from a list. A pattern GitHub could not search leaves its repos' rows
// as they were (stale: not read, ended, pruned or swept), and the other patterns go on. The
// settings are read again before the reads and at the end: a change in the meantime reads
// nothing the new settings forbid, and prunes and sweeps nothing (the next poll, due at once,
// does it with the new list).

// pollQuery is one search of a poll, with the pattern it is for.
type pollQuery struct {
	q         string
	limit     int
	pattern   string
	requested bool
}

// pollRun is what one poll carries between its steps.
type pollRun struct {
	current settings.Settings
	version uint64
	// file is the file the poll started with: it writes into that one.
	file  *File
	allow []string
	me    string
	// patterns are the patterns searched, queries the searches made of them.
	patterns []string
	queries  []pollQuery
	results  []SearchResult
	rate     *ghbudget.RateInfo
	// found are the open PRs the searches listed, requested those "Requests me" listed.
	found     *js.Map[*PR]
	requested map[string]bool
	capped    []string
	failed    *js.Map[string]
	failedOn  []string
	now       float64
	wanted    []KeyParts
	undecided map[string]bool
	read      *js.Map[*PR]
	spent     *ghbudget.RateInfo
}

// stale is whether a repo is covered by a pattern GitHub could not search: what its searches
// would have said is not known.
func (p *pollRun) stale(repo string) bool {
	if len(p.failedOn) == 0 {
		return false
	}

	return repos.Allowed(repo, p.failedOn)
}

// startPoll runs the start of a poll on the tick, as Bun's does, and the rest on its own
// goroutine.
func (r *Reviews) startPoll(current settings.Settings, version uint64) {
	allow := current.Repos.Allow
	if allow == nil {
		allow = []string{}
	}

	run := &pollRun{
		current: current, version: version, file: nil, allow: allow, me: "", patterns: nil,
		queries: nil,
		results: nil, rate: nil, found: js.NewMap[*PR](), requested: map[string]bool{},
		capped: []string{}, failed: js.NewMap[string](), failedOn: nil, now: 0, wanted: nil,
		undecided: map[string]bool{}, read: js.NewMap[*PR](), spent: nil,
	}

	r.addedDuringPoll.Clear()

	if r.file == nil {
		file, err := r.open(&current)
		if err != nil {
			r.pollFailed(err)
			r.pollEnded()

			return
		}

		r.file = file
	}

	run.file = r.file

	r.spawn(func() {
		err := r.poll(run)
		if err != nil {
			r.pollFailed(err)
		}

		r.pollEnded()
	})
}

func (r *Reviews) pollEnded() {
	r.polling = false
	r.settle()
	r.bump()
}

// pollFailed keeps the list as it was. A rate limit is not a failure of the list: nothing more
// is asked of GitHub until it resets.
func (r *Reviews) pollFailed(err error) {
	if limit, isLimit := errors.AsType[*ghbudget.RateLimitError](err); isLimit {
		r.pauseFor(limit)

		return
	}

	why := message(err)
	r.err = &why
	r.record(moduleGitHub, false, "Reviews could not read GitHub")
}

// pauseFor pauses every GitHub call (this poller's and the others') until the limit resets.
func (r *Reviews) pauseFor(limit *ghbudget.RateLimitError) {
	now := r.now()

	until := now + ghbudget.RatePauseMs
	if limit.ResetAt != nil {
		if *limit.ResetAt > now {
			until = *limit.ResetAt
		}
	}

	r.pausedUntil = &until
	r.deps.Budget.Pause(until)
	r.record(moduleGitHub, false, rateDetail)
}

// poll runs a poll from the login on. It holds the mutex, and lets go of it for each GitHub call.
func (r *Reviews) poll(run *pollRun) error {
	err := r.knowMe()
	if err != nil {
		return err
	}

	run.me = *r.me

	err = r.search(run)
	if err != nil {
		return err
	}

	r.readSearches(run)
	run.now = r.now()
	r.chooseReads(run)

	if len(run.wanted) > 0 {
		var read *js.Map[*PR]

		r.outside(func() {
			read, err = r.deps.Gh.Fetch(r.ctx, run.wanted, func(rate *ghbudget.RateInfo) {
				run.spent = ghbudget.AddRate(run.spent, rate)
			})
		})

		if err != nil {
			return asIs(err)
		}

		run.read = read
	}

	r.applyPoll(run)

	return nil
}

// knowMe makes sure the reviewer's login is known: CONN_REVIEW_ME, else the signed-in account.
func (r *Reviews) knowMe() error {
	if r.me != nil {
		return nil
	}

	if r.deps.Config.Me != "" {
		me := r.deps.Config.Me
		r.me = &me

		return nil
	}

	var (
		login string
		err   error
	)

	r.outside(func() { login, err = r.deps.Gh.Login(r.ctx) })

	if err != nil {
		return asIs(err)
	}

	if r.me == nil {
		r.me = &login
	}

	return nil
}

// search runs the poll's searches: "Requests me" per pattern, and "every open PR" per pattern
// while it is on.
func (r *Reviews) search(run *pollRun) error {
	run.patterns = SearchPatterns(run.allow)

	for _, pattern := range run.patterns {
		if !run.current.Reviews.Requested {
			break
		}

		if q := SearchFor(SearchRequested, pattern); q != "" {
			run.queries = append(run.queries, pollQuery{
				q: q, limit: requestedLimit, pattern: pattern, requested: true,
			})
		}
	}

	for _, pattern := range run.patterns {
		if !run.current.Reviews.AllOpen {
			break
		}

		if q := SearchFor(SearchOpen, pattern); q != "" {
			run.queries = append(run.queries, pollQuery{
				q: q, limit: OpenLimit, pattern: pattern, requested: false,
			})
		}
	}

	if len(run.queries) == 0 {
		return nil
	}

	queries := make([]Query, 0, len(run.queries))
	for _, query := range run.queries {
		queries = append(queries, Query{Q: query.q, Limit: query.limit})
	}

	var err error

	r.outside(func() { run.results, run.rate, err = r.deps.Gh.Search(r.ctx, queries) })

	return asIs(err)
}

// readSearches sorts what the searches found: every PR is checked against the list again (a
// search qualifier is GitHub's, the list is ours).
func (r *Reviews) readSearches(run *pollRun) {
	for index, result := range run.results {
		query := run.queries[index]
		if result.Error != nil {
			run.failed.Set(query.pattern, *result.Error)

			continue
		}

		if result.Capped {
			if !query.requested {
				run.capped = append(run.capped, query.pattern)
			}
		}

		for _, pull := range result.PRs {
			if !repos.Allowed(pull.Repo, run.allow) {
				continue
			}

			if pull.Ended != nil {
				continue
			}

			key := keyOf(pull.Repo, pull.Number)
			run.found.Set(key, pull)

			if query.requested {
				run.requested[key] = true
			}
		}
	}

	run.failedOn = run.failed.Keys()
}

// chooseReads picks the tracked records the searches did not list that are read by number:
// those of allowed repos with a reason to be read now.
func (r *Reviews) chooseReads(run *pollRun) {
	live, _ := r.settings()
	src := SourcesOf(&live)
	githubOn := r.githubOn(&live)

	for _, key := range run.file.PRs.Keys() {
		parts, ok := ParseKey(key)
		if !ok {
			continue
		}

		if !repos.Allowed(parts.Repo, run.allow) {
			continue
		}

		if run.found.Has(key) {
			continue
		}

		if !all(!run.stale(parts.Repo), repos.Allowed(parts.Repo, live.Repos.Allow), githubOn) {
			run.undecided[key] = true

			continue
		}

		entry, _ := run.file.PRs.Get(key)
		if r.readNoMore(key, entry, run.now) {
			continue
		}

		// Only a record something on could list is read (a launch counts: its session may still
		// need closing).
		if !listable(entry, src) {
			// Held back only while it holds something a source could list again; an empty record
			// ages out as any other.
			if anyOf(entry.Requested, entry.Slack != nil, entry.DM != nil) {
				run.undecided[key] = true
			}

			continue
		}

		run.wanted = append(run.wanted, parts)
	}
}

// readNoMore is whether an ended record is past reading: an ended PR is read once more only to
// show it for its 24 hours after a restart; after that, and once gone, never again.
func (r *Reviews) readNoMore(key string, entry *Entry, now float64) bool {
	if entry.Ended == nil {
		return false
	}

	if entry.Ended.How == endedGone {
		return true
	}

	if now-entry.Ended.At >= endedKeepMs {
		return true
	}

	return r.endedPrs.Has(key)
}

func listable(entry *Entry, src Sources) bool {
	return anyOf(
		all(entry.Requested, src.Requested),
		all(entry.Slack != nil, src.Channels != nil),
		all(entry.DM != nil, src.DMs),
		len(entry.Launches) > 0,
		src.AllOpen,
	)
}

// anyOf is whether one check passed.
func anyOf(checks ...bool) bool {
	for _, check := range checks {
		if check {
			return true
		}
	}

	return false
}

// applyPoll makes what the poll read the list, in one step, so a finished PR is never missing
// from the rows between the two lists.
func (r *Reviews) applyPoll(run *pollRun) {
	live, _ := r.settings()
	next := js.NewMap[*PR]()
	ended := r.stillEnded(run)

	for _, key := range run.found.Keys() {
		pull, _ := run.found.Get(key)

		// A repo taken off the list while the search ran is neither listed nor stored.
		if !repos.Allowed(pull.Repo, live.Repos.Allow) {
			continue
		}

		next.Set(key, pull)

		// Drafts and my own PRs are not reviews to track (DeriveRow does not list them either).
		if anyOf(pull.IsDraft, same(pull.Author, run.me)) {
			continue
		}

		entry := TrackOf(run.file, key, run.now)
		entry.SeenAt = run.now
		entry.Ended = nil

		if run.requested[key] {
			entry.Requested = true
		}
	}

	r.applyReads(run, next, ended)
	r.keepUnread(run, next)

	r.prs = next
	r.endedPrs = ended
	r.capped = run.capped
	r.failed = failedList(run.failed)
	r.rate = ghbudget.AddRate(run.rate, run.spent)
	r.pausedUntil = nil
	at := run.now
	r.lastPollAt = &at
	r.err = nil

	if anyOf(len(run.queries) > 0, len(run.wanted) > 0) {
		r.deps.Budget.Note(budgetName, run.now)
		r.deps.Budget.Resume()
		r.record(moduleGitHub, true, pollDetail(run, next.Len()))
	}

	// The settings changed while this poll ran: the next poll (due at once) prunes and sweeps
	// with the new list.
	if _, version := r.settings(); version == run.version {
		Prune(run.file, func(key string) bool { return anyOf(next.Has(key), run.undecided[key]) },
			run.allow, run.now, r.keepForWorktree)
		r.startSweep(run.current, run.stale)
	}

	r.save()
}

// stillEnded are the PRs that ended within the day and were not found open again.
func (r *Reviews) stillEnded(run *pollRun) *js.Map[*PR] {
	ended := js.NewMap[*PR]()

	for _, key := range r.endedPrs.Keys() {
		pull, _ := r.endedPrs.Get(key)
		if !repos.Allowed(pull.Repo, run.allow) {
			continue
		}

		if run.now-valueOr(pull.EndedAt, run.now) >= endedKeepMs {
			continue
		}

		if !run.found.Has(key) {
			ended.Set(key, pull)
		}
	}

	return ended
}

// applyReads applies the reads by number: gone, ended, or open again.
func (r *Reviews) applyReads(run *pollRun, next, ended *js.Map[*PR]) {
	for _, parts := range run.wanted {
		key := keyOf(parts.Repo, parts.Number)

		entry, tracked := run.file.PRs.Get(key)
		if !tracked {
			continue
		}

		pull, answered := run.read.Get(key)
		if !answered {
			// Its read did not answer: what was known stands, and nothing is decided about it.
			run.undecided[key] = true

			if had, found := r.prs.Get(key); found {
				next.Set(key, had)
			}

			continue
		}

		applyRead(run, key, entry, pull, next, ended)
	}
}

// applyRead applies one answered read: gone (pull is nil), ended, or open again.
func applyRead(run *pollRun, key string, entry *Entry, pull *PR, next, ended *js.Map[*PR]) {
	switch {
	case pull == nil:
		entry.Ended = &Ended{How: endedGone, At: run.now}

		ended.Delete(key)
	case pull.Ended != nil:
		when := valueOr(pull.EndedAt, run.now)
		if entry.Ended == nil {
			entry.Ended = &Ended{How: *pull.Ended, At: when}
		}

		if run.now-when < endedKeepMs {
			copied := *pull
			copied.EndedAt = &when
			ended.Set(key, &copied)
		}
	default:
		entry.Ended = nil
		entry.SeenAt = run.now

		if requestsMe(pull, run.me) {
			entry.Requested = true
		}

		next.Set(key, pull)
		ended.Delete(key)
	}
}

// keepUnread keeps what a stale pattern covered, and what a scan read in while this poll ran.
func (r *Reviews) keepUnread(run *pollRun, next *js.Map[*PR]) {
	for _, key := range r.prs.Keys() {
		pr, _ := r.prs.Get(key)
		if all(!next.Has(key), repos.Allowed(pr.Repo, run.allow), run.stale(pr.Repo)) {
			next.Set(key, pr)
		}
	}

	for _, key := range r.addedDuringPoll.Keys() {
		pull, found := r.prs.Get(key)
		if !found {
			continue
		}

		if all(!next.Has(key), repos.Allowed(pull.Repo, run.allow)) {
			next.Set(key, pull)
		}
	}
}

func failedList(failed *js.Map[string]) []failedPattern {
	out := make([]failedPattern, 0, failed.Len())

	for _, pattern := range failed.Keys() {
		why, _ := failed.Get(pattern)
		out = append(out, failedPattern{Pattern: pattern, Error: why})
	}

	return out
}

// pollDetail is what the GitHub card says after a poll.
func pollDetail(run *pollRun, open int) string {
	if run.failed.Len() > 0 {
		return "Reviews read GitHub, but the search failed for " + strconv.Itoa(run.failed.Len()) +
			" of " + strconv.Itoa(len(run.patterns)) + " patterns"
	}

	plural := "s"
	if open == 1 {
		plural = ""
	}

	return "Reviews read " + strconv.Itoa(open) + " open PR" + plural
}

// keepForWorktree holds a record back from the prune while one of its worktrees is on disk (the
// sweep finds it through the record).
func (r *Reviews) keepForWorktree(key string, entry *Entry) bool {
	for _, launch := range entry.Launches {
		if !r.ownsWorktree(key, launch) {
			continue
		}

		if r.exists(launch.Worktree) {
			return true
		}
	}

	return false
}
