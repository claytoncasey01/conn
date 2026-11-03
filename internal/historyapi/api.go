// Package historyapi answers /api/history: the list, one entry, one agent page, a summary and a
// resume, over the archive.
package historyapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/claytoncasey01/conn/internal/archive"
	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/history"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
)

const (
	dayMs        = 86_400_000
	limitDefault = 100
	limitMax     = 500
	listMax      = 50
	textMax      = 200
	safeInteger  = 1<<53 - 1
	prefix       = "/api/history"
	// actionSegments is an id and what to do with it; agentSegments an id, agents and an agent.
	actionSegments = 2
	agentSegments  = 3
	// resumeCooldown is how long a second resume of the same session is refused by default.
	resumeCooldown = 30 * time.Second
)

var (
	agentID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	bareDay = regexp.MustCompile(`^([0-9]{4})-([0-9]{2})-([0-9]{2})$`)
)

// Route kinds.
const (
	RouteNone = iota
	RouteList
	RouteEntry
	RouteAgent
	RouteSummary
	RouteResume
)

// Route is what a path under /api/history is. Post is set for summary and resume.
type Route struct {
	Kind    int
	ID      string
	AgentID string
}

// Post is whether the route takes a POST.
func (r Route) Post() bool {
	switch r.Kind {
	case RouteSummary, RouteResume:
		return true
	}

	return false
}

// decode is decodeURIComponent, or the text as it is when it is not valid.
func decode(segment string) string {
	decoded, err := url.PathUnescape(strings.ReplaceAll(segment, "+", "%2B"))
	if err != nil {
		return segment
	}

	return decoded
}

// RouteOf is what a request path under /api/history is, and false for any other path. The ids
// are decoded but not checked yet.
func RouteOf(path string) (Route, bool) {
	if path != prefix {
		if !strings.HasPrefix(path, prefix+"/") {
			return Route{}, false
		}
	}

	segments := strings.Split(path[len(prefix):], "/")[1:]
	if len(segments) > 1 {
		if segments[len(segments)-1] == "" {
			segments = segments[:len(segments)-1]
		}
	}

	if len(segments) == 0 {
		return Route{Kind: RouteList, ID: "", AgentID: ""}, true
	}

	if len(segments) == 1 {
		if segments[0] == "" {
			return Route{Kind: RouteList, ID: "", AgentID: ""}, true
		}
	}

	if slices.Contains(segments, "") {
		return Route{Kind: RouteNone, ID: "", AgentID: ""}, true
	}

	return routeFor(segments), true
}

func routeFor(segments []string) Route {
	sessionID := decode(segments[0])

	switch {
	case len(segments) == 1:
		return Route{Kind: RouteEntry, ID: sessionID, AgentID: ""}
	case len(segments) == actionSegments:
		switch segments[1] {
		case "summary":
			return Route{Kind: RouteSummary, ID: sessionID, AgentID: ""}
		case "resume":
			return Route{Kind: RouteResume, ID: sessionID, AgentID: ""}
		}
	case len(segments) == agentSegments:
		if segments[1] == "agents" {
			return Route{Kind: RouteAgent, ID: sessionID, AgentID: decode(segments[2])}
		}
	}

	return Route{Kind: RouteNone, ID: "", AgentID: ""}
}

// Reply is a handler's answer.
type Reply struct {
	Code int
	Body any
}

type errorBody struct {
	Error string `json:"error"`
}

func bad(code int, message string) Reply {
	return Reply{Code: code, Body: errorBody{Error: message}}
}

// Deps are what the API reads and starts.
type Deps struct {
	DB         *history.DB
	Summarizer *archive.Summarizer
	// LiveIDs are the sessions running now: their rows are only checkpoints.
	LiveIDs func(ctx context.Context) map[string]bool
	// Allow is the allowlist; nil shows every row.
	Allow func() []string
	Open  cmux.Opener
	// Dry launches nothing (CONN_WORK_DRY).
	Dry func() bool
	Now func() time.Time
	// ResumeCooldown refuses a second resume of the same session while the first shows up.
	ResumeCooldown time.Duration
}

// API answers /api/history.
type API struct {
	deps     Deps
	mutex    sync.Mutex
	opening  map[string]bool
	openedAt map[string]time.Time
}

// New is the API over deps.
func New(deps Deps) *API {
	if deps.ResumeCooldown == 0 {
		deps.ResumeCooldown = resumeCooldown
	}

	return &API{
		deps: deps, mutex: sync.Mutex{}, opening: map[string]bool{},
		openedAt: map[string]time.Time{},
	}
}

