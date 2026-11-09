package server_test

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/newsession"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/realpath"
	"github.com/claytoncasey01/conn/internal/safefolder"
	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/server"
	"github.com/claytoncasey01/conn/internal/settings"
)

// The New session route through a server: the handler over a New session API whose opener only
// counts what it was asked to open, over a real clone folder.

const (
	newSessionPath = "/api/sessions/new"
	newSessionID   = "0b6c4f7e-2d1a-4c3b-9e8f-112233445566"
	// startBody is a start the API accepts.
	startBody = `{"repo":"acme/widgets","name":"fix it"}`
)

// newSessionRig is a server answering /api/sessions/new, and the sessions it opened.
type newSessionRig struct {
	handler *server.Server
	mu      sync.Mutex
	opened  []pty.Spec
}

func newNewSessionRig(t *testing.T) *newSessionRig {
	t.Helper()

	base := t.TempDir()

	// Private whatever the umask: a folder another user could write would rightly be refused.
	err := os.Chmod(base, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	clone := filepath.Join(base, "widgets")

	err = os.MkdirAll(filepath.Join(clone, ".git"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	current := settings.Defaults()
	current.Repos.Allow = []string{"acme/widgets"}
	rig := &newSessionRig{handler: nil, mu: sync.Mutex{}, opened: nil}
	api := newsession.New(newsession.Deps{
		Settings:     func() settings.Settings { return current },
		Repos:        func(settings.Settings) []string { return []string{"acme/widgets"} },
		CloneFor:     func(string, settings.Settings) (string, bool) { return clone, true },
		OriginOf:     func(string) string { return "acme/widgets" },
		CloneChecker: func() func(string) error { return checkClone }, Resolve: realpath.Resolve,
		Open: func(_ context.Context, spec pty.Spec) launch.Opened {
			rig.mu.Lock()
			defer rig.mu.Unlock()

			rig.opened = append(rig.opened, spec)

			return launch.Opened{OK: true}
		},
		Claude: "/opt/claude", Dry: false, Room: roomOf(0),
		NewID: func() (string, error) { return newSessionID, nil },
		Now:   time.Now, Cooldown: time.Minute,
	})
	rig.handler = &server.Server{
		UI: bundle(), Secret: testKey(t), Clients: func() int { return 0 }, NewSession: api,
	}

	return rig
}

func (r *newSessionRig) openedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.opened)
}

// newSessionCall is one request; a header set to "" is left off.
type newSessionCall struct {
	method, path, body string
	headers            map[string]string
}

// ownStart is a start as the page sends it.
func ownStart(body string) newSessionCall {
	return newSessionCall{
		method: http.MethodPost, path: newSessionPath, body: body,
		headers: map[string]string{
			"X-Conn": "1", "Origin": ownOrigin, "Content-Type": "application/json",
		},
	}
}

func (r *newSessionRig) send(t *testing.T, call newSessionCall) (int, string) {
	t.Helper()

	request := httptest.NewRequestWithContext(t.Context(), call.method, call.path,
		strings.NewReader(call.body))
	request.Host = "127.0.0.1:7777"
	request.Header.Set(secret.Header, testKey(t).Authorization())

	for name, value := range call.headers {
		switch {
		case name == "Host":
			request.Host = value
		case value == "":
			request.Header.Del(name)
		default:
			request.Header.Set(name, value)
		}
	}

	recorder := httptest.NewRecorder()
	r.handler.ServeHTTP(recorder, request)

	return recorder.Code, recorder.Body.String()
}

// without is call with header left off.
func without(call newSessionCall, header string) newSessionCall {
	headers := maps.Clone(call.headers)

	headers[header] = ""
	call.headers = headers

	return call
}

// with is call with header set to value.
func with(call newSessionCall, header, value string) newSessionCall {
	headers := maps.Clone(call.headers)

	headers[header] = value
	call.headers = headers

	return call
}

// TestNewSessionIsRefusedOutsideItsGuard: each request that is not the page's own start is
// refused with its code, and opens nothing.
func TestNewSessionIsRefusedOutsideItsGuard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		call     newSessionCall
		wantCode int
	}{
		{"no secret", without(ownStart(startBody), secret.Header), http.StatusUnauthorized},
		{
			"a wrong secret", with(ownStart(startBody), secret.Header, "Bearer nope"),
			http.StatusUnauthorized,
		},
		{"no Origin", without(ownStart(startBody), "Origin"), http.StatusForbidden},
		{
			"a foreign Origin", with(ownStart(startBody), "Origin", "http://evil.example"),
			http.StatusForbidden,
		},
		{
			"another port's Origin", with(ownStart(startBody), "Origin", "http://127.0.0.1:1"),
			http.StatusForbidden,
		},
		{"no X-Conn", without(ownStart(startBody), "X-Conn"), http.StatusForbidden},
		{"a wrong X-Conn", with(ownStart(startBody), "X-Conn", "0"), http.StatusForbidden},
		{
			"a foreign Host", with(ownStart(startBody), "Host", "evil.example"),
			http.StatusForbidden,
		},
		{"PUT", newSessionCall{
			method: http.MethodPut, path: newSessionPath, body: startBody,
			headers: ownStart("").headers,
		}, http.StatusMethodNotAllowed},
		{"DELETE", newSessionCall{
			method: http.MethodDelete, path: newSessionPath, body: "",
			headers: ownStart("").headers,
		}, http.StatusMethodNotAllowed},
		{"an oversized body", ownStart(`{"repo":"acme/widgets","name":"` +
			strings.Repeat("a", 64<<10) + `"}`), http.StatusRequestEntityTooLarge},
		{"a body that is not JSON", ownStart(`repo=acme/widgets`), http.StatusBadRequest},
		{"a body that is not an object", ownStart(`["acme/widgets"]`), http.StatusBadRequest},
		{
			"an extra field", ownStart(`{"repo":"acme/widgets","dir":"/"}`),
			http.StatusBadRequest,
		},
		{
			"a command", ownStart(`{"repo":"acme/widgets","argv":["/bin/sh"]}`),
			http.StatusBadRequest,
		},
		{
			"a bad name", ownStart(`{"repo":"acme/widgets","name":"a\u0007b"}`),
			http.StatusBadRequest,
		},
		{
			"a repository not allowed", ownStart(`{"repo":"evil/widgets"}`),
			http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newNewSessionRig(t)

			code, body := rig.send(t, tt.call)
			if code != tt.wantCode {
				t.Errorf("code %d, want %d: %s", code, tt.wantCode, body)
			}

			if rig.openedCount() != 0 {
				t.Errorf("opened %d sessions", rig.openedCount())
			}
		})
	}
}

