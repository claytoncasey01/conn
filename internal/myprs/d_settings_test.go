package myprs_test

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// My PRs under the settings: which repos are searched, what a
// change of the allowlist or a switch does, and which writes follow which module. Every
// dependency is a fake (the world): nothing here reaches the real gh, Slack or ~/.conn.

const (
	dDayMs     = 24 * hourMs
	dWidgets   = "acme/widgets#7"
	dTools     = "jane/tools#5"
	dExampleID = "PRRT_example0001"
	dToolsTree = "/data/myprs/jane-tools-1-5/tools"
)

// dSettingsOpts are what a settings rig starts from.
type dSettingsOpts struct {
	file   string
	budget *ghbudget.Budget
	dry    bool
	// deps changes the dependencies before My PRs is made.
	deps func(deps *myprs.Deps)
}

// dSettings is the settings rig over the world.
type dSettings struct {
	t    *testing.T
	w    *World
	m    *myprs.MyPrs
	file string
}

// dAcme is the PR of this file: acme/widgets#7.
func dAcme(change ...func(pull *myprs.PR)) *myprs.PR {
	named := func(pull *myprs.PR) {
		pull.Repo = "acme/widgets"
		pull.URL = "https://github.com/acme/widgets/pull/7"
	}

	return pr(append([]func(pull *myprs.PR){named}, change...)...)
}

// dIn is pr() of another repo and number.
func dIn(repo string, number float64, change ...func(pull *myprs.PR)) *myprs.PR {
	named := func(pull *myprs.PR) {
		pull.Repo = repo
		pull.Number = number
		pull.URL = "https://github.com/" + repo + "/pull/" + strconv.Itoa(int(number))
	}

	return pr(append([]func(pull *myprs.PR){named}, change...)...)
}

// dWorld is the world of this file: acme/* allowed, Slack on into #code-review, three clones,
// and a search that lists acme/widgets#7.
func dWorld(t *testing.T) *World {
	t.Helper()

	world := newWorld(t, dAcme())
	world.Scope.Allow = []string{"acme/*"}
	world.Clones = map[string]string{
		"acme/widgets": "/clone", "acme/api": "/clone-api", "jane/tools": "/clone-tools",
	}

	return world
}

func dSettingsOn(t *testing.T, world *World, opts dSettingsOpts) *dSettings {
	t.Helper()

	file := opts.file
	if file == "" {
		file = filepath.Join(t.TempDir(), "myprs.json")
	}

	deps := world.Deps(file)
	deps.Budget = opts.budget
	deps.Config.Dry = opts.dry

	if opts.deps != nil {
		opts.deps(&deps)
	}

	return &dSettings{t: t, w: world, m: myprs.New(context.Background(), deps), file: file}
}

// poll is one poll at the current time, run to its end.
func (r *dSettings) poll() { tick(r.m) }

// later is another poll ms later.
func (r *dSettings) later(ms float64) {
	r.w.Advance(ms)
	tick(r.m)
}

func (r *dSettings) keys() []string {
	rows := r.m.Snapshot().Rows
	out := make([]string, 0, len(rows))

	for _, row := range rows {
		out = append(out, myprs.EntryKey(row.Repo, row.Number))
	}

	return out
}

func (r *dSettings) sortedKeys() []string {
	keys := r.keys()
	slices.Sort(keys)

	return keys
}

func (r *dSettings) saved() *myprs.File { return myprs.LoadFile(r.file) }

func (r *dSettings) entry(key string) *myprs.Entry {
	entry, found := r.saved().PRs.Get(key)
	if !found {
		r.t.Fatalf("no entry %s saved", key)
	}

	return entry
}

func (r *dSettings) scope(change func(scope *myprs.Scope)) {
	r.w.Change(func(w *World) { change(&w.Scope) })
}

// searches are the patterns of every search so far.
func (r *dSettings) searches() [][]string {
	var out [][]string

	r.w.Change(func(w *World) { out = append([][]string{}, w.Patterns...) })

	return out
}

func (r *dSettings) asks() []string {
	out := []string{}

	r.w.Change(func(w *World) {
		for _, ask := range w.Asks {
			out = append(out, myprs.EntryKey(ask.Repo, ask.Number))
		}
	})

	return out
}

func (r *dSettings) channels() []string {
	out := []string{}

	r.w.Change(func(w *World) {
		for _, draft := range w.Drafts {
			out = append(out, draft.Channel)
		}
	})

	return out
}

func (r *dSettings) records() []string {
	var out []string

	r.w.Change(func(w *World) { out = append([]string{}, w.Records...) })

	return out
}

// dLastPatterns are the patterns of the search being answered.
func dLastPatterns(world *World) []string {
	var out []string

	world.Change(func(w *World) {
		if len(w.Patterns) > 0 {
			out = w.Patterns[len(w.Patterns)-1]
		}
	})

	return out
}

func dList(prs ...*myprs.PR) myprs.Search {
	return myprs.Search{PRs: prs, Capped: false, Failed: nil, Rate: nil}
}

func dLaunchAt(worktree string) *myprs.Launch {
	return &myprs.Launch{
		At: worldT0 - 20*dDayMs, Model: "sonnet", Name: "self-widgets-7", Worktree: worktree,
		HeadSha: "abc", Round: 1, Armed: false, AskedSha: nil, AskedAt: nil, ClosedAt: nil,
		AskTry: nil, Manual: false,
	}
}

// dSeedTools is a myprs.json with jane/tools#5 first seen 30 days ago, with one launch.
func dSeedTools(t *testing.T) string {
	t.Helper()

	file := filepath.Join(t.TempDir(), "myprs.json")
	dSeed(t, file, func(saved *myprs.File) {
		entry := myprs.TrackOf(saved, dTools, worldT0-30*dDayMs)
		entry.Launches = append(entry.Launches, dLaunchAt(dToolsTree))
	})

	return file
}

// dHold makes the first search wait: entered says it is waiting, release lets it answer.
type dHold struct {
	entered chan struct{}
	release chan struct{}
	first   atomic.Bool
}

func newDHold() *dHold {
	hold := &dHold{entered: make(chan struct{}, 1), release: make(chan struct{})}
	hold.first.Store(true)

	return hold
}

// wait holds the first search only.
func (h *dHold) wait() {
	if h.first.CompareAndSwap(true, false) {
		h.entered <- struct{}{}

		<-h.release
	}
}

