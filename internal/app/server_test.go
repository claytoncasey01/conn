package app_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/app"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/instance"
	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// stopWait bounds the wait for a started server to stop and let its folder go.
const stopWait = 3 * time.Second

// pollEvery is how often a test reads the lock file while it waits.
const pollEvery = 10 * time.Millisecond

func TestStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		check func(t *testing.T, env map[string]string)
	}{
		{"a free folder answers and publishes where", checkAnswersAndPublishes},
		{"a second start on the same folder is held", checkSecondIsHeld},
		{"a port another program holds is taken", checkPortTaken},
		{"an ended context stops it and lets the folder go", checkStopsOnCancel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.check(t, environment(t))
		})
	}
}

func checkAnswersAndPublishes(t *testing.T, env map[string]string) {
	t.Helper()

	server := start(t, env)

	status := statusOf(t, server.URL+"/")
	if status != http.StatusOK {
		t.Errorf("GET / = %d, want 200", status)
	}

	holder, running, err := instance.Running(env["CONN_DATA"])
	if err != nil {
		t.Fatal(err)
	}

	if !running {
		t.Fatal("the folder is not held while the server runs")
	}

	if holder.PID != os.Getpid() {
		t.Errorf("lock pid = %d, want %d", holder.PID, os.Getpid())
	}

	if holder.URL != server.URL {
		t.Errorf("lock url = %q, want %q", holder.URL, server.URL)
	}

	checkTheSecretIsAskedFor(t, server, holder)
	checkTheTerminalsAreServed(t, server)
}

// checkTheTerminalsAreServed checks that the terminal route is the hosted sessions' WebSocket,
// behind the secret: a request with the secret that is no handshake is asked to upgrade, and one
// without the secret is refused before that.
func checkTheTerminalsAreServed(t *testing.T, server *app.Server) {
	t.Helper()

	target := server.URL + "/api/term/0123456789abcdef0123456789abcdef"

	if status := statusOf(t, target); status != http.StatusUnauthorized {
		t.Errorf("GET a terminal without the secret = %d, want 401", status)
	}

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}

	request.Header.Set(secret.Header, server.Secret.Authorization())

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	_ = response.Body.Close()

	if response.StatusCode != http.StatusUpgradeRequired {
		t.Errorf("GET a terminal with the secret = %d, want 426", response.StatusCode)
	}
}

// checkTheSecretIsAskedFor checks that the API answers only with the secret the server published
// in the lock file.
func checkTheSecretIsAskedFor(t *testing.T, server *app.Server, holder instance.Holder) {
	t.Helper()

	if server.Secret.IsZero() {
		t.Fatal("the server has no secret")
	}

	if !holder.Secret.Matches(server.Secret.String()) {
		t.Fatal("the lock file holds another secret")
	}

	if status := statusOf(t, server.URL+"/api/health"); status != http.StatusUnauthorized {
		t.Errorf("GET /api/health without the secret = %d, want 401", status)
	}

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		server.URL+"/api/health", nil)
	if err != nil {
		t.Fatal(err)
	}

	request.Header.Set(secret.Header, holder.Secret.Authorization())

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	_ = response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Errorf("GET /api/health with the secret = %d, want 200", response.StatusCode)
	}
}

func checkSecondIsHeld(t *testing.T, env map[string]string) {
	t.Helper()

	start(t, env)

	second, err := app.Start(t.Context(), lookupFrom(env), io.Discard)
	if !errors.Is(err, instance.ErrHeld) {
		t.Errorf("second Start error = %v, want instance.ErrHeld", err)
	}

	if second != nil {
		t.Errorf("second Start gave a server on %s", second.URL)
	}
}

func checkPortTaken(t *testing.T, env map[string]string) {
	t.Helper()

	var listenConfig net.ListenConfig

	taken, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = taken.Close() })

	address, isTCP := taken.Addr().(*net.TCPAddr)
	if !isTCP {
		t.Fatalf("listening gave %T, want a TCP address", taken.Addr())
	}

	env["CONN_PORT"] = strconv.Itoa(address.Port)

	_, err = app.Start(t.Context(), lookupFrom(env), io.Discard)

	var portTaken app.PortTakenError
	if !errors.As(err, &portTaken) {
		t.Fatalf("Start error = %v, want a PortTakenError", err)
	}

	if portTaken.Port != address.Port {
		t.Errorf("PortTakenError.Port = %d, want %d", portTaken.Port, address.Port)
	}

	folderReleased(t, env["CONN_DATA"])
}

func checkStopsOnCancel(t *testing.T, env map[string]string) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	server, err := app.Start(ctx, lookupFrom(env), io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	cancel()

	select {
	case <-server.Done():
	case <-time.After(testtime.Scaled(stopWait)):
		t.Fatalf("Done did not close within %s of the context ending", stopWait)
	}

	if server.Err() != nil {
		t.Errorf("Err = %v after a clean shutdown, want nil", server.Err())
	}

	folderReleased(t, env["CONN_DATA"])
}

// environment is one Conn's: its own data folder, home and page bundle, on a port the system
// picks.
func environment(t *testing.T) map[string]string {
	t.Helper()

	dist := t.TempDir()

	err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<!doctype html>"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return map[string]string{
		"CONN_PORT": "0", "CONN_DATA": t.TempDir(), "HOME": t.TempDir(), "CONN_DIST": dist,
	}
}

// start starts a Conn on env, and stops it before the test's folders are removed.
func start(t *testing.T, env map[string]string) *app.Server {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	server, err := app.Start(ctx, lookupFrom(env), io.Discard)
	if err != nil {
		cancel()
		t.Fatal(err)
	}

	t.Cleanup(func() {
		cancel()
		<-server.Done()
	})

	return server
}

func lookupFrom(env map[string]string) config.LookupFunc {
	return func(key string) (string, bool) {
		value, found := env[key]

		return value, found
	}
}

func statusOf(t *testing.T, url string) int {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	_ = response.Body.Close()

	return response.StatusCode
}

// folderReleased fails the test unless the data folder is free within stopWait. A lock lasts
// until every copy of its file descriptor is closed, and a process another parallel test is
// starting holds a copy between fork and exec, so the folder can read as held for a moment.
func folderReleased(t *testing.T, dataDir string) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(stopWait))

	for {
		holder, running, err := instance.Running(dataDir)
		if err != nil {
			t.Fatal(err)
		}

		if !running {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("the folder is still held by %+v", holder)
		}

		time.Sleep(pollEvery)
	}
}
