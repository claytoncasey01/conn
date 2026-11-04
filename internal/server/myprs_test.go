package server_test

import (
	"net/http"
	"testing"

	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/server"
)

// The /api/myprs routes through a server: here the handler with a dry My PRs, so nothing it can
// launch or write is real.

func newMyPrsRig(t *testing.T) reviewsRig {
	t.Helper()

	channel := "#code-review"
	tab := myprs.New(t.Context(), myprs.Deps{
		Scope: func() myprs.Scope {
			return myprs.Scope{
				Setup: false, Allow: []string{"acme/*"}, GitHub: true, Slack: true,
				DraftChannel: &channel,
			}
		},
		Now:    func() float64 { return reviewsT0 },
		Config: myprs.Config{Dry: true},
	})

	return reviewsRig{
		handler: &server.Server{UI: bundle(), Clients: func() int { return 0 }, MyPrs: tab},
		tab:     nil, data: t.TempDir(),
	}
}

// TestMyPrsRoutesOutsideTheThreeAreNotFound: any other path under the prefix is a 404 JSON,
// whatever the method and header, before the guard; a path that only starts alike is not ours.
func TestMyPrsRoutesOutsideTheThreeAreNotFound(t *testing.T) {
	t.Parallel()

	rig := newMyPrsRig(t)
	paths := []string{
		"/api/myprs", "/api/myprs/", "/api/myprs/nope", "/api/myprs/start/",
		"/api/myprs/refresh/x", "/api/myprs//thread",
	}

	for _, path := range paths {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(method+" "+path, func(t *testing.T) {
				t.Parallel()

				code, body := rig.send(t, reviewsCall{
					method: method, path: path, body: "", headers: nil,
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

// TestMyPrsPostsAreGuarded: refused without x-conn: 1, and with a wrong method.
func TestMyPrsPostsAreGuarded(t *testing.T) {
	t.Parallel()

	rig := newMyPrsRig(t)
	cases := map[string]reviewsCall{
		"bare":        {method: http.MethodPost, headers: nil},
		"wrong value": {method: http.MethodPost, headers: map[string]string{"X-Conn": "2"}},
		"GET":         {method: http.MethodGet, headers: map[string]string{"X-Conn": "1"}},
	}

	for _, route := range []string{"start", "refresh", "thread"} {
		for name, call := range cases {
			t.Run(route+" "+name, func(t *testing.T) {
				t.Parallel()

				call.path = "/api/myprs/" + route
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

// TestMyPrsRoutesAnswer: each route answers its body, a body that is not a JSON object reading
// as empty; a dry My PRs launches and writes nothing.
func TestMyPrsRoutesAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, path, body, want string
		code                   int
	}{
		{"refresh", "/api/myprs/refresh", "{}", `{"ok":true,"seq":1}`, http.StatusOK},
		{
			"start", "/api/myprs/start", `{"repo":"acme/widgets","number":7,"model":"opus"}`,
			`{"ok":true,"dry":true}`, http.StatusOK,
		},
		{
			"start text number", "/api/myprs/start",
			`{"repo":"acme/widgets","number":"7","model":"opus"}`,
			`{"error":"not a PR number"}`, http.StatusBadRequest,
		},
		{
			"start not json", "/api/myprs/start", "not json", `{"error":"not a repo name"}`,
			http.StatusBadRequest,
		},
		{
			"start array", "/api/myprs/start", "[1,2]", `{"error":"not a repo name"}`,
			http.StatusBadRequest,
		},
		{
			"start model", "/api/myprs/start",
			`{"repo":"acme/widgets","number":7,"model":"haiku"}`,
			`{"error":"model must be sonnet or opus"}`, http.StatusBadRequest,
		},
		{
			"thread", "/api/myprs/thread",
			`{"repo":"acme/widgets","number":7,"threadId":"PRRT_abcdefgh","text":"hi"}`,
			`{"ok":false,"error":"dry run (CONN_MYPRS_DRY): nothing was posted or resolved"}`,
			http.StatusOK,
		},
		{
			"thread id", "/api/myprs/thread",
			`{"repo":"acme/widgets","number":7,"threadId":"x","text":"hi"}`,
			`{"error":"not a thread id"}`, http.StatusBadRequest,
		},
		{
			"thread text", "/api/myprs/thread",
			`{"repo":"acme/widgets","number":7,"threadId":"PRRT_abcdefgh","text":5}`,
			`{"error":"the reply must be text"}`, http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newMyPrsRig(t)

			code, body := rig.send(t, postOwn(tt.path, tt.body))
			if code != tt.code {
				t.Errorf("status %d, want %d", code, tt.code)
			}

			if body != tt.want {
				t.Errorf("body %s, want %s", body, tt.want)
			}
		})
	}
}