// inFlight ticks, and runs change while the poll waits on gh, then lets it answer and waits for
// the work.
func (r *dSettings) inFlight(hold *dHold, change func()) {
	r.m.Tick()

	select {
	case <-hold.entered:
	case <-time.After(testtime.Scaled(dWaitLimit)):
		r.t.Fatal("the poll never searched")
	}

	change()
	close(hold.release)
	r.m.Settle()
}

func TestDSettingsSetupSearchesNothing(t *testing.T) {
	t.Parallel()

	world := dWorld(t)
	world.Scope.Setup = true
	world.Scope.Allow = []string{}
	rig := dSettingsOn(t, world, dSettingsOpts{})
	rig.poll()
	press := rig.m.Refresh()
	rig.poll()

	world.Change(func(w *World) {
		dSame(t, "logins and searches", []int{w.Logins, len(w.Patterns)}, []int{0, 0})
	})

	view := rig.m.Snapshot()
	dSame(t, "off", view.Off, "setup")
	dSame(t, "rows", len(view.Rows), 0)
	dSame(t, "error", view.Error, nil)
	dSame(t, "loaded", view.Loaded, false)
	dSame(t, "channel", view.Channel, nil)

	if view.Refresh.Done < press {
		t.Error("the REFRESH button must not wait")
	}
}

func TestDSettingsEmptyAllowlistOrGitHubOff(t *testing.T) {
	t.Parallel()

	world := dWorld(t)
	world.Scope.Allow = []string{}
	rig := dSettingsOn(t, world, dSettingsOpts{})
	rig.poll()
	dSame(t, "empty allowlist", rig.m.Snapshot().Off, "setup")

	rig.scope(func(scope *myprs.Scope) {
		scope.Allow = []string{"acme/*"}
		scope.GitHub = false
	})

	press := rig.m.Refresh()
	rig.poll()
	rig.later(pollMs)

	world.Change(func(w *World) {
		dSame(t, "logins and searches", []int{w.Logins, len(w.Patterns)}, []int{0, 0})
	})

	view := rig.m.Snapshot()
	dSame(t, "off", view.Off, "github")
	dSame(t, "rows", len(view.Rows), 0)
	dSame(t, "error", view.Error, nil)

	if view.Refresh.Done < press {
		t.Error("the REFRESH button must not wait")
	}
}

func TestDSettingsSearchesTheAllowedPatternsOnce(t *testing.T) {
	t.Parallel()

	world := dWorld(t)
	world.Scope.Allow = []string{"acme/*", "acme/widgets", "jane/tools"}
	rig := dSettingsOn(t, world, dSettingsOpts{})
	rig.poll()
	dSame(t, "searches", rig.searches(), [][]string{{"acme/*", "jane/tools"}})
	dSame(t, "off", rig.m.Snapshot().Off, nil)
}

func TestDSettingsPROutsideTheListIsDropped(t *testing.T) {
	t.Parallel()

	world := dWorld(t)
	world.SetList(dAcme(), dIn("other/thing", 3))
	rig := dSettingsOn(t, world, dSettingsOpts{})
	rig.poll()
	dSame(t, "rows", rig.keys(), []string{dWidgets})
	dSame(t, "saved", rig.saved().PRs.Keys(), []string{dWidgets})
}

// dByPattern lists acme/widgets#7, and jane/tools#5 when the search has jane/tools.
func dByPattern(world *World) func() (myprs.Search, error) {
	return func() (myprs.Search, error) {
		if slices.Contains(dLastPatterns(world), "jane/tools") {
			return dList(dAcme(), dIn("jane/tools", 5)), nil
		}

		return dList(dAcme()), nil
	}
}

func TestDSettingsRemovedPatternKeepsItsRecord(t *testing.T) {
	t.Parallel()

	world := dWorld(t)
	world.Source = dByPattern(world)
	world.Scope.Allow = []string{"acme/*", "jane/tools"}
	world.Present[dToolsTree] = true
	rig := dSettingsOn(t, world, dSettingsOpts{file: dSeedTools(t)})
	rig.poll()
	dSame(t, "listed", rig.sortedKeys(), []string{dWidgets, dTools})

	// The pattern is removed: its row goes with the change itself, before any poll.
	rig.scope(func(scope *myprs.Scope) { scope.Allow = []string{"acme/*"} })
	rig.m.SettingsChanged()
	dSame(t, "after the change", rig.keys(), []string{dWidgets})

	// 15 days later, many polls: the entry (past the 14 days a PR off the list is kept) and its
	// worktree are still there.
	for range 3 {
		rig.later(5 * dDayMs)
	}

	world.Change(func(w *World) { dSame(t, "git calls", len(w.GitCalls), 0) })

	entry := rig.entry(dTools)
	dSame(t, "launches", len(entry.Launches), 1)
	// Not re-stamped either: the last poll that listed it was the first.
	dSame(t, "seenAt", entry.SeenAt, worldT0)

	for index, patterns := range rig.searches() {
		if index > 0 {
			if slices.Contains(patterns, "jane/tools") {
				t.Errorf("search %d had jane/tools: %q", index, patterns)
			}
		}
	}

	// Added back: it reappears after the next poll, with its record.
	rig.scope(func(scope *myprs.Scope) { scope.Allow = []string{"acme/*", "jane/tools"} })
	rig.m.SettingsChanged()
	rig.later(0)
	dSame(t, "added back", rig.sortedKeys(), []string{dWidgets, dTools})
	dSame(t, "launches kept", len(rig.entry(dTools).Launches), 1)
}

func TestDSettingsChangePollsOnTheNextTick(t *testing.T) {
	t.Parallel()

	rig := dSettingsOn(t, dWorld(t), dSettingsOpts{})
	rig.poll()
	dSame(t, "first", len(rig.searches()), 1)
	rig.later(1000)
	dSame(t, "the interval has not passed", len(rig.searches()), 1)
	rig.scope(func(scope *myprs.Scope) { scope.Allow = []string{"acme/*", "jane/tools"} })
	rig.m.SettingsChanged()
	rig.later(1000)

	searches := rig.searches()
	if len(searches) < 2 {
		t.Fatalf("searches %q", searches)
	}

	dSame(t, "second", searches[1], []string{"acme/*", "jane/tools"})
}

