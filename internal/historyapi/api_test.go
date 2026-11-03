package historyapi_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/historyapi"
)

func TestRouteOf(t *testing.T) {
	t.Parallel()

	sessionID := "0f8fad5b-d9cb-469f-a165-70867728950e"
	tests := []struct {
		path string
		ours bool
		kind int
	}{
		{"/api/history", true, historyapi.RouteList},
		{"/api/history/", true, historyapi.RouteList},
		{"/api/history/" + sessionID, true, historyapi.RouteEntry},
		{"/api/history/" + sessionID + "/", true, historyapi.RouteEntry},
		{"/api/history/" + sessionID + "/summary", true, historyapi.RouteSummary},
		{"/api/history/" + sessionID + "/resume", true, historyapi.RouteResume},
		{"/api/history/" + sessionID + "/agents/lead", true, historyapi.RouteAgent},
		{"/api/history//x", true, historyapi.RouteNone},
		{"/api/history/a/b/c/d", true, historyapi.RouteNone},
		{"/api/historyx", false, historyapi.RouteNone},
	}

	for _, tt := range tests {
		route, ours := historyapi.RouteOf(tt.path)
		if ours != tt.ours {
			t.Errorf("%s ours %v", tt.path, ours)
		}

		if route.Kind != tt.kind {
			t.Errorf("%s kind %d", tt.path, route.Kind)
		}
	}
}

func TestParseQuery(t *testing.T) {
	t.Parallel()

	utc := time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC)
	params, _ := url.ParseQuery("range=custom&from=2026-02-30&to=2026-11-03&limit=9999&offset=-1" +
		"&minCost=&repos=Acme/Widgets,,x&q=++fix++")

	query := historyapi.ParseQuery(params, utc)
	if query.From != nil {
		t.Error("an impossible day parsed")
	}

	if query.To == nil {
		t.Fatal("no to")
	}

	if query.Limit == nil {
		t.Fatal("no limit")
	}

	if *query.Limit != 500 {
		t.Errorf("limit %v", *query.Limit)
	}

	if query.Offset != nil {
		t.Error("a negative offset")
	}

	if query.MinCost != nil {
		t.Error("a blank minCost")
	}

	if len(query.Repos) != 2 {
		t.Errorf("repos %v", query.Repos)
	}

	if query.Repos[0] != "acme/widgets" {
		t.Errorf("repo %s", query.Repos[0])
	}

	if query.Text != "fix" {
		t.Errorf("text %q", query.Text)
	}
}
