package myprs

import (
	"math"
	"strings"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/sessions"
)

// The row statuses.
const (
	statusIdle        = "idle"
	statusRunning     = "running"
	statusFork        = "fork"
	statusNoClone     = "no-clone"
	statusDraftPR     = "draft-pr"
	statusReady       = "ready"
	statusDraftFailed = "draft-failed"
	statusFindings    = "findings"
	statusWaiting     = "waiting"
)

const textStale = "its repository could not be searched in the last poll: this PR may be out " +
	"of date"

// Row is one line of the My PRs list.
type Row struct {
	Repo          string  `json:"repo"`
	Number        float64 `json:"number"`
	Title         string  `json:"title"`
	URL           string  `json:"url"`
	Draft         bool    `json:"draft"`
	CreatedAt     float64 `json:"createdAt"`
	LastCommentAt float64 `json:"lastCommentAt"`
	// OpenBot, OpenHuman and Outdated are the thread counts (Counts).
	OpenBot   int         `json:"openBot"`
	OpenHuman int         `json:"openHuman"`
	Outdated  int         `json:"outdated"`
	Threads   []RowThread `json:"threads"`
	Partial   bool        `json:"partial"`
	Round     float64     `json:"round"`
	Status    string      `json:"status"`
	CanStart  bool        `json:"canStart"`
	Reason    *string     `json:"reason"`
	Label     string      `json:"label"`
	// SlackDraft is the draft saved for the ready head (nil until there is one).
	SlackDraft *SlackDraft `json:"slackDraft"`
	// DraftOff is that no draft is made now (Slack is off or has no channel).
	DraftOff bool `json:"draftOff,omitempty"`
	// Error is why the last Greptile ask failed, as one sentence to show as it is.
	Error *string `json:"error"`
	// DraftRetryAt is when the failed Slack draft for this head is tried again.
	DraftRetryAt *float64 `json:"draftRetryAt"`
	// IgnoredReplies are entries of the newest round's replies file that were not taken.
	IgnoredReplies int `json:"ignoredReplies"`
}

// Refresh is the REFRESH presses: Seq the last one pressed, Done the last one whose poll has
// finished.
type Refresh struct {
	Seq  int `json:"seq"`
	Done int `json:"done"`
}

// View is the My PRs tab's state.
type View struct {
	At            float64            `json:"at"`
	Me            *string            `json:"me"`
	Error         *string            `json:"error"`
	Capped        bool               `json:"capped"`
	Refresh       Refresh            `json:"refresh"`
	Loaded        bool               `json:"loaded"`
	DraftMaxTries int                `json:"draftMaxTries"`
	Rows          []*Row             `json:"rows"`
	Channel       *string            `json:"channel"`
	Off           *string            `json:"off"`
	Warning       *string            `json:"warning"`
	Rate          *ghbudget.RateInfo `json:"rate"`
	PausedUntil   *float64           `json:"pausedUntil"`
}

