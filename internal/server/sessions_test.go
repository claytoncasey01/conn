package server_test

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/server"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/testtime"
)

var errNone = errors.New("none")

// emptyLoop is a session list over an empty home: no processes, no gh.
func emptyLoop(t *testing.T) *sessions.Loop {
	t.Helper()

	return emptyLoopWith(t, func(*sessions.Hooks) {})
}

// emptyLoopWith is emptyLoop with its hooks changed by change.
func emptyLoopWith(t *testing.T, change func(hooks *sessions.Hooks)) *sessions.Loop {
	t.Helper()

	never := func() bool { return false }
	deps := sessions.Deps{
		Home: t.TempDir(), DataDir: t.TempDir(), Kinds: sessions.KindRules{},
		Procs: func() (string, error) { return "", nil },
		Env:   func(float64) ([]string, error) { return nil, errNone },
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
	}
	change(&deps.Hooks)

	return sessions.NewLoop(deps)
}

// TestTheSnapshotSaysWhereSessionsOpen: the Sessions page words its empty state by it.
func TestTheSnapshotSaysWhereSessionsOpen(t *testing.T) {
	t.Parallel()

	loop := emptyLoopWith(t, func(hooks *sessions.Hooks) {
		hooks.Where = func() string { return "command" }
	})

	go loop.Run(t.Context())

	handler := &server.Server{
		UI: bundle(), Secret: testKey(t), Clients: loop.Hub().Count, Sessions: loop,
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/snapshot", nil)
	request.Host = "127.0.0.1"
	request.Header.Set(secret.Header, testKey(t).Authorization())
	handler.ServeHTTP(recorder, request)

	if !strings.HasSuffix(recorder.Body.String(), `"openWork":false,"where":"command"}`) {
		t.Errorf("snapshot %q", recorder.Body.String())
	}
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
		UI: bundle(), Secret: testKey(t), Clients: loop.Hub().Count, Settings: nil, Themes: nil,
		Gallery: nil, Logger: nil, Sessions: loop,
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

	request.Header.Set(secret.Header, testKey(t).Authorization())

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = response.Body.Close() }()

	reader := bufio.NewReader(response.Body)

	first, _ := reader.ReadString('\n')

	// The page is counted before its first byte is sent, so as soon as anything arrives.
	if loop.Hub().Count() != 1 {
		t.Errorf("clients = %d once the first line arrived, want 1", loop.Hub().Count())
	}

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
		UI: bundle(), Secret: testKey(t), Clients: loop.Hub().Count, Settings: nil, Themes: nil,
		Gallery: nil, Logger: nil, Sessions: loop,
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
		// A hosted session's id is looked up like a pid: one the host never issued is not found.
		{
			http.MethodGet, "/api/sessions/0123456789abcdef0123456789abcdef/agents/lead",
			http.StatusNotFound, notFound,
		},
		{
			http.MethodGet, "/api/sessions/0123456789abcdef0123456789abcde/agents/lead",
			http.StatusOK, shell,
		},
		{
			http.MethodGet, "/api/sessions/0123456789ABCDEF0123456789ABCDEF/agents/lead",
			http.StatusOK, shell,
		},
		{http.MethodGet, "/api/health", http.StatusOK, `{"ok":true,"clients":0}`},
	}

	for _, tt := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequestWithContext(t.Context(), tt.method, tt.target, nil)
		request.Host = "127.0.0.1"
		request.Header.Set(secret.Header, testKey(t).Authorization())
		request.Header.Set(secret.Header, testKey(t).Authorization())
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
	request.Header.Set(secret.Header, testKey(t).Authorization())
	handler.ServeHTTP(recorder, request)

	if !strings.HasPrefix(recorder.Body.String(), `{"at":`) {
		t.Errorf("snapshot %q", recorder.Body.String())
	}

	if !strings.HasSuffix(recorder.Body.String(),
		`"sessions":[],"runs":[],"history":null,"reviews":null,"setup":false,"openWork":false}`) {
		t.Errorf("snapshot %q", recorder.Body.String())
	}
}