func TestNewSessionStartsThePagesStart(t *testing.T) {
	t.Parallel()

	rig := newNewSessionRig(t)

	code, body := rig.send(t, ownStart(startBody))
	if code != http.StatusOK {
		t.Fatalf("code %d: %s", code, body)
	}

	if body != `{"ok":true}` {
		t.Errorf("body %s", body)
	}

	if rig.openedCount() != 1 {
		t.Errorf("opened %d sessions, want 1", rig.openedCount())
	}
}

func TestNewSessionListsItsChoices(t *testing.T) {
	t.Parallel()

	rig := newNewSessionRig(t)

	code, body := rig.send(t, newSessionCall{
		method: http.MethodGet, path: newSessionPath, body: "", headers: nil,
	})
	if code != http.StatusOK {
		t.Fatalf("code %d: %s", code, body)
	}

	if body != `{"repos":["acme/widgets"],"where":"inside","dry":false}` {
		t.Errorf("body %s", body)
	}
}

// TestNewSessionIsOnlyItsOwnPath: a path below it, or a trailing slash, is not the route.
func TestNewSessionIsOnlyItsOwnPath(t *testing.T) {
	t.Parallel()

	paths := []string{newSessionPath + "/", newSessionPath + "/x", "/api/sessions/news"}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			rig := newNewSessionRig(t)
			call := ownStart(startBody)
			call.path = path

			rig.send(t, call)

			if rig.openedCount() != 0 {
				t.Errorf("opened %d sessions", rig.openedCount())
			}
		})
	}
}

// checkClone is the app's clone check: newsession.CheckClone over safefolder.Check.
func checkClone(folder string) error {
	err := newsession.CheckClone(folder, safefolder.Check)
	if err != nil {
		return fmt.Errorf("checking the clone: %w", err)
	}

	return nil
}

// roomOf is a host with running sessions running, at most pty.DefaultLimits().SessionsMax.
func roomOf(running int) func() pty.Room {
	return func() pty.Room {
		return pty.Room{Running: running, SessionsMax: pty.DefaultLimits().SessionsMax}
	}
}

// TestNewSessionsListThatFailsIsA500: a GET whose handler panics answers 500, as every route does,
// and the server goes on.
func TestNewSessionsListThatFailsIsA500(t *testing.T) {
	t.Parallel()

	api := newsession.New(newsession.Deps{
		Settings: settings.Defaults,
		Repos:    func(settings.Settings) []string { panic("the clone scan broke") },
	})
	handler := &server.Server{
		UI: bundle(), Secret: testKey(t), Clients: func() int { return 0 }, NewSession: api,
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, newSessionPath, nil)
	request.Host = "127.0.0.1:7777"
	request.Header.Set(secret.Header, testKey(t).Authorization())

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("code %d: %s", recorder.Code, recorder.Body.String())
	}
}
