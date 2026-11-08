package app

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// serveWait bounds each wait of the serve tests.
const serveWait = 10 * time.Second

// The quit takes the record when it begins, while the server still answers: a session that ends
// while open requests drain ran at the quit, and stays recorded.
func TestTheQuitRecordsBeforeTheServerDrains(t *testing.T) {
	t.Parallel()

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	address := "http://" + listener.Addr().String()
	httpServer := newHTTPServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))

	// The first record is the quit's: one taken again at its end is no change.
	answered := make(chan bool, 1)

	var first sync.Once

	none := func() {}
	steps := quitSteps{
		record:         func() { first.Do(func() { answered <- answers(t, address) }) },
		closeTerminals: none, cancel: none, stopSessions: none, release: none,
	}

	ctx, cancel := context.WithCancel(t.Context())
	running := &Server{
		URL: address, Secret: secret.Secret{}, done: make(chan struct{}), err: nil,
		resumed: make(chan struct{}),
	}

	go running.serve(ctx, httpServer, listener, steps)

	cancel()

	select {
	case <-running.Done():
	case <-time.After(testtime.Scaled(serveWait)):
		t.Fatal("the server did not stop")
	}

	select {
	case still := <-answered:
		if !still {
			t.Error("the record was taken after the server stopped answering")
		}
	default:
		t.Error("the quit took no record")
	}
}

// answers is whether the server at address answers a request now.
func answers(t *testing.T, address string) bool {
	t.Helper()

	client := &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true}, CheckRedirect: nil, Jar: nil,
		Timeout: testtime.Scaled(serveWait),
	}

	request, err := http.NewRequestWithContext(context.WithoutCancel(t.Context()),
		http.MethodGet, address, nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.Do(request)
	if err != nil {
		return false
	}

	_ = response.Body.Close()

	return true
}

// A quit waits for the launch's resume to end, but no longer than its limit.
func TestWaitForResume(t *testing.T) {
	t.Parallel()

	ended := make(chan struct{})
	close(ended)

	// A wait that must not time out has a limit far past the test's; the one that does times out
	// at once. Neither is a wait the test makes, so neither is scaled.
	tests := []struct {
		name    string
		resumed chan struct{}
		timeout time.Duration
	}{
		{"a resume that ended", ended, time.Hour},
		{"a resume that does not end", make(chan struct{}), time.Nanosecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			waited := make(chan struct{})

			go func() {
				defer close(waited)

				waitForResume(tt.resumed, tt.timeout)
			}()

			select {
			case <-waited:
			case <-time.After(testtime.Scaled(serveWait)):
				t.Fatal("the quit kept waiting")
			}
		})
	}
}
