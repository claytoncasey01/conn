package provided

import (
	"net/http"
	"strings"
	"sync"

	"github.com/claytoncasey01/conn/internal/js"
)

const routePrefix = "/api/provided"

// RouteKind is what a path under /api/provided asks for.
type RouteKind int

// The routes: the list, installing a skill, removing one, or nothing (a 404).
const (
	RouteNone RouteKind = iota
	RouteList
	RouteInstall
	RouteRemove
)

// Method is the one method a route takes.
func (k RouteKind) Method() string {
	if k == RouteList {
		return http.MethodGet
	}

	return http.MethodPost
}

// RouteOf reads an escaped path, and is false for one outside /api/provided.
func RouteOf(escaped string) (RouteKind, bool) {
	if escaped == routePrefix {
		return RouteList, true
	}

	rest, found := strings.CutPrefix(escaped, routePrefix+"/")
	if !found {
		return RouteNone, false
	}

	switch rest {
	case "install":
		return RouteInstall, true
	case "remove":
		return RouteRemove, true
	}

	return RouteNone, true
}

// Row is one item on the page.
type Row struct {
	Name  string `json:"name"`
	State State  `json:"state"`
	// Note says what is in the way when the state is a conflict.
	Note string `json:"note,omitempty"`
}

// Answer is what GET /api/provided answers, and what an install or a remove answers with.
type Answer struct {
	Agents []Row `json:"agents"`
	Skills []Row `json:"skills"`
	// LinkDir is Claude's folder the links go in, and CopyDir the data folder the copies are in.
	LinkDir string `json:"linkDir"`
	CopyDir string `json:"copyDir"`
	// Writable is whether INSTALL and REMOVE do anything: not in a dry run.
	Writable bool `json:"writable"`
}

type failed struct {
	Error string `json:"error"`
}

// Result is a route's answer: a status code and its JSON body.
type Result struct {
	Code int
	Body any
}

// Deps are what the API works on.
type Deps struct {
	Layout Layout
	Items  []Item
	// Writable is false for a dry run, which links nothing.
	Writable bool
}

// API answers the provided routes. The state is read from the disk on every call.
type API struct {
	deps Deps
	// guard keeps two presses from linking and unlinking at once.
	guard sync.Mutex
}

// New is the API over deps.
func New(deps Deps) *API { return &API{deps: deps, guard: sync.Mutex{}} }

// Handle answers a route; body is the request's JSON object (nil for the list).
func (a *API) Handle(route RouteKind, body *js.Object) Result {
	if route == RouteNone {
		return refuse(http.StatusNotFound, "not found")
	}

	a.guard.Lock()
	defer a.guard.Unlock()

	if route == RouteList {
		return a.answer()
	}

	value, _ := body.Get("name")
	name, _ := value.(string)

	item, found := Find(a.deps.Items, KindSkill, name)
	if !found {
		return refuse(http.StatusNotFound, "there is no provided skill named "+js.Stringify(name))
	}

	if !a.deps.Writable {
		return refuse(http.StatusConflict, "this is a dry run: nothing is linked")
	}

	if route == RouteInstall {
		return a.install(item)
	}

	return a.remove(item)
}

func (a *API) install(item Item) Result {
	layout := a.deps.Layout

	found, err := Look(layout.Link(item))
	if err != nil {
		return refuse(http.StatusInternalServerError, err.Error())
	}

	switch Judge(found, layout.Copy(item)) {
	case StateConflict:
		return refuse(http.StatusConflict, layout.Link(item)+" is "+Describe(found)+
			": Conn leaves it as it is. Move it away to install this skill.")
	case StateOn:
		return a.answer()
	case StateOff:
	}

	err = WriteCopies(layout, []Item{item})
	if err == nil {
		err = MakeLink(layout, item)
	}

	if err != nil {
		return refuse(http.StatusInternalServerError, err.Error())
	}

	return a.answer()
}

func (a *API) remove(item Item) Result {
	layout := a.deps.Layout

	found, err := Look(layout.Link(item))
	if err != nil {
		return refuse(http.StatusInternalServerError, err.Error())
	}

	switch Judge(found, layout.Copy(item)) {
	case StateConflict:
		return refuse(http.StatusConflict, layout.Link(item)+" is "+Describe(found)+
			", not a link of Conn's: it is left as it is.")
	case StateOff:
		return a.answer()
	case StateOn:
	}

	err = RemoveLink(layout, item)
	if err != nil {
		return refuse(http.StatusInternalServerError, err.Error())
	}

	return a.answer()
}

// answer is the state of every item, read from the disk.
func (a *API) answer() Result {
	layout := a.deps.Layout
	answer := Answer{
		Agents: []Row{}, Skills: []Row{}, LinkDir: layout.Claude, CopyDir: layout.Data,
		Writable: a.deps.Writable,
	}

	for _, item := range a.deps.Items {
		row, err := a.row(item)
		if err != nil {
			return refuse(http.StatusInternalServerError, err.Error())
		}

		if item.Kind == KindAgent {
			answer.Agents = append(answer.Agents, row)
		} else {
			answer.Skills = append(answer.Skills, row)
		}
	}

	return Result{Code: http.StatusOK, Body: answer}
}

func (a *API) row(item Item) (Row, error) {
	layout := a.deps.Layout

	found, err := Look(layout.Link(item))
	if err != nil {
		return Row{}, err
	}

	state := Judge(found, layout.Copy(item))
	if state == StateConflict {
		return Row{Name: item.Name, State: state, Note: Describe(found)}, nil
	}

	return Row{Name: item.Name, State: state, Note: ""}, nil
}

func refuse(code int, message string) Result {
	return Result{Code: code, Body: failed{Error: message}}
}
