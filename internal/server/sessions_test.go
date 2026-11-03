package server_test

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/server"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/testtime"
)

var errNone = errors.New("none")

// emptyLoop is a session list over an empty home: no processes, no gh.
func emptyLoop(t *testing.T) *sessions.Loop {
	t.Helper()

	never := func() bool { return false }

	return sessions.NewLoop(sessions.Deps{
		Home: t.TempDir(), DataDir: t.TempDir(), Kinds: sessions.KindRules{},
		Procs: func() (string, error) { return "", nil },
		Env:   func(float64) (string, error) { return "", errNone },
		PRView: func(context.Context, string, []string) ([]byte, error) {
			return nil, errNone
		},
		RepoOf:   func(string) string { return "" },
		Upstream: func(string, string) string { return "" },
		Hooks: sessions.Hooks{
			Runs:    func() []sessions.Run { return []sessions.Run{} },
			History: func() any { return nil }, Reviews: func() any { return nil }, MyPrs: nil,
			Setup: never, OpenWork: never, RepoVisible: func(string) bool { return true },
			GithubOn: never,
		},
		Now: time.Now,
	})
}

// TestEventsStreamOutlivesTheReadTimeout: the first frame is the snapshot after a retry hint, a
// change reaches the stream after the server's ReadTimeout has passed, /api/health counts it,
// and stopping the session list ends it.
func TestEventsStreamOutlivesTheReadTimeout(t *testing.T) {
	t.Parallel()

	ctx, stop := context.WithCancel(t.Context())
	loop := emptyLoop(t)

	go loop.Run(ctx)

	handler := &server.Server{
		UI: bundle(), Clients: loop.Hub().Count, Settings: nil, Themes: nil, Gallery: nil,
		Logger: nil, Sessions: loop,
	}
	test := httptest.NewUnstartedServer(handler)
	test.Config.ReadTimeout = testtime.Scaled(200 * time.Millisecond)
	test.Start()

	defer test.Close()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		test.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = response.Body.Close() }()

	reader := bufio.NewReader(response.Body)

	first, _ := reader.ReadString('\n')
	if first != "retry: 1500\n" {
		t.Errorf("first line %q", first)
	}

	second, _ := reader.ReadString('\n')
	if !strings.HasPrefix(second, `data: {"at":`) {
		t.Errorf("second line %q", second)
	}

	if loop.Hub().Count() != 1 {
		t.Errorf("clients = %d", loop.Hub().Count())
	}

	time.Sleep(testtime.Scaled(500 * time.Millisecond))
	loop.Bump()

	if !nextFrame(t, reader) {
		t.Fatal("a change never reached the stream past the read timeout")
	}

	stop()

	_, err = reader.ReadString('\n')
	for err == nil {
		_, err = reader.ReadString('\n')
	}
}

// nextFrame reads until a data line.
func nextFrame(t *testing.T, reader *bufio.Reader) bool {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(5 * time.Second))
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			return false
		}

		if strings.HasPrefix(line, "data: ") {
			return true
		}
	}

	return false
}

func TestSessionRoutes(t *testing.T) {
	t.Parallel()

	loop := emptyLoop(t)

	go loop.Run(t.Context())

	handler := &server.Server{
		UI: bundle(), Clients: loop.Hub().Count, Settings: nil, Themes: nil, Gallery: nil,
		Logger: nil, Sessions: loop,
	}

	notFound := `{"error":"not found"}`
	tests := []struct {
		method, target string
		status         int
		body           string
	}{
		{http.MethodGet, "/api/sessions/123/agents/lead", http.StatusNotFound, notFound},
		{http.MethodPost, "/api/sessions/123/agents/lead", http.StatusNotFound, notFound},
		{http.MethodGet, "/api/sessions/abc/agents/lead", http.StatusOK, shell},
		{http.MethodGet, "/api/sessions/1/agents/a.b", http.StatusOK, shell},
		{http.MethodGet, "/api/health", http.StatusOK, `{"ok":true,"clients":0}`},
	}

	for _, tt := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequestWithContext(t.Context(), tt.method, tt.target, nil)
		request.Host = "127.0.0.1"
		handler.ServeHTTP(recorder, request)

		if recorder.Code != tt.status {
			t.Errorf("%s %s = %d", tt.method, tt.target, recorder.Code)
		}

		if recorder.Body.String() != tt.body {
			t.Errorf("%s %s body %q", tt.method, tt.target, recorder.Body.String())
		}
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/snapshot", nil)
	request.Host = "127.0.0.1"
	handler.ServeHTTP(recorder, request)

	if !strings.HasPrefix(recorder.Body.String(), `{"at":`) {
		t.Errorf("snapshot %q", recorder.Body.String())
	}

	if !strings.HasSuffix(recorder.Body.String(),
		`"sessions":[],"runs":[],"history":null,"reviews":null,"setup":false,"openWork":false}`) {
		t.Errorf("snapshot %q", recorder.Body.String())
	}
}