func textPointer(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

// Snapshot is the view now.
func (m *MyPrs) Snapshot() View {
	m.mu.Lock()
	defer m.mu.Unlock()

	current := m.scope()
	off := offOf(current)

	var channel *string

	if off == "" {
		if current.Slack {
			channel = current.DraftChannel
		}
	}

	rows := []*Row{}
	if off == "" {
		rows = m.rows(current, channel == nil)
	}

	return View{
		At: m.now(), Me: m.me, Error: m.err, Capped: m.capped,
		Refresh: Refresh{Seq: m.refreshSeq, Done: m.refreshDone}, Loaded: m.loaded,
		DraftMaxTries: MaxDraftTries, Rows: rows, Channel: channel, Off: textPointer(off),
		Warning: textPointer(m.warning()), Rate: m.rate, PausedUntil: m.pausedUntil,
	}
}

// Repos are the repos of the rows, in row order: what the Settings page suggests from.
func (m *MyPrs) Repos() []string {
	rows := m.Snapshot().Rows
	out := make([]string, 0, len(rows))

	for _, row := range rows {
		out = append(out, row.Repo)
	}

	return out
}

// rows are the allowed repos' PRs (whatever the last poll kept: a change of the list takes
// effect here at once), newest opened first; the page sorts again to the viewer's choice.
func (m *MyPrs) rows(current Scope, draftOff bool) []*Row {
	live := m.deps.Sessions()
	// The clone lookup reads the disk: once per repo for the whole snapshot, not once per row.
	clones := map[string]string{}
	cloneOf := func(repo string) string {
		if clone, found := clones[repo]; found {
			return clone
		}

		clone := m.deps.CloneFor(repo)
		clones[repo] = clone

		return clone
	}

	rows := []*Row{}

	for _, listed := range m.listed() {
		if !repos.Allowed(listed.pull.Repo, current.Allow) {
			continue
		}

		var entry *Entry
		if m.file != nil {
			entry, _ = m.file.PRs.Get(listed.key)
		}

		rows = append(rows, m.rowFor(listed.pull, entry, live, cloneOf, draftOff))
	}

	js.Sort(rows, func(left, right *Row) float64 {
		if order := right.CreatedAt - left.CreatedAt; order != 0 {
			return order
		}

		if order := Collate(left.Repo, right.Repo); order != 0 {
			return float64(order)
		}

		return left.Number - right.Number
	})

	return rows
}

// rowState is what decides a row's status and reason.
type rowState struct {
	pull     *PR
	entry    *Entry
	counts   Counts
	open     *openRound
	asked    *Launch
	draft    *SlackDraft
	draftOff bool
}

func (m *MyPrs) rowFor(pull *PR, entry *Entry, live []sessions.LiveSession,
	cloneOf func(string) string, draftOff bool,
) *Row {
	var launches []*Launch
	if entry != nil {
		launches = entry.Launches
	}

	state := rowState{
		pull: pull, entry: entry, counts: ThreadCounts(pull, m.deps.Config.FindingBots),
		open: m.openSession(pull, entry, live, m.now()), asked: AskedLaunch(launches,
			pull.HeadSha),
		// The draft kept for this head only: one saved for an earlier head says nothing.
		draft: nil, draftOff: draftOff,
	}

	if entry != nil {
		if entry.Draft != nil {
			if entry.Draft.Sha == pull.HeadSha {
				state.draft = entry.Draft
			}
		}
	}

	round := 0.0
	for _, launch := range launches {
		round = math.Max(round, launch.Round)
	}

	status, reason := m.statusOf(state, cloneOf)
	reason = m.reasonOf(state, reason)

	var proposals []*Proposal
	if entry != nil {
		proposals = entry.Proposals
	}

	label := "Self-review"
	if round > 0 {
		label = "Re-review"
	}

	key := EntryKey(pull.Repo, pull.Number)

	return &Row{
		Repo: pull.Repo, Number: pull.Number, Title: pull.Title, URL: pull.URL,
		Draft: pull.Draft, CreatedAt: pull.CreatedAt, LastCommentAt: pull.LastCommentAt,
		OpenBot: state.counts.OpenBot, OpenHuman: state.counts.OpenHuman,
		Outdated: state.counts.Outdated, Threads: RowThreads(pull, proposals),
		Partial: pull.Partial,
		Round:   round, Status: status, CanStart: reason == "", Reason: textPointer(reason),
		Label: label, SlackDraft: state.draft, DraftOff: draftOff,
		Error:        textPointer(m.askErrorOf(key, pull, launches)),
		DraftRetryAt: draftRetryAt(status, state), IgnoredReplies: m.ignored[key],
	}
}

// statusOf is the row's status, and the reason a status of its own gives.
func (m *MyPrs) statusOf(state rowState, cloneOf func(string) string) (string, string) {
	pull := state.pull

	if state.open != nil {
		if !state.open.over {
			return statusRunning, "a session named " + state.open.name + " is running"
		}
	}

	switch {
	case pull.Fork:
		return statusFork, forkStart
	case cloneOf(pull.Repo) == "":
		return statusNoClone, noClone(pull.Repo)
	case pull.Draft:
		// Informational only: a draft PR can still be self-reviewed.
		return statusDraftPR, ""
	case state.asked != nil:
		return m.askedStatus(state), ""
	}

	return statusIdle, ""
}

// askedStatus is the status of a head a round pushed and Greptile was asked for: waiting for
// it, then findings to fix or ready.
func (m *MyPrs) askedStatus(state rowState) string {
	cfg := m.deps.Config
	pull := state.pull
	asked := state.asked

	if ReadyFor(pull, asked, cfg.FindingBots, cfg.GreptileBot) {
		return readyStatus(state)
	}

	if asked.AskedAt == nil {
		return statusWaiting
	}

	if !BotReviewed(pull, cfg.GreptileBot, *asked.AskedAt) {
		return statusWaiting
	}

	if state.counts.OpenBot > 0 {
		return statusFindings
	}

	return statusWaiting
}

// readyStatus is a ready row's status. No draft is made now (Slack off, or no channel): a
// failed one kept for this head is not being retried, so it never makes the row draft-failed.
func readyStatus(state rowState) string {
	if state.draftOff {
		return statusReady
	}

	if state.draft == nil {
		return statusReady
	}

	if state.draft.State == DraftFailed {
		return statusDraftFailed
	}

	return statusReady
}

// reasonOf is why the button cannot start: a round still open, else the row's own reason, else
// a protected branch, a stale row or an ask still owed.
func (m *MyPrs) reasonOf(state rowState, reason string) string {
	if state.open != nil {
		if state.open.over {
			reason = stillOpen(state.open.name)
		}
	}

	// No status of its own: the row says what it is, and the button says why it cannot start.
	if reason == "" {
		reason = m.headRefusal(state.pull)
	}

	if reason == "" {
		if m.staleKeys[EntryKey(state.pull.Repo, state.pull.Number)] {
			reason = textStale
		}
	}

	if reason == "" {
		if m.askOwed(state.pull, state.entry) {
			reason = askOwedReason
		}
	}

	return reason
}

// askErrorOf is why the Greptile ask failed, while it is still the head that is waiting for one
// (it is tried again a minute later), or that the tries for this head are used and none showed
// on the PR.
func (m *MyPrs) askErrorOf(key string, pull *PR, launches []*Launch) string {
	if len(launches) == 0 {
		return ""
	}

	newest := launches[len(launches)-1]
	if !newest.Armed {
		return ""
	}

	why := ""
	shown := ""

	if failure, found := m.askError[key]; found {
		if failure.sha == pull.HeadSha {
			why = failure.text
			shown = Clean("Could not ask Greptile for a review, trying again: "+why,
				maxMessageUnits)
		}
	}

	tried := triedFor(newest, pull.HeadSha)
	if tried == nil {
		return shown
	}

	if tried.Tries < MaxAskTries {
		return shown
	}

	// What to do comes before the reason, so a long reason is what the cut takes.
	text := "Could not ask Greptile for a review: the ask could not be confirmed after " +
		js.Number(MaxAskTries) + " tries, so conn has stopped. Look at the PR's comments, and " +
		"ask by hand if none is there."
	if why != "" {
		text += " (" + why + ")"
	}

	return Clean(text, maxMessageUnits)
}

// draftRetryAt is draftDue's retry, for the row to say when.
func draftRetryAt(status string, state rowState) *float64 {
	if state.draftOff {
		return nil
	}

	if status != statusDraftFailed {
		return nil
	}

	if state.draft == nil {
		return nil
	}

	if state.draft.Tries >= MaxDraftTries {
		return nil
	}

	at := state.draft.At + DraftRetryMs

	return &at
}

// warning is what to say beside the list when it is partial: a pattern whose search failed (the
// rest were read), or the rate limit that has polling waiting. "" when all is well.
func (m *MyPrs) warning() string {
	if m.pausedUntil != nil {
		return "GitHub's rate limit is used up: the list below is from the last poll, and conn " +
			"asks again once the limit resets."
	}

	if len(m.failedPatterns) == 0 {
		return ""
	}

	names := make([]string, 0, len(m.failedPatterns))
	for _, failure := range m.failedPatterns {
		names = append(names, failure.Pattern)
	}

	return Clean("Could not search "+strings.Join(names, ", ")+" ("+m.failedPatterns[0].Error+
		"): the other repositories are listed, and these PRs are as the last poll had them.",
		maxWarningUnits)
}
