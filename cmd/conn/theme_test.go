package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestTheThemeCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		code     int
		stdout   string
		inStderr string
	}{
		{
			// The test runs inside the checkout, so its themes/ folder is what is checked.
			name: "check with no argument judges every built-in", args: []string{"theme", "check"},
			code: exitOK, stdout: "crt: ok\ndefault: ok\nmatrix: ok\nneon: ok\n",
		},
		{
			name: "no theme command", args: []string{"theme"}, code: exitUsage,
			inStderr: "usage: conn theme",
		},
		{
			name: "an unknown theme command", args: []string{"theme", "nope"}, code: exitUsage,
			inStderr: `unknown theme command "nope"`,
		},
		{
			name: "index without options", args: []string{"theme", "index"}, code: exitUsage,
			inStderr: "usage: conn theme index",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			code := realMain(tt.args, lookupFrom(map[string]string{"CONN_DATA": t.TempDir()}),
				&stdout, false, &stderr)
			if code != tt.code {
				t.Errorf("code = %d, want %d: %s", code, tt.code, stderr.String())
			}

			if tt.stdout != "" {
				if stdout.String() != tt.stdout {
					t.Errorf("stdout = %q, want %q", stdout.String(), tt.stdout)
				}
			}

			if !strings.Contains(stderr.String(), tt.inStderr) {
				t.Errorf("stderr = %q, want it to hold %q", stderr.String(), tt.inStderr)
			}
		})
	}
}

func TestTheCheckoutsThemesAreFound(t *testing.T) {
	t.Parallel()

	dir, found := checkoutThemes()
	if !found {
		t.Fatal("the checkout's themes folder was not found")
	}

	if !strings.HasSuffix(dir, "/themes") {
		t.Errorf("dir = %s", dir)
	}
}
