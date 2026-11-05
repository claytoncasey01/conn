package run_test

import (
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/run"
	"github.com/claytoncasey01/conn/internal/testtime"
)

func TestHowARunEnds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		program string
		args    []string
		opts    run.Opts
		want    run.Result
		wantErr string
	}{
		{
			name: "success", program: "sh", args: []string{"-c", "printf hi; printf warn >&2"},
			want: run.Result{Stdout: "hi", Stderr: "warn", Code: 0},
		},
		{
			name: "not found", program: "nosuchbin-x", args: []string{"a"},
			want:    run.Result{Code: -1, Spawn: "ENOENT"},
			wantErr: `Executable not found in $PATH: "nosuchbin-x"`,
		},
		{
			name: "exit code with stderr", program: "sh",
			args:    []string{"-c", "echo oops >&2; exit 3"},
			want:    run.Result{Stderr: "oops\n", Code: 3},
			wantErr: "Command failed: sh -c echo oops >&2; exit 3\noops\n",
		},
		{
			name: "exit code alone", program: "sh", args: []string{"-c", "exit 3"},
			want:    run.Result{Code: 3},
			wantErr: "Command failed: sh -c exit 3",
		},
		{
			name: "time limit", program: "sleep", args: []string{"5"},
			opts:    run.Opts{Timeout: 200 * time.Millisecond},
			want:    run.Result{Code: -1, Killed: true, Signal: "SIGTERM"},
			wantErr: "Command failed: sleep 5",
		},
		{
			name: "output cap", program: "sh", args: []string{"-c", "yes | head -c 5000"},
			opts: run.Opts{OutputBytesMax: 100},
			want: run.Result{
				Stdout: strings.Repeat("y\n", 50), Code: -1, Killed: true, Signal: "SIGTERM",
				Overflowed: "stdout",
			},
			wantErr: "stdout maxBuffer length exceeded",
		},
		{
			// The program exits 0 by itself before the stop signal can land: still a failure.
			name: "output cap then a clean exit", program: "sh",
			args: []string{"-c", "head -c 5000 /dev/zero | tr '\\0' z; exit 0"},
			opts: run.Opts{OutputBytesMax: 100},
			want: run.Result{
				Stdout: strings.Repeat("z", 100), Code: -1, Killed: true, Signal: "SIGTERM",
				Overflowed: "stdout",
			},
			wantErr: "stdout maxBuffer length exceeded",
		},
		{
			name: "error output cap", program: "sh", args: []string{"-c", "yes >&2"},
			opts: run.Opts{OutputBytesMax: 10},
			want: run.Result{
				Stderr: strings.Repeat("y\n", 5), Code: -1, Killed: true, Signal: "SIGTERM",
				Overflowed: "stderr",
			},
			wantErr: "stderr maxBuffer length exceeded",
		},
		{
			// A background job keeps the output open after the shell exits 0: what was printed
			// is kept, and the run says the output was left open.
			name: "output left open by a background job", program: "sh",
			args: []string{"-c", "sleep 5 & printf done"},
			want: run.Result{
				Stdout: "done", Code: -1, Spawn: "exec: WaitDelay expired before I/O complete",
				OutputLeftOpen: true,
			},
			wantErr: "exec: WaitDelay expired before I/O complete",
		},
		{
			name: "input", program: "cat",
			opts: run.Opts{Stdin: "fed in"},
			want: run.Result{Stdout: "fed in"},
		},
		{
			name: "folder", program: "pwd",
			opts: run.Opts{Dir: "/"},
			want: run.Result{Stdout: "/\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := run.Cmd(t.Context(), tt.program, tt.args, tt.opts)
			checkError(t, got.Err, tt.wantErr)

			got.Err = nil
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func checkError(t *testing.T, err error, want string) {
	t.Helper()

	if want == "" {
		if err != nil {
			t.Fatalf("error %v", err)
		}

		return
	}

	if err == nil {
		t.Fatalf("no error, want %q", want)
	}

	if err.Error() != want {
		t.Fatalf("error %q, want %q", err, want)
	}
}

func TestATimedOutRunStopsPromptly(t *testing.T) {
	t.Parallel()

	start := time.Now()

	run.Cmd(t.Context(), "sleep", []string{"5"}, run.Opts{Timeout: 100 * time.Millisecond})

	if elapsed := time.Since(start); elapsed > testtime.Scaled(3*time.Second) {
		t.Fatalf("took %v", elapsed)
	}
}

func TestFirstLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text, want string
	}{
		{"one\ntwo", "one"},
		{"one", "one"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			if got := run.FirstLine(tt.text); got != tt.want {
				t.Fatalf("got %q", got)
			}
		})
	}
}