// TestActionRoutesAreGuarded: the session actions take POST with Conn's header only, a body over
// 64 KB is a 413, and a body that is not JSON is read as {}.
func TestActionRoutesAreGuarded(t *testing.T) {
	t.Parallel()

	loop := emptyLoop(t)

	go loop.Run(t.Context())

	handler := &server.Server{
		UI: bundle(), Secret: testKey(t), Clients: loop.Hub().Count, Settings: nil, Themes: nil,
		Gallery: nil, Logger: nil, Sessions: loop,
	}

	forbidden := `{"error":"forbidden"}`
	tests := []struct {
		method, target, body string
		header               bool
		status               int
		want                 string
	}{
		{http.MethodPost, "/api/sessions/5/stop", "", false, http.StatusForbidden, forbidden},
		{http.MethodGet, "/api/sessions/5/stop", "", true, http.StatusForbidden, forbidden},
		{
			http.MethodPost, "/api/sessions/5/stop", "garbage", true, http.StatusNotFound,
			`{"error":"no such live session"}`,
		},
		{http.MethodPost, "/api/sessions/5/reply", "{}", false, http.StatusForbidden, forbidden},
		{http.MethodPut, "/api/sessions/5/answer", "{}", true, http.StatusForbidden, forbidden},
		{
			http.MethodPost, "/api/sessions/5/reply", strings.Repeat("x", 70_000), true,
			http.StatusRequestEntityTooLarge, `{"error":"the request body is too large"}`,
		},
		{
			http.MethodPost, "/api/sessions/5/reply", "not json", true, http.StatusNotFound,
			`{"error":"no such live session"}`,
		},
		{
			http.MethodPost, "/api/sessions/5/terminal", "[1]", true, http.StatusNotFound,
			`{"error":"no such live session"}`,
		},
		{http.MethodPost, "/api/sessions/x/terminal", "{}", true, http.StatusOK, shell},
		// A hosted session's id is answered as an unknown pid is when the host never issued it.
		{
			http.MethodPost, "/api/sessions/0123456789abcdef0123456789abcdef/reply", "{}", true,
			http.StatusNotFound, `{"error":"no such live session"}`,
		},
		{
			http.MethodPost, "/api/sessions/0123456789abcdef0123456789abcdef/stop", "{}", true,
			http.StatusNotFound, `{"error":"no such live session"}`,
		},
		{
			http.MethodPost, "/api/sessions/0123456789abcdef0123456789abcdef/answer", "{}", true,
			http.StatusNotFound, `{"error":"no such live session"}`,
		},
		{
			http.MethodPost, "/api/sessions/0123456789abcdef0123456789abcdef/terminal", "{}", true,
			http.StatusNotFound, `{"error":"no such live session"}`,
		},
		{http.MethodPost, "/api/sessions/abc/terminal", "{}", true, http.StatusOK, shell},
		// The stop route takes the same ids, and nothing else.
		{http.MethodPost, "/api/sessions/abc/stop", "{}", true, http.StatusOK, shell},
		{
			http.MethodPost, "/api/sessions/0123456789abcdef0123456789abcde/stop", "{}", true,
			http.StatusOK, shell,
		},
	}

	for _, tt := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequestWithContext(t.Context(), tt.method, tt.target,
			strings.NewReader(tt.body))
		request.Host = "127.0.0.1"
		request.Header.Set(secret.Header, testKey(t).Authorization())
		request.Header.Set(secret.Header, testKey(t).Authorization())

		if tt.header {
			request.Header.Set("X-Conn", "1")
		}

		handler.ServeHTTP(recorder, request)

		if recorder.Code != tt.status {
			t.Errorf("%s %s = %d", tt.method, tt.target, recorder.Code)
		}

		if recorder.Body.String() != tt.want {
			t.Errorf("%s %s body %q", tt.method, tt.target, recorder.Body.String())
		}
	}
}

// The events stream joins the page before it takes the first snapshot: a change that lands
// between the two is pushed to the page. The snapshot's own build records how many pages were
// listening as it was taken.
func TestTheEventsStreamJoinsBeforeItsFirstSnapshot(t *testing.T) {
	t.Parallel()

	var (
		mutex sync.Mutex
		seen  []int
		loop  *sessions.Loop
	)

	record := func(listening int) {
		mutex.Lock()
		defer mutex.Unlock()

		seen = append(seen, listening)
	}

	never := func() bool { return false }
	loop = sessions.NewLoop(sessions.Deps{
		Home: t.TempDir(), DataDir: t.TempDir(), Kinds: sessions.KindRules{},
		Procs: func() (string, error) { return "", nil },
		Env:   func(float64) ([]string, error) { return nil, errNone },
		PRView: func(context.Context, string, []string) ([]byte, error) {
			return nil, errNone
		},
		RepoOf:   func(string) string { return "" },
		Upstream: func(string, string) string { return "" },
		Hooks: sessions.Hooks{
			Runs: func() []sessions.Run { return []sessions.Run{} },
			History: func() any {
				record(loop.Hub().Count())

				return nil
			},
			Reviews: func() any { return nil }, MyPrs: nil,
			Setup: never, OpenWork: never, RepoVisible: func(string) bool { return true },
			GithubOn: never,
		},
		Now: time.Now,
	})

	go loop.Run(t.Context())

	handler := &server.Server{
		UI: bundle(), Secret: testKey(t), Clients: loop.Hub().Count, Settings: nil, Themes: nil,
		Sessions: loop,
	}
	test := httptest.NewServer(handler)

	defer test.Close()

	// A change, so the stream's first snapshot is built for it, not taken from the cache.
	loop.Bump()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		test.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}

	request.Header.Set(secret.Header, testKey(t).Authorization())

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = response.Body.Close() }()

	reader := bufio.NewReader(response.Body)
	_, _ = reader.ReadString('\n')
	_, _ = reader.ReadString('\n')

	mutex.Lock()
	defer mutex.Unlock()

	if len(seen) == 0 {
		t.Fatal("no snapshot was built")
	}

	if last := seen[len(seen)-1]; last != 1 {
		t.Fatalf("the first snapshot was built with %d pages listening, want 1 (seen %v)", last,
			seen)
	}
}
