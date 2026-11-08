package main

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/desk"
	"github.com/claytoncasey01/conn/internal/instance"
	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// syncBuffer is a bytes.Buffer the server goroutine writes while the test reads.
type syncBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// A bytes.Buffer grows to fit and never fails a write.
	written, _ := b.buffer.Write(p)

	return written, nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buffer.String()
}

func lookupFrom(env map[string]string) config.LookupFunc {
	return func(key string) (string, bool) {
		value, ok := env[key]

		return value, ok
	}
}

func TestRealMainExitCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		want       int
		wantStdout string
		wantStderr string
	}{
		{"no command", nil, exitUsage, "", usage},
		{"help", []string{"help"}, exitOK, usage, ""},
		{"dash h", []string{"-h"}, exitOK, usage, ""},
		{"unknown", []string{"nope"}, exitUsage, "", "conn: unknown command \"nope\"\n\n" + usage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			got := realMain(tt.args, lookupFrom(nil), &stdout, false, &stderr)
			if got != tt.want {
				t.Errorf("exit %d, want %d", got, tt.want)
			}

			if stdout.String() != tt.wantStdout {
				t.Errorf("stdout %q", stdout.String())
			}

			if stderr.String() != tt.wantStderr {
				t.Errorf("stderr %q", stderr.String())
			}
		})
	}
}

func TestServeAnswersUntilStopped(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	var stdout, stderr syncBuffer

	env := map[string]string{
		"CONN_PORT": "0", "CONN_DIST": t.TempDir(), "CONN_DATA": t.TempDir(),
	}
	exited := make(chan int, 1)

	go func() { exited <- serve(ctx, nil, lookupFrom(env), &stdout, true, &stderr) }()

	page := waitForPage(t, &stdout)

	status := statusWith(ctx, t, page.address+"/api/health", secret.Secret{})
	if status != http.StatusUnauthorized {
		t.Fatalf("health status without the secret %d, want 401", status)
	}

	status = statusWith(ctx, t, page.address+"/api/health", page.key)
	if status != http.StatusOK {
		t.Fatalf("health status with the secret %d", status)
	}

	cancel()

	if code := <-exited; code != exitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
}

// statusWith is the status of a GET of url, carrying key when it is not zero.
func statusWith(ctx context.Context, t *testing.T, url string, key secret.Secret) int {
	t.Helper()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !key.IsZero() {
		request.Header.Set(secret.Header, key.Authorization())
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	_ = response.Body.Close()

	return response.StatusCode
}

// TestServeShowsTheSecretOnlyToATerminal: on a terminal serve prints the page's address with the
// secret in its fragment; anywhere else (a pipe, a file, a log) it prints the address alone and
// names the lock file the secret is in.
func TestServeShowsTheSecretOnlyToATerminal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		terminal bool
	}{
		{"a terminal", true},
		{"a pipe or a file", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())

			var stdout, stderr syncBuffer

			dataDir := t.TempDir()
			env := map[string]string{
				"CONN_PORT": "0", "CONN_DIST": t.TempDir(), "CONN_DATA": dataDir,
			}
			exited := make(chan int, 1)

			go func() {
				exited <- serve(ctx, nil, lookupFrom(env), &stdout, tt.terminal, &stderr)
			}()

			address := waitForAddress(t, &stdout)

			holder, _, err := instance.Running(dataDir)
			if err != nil {
				t.Fatal(err)
			}

			cancel()
			<-exited

			checkShown(t, stdout.String(), stderr.String(), tt.terminal,
				shown{address: address, key: holder.Secret, dataDir: dataDir})
		})
	}
}

// shown is what serve was to say: where it listens, its secret and its data folder.
type shown struct {
	address string
	key     secret.Secret
	dataDir string
}

// checkShown fails the test unless out is what serve says on a terminal or elsewhere, and
// errors never holds the secret.
func checkShown(t *testing.T, out, errors string, terminal bool, want shown) {
	t.Helper()

	address, _, _ := strings.Cut(want.address, "/#")

	expected := "conn listening on " + desk.PageURL(address, want.key) + "\n"
	if !terminal {
		expected = "conn listening on " + address + "\n" + lockLine(address, want.dataDir)
	}

	if out != expected {
		t.Errorf("stdout %q, want %q", out, expected)
	}

	if strings.Contains(errors, want.key.String()) {
		t.Error("stderr holds the secret")
	}
}

// lockLine is the line that says where the secret is when it is not printed.
func lockLine(address, dataDir string) string {
	return "conn: open " + address + "/#k=<secret>, with the \"secret\" in " +
		dataDir + "/" + instance.FileName + "\n"
}

func TestServeRefusesABadPort(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	code := serve(t.Context(), nil, lookupFrom(map[string]string{"CONN_PORT": "x"}), &stdout, false,
		&stderr)
	if code != exitError {
		t.Fatalf("exit %d", code)
	}

	if stderr.String() != "conn: CONN_PORT is not a port: \"x\"\n" {
		t.Fatalf("stderr %q", stderr.String())
	}
}

// waitForAddress reads the address on the "conn listening on" line serve prints.
func waitForAddress(t *testing.T, stdout *syncBuffer) string {
	t.Helper()

	const prefix = "conn listening on "

	deadline := time.Now().Add(testtime.Scaled(5 * time.Second))
	for time.Now().Before(deadline) {
		rest, found := strings.CutPrefix(stdout.String(), prefix)
		if found {
			line, _, _ := strings.Cut(rest, "\n")

			return strings.TrimSpace(line)
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("the server never said where it listens")

	return ""
}

// served is a running serve's address and the secret its page carries.
type served struct {
	address string
	key     secret.Secret
}

// waitForPage reads the page address serve prints on a terminal: the address, and the secret in
// its fragment.
func waitForPage(t *testing.T, stdout *syncBuffer) served {
	t.Helper()

	address, text, found := strings.Cut(waitForAddress(t, stdout), "/#k=")
	if !found {
		t.Fatal("the page address has no secret")
	}

	key, err := secret.Parse(text)
	if err != nil {
		t.Fatal(err)
	}

	return served{address: address, key: key}
}
