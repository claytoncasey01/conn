package myprs_test

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// The route parser, and the handler over a MyPrs of the fake world (start and thread are
// MyPrs' own, so a route is shown to reach them by answering exactly what a direct call
// answers). The server-process cases run against the handler with CONN_MYPRS_DRY (Config.Dry)
// and a seeded scope; the POST guard (GET, no x-conn,
// a foreign origin or host is 403) is internal/server's ownWrite, shared with Reviews.

func eDry(ctx context.Context, t *testing.T, dry bool) *myprs.MyPrs {
	t.Helper()

	deps := newWorld(t).Deps(filepath.Join(t.TempDir(), "myprs.json"))
	deps.Config.Dry = dry

	return myprs.New(ctx, deps)
}

func eBody(t *testing.T, text string) *js.Object {
	t.Helper()

	body, isObject := eParsed(t, text).(*js.Object)
	if !isObject {
		t.Fatalf("%s is not an object", text)
	}

	return body
}

func eReplyText(reply reviews.Reply) string {
	return js.Stringify(reply.Code) + " " + js.Stringify(reply.Body)
}

func TestERouteOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path, want string
		ours       bool
	}{
		{"/api/health", "", false},
		{"/api/myprsx", "", false},
		{"/api/myprs/refresh", myprs.RouteRefresh, true},
		{"/api/myprs/start", myprs.RouteStart, true},
		{"/api/myprs/thread", myprs.RouteThread, true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()

			route, ours := myprs.RouteOf(tt.path)
			if ours != tt.ours {
				t.Errorf("ours %v", ours)
			}

			if route != tt.want {
				t.Errorf("route %q", route)
			}
		})
	}

	// The server hands RouteOf the path as it was sent (EscapedPath): an escape is not a route.
	for _, tt := range []string{
		"/api/myprs", "/api/myprs/", "/api/myprs/refresh/", "/api/myprs/refresh/x",
		"/api/myprs/other", "/api/myprs//refresh", "/api/myprs/%72efresh", "/api/myprs/start/",
		"/api/myprs/start/x", "/api/myprs/Start", "/api/myprs/thread/", "/api/myprs/thread/x",
		"/api/myprs/Thread", "/api/myprs/%74hread", "/api/myprs/nothing",
	} {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()

			route, ours := myprs.RouteOf(tt)
			if !ours {
				t.Error("not ours")
			}

			if route != myprs.RouteNone {
				t.Errorf("route %q", route)
			}
		})
	}
}

func TestEHandleRefreshAndNotFound(t *testing.T) {
	t.Parallel()

	tab := eDry(context.Background(), t, true)
	view := tab.Snapshot()

	eSameJSON(t, "rows", view.Rows, "[]")

	if view.Error != nil {
		t.Errorf("error %q", *view.Error)
	}

	eSameJSON(t, "refresh", view.Refresh, `{"seq":0,"done":0}`)

	reply, err := tab.Handle(context.Background(), myprs.RouteRefresh, js.NewObject())
	if err != nil {
		t.Fatal(err)
	}

	if eReplyText(reply) != `200 {"ok":true,"seq":1}` {
		t.Errorf("refresh %s", eReplyText(reply))
	}

	if tab.Snapshot().Refresh.Seq != 1 {
		t.Errorf("seq %d", tab.Snapshot().Refresh.Seq)
	}

	reply, err = tab.Handle(context.Background(), myprs.RouteNone, js.NewObject())
	if err != nil {
		t.Fatal(err)
	}

	if eReplyText(reply) != `404 {"error":"not found"}` {
		t.Errorf("none %s", eReplyText(reply))
	}

	if tab.Snapshot().Refresh.Seq != 1 {
		t.Error("a route that does not exist counted as a press")
	}
}

