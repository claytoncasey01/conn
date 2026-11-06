package openwork

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
)

const (
	// staleMs is how old the sprint list may be before it is read again.
	staleMs = 10 * 60_000
	// dryListWait is how long the dry run's sprint read takes.
	dryListWait  = 800 * time.Millisecond
	stepStarting = "Starting Claude"
)

// ticketList is the sprint list: the tickets as read (the cache's own objects,
// kept whole) and the state of the read.
type ticketList struct {
	Tickets []any `json:"tickets"`
	// FetchedAt is the ms epoch of the last good read, as the cache holds it; nil before.
	FetchedAt      any      `json:"fetchedAt"`
	Fetching       bool     `json:"fetching"`
	FetchStartedAt *float64 `json:"fetchStartedAt"`
	// Step is what the running read is doing right now.
	Step *string `json:"step"`
	// NeedsAuth are the connectors that need signing in before the read can work.
	NeedsAuth []string `json:"needsAuth"`
	Error     *string  `json:"error"`
	// Truncated is that the sprint has more tickets than were read.
	Truncated bool `json:"truncated"`
}

func emptyList() ticketList {
	return ticketList{
		Tickets: []any{}, FetchedAt: nil, Fetching: false, FetchStartedAt: nil, Step: nil,
		NeedsAuth: []string{}, Error: nil, Truncated: false,
	}
}

// Build is where a run may build, as the list shows it: every allowed repository, the one the
// picker starts on when the browser remembers none, and the layout it starts on.
type Build struct {
	Repos   []BuildRepo `json:"repos"`
	Default *string     `json:"default"`
	Layout  string      `json:"layout"`
}

// BuildRepo is one repository a run may build in: the branch a run starts from there, and
// whether one can start there (the reason says what to fix when not).
type BuildRepo struct {
	Repo   string  `json:"repo"`
	Base   *string `json:"base"`
	Ready  bool    `json:"ready"`
	Reason *string `json:"reason"`
}

// WorkList is GET /api/work: the list, the projects it is read for, and where a run builds.
type WorkList struct {
	Tickets        []any    `json:"tickets"`
	FetchedAt      any      `json:"fetchedAt"`
	Fetching       bool     `json:"fetching"`
	FetchStartedAt *float64 `json:"fetchStartedAt"`
	Step           *string  `json:"step"`
	NeedsAuth      []string `json:"needsAuth"`
	Error          *string  `json:"error"`
	Truncated      bool     `json:"truncated"`
	Projects       []string `json:"projects"`
	Build          Build    `json:"build"`
}

func (w *Work) readCache() {
	data, err := os.ReadFile(w.listFile)
	if err != nil {
		return
	}

	parsed, err := js.Parse(data)
	if err != nil {
		return
	}

	cache, isObject := parsed.(*js.Object)
	if !isObject {
		return
	}

	if tickets, isList := field(cache, "tickets").([]any); isList {
		w.list.Tickets = tickets
		w.list.FetchedAt = field(cache, "fetchedAt")
		w.list.Truncated = js.Truthy(field(cache, "truncated"))
	}

	if projects, isText := field(cache, "projects").(string); isText {
		w.listFor = &projects
	}
}

func field(object *js.Object, key string) any {
	value, _ := object.Get(key)

	return value
}

// sync drops the list when it was read for other projects than Settings lists now (and a read
// for the old ones): the first call after reads afresh.
func (w *Work) sync() {
	now := strings.Join(jira.Projects(w.settings()), ",")
	if w.listFor != nil {
		if *w.listFor == now {
			return
		}
	}

	w.listFor = &now

	if w.listHandle != nil {
		w.listHandle.cancel()
		w.listHandle = nil
	}

	w.list = emptyList()
}

// Tickets is GET /api/work. A list that is old is read again; only tickets of the projects in
// Settings are shown (a cache from before them can hold others).
func (w *Work) Tickets() WorkList {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.sync()

	if w.stale() {
		w.refresh()
	}

	tickets := []any{}

	if pattern := jira.Pattern(w.settings()); pattern != nil {
		for _, ticket := range w.list.Tickets {
			if pattern.MatchString(js.String(keyOf(ticket))) {
				tickets = append(tickets, ticket)
			}
		}
	}

	list := w.list

	return WorkList{
		Tickets: tickets, FetchedAt: list.FetchedAt, Fetching: list.Fetching,
		FetchStartedAt: list.FetchStartedAt, Step: list.Step, NeedsAuth: list.NeedsAuth,
		Error: list.Error, Truncated: list.Truncated,
		Projects: append([]string{}, jira.Projects(w.settings())...),
		Build:    w.build(),
	}
}

// build is where a run may build now. The default repository is offered only while it is one of
// the repositories listed.
func (w *Work) build() Build {
	current := w.settings()
	targets := BuildTargets(current, w.deps.CloneFor, DefaultBranch)
	repos := make([]BuildRepo, 0, len(targets))

	var fallback *string

	for _, target := range targets {
		repos = append(repos, BuildRepo{
			Repo: target.Repo, Base: textOrNil(target.Base), Ready: target.Reason == "",
			Reason: textOrNil(target.Reason),
		})

		if current.Atlassian.Jira.BuildRepo != nil {
			if js.Lower(target.Repo) == js.Lower(*current.Atlassian.Jira.BuildRepo) {
				fallback = &target.Repo
			}
		}
	}

	return Build{Repos: repos, Default: fallback, Layout: current.Atlassian.Jira.SessionLayout}
}

