package myprs

import (
	"context"
	"net/http"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// The My PRs HTTP API: a pure handler over MyPrs, with no HTTP server in here (the server routes
// and guards, as for Reviews).

// The routes under /api/myprs.
const (
	RouteRefresh = "refresh"
	RouteStart   = "start"
	RouteThread  = "thread"
	RouteNone    = "none"
)

// RouteOf is the route for a path under /api/myprs, and false for any other path.
func RouteOf(path string) (string, bool) {
	if path != "/api/myprs" {
		if !strings.HasPrefix(path, "/api/myprs/") {
			return "", false
		}
	}

	switch path {
	case "/api/myprs/refresh":
		return RouteRefresh, true
	case "/api/myprs/start":
		return RouteStart, true
	case "/api/myprs/thread":
		return RouteThread, true
	}

	return RouteNone, true
}

// Handle answers one route with a POST body: SELF-REVIEW or RE-REVIEW (start), POST AND RESOLVE
// or RESOLVE (thread), REFRESH. Any value can come in: each validates it and refuses before any
// git, cmux or GitHub call. The error is always nil: the server answers 500 for one.
func (m *MyPrs) Handle(ctx context.Context, route string, body *js.Object) (
	reviews.Reply, error,
) {
	value := func(key string) any {
		found, _ := body.Get(key)

		return found
	}

	switch route {
	case RouteRefresh:
		return reply("ok", true, "seq", m.Refresh()), nil
	case RouteThread:
		return m.Thread(ctx, value("repo"), value("number"), value("threadId"),
			value("text")), nil
	case RouteStart:
		return m.Start(ctx, value("repo"), value("number"), value("model")), nil
	}

	return bad(http.StatusNotFound, "not found"), nil
}