func TestDSettingsGitHubOffEmptiesTheList(t *testing.T) {
	t.Parallel()

	rig := dSettingsOn(t, dWorld(t), dSettingsOpts{})
	rig.poll()
	dSame(t, "listed", rig.keys(), []string{dWidgets})
	rig.scope(func(scope *myprs.Scope) { scope.GitHub = false })
	rig.m.SettingsChanged()
	dSame(t, "off", rig.m.Snapshot().Off, "github")
	dSame(t, "rows", rig.keys(), []string{})

	calls := len(rig.searches())
	rig.later(1000)
	dSame(t, "no search while off", len(rig.searches()), calls)
	rig.scope(func(scope *myprs.Scope) { scope.GitHub = true })
	rig.m.SettingsChanged()
	rig.later(1000)
	dSame(t, "on again", len(rig.searches()), calls+1)
	dSame(t, "listed again", rig.keys(), []string{dWidgets})
}

func TestDSettingsPatternAddedInFlight(t *testing.T) {
	t.Parallel()

	world := dWorld(t)
	world.Present[dToolsTree] = true
	hold := newDHold()
	byPattern := dByPattern(world)
	world.Source = func() (myprs.Search, error) {
		hold.wait()

		return byPattern()
	}
	rig := dSettingsOn(t, world, dSettingsOpts{file: dSeedTools(t)})

	// The poll is waiting on gh, searching only acme/*; the person adds jane/tools.
	rig.inFlight(hold, func() {
		rig.scope(func(scope *myprs.Scope) { scope.Allow = []string{"acme/*", "jane/tools"} })
		rig.m.SettingsChanged()
	})

	world.Change(func(w *World) { dSame(t, "its entry was not read as gone", w.GitCalls, nil) })
	dSame(t, "seenAt", rig.entry(dTools).SeenAt, worldT0-30*dDayMs)

	// And the repo added meanwhile is searched by the next poll.
	rig.later(0)

	searches := rig.searches()
	dSame(t, "last search", searches[len(searches)-1], []string{"acme/*", "jane/tools"})
	dSame(t, "listed", rig.sortedKeys(), []string{dWidgets, dTools})
}

func TestDSettingsChangeInFlightDropsTheAnswer(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		change func(scope *myprs.Scope)
		off    any
		keys   []string
	}{
		{
			name:   "a pattern removed",
			change: func(scope *myprs.Scope) { scope.Allow = []string{"acme/*"} },
			off:    nil, keys: []string{dWidgets},
		},
		{
			name:   "GitHub switched off",
			change: func(scope *myprs.Scope) { scope.GitHub = false },
			off:    "github", keys: []string{},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := dWorld(t)
			world.Scope.Allow = []string{"acme/*", "jane/tools"}
			hold := newDHold()
			world.Source = func() (myprs.Search, error) {
				hold.wait()

				return dList(dAcme(), dIn("jane/tools", 5)), nil
			}
			rig := dSettingsOn(t, world, dSettingsOpts{})
			rig.inFlight(hold, func() {
				rig.scope(tt.change)
				rig.m.SettingsChanged()
			})

			dSame(t, "off", rig.m.Snapshot().Off, tt.off)
			dSame(t, "rows", rig.keys(), tt.keys)
			dSame(t, "saved", rig.saved().PRs.Keys(), tt.keys)
			dSame(t, "asks", rig.asks(), []string{})
			dSame(t, "drafts", rig.channels(), []string{})
		})
	}
}

func TestDSettingsFailedSearchKeepsTheLastList(t *testing.T) {
	t.Parallel()

	world := dWorld(t)
	world.Scope.Allow = []string{"acme/*", "jane/tools"}
	rig := dSettingsOn(t, world, dSettingsOpts{})
	rig.poll()
	world.Change(func(w *World) {
		w.Source = func() (myprs.Search, error) {
			return dList(), dFailure("jane/tools: gh: SAML enforcement")
		}
	})
	rig.later(pollMs)

	view := rig.m.Snapshot()
	if view.Error == nil {
		t.Fatal("no error")
	}

	if !strings.Contains(*view.Error, "jane/tools: gh: SAML") {
		t.Errorf("error %q", *view.Error)
	}

	dSame(t, "rows", rig.keys(), []string{dWidgets})
}

// ---- the Slack draft: channel and module ----

// dReadyFile is myprs.json with acme/widgets#7's round asked for and reviewed: a draft is due.
func dReadyFile(t *testing.T, draft *myprs.SlackDraft) string {
	t.Helper()

	file := filepath.Join(t.TempDir(), "myprs.json")
	asked := "new"
	askedAt := float64(worldT0 - hourMs)

	dSeed(t, file, func(saved *myprs.File) {
		entry := myprs.TrackOf(saved, dWidgets, worldT0-hourMs)
		launch := dLaunchAt("/data/myprs/acme-widgets-x-7/widgets")
		launch.At = worldT0 - 2*hourMs
		launch.AskedSha = &asked
		launch.AskedAt = &askedAt
		entry.Launches = append(entry.Launches, launch)
		entry.Draft = draft
	})

	return file
}

// dReviewed is acme/widgets#7 at head new, reviewed by Greptile half an hour ago.
func dReviewed() *myprs.PR {
	return dAcme(func(pull *myprs.PR) {
		pull.HeadSha = "new"
		pull.Reviews = []myprs.Review{{By: greptileBot, Sha: "new", At: worldT0 - 30*dMinuteMs}}
	})
}

// dReady is a PR whose draft is due, polled once after tweak.
func dReady(t *testing.T, tweak func(scope *myprs.Scope)) *dSettings {
	t.Helper()

	world := dWorld(t)
	world.SetList(dReviewed())
	rig := dSettingsOn(t, world, dSettingsOpts{file: dReadyFile(t, nil)})
	rig.scope(tweak)
	rig.m.SettingsChanged()
	rig.poll()

	return rig
}

func TestDSettingsDraftGoesToTheSettingsChannel(t *testing.T) {
	t.Parallel()

	rig := dReady(t, func(scope *myprs.Scope) { scope.DraftChannel = new("#reviews-eu") })
	dSame(t, "drafts", rig.channels(), []string{"#reviews-eu"})

	view := rig.m.Snapshot()
	dSame(t, "channel", view.Channel, "#reviews-eu")
	dSame(t, "saved", rig.entry(dWidgets).Draft.Channel, "#reviews-eu")

	if view.Rows[0].SlackDraft == nil {
		t.Fatal("no draft on the row")
	}

	dSame(t, "row", view.Rows[0].SlackDraft.Channel, "#reviews-eu")
}

