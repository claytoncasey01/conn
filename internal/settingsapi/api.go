// Package settingsapi is the Settings HTTP API over its dependencies, with no HTTP in it: the
// server routes, guards and reads the body. GET answers the settings with what Conn has seen;
// PUT replaces one section; POST browse lists what the signed-in gh can reach and POST check
// runs one module's probe, each only when called. Nothing here reads a repository: what was seen
// comes from folders and lists already in memory.
package settingsapi

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/flight"
	"github.com/claytoncasey01/conn/internal/gh"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/modules"
	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	// seenMax is the most repositories the page is offered.
	seenMax = 200

	prefix = "/api/settings"
)

// Kind is what a path under /api/settings asks for.
type Kind int

// The routes.
const (
	KindNone Kind = iota
	KindGet
	KindPut
	KindBrowse
	KindCheck
)

// Route is one path under /api/settings.
type Route struct {
	Kind Kind
	// Section is the section a PUT replaces.
	Section string
	// Module is the module a check probes.
	Module string
}

// Method is the one HTTP method the route answers.
func (r Route) Method() string {
	switch r.Kind {
	case KindGet:
		return http.MethodGet
	case KindPut:
		return http.MethodPut
	case KindNone, KindBrowse, KindCheck:
	}

	return http.MethodPost
}

// RouteOf is the route for a path, as sent (not decoded), and false for a path that is not
// under /api/settings. A path under it that names nothing is KindNone.
func RouteOf(path string) (Route, bool) {
	if path == prefix {
		return Route{Kind: KindGet, Section: "", Module: ""}, true
	}

	rest, found := strings.CutPrefix(path, prefix+"/")
	if !found {
		return Route{}, false
	}

	if rest == "browse" {
		return Route{Kind: KindBrowse, Section: "", Module: ""}, true
	}

	if module, isCheck := strings.CutPrefix(rest, "check/"); isCheck {
		if modules.IsModule(module) {
			return Route{Kind: KindCheck, Section: "", Module: module}, true
		}

		return Route{}, true
	}

	if slices.Contains(settings.Sections(), rest) {
		return Route{Kind: KindPut, Section: rest, Module: ""}, true
	}

	return Route{}, true
}

// Holder is the settings the API reads and changes: settings.Store, or a fake in a test.
type Holder interface {
	Get() settings.Settings
	Setup() bool
	Dropped() []string
	BackedUp() string
	Update(name string, body any) settings.UpdateResult
}

// Deps are what the API reads. A nil function is a source with nothing in it yet.
type Deps struct {
	Settings Holder
	// Sessions are the live sessions' folders.
	Sessions func() []string
	// MyPrRepos are the repositories of the My PRs rows in memory.
	MyPrRepos func() []string
	// HistoryRepos are the repositories History holds.
	HistoryRepos func() []string
	// OriginOf is a folder's repository, or "".
	OriginOf func(folder string) string
	// CloneFor is where a repository is cloned on this machine.
	CloneFor func(repo string) (origin.Clone, bool)
	// Gh runs gh; only browse calls it.
	Gh gh.Exec
	// Modules are the module statuses; nil leaves them out of GET and makes check a 404.
	Modules *modules.Statuses
}

// API answers the Settings routes.
type API struct {
	deps     Deps
	browsing flight.Group[struct{}, Reply]
}

// New is the API over deps.
func New(deps Deps) *API {
	return &API{deps: deps, browsing: flight.Group[struct{}, Reply]{}}
}

// Reply is a status code and the JSON body to send with it.
type Reply struct {
	Code int
	Body any
}

// ErrorBody is the body of every refusal.
type ErrorBody struct {
	Error string `json:"error"`
}

type okBody struct {
	OK bool `json:"ok"`
}

type statusBody struct {
	Status modules.Status `json:"status"`
}

func refuse(code int, message string) Reply {
	return Reply{Code: code, Body: ErrorBody{Error: message}}
}

// Handle answers a route. body is the parsed request body (a *js.Object), unused by GET.
func (a *API) Handle(ctx context.Context, route Route, body any) Reply {
	switch route.Kind {
	case KindGet:
		return Reply{Code: http.StatusOK, Body: a.document()}
	case KindPut:
		return a.put(route.Section, body)
	case KindCheck:
		return a.check(ctx, route.Module)
	case KindBrowse:
		reply, answered := a.browsing.Do(ctx, struct{}{}, a.browse)
		if !answered {
			return refuse(http.StatusServiceUnavailable, "the request ended before gh answered")
		}

		return reply
	case KindNone:
	}

	return refuse(http.StatusNotFound, "not found")
}

