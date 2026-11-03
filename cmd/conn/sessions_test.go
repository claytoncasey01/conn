package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// serveFor starts conn with an empty home and data folder and gives its address.
func serveFor(t *testing.T) (context.Context, string) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	var stdout, stderr syncBuffer

	env := map[string]string{
		"CONN_PORT": "0", "CONN_DIST": t.TempDir(), "CONN_DATA": t.TempDir(),
		"HOME": t.TempDir(),
	}
	exited := make(chan int, 1)

	go func() { exited <- serve(ctx, lookupFrom(env), &stdout, &stderr) }()

	t.Cleanup(func() {
		cancel()
		<-exited
	})

	return ctx, waitForAddress(t, &stdout)
}

func call(ctx context.Context, t *testing.T, method, url, body string) string {
	t.Helper()

	request, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	request.Header.Set("X-Conn", "1")
	request.Header.Set("Origin", "http://127.0.0.1")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = response.Body.Close() }()

	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

type snapshotFlags struct {
	Setup    bool  `json:"setup"`
	OpenWork bool  `json:"openWork"`
	Sessions []any `json:"sessions"`
}

func flags(ctx context.Context, t *testing.T, address string) snapshotFlags {
	t.Helper()

	var got snapshotFlags

	err := json.Unmarshal([]byte(call(ctx, t, http.MethodGet, address+"/api/snapshot", "")), &got)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

// TestSnapshotSetupFollowsTheAllowlist: with no settings file the snapshot says setup, and a PUT
// of the repos section ends it at once (a saved setting moves the snapshot's version).
func TestSnapshotSetupFollowsTheAllowlist(t *testing.T) {
	t.Parallel()

	ctx, address := serveFor(t)

	before := flags(ctx, t, address)
	if !before.Setup {
		t.Error("setup = false with no settings file")
	}

	if before.Sessions == nil {
		t.Error("sessions is not a list")
	}

	saved := call(ctx, t, http.MethodPut, address+"/api/settings/repos",
		`{"allow":["acme/*"]}`)
	if saved != `{"ok":true}` {
		t.Fatalf("PUT answered %s", saved)
	}

	if flags(ctx, t, address).Setup {
		t.Error("setup = true straight after the allowlist was saved")
	}
}
