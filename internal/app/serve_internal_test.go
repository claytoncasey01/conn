package app

import (
	"context"
	"net"
	"net/http"
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

	answered := make(chan bool, 1)
	record := func() { answered <- answers(t, address) }

	ctx, cancel := context.WithCancel(t.Context())
	running := &Server{URL: address, Secret: secret.Secret{}, done: make(chan struct{}), err: nil}

	go running.serve(ctx, httpServer, listener, record, func() {})

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