func (a *API) put(section string, body any) Reply {
	result := a.deps.Settings.Update(section, body)
	if result.OK {
		return Reply{Code: http.StatusOK, Body: okBody{OK: true}}
	}

	if result.SaveFailed {
		return refuse(http.StatusInternalServerError, result.Error)
	}

	return refuse(http.StatusBadRequest, result.Error)
}

func (a *API) check(ctx context.Context, module string) Reply {
	if a.deps.Modules == nil {
		return refuse(http.StatusNotFound, "not found")
	}

	// A module whose switch is off is never called, not even by a press: its switch is saved on
	// first.
	if !a.deps.Settings.Get().Switch(module) {
		return refuse(http.StatusConflict,
			label(module)+" is off: save its switch on before checking it")
	}

	return Reply{Code: http.StatusOK, Body: statusBody{Status: a.deps.Modules.Check(ctx, module)}}
}

func label(module string) string {
	switch module {
	case settings.ModuleGitHub:
		return "GitHub"
	case settings.ModuleSlack:
		return "Slack"
	}

	return "Atlassian"
}

// Seen is a repository Conn has seen, and where it was seen first.
type Seen struct {
	Repo string `json:"repo"`
	From string `json:"from"`
}

// cloneRow is where an allowed repository that was seen is cloned; nil fields when it is not.
type cloneRow struct {
	Repo string      `json:"repo"`
	Path *string     `json:"path"`
	How  *origin.How `json:"how"`
}

// document is GET's body.
type document struct {
	Settings settings.Settings `json:"settings"`
	Setup    bool              `json:"setup"`
	Seen     []Seen            `json:"seen"`
	Clones   []cloneRow        `json:"clones"`
	// Matched counts the seen repositories each pattern allows, keyed in the list's order.
	Matched  *js.Object   `json:"matched"`
	Dropped  []string     `json:"dropped"`
	BackedUp *string      `json:"backedUp"`
	Modules  *modules.All `json:"modules,omitempty"`
}

func (a *API) document() document {
	current := a.deps.Settings.Get()
	seen := a.seen()
	allow := current.Repos.Allow

	matched := js.NewObject()
	for _, pattern := range allow {
		matched.Set(pattern, float64(countAllowed(seen, pattern)))
	}

	body := document{
		Settings: current,
		Setup:    a.deps.Settings.Setup(),
		Seen:     seen,
		Clones:   a.clones(seen, allow),
		Matched:  matched,
		Dropped:  orEmpty(a.deps.Settings.Dropped()),
		BackedUp: nil,
		Modules:  nil,
	}

	if backedUp := a.deps.Settings.BackedUp(); backedUp != "" {
		body.BackedUp = &backedUp
	}

	if a.deps.Modules != nil {
		all := a.deps.Modules.All()
		body.Modules = &all
	}

	return body
}

func countAllowed(seen []Seen, pattern string) int {
	count := 0

	for _, repo := range seen {
		if repos.Allowed(repo.Repo, []string{pattern}) {
			count++
		}
	}

	return count
}

func (a *API) clones(seen []Seen, allow []string) []cloneRow {
	rows := []cloneRow{}

	for _, repo := range seen {
		if !repos.Allowed(repo.Repo, allow) {
			continue
		}

		row := cloneRow{Repo: repo.Repo, Path: nil, How: nil}

		if a.deps.CloneFor != nil {
			if clone, found := a.deps.CloneFor(repo.Repo); found {
				row.Path = &clone.Path
				row.How = &clone.How
			}
		}

		rows = append(rows, row)
	}

	return rows
}

// seen are the repositories of the live sessions, the My PRs rows and History, each once
// (lowercase, where it was seen first), at most seenMax. A name that is not owner/repo is left
// out.
func (a *API) seen() []Seen {
	found := []Seen{}
	listed := map[string]bool{}

	add := func(repo, from string) {
		if len(found) >= seenMax {
			return
		}

		if !repos.IsName(repo) {
			return
		}

		key := strings.ToLower(repo)
		if listed[key] {
			return
		}

		listed[key] = true
		found = append(found, Seen{Repo: key, From: from})
	}

	for _, folder := range call(a.deps.Sessions) {
		if a.deps.OriginOf != nil {
			add(a.deps.OriginOf(folder), "session")
		}
	}

	for _, repo := range call(a.deps.MyPrRepos) {
		add(repo, "myprs")
	}

	for _, repo := range call(a.deps.HistoryRepos) {
		add(repo, "history")
	}

	return found
}

// call is what a source holds; nothing when there is no source.
func call(source func() []string) []string {
	if source == nil {
		return nil
	}

	return source()
}

func orEmpty(list []string) []string {
	if list == nil {
		return []string{}
	}

	return list
}
