package claude_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/testtime"
)

const (
	initLine      = `{"type":"system","subtype":"init","mcp_servers":[]}`
	assistantLine = `{"type":"assistant","message":{"content":[` +
		`{"type":"tool_use","name":"Grep","input":{"pattern":"needle"}}]}}`
	resultLine = `{"type":"result","is_error":false,"result":"done",` +
		`"structured_output":{"ok":true}}`
)

// fake writes an executable shell script into a test folder and returns its path.
func fake(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "claude")

	err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	return path
}

// answering is a fake that records its arguments and input, then answers like the real binary.
func answering(t *testing.T) (string, string) {
	t.Helper()

	folder := t.TempDir()
	script := fake(t, `printf '%s\n' "$@" > "`+folder+`/argv.txt"
cat > "`+folder+`/stdin.txt"
echo '`+initLine+`'
echo '`+assistantLine+`'
echo '`+resultLine+`'`)

	return script, folder
}

func opts() claude.Opts {
	return claude.Opts{
		Prompt: "find the needle", Model: "haiku", Name: "parity",
		AllowedTools: []string{"Grep"}, Schema: `{"type":"object"}`,
		Timeout: testtime.Scaled(10 * time.Second),
	}
}

func TestRunAnswersAndReportsProgress(t *testing.T) {
	t.Parallel()

	script, folder := answering(t)

	var progress []string

	run := opts()
	run.OnProgress = func(line string) { progress = append(progress, line) }

	answer, err := claude.Runner{Bin: script}.Run(t.Context(), run)
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}

	if js.Stringify(answer.Structured) != `{"ok":true}` {
		t.Errorf("Structured = %s, want {\"ok\":true}", js.Stringify(answer.Structured))
	}

	if !slices.Equal(progress, []string{"Grep needle"}) {
		t.Errorf("progress = %q, want [Grep needle]", progress)
	}

	argv := readLines(t, filepath.Join(folder, "argv.txt"))
	if !slices.Equal(argv, claude.Args(run)) {
		t.Errorf("argv = %q, want %q", argv, claude.Args(run))
	}

	stdin := readText(t, filepath.Join(folder, "stdin.txt"))
	if stdin != "find the needle" {
		t.Errorf("stdin = %q, want the prompt", stdin)
	}
}

func TestRunFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		timeout time.Duration
		wantErr error
		wantMsg string
	}{
		{
			name: "an error result",
			body: "cat > /dev/null\necho '" + initLine + "'\n" +
				`echo '{"type":"result","is_error":true,"result":"the model said no"}'`,
			wantErr: claude.ErrReported,
			wantMsg: "the model said no",
		},
		{
			name:    "an exit with no result, with its error output",
			body:    "cat > /dev/null\necho 'not logged in' >&2\nexit 3",
			wantErr: claude.ErrNoResult,
			wantMsg: "claude exited 3 without a result: not logged in",
		},
		{
			name:    "a run past its time limit",
			body:    "exec sleep 30",
			timeout: 300 * time.Millisecond,
			wantErr: claude.ErrTimedOut,
			wantMsg: "timed out after 0s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			run := opts()
			if tt.timeout > 0 {
				run.Timeout = tt.timeout
			}

			_, err := claude.Runner{Bin: fake(t, tt.body)}.Run(t.Context(), run)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Run() error = %v, want %v", err, tt.wantErr)
			}

			if err.Error() != tt.wantMsg {
				t.Errorf("Run() error = %q, want %q", err.Error(), tt.wantMsg)
			}
		})
	}
}

func TestRunStopsWhenCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)

	_, err := claude.Runner{Bin: fake(t, "exec sleep 30")}.Run(ctx, opts())
	if !errors.Is(err, claude.ErrCancelled) {
		t.Fatalf("Run() error = %v, want %v", err, claude.ErrCancelled)
	}
}

func TestRunWithAMissingProgram(t *testing.T) {
	t.Parallel()

	bin := filepath.Join(t.TempDir(), "claude")

	_, err := claude.Runner{Bin: bin}.Run(t.Context(), opts())
	if err == nil {
		t.Fatal("Run() error = nil, want one")
	}

	want := "ENOENT: no such file or directory, posix_spawn '" + bin + "'"
	if err.Error() != want {
		t.Errorf("Run() error = %q, want %q", err.Error(), want)
	}
}

func TestRunWithAProgramNotOnThePath(t *testing.T) {
	t.Parallel()

	_, err := claude.Runner{Bin: "no-such-claude-for-conn"}.Run(t.Context(), opts())
	if err == nil {
		t.Fatal("Run() error = nil, want one")
	}

	want := `Executable not found in $PATH: "no-such-claude-for-conn"`
	if err.Error() != want {
		t.Errorf("Run() error = %q, want %q", err.Error(), want)
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func readLines(t *testing.T, path string) []string {
	t.Helper()

	return strings.Split(strings.TrimSuffix(readText(t, path), "\n"), "\n")
}
