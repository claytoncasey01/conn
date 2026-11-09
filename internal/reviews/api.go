package reviews

import (
	"context"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
)

// The Reviews HTTP API: a pure handler over Reviews, with no HTTP server in here (the server
// routes and guards, as for History).

// The routes under /api/reviews.
const (
	RouteStart   = "start"
	RouteRefresh = "refresh"
	RouteLegacy  = "legacy"
	RouteNone    = "none"
)

// RouteOf is the route for a path under /api/reviews, and false for any other path. What to
// start or place is in the body, and is validated there.
func RouteOf(path string) (string, bool) {
	if path != "/api/reviews" {
		if !strings.HasPrefix(path, "/api/reviews/") {
			return "", false
		}
	}

	switch path {
	case "/api/reviews/refresh":
		return RouteRefresh, true
	case "/api/reviews/start":
		return RouteStart, true
	case "/api/reviews/legacy":
		return RouteLegacy, true
	}

	return RouteNone, true
}

// Handle answers one route with a POST body. An error is a program fault: the server answers
// 500.
func (r *Reviews) Handle(ctx context.Context, route string, body *js.Object) (Reply, error) {
	switch route {
	case RouteRefresh:
		seq, err := r.Refresh()
		if err != nil {
			return Reply{}, err
		}

		return okReply("seq", seq), nil
	case RouteLegacy:
		number, _ := body.Get("number")
		repo, _ := body.Get("repo")

		return r.PlaceLegacy(number, repo)
	case RouteStart:
		repo, _ := body.Get("repo")
		number, _ := body.Get("number")
		model, _ := body.Get("model")
		chosenAdvisor, _ := body.Get("advisor")

		return r.Start(ctx, repo, number, model, chosenAdvisor)
	}

	return bad(codeNotFound, "not found"), nil
}
