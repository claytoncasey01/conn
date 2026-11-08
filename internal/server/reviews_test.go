package server_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/server"
	"github.com/claytoncasey01/conn/internal/settings"
)

// The Reviews routes through a server: here the handler with a dry Reviews over a temp data
// folder, so nothing it can launch is real and nothing is written.

const reviewsT0 = 1_800_000_000_000

// reviewsRig is a server answering /api/reviews from a dry Reviews, and the folder it would write
// into.
type reviewsRig struct {
	handler *server.Server
	tab     *reviews.Reviews
	data    string
}

func newReviewsRig(t *testing.T) reviewsRig {
	t.Helper()

	data := t.TempDir()
	cfg := config.FromEnv(func(string) (string, bool) { return "", false })
	tab := reviews.New(t.Context(), reviews.Deps{
		File: filepath.Join(data, "reviews.json"), Settings: settings.NewStore(nil),
		Now:    func() float64 { return reviewsT0 },
		Config: reviews.Config{Dry: true, NamePattern: cfg.ReviewNamePattern},
	})

	return reviewsRig{
		handler: &server.Server{
			UI: bundle(), Secret: testKey(t), Clients: func() int { return 0 }, Reviews: tab,
		},
		tab: tab, data: data,
	}
}

// reviewsCall is one request: the method, path, raw body and headers it is sent with.
type reviewsCall struct {
	method, path, body string
	headers            map[string]string
}

// postOwn is a POST with Conn's header from its own page.
func postOwn(path, body string) reviewsCall {
	return reviewsCall{
		method: http.MethodPost, path: path, body: body,
		headers: map[string]string{"X-Conn": "1", "Origin": "http://127.0.0.1:7777"},
	}
}

func (r reviewsRig) send(t *testing.T, call reviewsCall) (int, string) {
	t.Helper()

	request := httptest.NewRequestWithContext(t.Context(), call.method, call.path,
		strings.NewReader(call.body))
	request.Host = "127.0.0.1:7777"
	request.Header.Set(secret.Header, testKey(t).Authorization())

	for name, value := range call.headers {
		if name == "Host" {
			request.Host = value

			continue
		}

		request.Header.Set(name, value)
	}

	recorder := httptest.NewRecorder()
	r.handler.ServeHTTP(recorder, request)

	return recorder.Code, recorder.Body.String()
}

// TestReviewsRoutesOutsideTheThreeAreNotFound: any other path under the prefix is a 404 JSON,
// whatever the method and header, before the guard.
func TestReviewsRoutesOutsideTheThreeAreNotFound(t *testing.T) {
	t.Parallel()

	rig := newReviewsRig(t)
	paths := []string{
		"/api/reviews", "/api/reviews/", "/api/reviews/12/start", "/api/reviews/start/",
		"/api/reviews/refresh/x", "/api/reviews//start", "/api/reviews/acme/widgets/12/start",
	}

	for _, path := range paths {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(method+" "+path, func(t *testing.T) {
				t.Parallel()

				code, body := rig.send(t, reviewsCall{
					method: method, path: path, body: "",
					headers: nil,
				})
				if code != http.StatusNotFound {
					t.Errorf("status %d", code)
				}

				if body != `{"error":"not found"}` {
					t.Errorf("body %s", body)
				}
			})
		}
	}
}

// TestReviewsPostsAreGuarded: refused without x-conn: 1, with a wrong method, and with a foreign
// Origin or Host.
func TestReviewsPostsAreGuarded(t *testing.T) {
	t.Parallel()

	rig := newReviewsRig(t)
	cases := map[string]reviewsCall{
		"bare":        {method: http.MethodPost, headers: nil},
		"wrong value": {method: http.MethodPost, headers: map[string]string{"X-Conn": "0"}},
		"GET":         {method: http.MethodGet, headers: map[string]string{"X-Conn": "1"}},
		"foreign origin": {
			method: http.MethodPost, headers: map[string]string{
				"X-Conn": "1", "Origin": "http://evil.example",
			},
		},
		"foreign host": {
			method:  http.MethodPost,
			headers: map[string]string{"X-Conn": "1", "Host": "evil.example"},
		},
	}

	for _, route := range []string{"start", "refresh", "legacy"} {
		for name, call := range cases {
			t.Run(route+" "+name, func(t *testing.T) {
				t.Parallel()

				call.path = "/api/reviews/" + route
				code, body := rig.send(t, call)

				if code != http.StatusForbidden {
					t.Errorf("status %d", code)
				}

				if body != `{"error":"forbidden"}` {
					t.Errorf("body %s", body)
				}
			})
		}
	}
}