// Handle answers a route; an unexpected failure is the error, for a 500.
func (a *API) Handle(ctx context.Context, route Route, params url.Values, body *js.Object,
) (Reply, error) {
	switch route.Kind {
	case RouteNone:
		return bad(http.StatusNotFound, "not found"), nil
	case RouteList:
		return a.list(ctx, params)
	}

	// Checked before the id touches the database, a file or a command.
	if !cmux.IsSessionID(route.ID) {
		return bad(http.StatusBadRequest, "not a session id"), nil
	}

	hidden, err := a.hiddenRow(ctx, route.ID)
	if err != nil {
		return Reply{}, err
	}

	if hidden {
		return bad(http.StatusNotFound, "not found"), nil
	}

	switch route.Kind {
	case RouteEntry:
		return a.entry(ctx, route.ID)
	case RouteAgent:
		return a.agent(ctx, route)
	case RouteSummary:
		return a.summary(ctx, route.ID, body)
	}

	return a.resume(ctx, route.ID)
}

func (a *API) hiddenRow(ctx context.Context, sessionID string) (bool, error) {
	if a.deps.Allow == nil {
		return false, nil
	}

	repo, found, err := a.deps.DB.RepoOf(ctx, sessionID)
	if err != nil {
		return false, fmt.Errorf("reading the repo: %w", err)
	}

	if !found {
		return false, nil
	}

	if repo == "" {
		return false, nil
	}

	return !repos.Allowed(repo, a.deps.Allow()), nil
}

func (a *API) list(ctx context.Context, params url.Values) (Reply, error) {
	query := ParseQuery(params, a.deps.Now())

	for id := range a.deps.LiveIDs(ctx) {
		query.ExcludeIDs = append(query.ExcludeIDs, id)
	}

	if a.deps.Allow != nil {
		allow := a.deps.Allow()
		if allow == nil {
			allow = []string{}
		}

		query.Allow = &allow
	}

	listed, err := a.deps.DB.List(ctx, query)
	if err != nil {
		return Reply{}, fmt.Errorf("listing: %w", err)
	}

	return Reply{Code: http.StatusOK, Body: listed}, nil
}

func (a *API) agent(ctx context.Context, route Route) (Reply, error) {
	if !agentID.MatchString(route.AgentID) {
		return bad(http.StatusBadRequest, "not an agent id"), nil
	}

	page, found, err := a.deps.DB.Agent(ctx, route.ID, route.AgentID)
	if err != nil {
		return Reply{}, fmt.Errorf("reading an agent page: %w", err)
	}

	if !found {
		return bad(http.StatusNotFound, "not found"), nil
	}

	return Reply{Code: http.StatusOK, Body: page}, nil
}

func (a *API) entry(ctx context.Context, sessionID string) (Reply, error) {
	stored, found, err := a.deps.DB.Get(ctx, sessionID)
	if err != nil {
		return Reply{}, fmt.Errorf("reading the entry: %w", err)
	}

	if !found {
		return bad(http.StatusNotFound, "not found"), nil
	}

	stored, err = a.summarised(ctx, sessionID, stored)
	if err != nil {
		return Reply{}, err
	}

	// The archive recorded whether the file existed then; the person needs to know about now.
	if archived, isObject := value(stored.Detail, "archived").(*js.Object); isObject {
		path, err := a.deps.DB.TranscriptPath(ctx, sessionID)
		if err != nil {
			return Reply{}, fmt.Errorf("reading the transcript path: %w", err)
		}

		archived.Set("transcriptAvailable", isFile(path))
	}

	return Reply{Code: http.StatusOK, Body: stored}, nil
}

// summarised starts the summary of an entry that has none and reads the entry back. A running
// session's row is a partial checkpoint: it gets its summary when it ends.
func (a *API) summarised(ctx context.Context, sessionID string, stored history.Stored,
) (history.Stored, error) {
	if stored.Summary.State != history.SummaryNone {
		return stored, nil
	}

	if a.deps.LiveIDs(ctx)[sessionID] {
		return stored, nil
	}

	a.deps.Summarizer.Enqueue(ctx, sessionID, "", false)

	again, refound, err := a.deps.DB.Get(ctx, sessionID)
	if err != nil {
		return history.Stored{}, fmt.Errorf("reading the entry back: %w", err)
	}

	if refound {
		return again, nil
	}

	return stored, nil
}

func value(object *js.Object, key string) any {
	found, _ := object.Get(key)

	return found
}

func isFile(path string) bool {
	if path == "" {
		return false
	}

	info, err := os.Stat(path)
	if err != nil {
		return false
	}

	return info.Mode().IsRegular()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}

	return info.IsDir()
}

type summaryBody struct {
	State    string `json:"state"`
	Accepted bool   `json:"accepted"`
}