func (w *Work) stale() bool {
	if w.list.Fetching {
		return false
	}

	if w.list.Error != nil {
		if *w.list.Error != "" {
			return false
		}
	}

	if !js.Truthy(w.list.FetchedAt) {
		return true
	}

	return w.now()-number(w.list.FetchedAt) > staleMs
}

// keyOf is a ticket's key as the cache holds it (anything, for a hand-edited cache).
func keyOf(ticket any) any {
	object, isObject := ticket.(*js.Object)
	if !isObject {
		return nil
	}

	return field(object, "key")
}

func textOrNil(text string) *string {
	if text == "" {
		return nil
	}

	return &text
}

// number is Number(value) for a parsed JSON value that is a number; NaN for anything else.
func number(value any) float64 {
	if scalar, isNumber := value.(float64); isNumber {
		return scalar
	}

	if text, isText := value.(string); isText {
		return js.ToNumber(text)
	}

	return math.NaN()
}

// Refresh reads the sprint again.
func (w *Work) Refresh() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.refresh()
}

// refresh starts a sprint read. Off, in setup or with no project: no call; nor while one runs.
func (w *Work) refresh() {
	w.sync()

	if !w.on() {
		return
	}

	if w.list.Fetching {
		return
	}

	started := w.now()
	step := stepStarting
	w.list.Fetching = true
	w.list.FetchStartedAt = &started
	w.list.Step = &step
	w.list.NeedsAuth = []string{}
	w.list.Error = nil
	w.bump()

	if w.deps.Dry {
		w.launch(func(step *task) {
			step.outside(func() { w.deps.Sleep(w.ctx, dryListWait) })
			w.settleList(dryTickets(), false)
		})

		return
	}

	ctx, cancel := w.trackList()
	reading := w.listHandle

	w.launch(func(step *task) {
		defer cancel()

		var (
			sprint jira.Sprint
			err    error
		)

		step.outside(func() { sprint, err = w.deps.FetchSprint(ctx, w.listHooks()) })
		w.listRead(reading, sprint, err)
	})
}

// trackList starts the sprint read's context: cancelled when the list is dropped or Atlassian
// is turned off.
func (w *Work) trackList() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(w.ctx)
	w.listHandle = &handle{cancel: cancel}

	return ctx, cancel
}

func (w *Work) listHooks() Hooks {
	return Hooks{
		OnProgress: func(line string) {
			w.mu.Lock()
			defer w.mu.Unlock()

			w.list.Step = &line
			w.bump()
		},
		OnAuth: func(needing []string) {
			w.mu.Lock()
			defer w.mu.Unlock()

			w.list.NeedsAuth = needing
			w.bump()
		},
	}
}

// listRead takes a sprint read's answer. A read that was stopped (Atlassian turned off, another
// read started) says nothing about the connector.
func (w *Work) listRead(reading *handle, sprint jira.Sprint, err error) {
	if w.listHandle != reading {
		return
	}

	w.listHandle = nil

	if err == nil {
		count := len(sprint.Tickets)
		plural := "s"

		if count == 1 {
			plural = ""
		}

		w.record(moduleAtlassian, true, "Open work read the sprint: "+js.Number(float64(count))+
			" ticket"+plural)

		tickets := make([]any, 0, count)
		for _, ticket := range sprint.Tickets {
			tickets = append(tickets, ticket)
		}

		w.settleList(tickets, sprint.Truncated)

		return
	}

	need := w.list.NeedsAuth
	message := err.Error()

	if len(need) > 0 {
		w.record(moduleAtlassian, false, "Atlassian is signed out: sign in to the claude.ai "+
			"Atlassian connector")

		message = strings.Join(need, " and ") + " is not signed in"
	} else {
		w.record(moduleAtlassian, false, "Open work could not read the sprint from Jira")
	}

	w.list.Fetching = false
	w.list.Step = nil
	w.list.Error = &message
	w.bump()
}

// cacheFile is work-cache.json.
type cacheFile struct {
	Tickets   []any   `json:"tickets"`
	FetchedAt any     `json:"fetchedAt"`
	Truncated bool    `json:"truncated"`
	Projects  *string `json:"projects"`
}

func (w *Work) settleList(tickets []any, truncated bool) {
	if w.ctx.Err() != nil {
		return
	}

	now := w.now()
	w.list = ticketList{
		Tickets: tickets, FetchedAt: now, Fetching: false, FetchStartedAt: nil, Step: nil,
		NeedsAuth: []string{}, Error: nil, Truncated: truncated,
	}

	err := os.MkdirAll(filepath.Dir(w.listFile), folderMode)
	if err == nil {
		_ = os.WriteFile(w.listFile, js.Marshal(cacheFile{
			Tickets: tickets, FetchedAt: now, Truncated: truncated, Projects: w.listFor,
		}), fileMode)
	}

	w.bump()
}
