package main

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// Bun's myPrsDry.test.ts: the server's snapshot says why My PRs reads nothing (setup, GitHub
// off), and a dry run with no settings file reads the seeded allowlist. No case here can reach
// the real gh: no page is open, so nothing polls, and the seeded run is dry throughout.

type myPrsFlags struct {
	MyPrs struct {
		Off     *string `json:"off"`
		Channel *string `json:"channel"`
		Rows    []any   `json:"rows"`
	} `json:"myPrs"`
}

func serveWith(t *testing.T, env map[string]string, settingsJSON string) (
	context.Context, string, string,
) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	data := t.TempDir()

	if settingsJSON != "" {
		err := os.WriteFile(filepath.Join(data, "settings.json"), []byte(settingsJSON), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	all := map[string]string{
		"CONN_PORT": "0", "CONN_DIST": t.TempDir(), "CONN_DATA": data, "HOME": t.TempDir(),
	}
	maps.Copy(all, env)

	var stdout, stderr syncBuffer

	exited := make(chan int, 1)

	go func() { exited <- serve(ctx, lookupFrom(all), &stdout, &stderr) }()

	t.Cleanup(func() {
		cancel()
		<-exited
	})

	return ctx, waitForAddress(t, &stdout), data
}

func myPrsOf(ctx context.Context, t *testing.T, address string) myPrsFlags {
	t.Helper()

	var got myPrsFlags

	err := json.Unmarshal([]byte(call(ctx, t, http.MethodGet, address+"/api/snapshot", "")), &got)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

func TestMyPrsInTheSnapshot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		env      map[string]string
		settings string
		wantOff  string
	}{
		{"no settings file is setup", map[string]string{"CONN_DRY_SEED": "0"}, "", "setup"},
		{
			"GitHub off", nil,
			`{"version":1,"repos":{"allow":["acme/*"]},"github":{"enabled":false}}`, "github",
		},
		{
			"a dry run reads the seeded allowlist",
			map[string]string{"CONN_MYPRS_DRY": "1", "CONN_DRY_SEED": "1"},
			"", "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, address, data := serveWith(t, tt.env, tt.settings)
			got := myPrsOf(ctx, t, address).MyPrs

			off := ""
			if got.Off != nil {
				off = *got.Off
			}

			if off != tt.wantOff {
				t.Errorf("off = %q, want %q", off, tt.wantOff)
			}

			if got.Rows == nil {
				t.Error("rows is not a list")
			}

			if tt.wantOff == "github" {
				if got.Channel != nil {
					t.Errorf("channel = %q with GitHub off", *got.Channel)
				}
			}

			if tt.wantOff == "" {
				_, err := os.Stat(filepath.Join(data, "settings.json"))
				if err == nil {
					t.Error("the dry seed wrote settings.json into the data folder")
				}
			}
		})
	}
}
