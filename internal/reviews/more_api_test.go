package reviews_test

import (
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/settings"
)

// Bun's reviewsApi.test.ts, the route parser and the handler. Bun's handler takes fakes; Go's is a
// method of Reviews, so it runs over the dry view, which calls nothing.

// TestDRouteOfParsesTheThreeRoutes: and nothing else under the prefix is one.
func TestDRouteOfParsesTheThreeRoutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path  string
		route string
		ours  bool
	}{
		{"/api/health", "", false},
		{"/api/reviewsx", "", false},
		{"/api/reviews/refresh", reviews.RouteRefresh, true},
		{"/api/reviews/start", reviews.RouteStart, true},
		{"/api/reviews/legacy", reviews.RouteLegacy, true},
		{"/api/reviews", reviews.RouteNone, true},
		{"/api/reviews/", reviews.RouteNone, true},
		{"/api/reviews/12/start", reviews.RouteNone, true},
		{"/api/reviews/start/", reviews.RouteNone, true},
		{"/api/reviews/refresh/x", reviews.RouteNone, true},
		{"/api/reviews//start", reviews.RouteNone, true},
		{"/api/reviews/acme/widgets/12/start", reviews.RouteNone, true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()

			route, ours := reviews.RouteOf(tt.path)
			if route != tt.route {
				t.Errorf("route %q, want %q", route, tt.route)
			}

			if ours != tt.ours {
				t.Errorf("ours %v, want %v", ours, tt.ours)
			}
		})
	}
}

func dDryTab(t *testing.T) *reviews.Reviews {
	t.Helper()

	return reviews.New(t.Context(), reviews.Deps{
		Settings: settings.NewStore(nil), Now: func() float64 { return answersT0 },
		Config: reviews.Config{Dry: true, NamePattern: dConfig().ReviewNamePattern},
	})
}

func dBody(t *testing.T, text string) *js.Object {
	t.Helper()

	parsed, err := js.Parse([]byte(text))
	dMust(t, err)

	object, isObject := parsed.(*js.Object)
	if !isObject {
		t.Fatalf("%s is not an object", text)
	}

	return object
}

func dHandle(t *testing.T, tab *reviews.Reviews, route, body string) string {
	t.Helper()

	reply, err := tab.Handle(t.Context(), route, dBody(t, body))
	dMust(t, err)

	return js.Number(float64(reply.Code)) + " " + dMarshal(reply.Body)
}

// TestDHandlePassesStartTheBodyAsSent: the repo, number and model as sent (an extra field is
// ignored, a missing one is absent), answering what Start says.
func TestDHandlePassesStartTheBodyAsSent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		body                string
		repo, number, model any
	}{
		{
			`{"repo":"acme/widgets","number":4110,"model":"opus","extra":1}`,
			"acme/widgets", float64(4110), "opus",
		},
		{
			`{"repo":"acme/widgets","number":"4101","model":"sonnet"}`,
			"acme/widgets", "4101", "sonnet",
		},
		{`{}`, nil, nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.body, func(t *testing.T) {
			t.Parallel()

			got := dHandle(t, dDryTab(t), reviews.RouteStart, tt.body)

			direct, err := dDryTab(t).Start(t.Context(), tt.repo, tt.number, tt.model)
			dMust(t, err)

			want := js.Number(float64(direct.Code)) + " " + dMarshal(direct.Body)
			if got != want {
				t.Errorf("Handle = %s, Start = %s", got, want)
			}
		})
	}
}

// TestDHandleRefreshLegacyAndNone: refresh answers ok with its number; legacy passes the number
// and repo (the dry view keeps nothing); an unknown route is a 404 that runs nothing.
func TestDHandleRefreshLegacyAndNone(t *testing.T) {
	t.Parallel()

	tab := dDryTab(t)

	if got := dHandle(t, tab, reviews.RouteRefresh, `{}`); got != `200 {"ok":true,"seq":1}` {
		t.Errorf("refresh %s", got)
	}

	legacy := dHandle(t, tab, reviews.RouteLegacy, `{"number":7,"repo":"acme/widgets"}`)
	if legacy != `409 {"error":"nothing is kept in the dry view"}` {
		t.Errorf("legacy %s", legacy)
	}

	if got := dHandle(t, tab, reviews.RouteNone, `{}`); got != `404 {"error":"not found"}` {
		t.Errorf("none %s", got)
	}

	if !strings.Contains(dMarshal(tab.Snapshot()), `"refresh":{"seq":1,"done":1}`) {
		t.Errorf("a route ran a refresh: %s", dMarshal(tab.Snapshot()))
	}
}
