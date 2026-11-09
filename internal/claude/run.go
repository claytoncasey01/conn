package claude

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
)

const (
	// maxLineBytes is the longest output line read. A result line carries the whole answer, so
	// the limit is generous; a longer line is skipped and the reading goes on.
	maxLineBytes = 16 << 20

	// maxStderrBytes is how much error output is kept: a failure shows only its start.
	maxStderrBytes = 64 << 10

	// waitDelay is how long the output pipes are read after the run stops: a connector's server
	// the run started can keep them open after claude itself has exited.
	waitDelay = 5 * time.Second

	exitUnknown = "null"
)

// Runner starts claude runs.
type Runner struct {
	// Bin is the claude program: "claude", or CONN_CLAUDE.
	Bin string
}

// spawnError is a run that never started, and the words that say why.
type spawnError struct {
	message string
	err     error
}

func (e spawnError) Error() string { return e.message }

func (e spawnError) Unwrap() error { return e.err }

// Run starts one run, gives it the prompt on stdin and waits for its answer. The callbacks in
// opts are called while it runs, one at a time, from a goroutine of the run's own.
func (r Runner) Run(ctx context.Context, opts Opts) (Answer, error) {
	timeout := opts.timeout()

	runCtx, cancel := context.WithTimeoutCause(ctx, timeout, ErrTimedOut)
	defer cancel()

	reading := &stream{opts: opts, final: nil}
	stdout := &lineWriter{mu: sync.Mutex{}, pending: nil, skipping: false, onLine: reading.read}
	stderr := &headWriter{mu: sync.Mutex{}, text: strings.Builder{}}

	cmd := exec.CommandContext(runCtx, r.Bin, Args(opts)...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = waitDelay
	cmd.Dir = opts.Dir
	cmd.Stdin = strings.NewReader(opts.Prompt)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Start()
	if err != nil {
		return Answer{}, spawnFailure(r.Bin, err)
	}

	// Wait's error is not needed: a run is judged by its result line, and its exit code is
	// read from ProcessState.
	_ = cmd.Wait()

	stdout.flush()

	if runCtx.Err() != nil {
		return Answer{}, stopped(runCtx, timeout)
	}

	return outcome(reading.final, opts, exitCode(cmd), stderr.String())
}

// stopped is the error of a run its time limit or its caller stopped.
func stopped(runCtx context.Context, timeout time.Duration) error {
	if errors.Is(context.Cause(runCtx), ErrTimedOut) {
		return fmt.Errorf("%w after %ds", ErrTimedOut, int(math.Round(timeout.Seconds())))
	}

	return ErrCancelled
}

// spawnFailure says why a run did not start: a name not found on PATH, or the error code and
// the path for a path that cannot be started.
func spawnFailure(bin string, err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return spawnError{message: fmt.Sprintf("Executable not found in $PATH: %q", bin), err: err}
	}

	if errors.Is(err, fs.ErrNotExist) {
		return spawnError{message: posixSpawn("ENOENT: no such file or directory", bin), err: err}
	}

	if errors.Is(err, fs.ErrPermission) {
		return spawnError{message: posixSpawn("EACCES: permission denied", bin), err: err}
	}

	return spawnError{message: err.Error(), err: err}
}

func posixSpawn(reason, bin string) string { return reason + ", posix_spawn '" + bin + "'" }

// exitCode is the run's exit code as Node reports it: "null" when a signal ended it.
func exitCode(cmd *exec.Cmd) string {
	state := cmd.ProcessState
	if state == nil {
		return exitUnknown
	}

	code := state.ExitCode()
	if code < 0 {
		return exitUnknown
	}

	return strconv.Itoa(code)
}

// lineWriter hands each complete line written to it to onLine. A line longer than maxLineBytes
// is dropped whole.
type lineWriter struct {
	mu       sync.Mutex
	pending  []byte
	skipping bool
	onLine   func(line []byte)
}

func (w *lineWriter) Write(chunk []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	rest := chunk
	for len(rest) > 0 {
		end := bytes.IndexByte(rest, '\n')
		if end < 0 {
			w.take(rest)

			break
		}

		w.take(rest[:end])
		w.end()

		rest = rest[end+1:]
	}

	return len(chunk), nil
}

// take adds part of a line, or drops the line once it is too long.
func (w *lineWriter) take(part []byte) {
	if w.skipping {
		return
	}

	if len(w.pending)+len(part) > maxLineBytes {
		w.pending = nil
		w.skipping = true

		return
	}

	w.pending = append(w.pending, part...)
}

// end finishes the current line.
func (w *lineWriter) end() {
	line := w.pending
	skipped := w.skipping

	w.pending = nil
	w.skipping = false

	if skipped {
		return
	}

	w.onLine(line)
}

// flush reads a last line the output did not end with a newline, unless it is only space.
func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if js.Trim(string(w.pending)) == "" {
		w.pending = nil
		w.skipping = false

		return
	}

	w.end()
}

// headWriter keeps the first maxStderrBytes bytes written to it.
type headWriter struct {
	mu   sync.Mutex
	text strings.Builder
}

func (w *headWriter) Write(chunk []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	room := maxStderrBytes - w.text.Len()
	if room > 0 {
		w.text.Write(chunk[:min(room, len(chunk))])
	}

	return len(chunk), nil
}

func (w *headWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.text.String()
}