func (a *API) summary(ctx context.Context, sessionID string, body *js.Object) (Reply, error) {
	model := ""

	if raw, present := body.Get("model"); present {
		name, isText := raw.(string)

		chosen := map[string]string{"haiku": config.Haiku, "sonnet": config.Sonnet}[name]
		if !isText {
			chosen = ""
		}

		if chosen == "" {
			return bad(http.StatusBadRequest, "model must be haiku or sonnet"), nil
		}

		model = chosen
	}

	before, found, err := a.deps.DB.Get(ctx, sessionID)
	if err != nil {
		return Reply{}, fmt.Errorf("reading the entry: %w", err)
	}

	if !found {
		return bad(http.StatusNotFound, "not found"), nil
	}

	if a.deps.LiveIDs(ctx)[sessionID] {
		return bad(http.StatusConflict,
			"that session is still running: its summary is written when it ends"), nil
	}

	// The summarizer ignores an id already queued or running; say so rather than let a
	// regenerate look accepted.
	accepted := before.Summary.State != history.SummaryQueued
	if before.Summary.State == history.SummaryRunning {
		accepted = false
	}

	a.deps.Summarizer.Enqueue(ctx, sessionID, model, false)

	state := before.Summary.State

	after, refound, readErr := a.deps.DB.Get(ctx, sessionID)
	if readErr == nil {
		if refound {
			state = after.Summary.State
		}
	}

	return Reply{Code: http.StatusOK, Body: summaryBody{State: state, Accepted: accepted}}, nil
}

type resumed struct {
	OK            bool    `json:"ok"`
	Dry           *bool   `json:"dry,omitempty"`
	Command       *string `json:"command,omitempty"`
	Error         *string `json:"error,omitempty"`
	ManualCommand *string `json:"manualCommand,omitempty"`
}

func (a *API) resume(ctx context.Context, sessionID string) (Reply, error) {
	info, found, err := a.deps.DB.ResumeInfo(ctx, sessionID)
	if err != nil {
		return Reply{}, fmt.Errorf("reading the resume facts: %w", err)
	}

	if refused := a.resumable(ctx, sessionID, info, found); refused != nil {
		return *refused, nil
	}

	var name *string
	if info.Named {
		name = &info.Name
	}

	command, err := cmux.ResumeCommand(sessionID, name)
	if err != nil {
		return Reply{}, fmt.Errorf("building the resume command: %w", err)
	}

	if a.deps.Dry() {
		dry := true

		return Reply{Code: http.StatusOK, Body: resumed{
			OK: true, Dry: &dry, Command: &command, Error: nil, ManualCommand: nil,
		}}, nil
	}

	a.mutex.Lock()
	a.opening[sessionID] = true
	a.mutex.Unlock()

	opened := a.deps.Open(ctx, cmux.WorkspaceName(info.Name, sessionID), info.Cwd, command)

	a.mutex.Lock()
	delete(a.opening, sessionID)

	if opened.OK {
		a.openedAt[sessionID] = a.deps.Now()
	}

	a.mutex.Unlock()

	if opened.OK {
		return Reply{
			Code: http.StatusOK,
			Body: resumed{OK: true, Dry: nil, Command: nil, Error: nil, ManualCommand: nil},
		}, nil
	}

	// The request worked and the person has the command to paste.
	return Reply{Code: http.StatusOK, Body: resumed{
		OK: false, Dry: nil, Command: nil,
		Error: &opened.Error, ManualCommand: &opened.ManualCommand,
	}}, nil
}

// resumable is why a session cannot be resumed now, or nil.
func (a *API) resumable(ctx context.Context, sessionID string, info history.Resume, found bool,
) *Reply {
	refuse := func(code int, message string) *Reply {
		reply := bad(code, message)

		return &reply
	}

	switch {
	case !found:
		return refuse(http.StatusNotFound, "not found")
	case a.deps.LiveIDs(ctx)[sessionID]:
		return refuse(http.StatusConflict, "that session is already running")
	case !isFile(info.TranscriptPath):
		return refuse(http.StatusConflict,
			"the transcript is gone, so the conversation cannot be resumed")
	case info.Cwd == "", !filepath.IsAbs(info.Cwd), !isDir(info.Cwd):
		folder := info.Cwd
		if folder == "" {
			folder = "the working directory"
		}

		return refuse(http.StatusConflict, folder+" no longer exists")
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	last, opened := a.openedAt[sessionID]

	recent := opened
	if opened {
		recent = a.deps.Now().Sub(last) < a.deps.ResumeCooldown
	}

	if a.opening[sessionID] {
		recent = true
	}

	if recent {
		return refuse(http.StatusConflict,
			"a session was just opened for this one: give it a few seconds to appear")
	}

	return nil
}