func TestDSettingsNoDraftWithSlackOffOrNoChannel(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		tweak func(scope *myprs.Scope)
	}{
		{name: "Slack off", tweak: func(scope *myprs.Scope) { scope.Slack = false }},
		{name: "no channel", tweak: func(scope *myprs.Scope) { scope.DraftChannel = nil }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := dReady(t, tt.tweak)
			dSame(t, "drafts", rig.channels(), []string{})

			view := rig.m.Snapshot()
			dSame(t, "channel", view.Channel, nil)
			dSame(t, "status", view.Rows[0].Status, "ready")
			dSame(t, "draftOff", view.Rows[0].DraftOff, true)
			dSame(t, "slackDraft", view.Rows[0].SlackDraft, nil)
			dSame(t, "saved", rig.entry(dWidgets).Draft, nil)
		})
	}
}

func TestDSettingsSlackOnLaterDrafts(t *testing.T) {
	t.Parallel()

	rig := dReady(t, func(scope *myprs.Scope) { scope.Slack = false })
	dSame(t, "off", rig.channels(), []string{})
	rig.scope(func(scope *myprs.Scope) { scope.Slack = true })
	rig.m.SettingsChanged()
	rig.later(0)
	dSame(t, "on", len(rig.channels()), 1)
}

// ---- GitHub writes follow the GitHub module ----

func TestDSettingsStartAndClickRefused(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		change func(scope *myprs.Scope)
		why    string
	}{
		{
			name:   "GitHub off",
			change: func(scope *myprs.Scope) { scope.GitHub = false },
			why:    "GitHub is off. Turn it on in Settings.",
		},
		{
			name: "setup",
			change: func(scope *myprs.Scope) {
				scope.Setup = true
				scope.Allow = []string{}
			},
			why: "No repositories allowed. Add one in Settings.",
		},
		{
			name:   "not allowed",
			change: func(scope *myprs.Scope) { scope.Allow = []string{"jane/*"} },
			why:    "acme/widgets is not an allowed repository",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := dWorld(t)
			rig := dSettingsOn(t, world, dSettingsOpts{})
			rig.poll()

			var logins int

			world.Change(func(w *World) { logins = w.Logins })
			rig.scope(tt.change)

			want := `{"error":` + dJSON(t, tt.why) + `}`
			started := rig.m.Start(context.Background(), "acme/widgets", 7.0, "sonnet")
			dSame(t, "start code", started.Code, 409)
			dSame(t, "start", dBody(started), want)

			clicked := rig.m.Thread(context.Background(), "acme/widgets", 7.0, dExampleID, "ok")
			dSame(t, "click code", clicked.Code, 409)
			dSame(t, "click", dBody(clicked), want)

			world.Change(func(w *World) {
				dSame(t, "logins", w.Logins-logins, 0)
				dSame(t, "launches", len(w.Launched), 0)
				dSame(t, "replies", len(w.ReplyCalls), 0)
				dSame(t, "resolves", len(w.ResolveIDs), 0)
				dSame(t, "git", len(w.GitCalls), 0)
			})
		})
	}
}

func TestDSettingsGreptileAskFollowsTheGate(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		flip func(scope *myprs.Scope)
		asks []string
	}{
		{name: "allowed", flip: nil, asks: []string{dWidgets}},
		{
			name: "GitHub off just before",
			flip: func(scope *myprs.Scope) { scope.GitHub = false }, asks: []string{},
		},
		{
			name: "repo left the list just before",
			flip: func(scope *myprs.Scope) { scope.Allow = []string{"jane/*"} }, asks: []string{},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// A round that pushed and whose session never showed up: over, so the push is
			// asked for.
			file := filepath.Join(t.TempDir(), "myprs.json")
			dSeed(t, file, func(saved *myprs.File) {
				launch := dLaunchAt("/data/myprs/acme-widgets-x-7/widgets")
				launch.At = worldT0 - 10*dMinuteMs
				launch.Armed = true
				entry := myprs.TrackOf(saved, dWidgets, worldT0-hourMs)
				entry.Launches = append(entry.Launches, launch)
			})

			world := dWorld(t)
			world.SetList(dAcme(func(pull *myprs.PR) { pull.HeadSha = "pushed" }))
			// The scope changes when the poll reads the round's replies, which comes after
			// the list was kept and before the ask.
			rig := dSettingsOn(t, world, dSettingsOpts{file: file, deps: func(deps *myprs.Deps) {
				deps.RepliesAt = func(string) *float64 {
					if tt.flip != nil {
						world.Change(func(w *World) { tt.flip(&w.Scope) })
					}

					return nil
				}
			}})
			rig.poll()
			dSame(t, "asks", rig.asks(), tt.asks)
		})
	}
}

// ---- the clone ----

func TestDSettingsNoCloneCannotStart(t *testing.T) {
	t.Parallel()

	world := dWorld(t)
	delete(world.Clones, "acme/widgets")
	rig := dSettingsOn(t, world, dSettingsOpts{})
	rig.poll()

	why := "No local clone of acme/widgets: set a folder in Settings"
	row := rig.m.Snapshot().Rows[0]
	dSame(t, "status", row.Status, "no-clone")
	dSame(t, "canStart", row.CanStart, false)
	dSame(t, "reason", row.Reason, why)

	started := rig.m.Start(context.Background(), "acme/widgets", 7.0, "sonnet")
	dSame(t, "code", started.Code, 409)
	dSame(t, "body", dBody(started), `{"error":`+dJSON(t, why)+`}`)
	world.Change(func(w *World) { dSame(t, "launches", len(w.Launched), 0) })
}

func TestDSettingsCloneLookupOncePerRepo(t *testing.T) {
	t.Parallel()

	world := dWorld(t)
	world.SetList(dIn("acme/widgets", 1), dIn("acme/widgets", 2), dIn("acme/widgets", 3))
	rig := dSettingsOn(t, world, dSettingsOpts{})
	rig.poll()

	var asked atomic.Int32

	rig.m.SetDeps(func(deps *myprs.Deps) {
		clone := deps.CloneFor
		deps.CloneFor = func(repo string) string {
			asked.Add(1)

			return clone(repo)
		}
	})
	dSame(t, "rows", len(rig.m.Snapshot().Rows), 3)
	dSame(t, "lookups", asked.Load(), 1)
}