func TestEHandlePassesTheBodyOn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tests := []struct {
		name, route, body string
		direct            func(m *myprs.MyPrs) reviews.Reply
	}{
		{
			"start", myprs.RouteStart, `{"repo":"org/repo","number":7,"model":"opus","extra":1}`,
			func(m *myprs.MyPrs) reviews.Reply { return m.Start(ctx, "org/repo", 7.0, "opus") },
		},
		{
			"start, empty", myprs.RouteStart, "{}",
			func(m *myprs.MyPrs) reviews.Reply { return m.Start(ctx, nil, nil, nil) },
		},
		{
			"thread", myprs.RouteThread, `{"repo":"org/repo","number":7,` +
				`"threadId":"PRRT_kwDOabc123","text":"hi","model":"opus"}`,
			func(m *myprs.MyPrs) reviews.Reply {
				return m.Thread(ctx, "org/repo", 7.0, "PRRT_kwDOabc123", "hi")
			},
		},
		{
			"thread, empty", myprs.RouteThread, "{}",
			func(m *myprs.MyPrs) reviews.Reply { return m.Thread(ctx, nil, nil, nil, nil) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tab := eDry(ctx, t, false)

			reply, err := tab.Handle(ctx, tt.route, eBody(t, tt.body))
			if err != nil {
				t.Fatal(err)
			}

			if eReplyText(reply) != eReplyText(tt.direct(tab)) {
				t.Errorf("handled %s, direct %s", eReplyText(reply), eReplyText(tt.direct(tab)))
			}
		})
	}
}

func TestEHandleDryStart(t *testing.T) {
	t.Parallel()

	good := `"repo":"org/repo","number":7,"model":"sonnet"`
	tests := []struct {
		name, body string
		code       int
	}{
		{"empty", "{}", http.StatusBadRequest},
		{"not a repo", `{"repo":"nope","number":7,"model":"sonnet"}`, http.StatusBadRequest},
		{
			"a number as text", `{"repo":"org/repo","number":"7","model":"sonnet"}`,
			http.StatusBadRequest,
		},
		{"number 0", `{"repo":"org/repo","number":0,"model":"sonnet"}`, http.StatusBadRequest},
		{"haiku", `{"repo":"org/repo","number":7,"model":"haiku"}`, http.StatusBadRequest},
		{"good", "{" + good + "}", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			eCheckDry(t, myprs.RouteStart, tt.body, tt.code, `{"ok":true,"dry":true}`)
		})
	}
}

func TestEHandleDryThread(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, body string
		code       int
	}{
		{"empty", "{}", http.StatusBadRequest},
		{"not a repo", eThreadBody("repo", `"nope"`), http.StatusBadRequest},
		{"a number as text", eThreadBody("number", `"7"`), http.StatusBadRequest},
		{"not a thread id", eThreadBody("threadId", `"x y"`), http.StatusBadRequest},
		{"a number as text", eThreadBody("text", "5"), http.StatusBadRequest},
		{
			"a long text", eThreadBody("text", `"`+strings.Repeat("a", 2001)+`"`),
			http.StatusBadRequest,
		},
		{"good", eThreadBody("", ""), http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			eCheckDry(t, myprs.RouteThread, tt.body, tt.code,
				`{"ok":false,"error":"dry run (CONN_MYPRS_DRY): nothing was posted or resolved"}`)
		})
	}
}

// eThreadBody is a good thread body with key set to value (raw JSON); "" changes nothing.
func eThreadBody(key, value string) string {
	fields := []struct{ key, value string }{
		{"repo", `"org/repo"`},
		{"number", "7"},
		{"threadId", `"PRRT_kwDOabc123"`},
		{"text", `"A reply."`},
	}
	parts := make([]string, 0, len(fields))

	for _, field := range fields {
		if field.key == key {
			field.value = value
		}

		parts = append(parts, `"`+field.key+`":`+field.value)
	}

	return "{" + strings.Join(parts, ",") + "}"
}

// eCheckDry posts body to route of a dry MyPrs: a refusal is code with a text error, a good body
// answers good.
func eCheckDry(t *testing.T, route, body string, code int, good string) {
	t.Helper()

	ctx := context.Background()

	reply, err := eDry(ctx, t, true).Handle(ctx, route, eBody(t, body))
	if err != nil {
		t.Fatal(err)
	}

	if reply.Code != code {
		t.Fatalf("%s", eReplyText(reply))
	}

	if code == http.StatusOK {
		eSameJSON(t, "good", reply.Body, good)

		return
	}

	why, _ := reply.Body.Get("error")
	if _, isText := why.(string); !isText {
		t.Errorf("error %v", why)
	}
}