// TestReviewsRefreshAnswersOK: the first press is seq 1, and the dry snapshot has it done.
func TestReviewsRefreshAnswersOK(t *testing.T) {
	t.Parallel()

	rig := newReviewsRig(t)

	code, body := rig.send(t, postOwn("/api/reviews/refresh", ""))
	if code != http.StatusOK {
		t.Errorf("status %d", code)
	}

	if body != `{"ok":true,"seq":1}` {
		t.Errorf("body %s", body)
	}

	snapshot := string(js.Marshal(rig.tab.Snapshot()))
	if !strings.Contains(snapshot, `"refresh":{"seq":1,"done":1}`) {
		t.Errorf("snapshot refresh not done: %s", snapshot)
	}
}

// TestReviewsStartAnswersOKAndWritesNothing: REVIEW on an open dry row is a launch name, and
// neither reviews.json nor settings.json appears in the data folder.
func TestReviewsStartAnswersOKAndWritesNothing(t *testing.T) {
	t.Parallel()

	rig := newReviewsRig(t)

	code, body := rig.send(t, postOwn("/api/reviews/start",
		`{"repo":"acme/widgets","number":4110,"model":"sonnet"}`))
	if code != http.StatusOK {
		t.Fatalf("status %d %s", code, body)
	}

	if !regexp.MustCompile(`^\{"ok":true,"name":"pr-\d+(\.\d+)?-acme--[a-z]+"\}$`).
		MatchString(body) {
		t.Errorf("body %s", body)
	}

	for _, name := range []string{"reviews.json", "settings.json"} {
		_, err := os.Stat(filepath.Join(rig.data, name))
		if err == nil {
			t.Errorf("%s was written", name)
		}
	}
}

// TestReviewsStartRefusals: the validation refusals through the route, every body as raw
// JSON (a body that is not JSON, or not an object, is {}).
func TestReviewsStartRefusals(t *testing.T) {
	t.Parallel()

	rig := newReviewsRig(t)
	notRepo := `{"error":"not a repository"}`
	notNumber := `{"error":"not a PR number"}`
	notListed := `{"error":"that PR is not in the list"}`
	notModel := `{"error":"model must be sonnet or opus"}`
	tests := []struct {
		body string
		code int
		want string
	}{
		{`{"repo":"acme/widgets","number":4101,"model":"haiku"}`, 400, notModel},
		{`{"repo":"acme/widgets","number":4101}`, 400, notModel},
		{`{not json`, 400, notRepo},
		{`[1]`, 400, notRepo},
		{`null`, 400, notRepo},
		{``, 400, notRepo},
		{`{"repo":"acme/widgets","number":0,"model":"sonnet"}`, 400, notNumber},
		{`{"repo":"acme/widgets","number":-1,"model":"sonnet"}`, 400, notNumber},
		{`{"repo":"acme/widgets","number":1.5,"model":"sonnet"}`, 400, notNumber},
		{`{"repo":"acme/widgets","number":"12abc","model":"sonnet"}`, 400, notNumber},
		{`{"repo":"acme/widgets","number":"1e9","model":"sonnet"}`, 400, notNumber},
		{`{"repo":"acme/widgets","number":99999999999999999999,"model":"sonnet"}`, 400, notNumber},
		{`{"repo":"../x","number":4101,"model":"sonnet"}`, 400, notRepo},
		{`{"repo":"acme","number":4101,"model":"sonnet"}`, 400, notRepo},
		{`{"repo":"acme/w x","number":4101,"model":"sonnet"}`, 400, notRepo},
		{`{"repo":"acme/w?x","number":4101,"model":"sonnet"}`, 400, notRepo},
		{`{"repo":"","number":4101,"model":"sonnet"}`, 400, notRepo},
		{`{"repo":7,"number":4101,"model":"sonnet"}`, 400, notRepo},
		{`{"repo":"acme/widgets","number":999999,"model":"sonnet"}`, 404, notListed},
		{`{"repo":"other/repo","number":4101,"model":"sonnet"}`, 404, notListed},
		{`{"repo":"acme/gadgets","number":4070,"model":"sonnet"}`, 404, notListed},
		{
			`{"repo":"jane/tools","number":12,"model":"sonnet"}`, 409,
			`{"error":"No local clone of jane/tools: set a folder in Settings"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.body, func(t *testing.T) {
			t.Parallel()

			code, body := rig.send(t, postOwn("/api/reviews/start", tt.body))
			if code != tt.code {
				t.Errorf("status %d, want %d", code, tt.code)
			}

			if body != tt.want {
				t.Errorf("body %s, want %s", body, tt.want)
			}
		})
	}
}

// TestReviewsLegacyReachesTheTab: the number and repo go to PlaceLegacy, which the dry view
// refuses.
func TestReviewsLegacyReachesTheTab(t *testing.T) {
	t.Parallel()

	rig := newReviewsRig(t)

	code, body := rig.send(t, postOwn("/api/reviews/legacy",
		`{"number":1,"repo":"acme/widgets"}`))
	if code != http.StatusConflict {
		t.Errorf("status %d", code)
	}

	if body != `{"error":"nothing is kept in the dry view"}` {
		t.Errorf("body %s", body)
	}
}