func TestDSettingsDryRunCallsNothing(t *testing.T) {
	t.Parallel()

	world := dWorld(t)
	rig := dSettingsOn(t, world, dSettingsOpts{dry: true})
	rig.poll()
	world.Change(func(w *World) {
		dSame(t, "logins and searches", []int{w.Logins, len(w.Patterns)}, []int{0, 0})
	})
	dSame(t, "off", rig.m.Snapshot().Off, nil)

	started := rig.m.Start(context.Background(), "acme/widgets", 7.0, "sonnet")
	dSame(t, "code", started.Code, 200)
	dSame(t, "body", dBody(started), `{"ok":true,"dry":true}`)
}

// ---- cost, partial failure, fingerprint, the click, the channel ----

func TestDSettingsPollEveryStretchesWithThePatterns(t *testing.T) {
	t.Parallel()

	for _, tt := range [][2]int{{0, 1}, {1, 1}, {2, 1}, {3, 2}, {4, 2}, {5, 3}, {25, 13}} {
		t.Run(strconv.Itoa(tt[0]), func(t *testing.T) {
			t.Parallel()

			dSame(t, "every", myprs.PollEvery(tt[0], pollMs), float64(tt[1]*pollMs))
		})
	}
}

func TestDSettingsFourPatternsPollAtTwiceTheBase(t *testing.T) {
	t.Parallel()

	rig := dSettingsOn(t, dWorld(t), dSettingsOpts{})
	rig.scope(func(scope *myprs.Scope) { scope.Allow = []string{"a/*", "b/*", "c/*", "d/*"} })
	rig.m.SettingsChanged()
	rig.poll()
	dSame(t, "first", len(rig.searches()), 1)
	rig.later(pollMs)
	dSame(t, "four patterns: twice the base", len(rig.searches()), 1)
	rig.later(pollMs)
	dSame(t, "second", len(rig.searches()), 2)
}

func dRateLimited(resetAt *float64) func() (myprs.Search, error) {
	return func() (myprs.Search, error) {
		return dList(), &ghbudget.RateLimitError{ResetAt: resetAt}
	}
}

func TestDSettingsRateLimitPausesThePoll(t *testing.T) {
	t.Parallel()

	world := dWorld(t)
	rate := &ghbudget.RateInfo{Cost: 105, Remaining: 4800, ResetAt: worldT0 + hourMs}
	world.Source = func() (myprs.Search, error) {
		return myprs.Search{PRs: []*myprs.PR{dAcme()}, Capped: false, Failed: nil, Rate: rate}, nil
	}
	rig := dSettingsOn(t, world, dSettingsOpts{})
	rig.poll()
	dSame(t, "rate", rig.m.Snapshot().Rate, rate)

	world.Change(func(w *World) { w.Source = dRateLimited(new(float64(worldT0 + 20*dMinuteMs))) })
	rig.later(pollMs)

	view := rig.m.Snapshot()
	dSame(t, "error", view.Error, nil)
	dSame(t, "pausedUntil", view.PausedUntil, worldT0+20*dMinuteMs)
	dSame(t, "rows", rig.keys(), []string{dWidgets})
	dContains(t, "warning", view.Warning, "rate limit is used up")

	calls := len(rig.searches())
	press := rig.m.Refresh()
	rig.later(pollMs)
	rig.later(pollMs)
	dSame(t, "nothing asked while paused", len(rig.searches()), calls)

	if rig.m.Snapshot().Refresh.Done < press {
		t.Error("the press waits")
	}

	world.SetList(dAcme(), dAcme(func(pull *myprs.PR) { pull.Number = 8 }))
	rig.later(20 * dMinuteMs)
	dSame(t, "polled after the reset", len(rig.searches()), calls+1)

	after := rig.m.Snapshot()
	dSame(t, "pausedUntil after", after.PausedUntil, nil)
	dSame(t, "warning after", after.Warning, nil)
	dSame(t, "rows after", len(rig.keys()), 2)

	// No reset time from GitHub: ten minutes.
	world.Change(func(w *World) { w.Source = dRateLimited(nil) })
	rig.later(pollMs)
	dSame(t, "ten minutes", rig.m.Snapshot().PausedUntil, world.now()+10*dMinuteMs)
}

func dContains(t *testing.T, what string, got *string, want string) {
	t.Helper()

	if got == nil {
		t.Errorf("%s: nil, want %q in it", what, want)

		return
	}

	if !strings.Contains(*got, want) {
		t.Errorf("%s: %q, want %q in it", what, *got, want)
	}
}

func TestDSettingsOnePatternFailing(t *testing.T) {
	t.Parallel()

	world := dWorld(t)
	world.Scope.Allow = []string{"acme/*", "jane/tools"}
	world.Present[dToolsTree] = true

	var failJane atomic.Bool

	world.Source = func() (myprs.Search, error) {
		if failJane.Load() {
			return myprs.Search{
				PRs: []*myprs.PR{dAcme()}, Capped: false, Rate: nil,
				Failed: []myprs.SearchFailure{{Pattern: "jane/tools", Error: "gh: HTTP 422"}},
			}, nil
		}

		return dList(dAcme(), dIn("jane/tools", 5)), nil
	}
	rig := dSettingsOn(t, world, dSettingsOpts{file: dSeedTools(t)})
	rig.m.SettingsChanged()
	rig.poll()
	dSame(t, "listed", rig.sortedKeys(), []string{dWidgets, dTools})

	stamped := rig.entry(dTools).SeenAt

	failJane.Store(true)

	for range 3 {
		rig.later(5 * dDayMs)
	}

	dSame(t, "the failed pattern keeps its last rows", rig.sortedKeys(),
		[]string{dWidgets, dTools})

	view := rig.m.Snapshot()
	dSame(t, "error", view.Error, nil)
	dContains(t, "warning", view.Warning, "Could not search jane/tools (gh: HTTP 422)")
	world.Change(func(w *World) { dSame(t, "git", len(w.GitCalls), 0) })
	dSame(t, "not stamped", rig.entry(dTools).SeenAt, stamped)
	dOnePatternRows(t, view)

	started := rig.m.Start(context.Background(), "jane/tools", 5.0, "sonnet")
	dSame(t, "start", dBody(started), `{"error":"jane/tools could not be searched `+
		`(gh: HTTP 422): its PRs may be out of date"}`)
	world.Change(func(w *World) { dSame(t, "launches", len(w.Launched), 0) })

	// It works again: the warning goes and the entry is stamped.
	failJane.Store(false)
	rig.later(5 * dDayMs)
	dSame(t, "warning gone", rig.m.Snapshot().Warning, nil)

	if rig.entry(dTools).SeenAt <= stamped {
		t.Error("not stamped again")
	}
}

