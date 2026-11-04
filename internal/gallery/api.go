package gallery

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/claytoncasey01/conn/internal/flight"
	"github.com/claytoncasey01/conn/internal/js"
)

// How long an index answer is kept: a good read a minute, a failed one only briefly, so a network
// that came back shows up quickly.
const (
	IndexTTL     = 60 * time.Second
	IndexFailTTL = 5 * time.Second

	routePrefix = "/api/gallery"
	indexKey    = "index"
)

// RouteKind is what a path under /api/gallery asks for.
type RouteKind int

// The routes: the index, an install, an uninstall, or nothing (a 404).
const (
	RouteNone RouteKind = iota
	RouteIndex
	RouteInstall
	RouteUninstall
)

// Method is the one method a route takes.
func (k RouteKind) Method() string {
	if k == RouteIndex {
		return http.MethodGet
	}

	return http.MethodPost
}

// RouteOf reads an escaped path, and is false for one outside /api/gallery.
func RouteOf(escaped string) (RouteKind, bool) {
	if escaped == routePrefix {
		return RouteIndex, true
	}

	rest, found := strings.CutPrefix(escaped, routePrefix+"/")
	if !found {
		return RouteNone, false
	}

	switch rest {
	case "install":
		return RouteInstall, true
	case "uninstall":
		return RouteUninstall, true
	}

	return RouteNone, true
}

// Answer is what GET /api/gallery answers.
type Answer struct {
	Enabled   bool    `json:"enabled"`
	Repo      *string `json:"repo"`
	CheckedAt *int64  `json:"checkedAt,omitempty"`
	Themes    []Entry `json:"themes"`
	Dropped   *int    `json:"dropped,omitempty"`
	Error     string  `json:"error,omitempty"`
}

// kept is the last index answer and how long it holds.
type kept struct {
	at     int64
	ttl    time.Duration
	answer Answer
}

// API answers the gallery routes. Any page can make a browser fire a GET at the index, so it is
// read at most once a minute and GETs at once share one read. Install and uninstall never use
// the kept answer: an install reads its own fresh index.
type API struct {
	gallery *Gallery
	repo    string
	now     func() int64

	guard sync.Mutex
	last  *kept
	reads flight.Group[string, Answer]
}

// NewAPI answers for gallery, whose index is repo ("" when CONN_THEME_INDEX is off: then
// nothing is fetched, and install and uninstall do not exist). now is the clock, in Unix ms.
func NewAPI(gallery *Gallery, repo string, now func() int64) *API {
	return &API{
		gallery: gallery, repo: repo, now: now,
		guard: sync.Mutex{}, last: nil, reads: flight.Group[string, Answer]{},
	}
}

// Handle answers a route; body is the request's JSON object (nil for the index).
func (a *API) Handle(ctx context.Context, route RouteKind, body *js.Object) Result {
	notFound := refuse(http.StatusNotFound, "not found")

	if route == RouteNone {
		return notFound
	}

	if a.repo == "" {
		if route != RouteIndex {
			return notFound
		}

		return Result{
			Code: http.StatusOK,
			Body: Answer{Enabled: false, Repo: nil, Themes: []Entry{}},
		}
	}

	themeID, _ := body.Get("id")

	switch route {
	case RouteInstall:
		return a.gallery.Install(ctx, themeID)
	case RouteUninstall:
		return a.gallery.Uninstall(themeID)
	case RouteIndex, RouteNone:
	}

	return Result{Code: http.StatusOK, Body: a.index(ctx)}
}

// Reads is how many callers wait on the index read in flight: a test's view.
func (a *API) Reads() int { return a.reads.Waiting(indexKey) }

func (a *API) fresh() (Answer, bool) {
	a.guard.Lock()
	defer a.guard.Unlock()

	if a.last == nil {
		return Answer{}, false
	}

	age := time.Duration(a.now()-a.last.at) * time.Millisecond
	if age >= a.last.ttl {
		return Answer{}, false
	}

	return a.last.answer, true
}

func (a *API) index(ctx context.Context) Answer {
	answer, ok := a.fresh()
	if ok {
		return answer
	}

	answer, _ = a.reads.Do(ctx, indexKey, a.read)

	return answer
}

// read reads the index and keeps the answer. A failed read is a 200 with its message: the
// gallery is up, and the page shows why it has nothing to list.
func (a *API) read(ctx context.Context) Answer {
	repo := a.repo
	index, problem := a.gallery.FetchIndex(ctx)

	answer := Answer{Enabled: true, Repo: &repo, Themes: []Entry{}}
	ttl := IndexFailTTL

	if problem == "" {
		dropped := index.Dropped
		checkedAt := index.CheckedAt
		answer.CheckedAt, answer.Themes, answer.Dropped = &checkedAt, index.Themes, &dropped
		ttl = IndexTTL
	} else {
		answer.Error = problem
	}

	a.guard.Lock()
	a.last = &kept{at: a.now(), ttl: ttl, answer: answer}
	a.guard.Unlock()

	return answer
}
