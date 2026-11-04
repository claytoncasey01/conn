package reviews

import (
	"math"
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/nodepath"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/settings"
)

// refreshCount is the REFRESH presses counted: seq is the last one pressed, done the last one
// whose poll (and scan) has finished.
type refreshCount struct {
	Seq  int `json:"seq"`
	Done int `json:"done"`
}

// viewSources are which review sources are on, so the page can say what it lists.
type viewSources struct {
	Requested bool     `json:"requested"`
	Channels  []string `json:"channels"`
	DMs       bool     `json:"dms"`
	AllOpen   bool     `json:"allOpen"`
}

// LegacyReview is an old record the migration could not place: the newest launch's clone
// folder is the hint.
type LegacyReview struct {
	Number float64 `json:"number"`
	Folder *string `json:"folder"`
	At     float64 `json:"at"`
}

// View is the Reviews tab's state, as the snapshot carries it.
type View struct {
	// Channel is the chosen channels joined for display ("" when the channel scan is off).
	Channel        string          `json:"channel"`
	Enabled        bool            `json:"enabled"`
	DisabledReason *string         `json:"disabledReason"`
	Me             *string         `json:"me"`
	Rows           []*Row          `json:"rows"`
	LastPollAt     *float64        `json:"lastPollAt"`
	LastScanAt     *float64        `json:"lastScanAt"`
	Error          *string         `json:"error"`
	Slack          string          `json:"slack"`
	SlackError     *string         `json:"slackError"`
	DMError        *string         `json:"dmError"`
	Refresh        refreshCount    `json:"refresh"`
	Capped         []string        `json:"capped"`
	Failed         []failedPattern `json:"failed"`
	StoreError     *string         `json:"storeError"`
	PausedUntil    *float64        `json:"pausedUntil"`
	Sources        viewSources     `json:"sources"`
	Legacy         []LegacyReview  `json:"legacy"`
}

// dryView is the fixed view of a dry run: it has no store, failures or pause to show.
type dryView struct {
	Channel        string         `json:"channel"`
	Enabled        bool           `json:"enabled"`
	DisabledReason *string        `json:"disabledReason"`
	Me             *string        `json:"me"`
	Rows           []*Row         `json:"rows"`
	LastPollAt     float64        `json:"lastPollAt"`
	LastScanAt     float64        `json:"lastScanAt"`
	Error          *string        `json:"error"`
	Slack          string         `json:"slack"`
	SlackError     *string        `json:"slackError"`
	DMError        *string        `json:"dmError"`
	Refresh        refreshCount   `json:"refresh"`
	Capped         []string       `json:"capped"`
	Sources        viewSources    `json:"sources"`
	Legacy         []LegacyReview `json:"legacy"`
}

// rowFor is a PR's row, against its record (a fresh one when there is none).
func (r *Reviews) rowFor(
	pull *PR, now float64, live map[string]*RowSession, sources Sources,
) *Row {
	if r.me == nil {
		return nil
	}

	key := keyOf(pull.Repo, pull.Number)

	var tracking *Entry
	if r.file != nil {
		tracking, _ = r.file.PRs.Get(key)
	}

	if tracking == nil {
		tracking = &Entry{
			Slack: nil, SlackAskAt: nil, DM: nil, DMLastAt: nil, FirstSeenAt: now,
			Launches: []*Launch{}, Scan: Scan{Tries: 0, LastAt: 0},
			ThreadScan: ThreadScan{Tries: 0, LastAt: 0, HeadSha: ""}, Requested: false,
			Ended: nil, SeenAt: now, PlacedAt: nil,
		}
	}

	return DeriveRow(pull, tracking, &DeriveContext{
		Me: *r.me, Sources: sources, Session: live[key], Now: now,
	}, key)
}

// shownSources are the sources the page lists by now: a Slack source whose last read failed
// lists nothing until a read succeeds. The records stay, so its rows come back with the next good
// scan.
func (r *Reviews) shownSources(current *settings.Settings) Sources {
	src := SourcesOf(current)

	if anyOf(r.slack == slackNeedsAuth, r.slack == slackError) {
		src.Channels = nil
	}

	if anyOf(r.slack == slackNeedsAuth, r.dmError != nil) {
		src.DMs = false
	}

	return src
}

// disabledReason says why the tab is off: in setup, while GitHub is off, and while GitHub has
// never been read at all.
func (r *Reviews) disabledReason(current *settings.Settings) *string {
	if r.deps.Settings.Setup() {
		why := "No repositories are allowed yet. Choose them in Settings."

		return &why
	}

	if !current.GitHub.Enabled {
		why := "GitHub is off. Turn it on in Settings to list reviews."

		return &why
	}

	if r.lastPollAt == nil {
		return r.err
	}

	return nil
}