func dOnePatternRows(t *testing.T, view myprs.View) {
	t.Helper()

	for _, row := range view.Rows {
		switch row.Repo {
		case "jane/tools":
			dSame(t, "canStart", row.CanStart, false)
			dContains(t, "reason", row.Reason, "could not be searched")
		case "acme/widgets":
			dSame(t, "widgets reason", row.Reason, nil)
		}
	}
}

func TestDSettingsCarriedOverPRIsNotAsked(t *testing.T) {
	t.Parallel()

	// Rounds that pushed a minute ago: their sessions have not had the 5 minutes to show up
	// yet, so no ask is due in the first poll.
	file := filepath.Join(t.TempDir(), "myprs.json")
	dSeed(t, file, func(saved *myprs.File) {
		for _, repo := range []string{"acme/widgets", "jane/tools"} {
			launch := dLaunchAt("/data/myprs/" + strings.Replace(repo, "/", "-", 1) + "-x-7/w")
			launch.At = worldT0 - dMinuteMs
			launch.Armed = true
			entry := myprs.TrackOf(saved, myprs.EntryKey(repo, 7), worldT0-hourMs)
			entry.Launches = append(entry.Launches, launch)
		}
	})

	world := dWorld(t)
	world.Scope.Allow = []string{"acme/*", "jane/tools"}

	var fail atomic.Bool

	pushed := func(pull *myprs.PR) { pull.HeadSha = "pushed" }
	world.Source = func() (myprs.Search, error) {
		if fail.Load() {
			return myprs.Search{
				PRs: []*myprs.PR{dAcme(pushed)}, Capped: false, Rate: nil,
				Failed: []myprs.SearchFailure{{Pattern: "jane/tools", Error: "x"}},
			}, nil
		}

		return dList(dAcme(pushed), dIn("jane/tools", 7, pushed)), nil
	}
	rig := dSettingsOn(t, world, dSettingsOpts{file: file})
	rig.m.SettingsChanged()
	rig.poll()
	dSame(t, "no ask yet", rig.asks(), []string{})

	// jane/tools now fails; both rounds are over by now: the one read is asked for, the one
	// carried over is not.
	fail.Store(true)
	rig.later(6 * dMinuteMs)
	dSame(t, "rows", rig.sortedKeys(), []string{dWidgets, "jane/tools#7"})
	dSame(t, "asks", rig.asks(), []string{dWidgets})

	// And once it is read again, it is asked for.
	fail.Store(false)
	rig.later(pollMs)

	asks := rig.asks()
	slices.Sort(asks)
	dSame(t, "asks after", asks, []string{dWidgets, "jane/tools#7"})
}

func TestDSettingsSaveThatChangesNothingForcesNoPoll(t *testing.T) {
	t.Parallel()

	rig := dSettingsOn(t, dWorld(t), dSettingsOpts{})
	rig.poll()

	// polled is whether change then a settings save made a poll a second later.
	polled := func(change func(scope *myprs.Scope)) bool {
		before := len(rig.searches())
		rig.scope(change)
		rig.m.SettingsChanged()
		rig.later(1000)

		return len(rig.searches()) > before
	}

	for _, tt := range []struct {
		name   string
		change func(scope *myprs.Scope)
		polls  bool
	}{
		{name: "another section saved", change: func(*myprs.Scope) {}, polls: false},
		{
			name:   "the same list in another case",
			change: func(scope *myprs.Scope) { scope.Allow = []string{"ACME/*"} }, polls: false,
		},
		{
			name:   "the list",
			change: func(scope *myprs.Scope) { scope.Allow = []string{"acme/*", "jane/tools"} },
			polls:  true,
		},
		{
			name:   "the draft channel",
			change: func(scope *myprs.Scope) { scope.DraftChannel = new("#other") }, polls: true,
		},
		{name: "Slack", change: func(scope *myprs.Scope) { scope.Slack = false }, polls: true},
		{
			name:   "the draft channel while Slack is off",
			change: func(scope *myprs.Scope) { scope.DraftChannel = new("#another") },
			polls:  false,
		},
	} {
		// In order: each row changes the scope the row before left.
		dSame(t, tt.name, polled(tt.change), tt.polls)
	}
}

// dFlipWrites are the world's writes, with a hook after the reply that does not hold the world.
type dFlipWrites struct {
	w       *World
	onReply func()
}

func (x dFlipWrites) TriggerReview(_ context.Context, repo string, number float64) error {
	x.w.Change(func(w *World) { w.Asks = append(w.Asks, Ask{Repo: repo, Number: number}) })

	return nil
}

func (x dFlipWrites) ReplyToThread(_ context.Context, threadID, body string) error {
	x.w.Change(func(w *World) {
		w.ReplyCalls = append(w.ReplyCalls, DraftCall{Channel: threadID, Text: body})
	})
	x.onReply()

	return nil
}

func (x dFlipWrites) ResolveThread(_ context.Context, threadID string) error {
	x.w.Change(func(w *World) { w.ResolveIDs = append(w.ResolveIDs, threadID) })

	return nil
}

// dClick is one click of the gate test: what changes the scope on the thread read and on the
// reply, and what the click answers and writes.
type dClick struct {
	name            string
	onRead, onReply func(scope *myprs.Scope)
	code            int
	ok              any
	why             string
	replies         int
	resolves        int
}

func dClickRows() []dClick {
	githubOff := func(scope *myprs.Scope) { scope.GitHub = false }
	leftList := func(scope *myprs.Scope) { scope.Allow = []string{"jane/*"} }

	return []dClick{
		{name: "whole", code: 200, ok: true, replies: 1, resolves: 1},
		{name: "GitHub off after the read", onRead: githubOff, code: 409, ok: nil},
		{name: "repo left after the read", onRead: leftList, code: 409, ok: nil},
		// The reply took a moment and GitHub went off meanwhile: no resolve, and it says the
		// reply is posted.
		{
			name: "GitHub off during the reply", onReply: githubOff, code: 200, ok: false,
			why: "the reply is posted, but the thread was not resolved: GitHub is off", replies: 1,
		},
	}
}

