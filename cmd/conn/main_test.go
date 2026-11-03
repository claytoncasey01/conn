package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

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

func lookupFrom(env map[string]string) lookupFunc {
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

			got := realMain(tt.args, lookupFrom(nil), &stdout, &stderr)
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

func TestPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value   string
		set     bool
		want    int
		wantErr error
	}{
		{"", false, portDefault, nil},
		{"  ", true, portDefault, nil},
		{"8123", true, 8123, nil},
		{" 8123 ", true, 8123, nil},
		{"0", true, 0, nil},
		{"65535", true, 65535, nil},
		{"65536", true, 0, errNotPort},
		{"-1", true, 0, errNotPort},
		{"http", true, 0, errNotPort},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Parallel()

			env := map[string]string{}
			if tt.set {
				env["CONN_PORT"] = tt.value
			}

			got, err := port(lookupFrom(env))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error %v, want %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Fatalf("port %d, want %d", got, tt.want)
			}
		})
	}
}

func TestServeAnswersUntilStopped(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	var stdout, stderr syncBuffer

	env := map[string]string{"CONN_PORT": "0", "CONN_DIST": t.TempDir()}
	exited := make(chan int, 1)

	go func() { exited <- serve(ctx, lookupFrom(env), &stdout, &stderr) }()

	address := waitForAddress(t, &stdout)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"/api/health", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	_ = response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status %d", response.StatusCode)
	}

	cancel()

	if code := <-exited; code != exitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
}

func TestServeRefusesABadPort(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	code := serve(t.Context(), lookupFrom(map[string]string{"CONN_PORT": "x"}), &stdout, &stderr)
	if code != exitError {
		t.Fatalf("exit %d", code)
	}

	if stderr.String() != "conn: CONN_PORT is not a port: \"x\"\n" {
		t.Fatalf("stderr %q", stderr.String())
	}
}

// waitForAddress reads the "conn listening on" line serve prints.
func waitForAddress(t *testing.T, stdout *syncBuffer) string {
	t.Helper()

	const prefix = "conn listening on "

	deadline := time.Now().Add(testtime.Scaled(5 * time.Second))
	for time.Now().Before(deadline) {
		address, found := strings.CutPrefix(stdout.String(), prefix)
		if found {
			return strings.TrimSpace(address)
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("the server never said where it listens")

	return ""
}