// legacy are the old records the migration could not place, newest first.
func (r *Reviews) legacy() []LegacyReview {
	out := []LegacyReview{}

	if r.file == nil {
		return out
	}

	for _, old := range r.file.Legacy.Keys() {
		entry, _ := r.file.Legacy.Get(old)
		when := entry.FirstSeenAt
		posted := 0.0

		if entry.Slack != nil {
			posted = entry.Slack.PostedAt
		}

		when = math.Max(when, posted)
		posted = 0

		if entry.DM != nil {
			posted = entry.DM.PostedAt
		}

		when = math.Max(when, posted)

		launches := slices.Clone(entry.Launches)
		for _, launch := range launches {
			when = math.Max(when, launch.At)
		}

		js.Sort(launches, func(first, second *Launch) float64 { return second.At - first.At })

		var folder *string

		if len(launches) > 0 {
			if base := nodepath.Base(launches[0].Worktree); base != "" {
				folder = &base
			}
		}

		out = append(out, LegacyReview{Number: js.ToNumber(old), Folder: folder, At: when})
	}

	js.Sort(out, func(first, second LegacyReview) float64 { return second.At - first.At })

	return out
}

// liveSessions are the review sessions by key: the one its launch record names, never one found
// by its name.
func (r *Reviews) liveSessions() map[string]*RowSession {
	out := map[string]*RowSession{}
	live := r.deps.Sessions()

	for _, ref := range r.entries() {
		for _, launch := range slices.Backward(ref.entry.Launches) {
			session := r.sessionOf(launch, live)
			if session != nil {
				out[ref.key] = &RowSession{Name: session.Name, Status: session.Status}

				break
			}
		}
	}

	return out
}

// Snapshot is the Reviews tab as the snapshot carries it.
func (r *Reviews) Snapshot() any {
	r.mu.Lock()
	defer r.mu.Unlock()

	refresh := refreshCount{Seq: r.refreshSeq, Done: r.refreshDone}
	if r.deps.Config.Dry {
		return r.dryView(r.now(), refresh)
	}

	now := r.now()
	current, _ := r.settings()

	return r.view(now, &current, refresh)
}

func (r *Reviews) view(now float64, current *settings.Settings, refresh refreshCount) View {
	allow := current.Repos.Allow
	src := SourcesOf(current)
	rows := r.rows(now, current)
	reason := r.disabledReason(current)

	view := View{
		Channel: "", Enabled: reason == nil, DisabledReason: reason, Me: r.me,
		Rows: SortRows(rows), LastPollAt: r.lastPollAt, LastScanAt: r.lastScanAt, Error: r.err,
		Slack: slackUnused, SlackError: nil, DMError: nil, Refresh: refresh, Capped: []string{},
		Failed: []failedPattern{}, StoreError: r.storeError, PausedUntil: r.pausedUntil,
		Sources: viewSources{
			Requested: src.Requested, Channels: append([]string{}, src.Channels...),
			DMs: src.DMs, AllOpen: src.AllOpen,
		},
		Legacy: r.legacy(),
	}

	if src.Channels != nil {
		view.Channel = strings.Join(src.Channels, ", ")
		view.Slack = r.slack
		view.SlackError = r.slackError
	} else if all(src.DMs, r.slack == slackNeedsAuth) {
		view.Slack = slackNeedsAuth
	}

	if src.DMs {
		view.DMError = r.dmError
	}

	if current.Reviews.AllOpen {
		for _, pattern := range r.capped {
			if slices.Contains(allow, pattern) {
				view.Capped = append(view.Capped, pattern)
			}
		}
	}

	for _, failed := range r.failed {
		if slices.Contains(allow, failed.Pattern) {
			view.Failed = append(view.Failed, failed)
		}
	}

	return view
}

// rows are the rows the page lists, checked against the list as it is now: a repo removed a
// moment ago is gone from the page at once.
func (r *Reviews) rows(now float64, current *settings.Settings) []*Row {
	rows := []*Row{}
	if !r.githubOn(current) {
		return rows
	}

	allow := current.Repos.Allow
	shown := r.shownSources(current)
	live := r.liveSessions()
	failedOn := r.failedPatterns()

	for _, pull := range append(values(r.prs), values(r.endedPrs)...) {
		if !repos.Allowed(pull.Repo, allow) {
			continue
		}

		row := r.rowFor(pull, now, live, shown)
		if row == nil {
			continue
		}

		if row.Ended == nil {
			row.NoClone = r.noClone(pull.Repo)

			if len(failedOn) > 0 {
				row.Stale = repos.Allowed(pull.Repo, failedOn)
			}
		}

		rows = append(rows, row)
	}

	return rows
}

// dryView is the fixed view for a dry run: every state, both kinds of re-ask, a bot, a merged
// PR, an UPDATED one, two repos and one with no clone. No gh, Slack or file.
func (r *Reviews) dryView(now float64, refresh refreshCount) dryView {
	hour := float64(dryHourMs)
	rows := dryRows(now, hour)
	me := "jane-dry"
	widgets := "widgets"

	return dryView{
		Channel: "#code-review", Enabled: true, DisabledReason: nil, Me: &me,
		Rows: SortRows(rows), LastPollAt: now, LastScanAt: now, Error: nil, Slack: slackOK,
		SlackError: nil, DMError: nil, Refresh: refresh, Capped: []string{},
		Sources: viewSources{
			Requested: true, Channels: []string{"#code-review"}, DMs: true, AllOpen: true,
		},
		Legacy: []LegacyReview{
			{Number: dryLegacyWidgetsNumber, Folder: &widgets, At: now - 30*24*hour},
			{Number: dryLegacyOtherNumber, Folder: nil, At: now - 40*24*hour},
		},
	}
}