func TestDSettingsClickStopsWhenTheGateCloses(t *testing.T) {
	t.Parallel()

	for _, tt := range dClickRows() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := dWorld(t)
			flip := func(change func(scope *myprs.Scope)) {
				if change != nil {
					world.Change(func(w *World) { change(&w.Scope) })
				}
			}

			world.SetList(dAcme(func(pull *myprs.PR) {
				pull.Threads = []myprs.Thread{dThreadOf(dExampleID)}
			}))
			world.ThreadInfo = func(string) (*myprs.ThreadInfo, error) {
				flip(tt.onRead)

				return &myprs.ThreadInfo{
					Resolved: false, Number: 7, State: "OPEN", Author: "jane",
					Repo: "acme/widgets", Last: nil, Recent: []myprs.RecentComment{},
				}, nil
			}
			rig := dSettingsOn(t, world, dSettingsOpts{deps: func(deps *myprs.Deps) {
				deps.Writes = dFlipWrites{w: world, onReply: func() { flip(tt.onReply) }}
			}})
			rig.poll()

			res := rig.m.Thread(context.Background(), "acme/widgets", 7.0, dExampleID, "Fixed.")
			dSame(t, "code", res.Code, tt.code)
			dSame(t, "ok", dField(res, "ok"), tt.ok)

			if tt.why != "" {
				why, _ := dField(res, "error").(string)
				dContains(t, "error", &why, tt.why)
			}

			world.Change(func(w *World) {
				dSame(t, "replies", len(w.ReplyCalls), tt.replies)
				dSame(t, "resolves", len(w.ResolveIDs), tt.resolves)
			})
		})
	}
}

func TestDSettingsFailedDraftIsTriedInTheNewChannel(t *testing.T) {
	t.Parallel()

	world := dWorld(t)
	world.SetList(dReviewed())

	refused := "a draft is already waiting in #a"
	file := dReadyFile(t, &myprs.SlackDraft{
		Sha: "new", Text: "t", State: myprs.DraftFailed, Error: &refused, At: worldT0,
		Tries: 3, Channel: "#a",
	})
	rig := dSettingsOn(t, world, dSettingsOpts{file: file})
	rig.scope(func(scope *myprs.Scope) { scope.DraftChannel = new("#a") })
	rig.m.SettingsChanged()
	rig.poll()
	dSame(t, "its tries are used up in #a", rig.channels(), []string{})

	rig.scope(func(scope *myprs.Scope) { scope.DraftChannel = new("#b") })
	rig.m.SettingsChanged()
	rig.later(1000)
	dSame(t, "drafts", rig.channels(), []string{"#b"})

	saved := rig.entry(dWidgets).Draft
	if saved == nil {
		t.Fatal("no draft saved")
	}

	dSame(t, "state", saved.State, myprs.DraftSaved)
	dSame(t, "channel", saved.Channel, "#b")
	dSame(t, "tries", saved.Tries, 1)
}

// ---- what the Settings cards learn from My PRs (moduleStatus) ----

func TestDSettingsPollRecordsHowGitHubAnswered(t *testing.T) {
	t.Parallel()

	world := dWorld(t)
	world.Scope.Setup = true
	rig := dSettingsOn(t, world, dSettingsOpts{})
	rig.poll()
	dSame(t, "nothing ran, nothing is recorded", len(rig.records()), 0)

	rig.scope(func(scope *myprs.Scope) { scope.Setup = false })
	rig.m.SettingsChanged()
	rig.later(0)
	dSame(t, "a list", rig.records(), []string{"github true My PRs read 1 open PR"})

	rig.scope(func(scope *myprs.Scope) { scope.Allow = []string{"acme/*", "jane/tools"} })
	rig.m.SettingsChanged()
	world.Change(func(w *World) {
		w.Source = func() (myprs.Search, error) {
			failed := []myprs.SearchFailure{}

			for _, pattern := range dLastPatterns(world) {
				if pattern == "jane/tools" {
					failed = append(failed, myprs.SearchFailure{Pattern: pattern, Error: "SAML"})
				}
			}

			return myprs.Search{PRs: []*myprs.PR{dAcme()}, Capped: false, Failed: failed}, nil
		}
	})
	rig.later(0)
	dSame(t, "a partial list", dLast(rig.records()),
		"github true My PRs read GitHub, but 1 of 2 searches failed")

	world.Change(func(w *World) {
		w.Source = func() (myprs.Search, error) {
			return dList(), dFailure("gh: could not resolve host")
		}
	})
	rig.later(pollMs)
	dSame(t, "a failure", dLast(rig.records()), "github false My PRs could not read GitHub")

	world.Change(func(w *World) { w.Source = dRateLimited(nil) })
	rig.later(pollMs)
	dSame(t, "a rate limit", dLast(rig.records()),
		"github false My PRs is paused: GitHub's rate limit is used up")

	for _, line := range rig.records() {
		if strings.HasPrefix(line, "slack") {
			t.Errorf("a Slack record: %q", line)
		}
	}
}

func dLast(lines []string) string {
	if len(lines) == 0 {
		return ""
	}

	return lines[len(lines)-1]
}

func dSlackRecords(lines []string) []string {
	out := []string{}

	for _, line := range lines {
		if strings.HasPrefix(line, "slack") {
			out = append(out, line)
		}
	}

	return out
}

func TestDSettingsDraftRecordsHowSlackAnswered(t *testing.T) {
	t.Parallel()

	saved := dReady(t, func(*myprs.Scope) {})
	if !slices.Contains(saved.records(), "slack true My PRs saved a Slack draft") {
		t.Errorf("records %q", saved.records())
	}

	for _, tt := range []struct {
		name   string
		answer myprs.DraftResult
		want   []string
	}{
		{
			name: "signed out",
			answer: myprs.DraftResult{
				OK: false, Error: "Slack is signed out: sign in to the claude.ai Slack connector",
				Final: false,
			},
			want: []string{"slack false My PRs could not save a Slack draft"},
		},
		{
			name: "one is waiting",
			answer: myprs.DraftResult{
				OK: false, Error: "a draft is already waiting in #code-review", Final: true,
			},
			want: []string{},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := dWorld(t)
			world.SetList(dReviewed())
			world.DraftResult = func() myprs.DraftResult { return tt.answer }
			rig := dSettingsOn(t, world, dSettingsOpts{file: dReadyFile(t, nil)})
			rig.poll()
			dSame(t, "drafts", len(rig.channels()), 1)
			dSame(t, "records", dSlackRecords(rig.records()), tt.want)
		})
	}
}

// ---- the poll cadence and the budget shared with Reviews ----

// dCosting answers acme/widgets#7 at a cost, the reset a millisecond after the search.
func dCosting(world *World, cost float64, prs ...*myprs.PR) func() (myprs.Search, error) {
	return func() (myprs.Search, error) {
		rate := &ghbudget.RateInfo{Cost: cost, Remaining: 5000, ResetAt: world.now() + 1}

		return myprs.Search{PRs: prs, Capped: false, Failed: nil, Rate: rate}, nil
	}
}

func TestDSettingsPacedByTheMeasuredCost(t *testing.T) {
	t.Parallel()

	world := dWorld(t)
	rig := dSettingsOn(t, world, dSettingsOpts{budget: ghbudget.New()})
	rig.poll()
	dSame(t, "first", len(rig.searches()), 1)
	// Nothing measured: the pattern count (one pattern is the base interval).
	rig.later(pollMs - 1)
	dSame(t, "before the base", len(rig.searches()), 1)
	rig.later(1)
	dSame(t, "at the base", len(rig.searches()), 2)
	// 600 points a poll against 2500 an hour: every 864 s, not every 180 s.
	world.Change(func(w *World) { w.Source = dCosting(world, 600, dAcme()) })
	rig.later(pollMs)
	dSame(t, "measured", len(rig.searches()), 3)
	rig.later(5 * dMinuteMs)
	dSame(t, "not due after 5 minutes", len(rig.searches()), 3)
	rig.later(10 * dMinuteMs)
	dSame(t, "due after 15 minutes (864 s)", len(rig.searches()), 4)
}

func TestDSettingsSharesTheBudgetWithReviews(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		// reviews is that Reviews has been spending.
		reviews bool
		due     float64
	}{
		{name: "alone", reviews: false, due: 288_000},
		{name: "with Reviews spending", reviews: true, due: 576_000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			budget := ghbudget.New()
			if tt.reviews {
				budget.Note("reviews", worldT0)
			}

			world := dWorld(t)
			world.Source = dCosting(world, 200, dAcme())
			rig := dSettingsOn(t, world, dSettingsOpts{budget: budget})
			rig.poll()

			if tt.due > 288_000 {
				rig.later(288_000)
				dSame(t, "not at 288 s", len(rig.searches()), 1)
				rig.later(tt.due - 288_000)
			} else {
				rig.later(tt.due)
			}

			dSame(t, "due", len(rig.searches()), 2)
			// Its answered poll makes it a spender.
			dSame(t, "parties", budget.Parties("reviews", world.now()), 2)
		})
	}
}

func TestDSettingsLeavesTheBudget(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		leave func(w *World)
	}{
		{name: "GitHub off", leave: func(w *World) { w.Scope.GitHub = false }},
		// No dashboard open: nothing is asked, so no share either.
		{name: "no dashboard", leave: func(w *World) { w.Clients = false }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			budget := ghbudget.New()
			world := dWorld(t)
			rig := dSettingsOn(t, world, dSettingsOpts{budget: budget})
			rig.poll()
			dSame(t, "spending", budget.Parties("reviews", world.now()), 2)
			world.Change(tt.leave)
			rig.later(pollMs)
			dSame(t, "left", budget.Parties("reviews", world.now()), 1)
		})
	}
}

func TestDSettingsMeasuredCostOnlyLengthensTheWait(t *testing.T) {
	t.Parallel()

	patterns := make([]string, 0, 20)
	for index := range 20 {
		patterns = append(patterns, "acme/repo"+strconv.Itoa(index))
	}

	world := dWorld(t)
	world.Scope.Allow = patterns
	// 20 patterns at about 105 points each: 2100 a poll, which the formula spaces 50 minutes
	// apart and the cap cuts to 15; the pattern count says 30.
	world.Source = dCosting(world, 2100, dIn("acme/repo0", 7))
	rig := dSettingsOn(t, world, dSettingsOpts{budget: ghbudget.New()})
	rig.poll()
	dSame(t, "first", len(rig.searches()), 1)
	dSame(t, "pattern count", myprs.PollEvery(20, pollMs), float64(pollMs*10))
	rig.later(15 * dMinuteMs)
	dSame(t, "not at 15 minutes", len(rig.searches()), 1)
	rig.later(14 * dMinuteMs)
	dSame(t, "not at 29 minutes", len(rig.searches()), 1)
	rig.later(dMinuteMs)
	dSame(t, "at 30 minutes", len(rig.searches()), 2)
}

func TestDSettingsRateLimitIsShared(t *testing.T) {
	t.Parallel()

	budget := ghbudget.New()
	budget.Pause(worldT0 + 20*dMinuteMs)

	world := dWorld(t)
	rig := dSettingsOn(t, world, dSettingsOpts{budget: budget})
	rig.poll()
	dSame(t, "not asked while the limit is in force", len(rig.searches()), 0)
	dSame(t, "pausedUntil", rig.m.Snapshot().PausedUntil, worldT0+20*dMinuteMs)
	dContains(t, "warning", rig.m.Snapshot().Warning, "rate limit is used up")
	rig.later(19 * dMinuteMs)
	dSame(t, "still paused", len(rig.searches()), 0)
	rig.later(dMinuteMs)
	dSame(t, "polls at the reset", len(rig.searches()), 1)
	dSame(t, "pausedUntil after", rig.m.Snapshot().PausedUntil, nil)

	// Its own limit is Reviews'.
	world.Change(func(w *World) {
		w.Source = func() (myprs.Search, error) {
			return dRateLimited(new(world.now() + 30*dMinuteMs))()
		}
	})
	rig.later(pollMs)
	dSame(t, "Reviews paused", budget.PausedUntil(world.now()), world.now()+30*dMinuteMs)
}
