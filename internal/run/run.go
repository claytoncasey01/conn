// Package run starts an outside program (gh, git, ps, cmux) with a time limit and a cap on what
// it may print, and describes how it ended in the words Conn shows: "Command failed: gh api user"
// with the program's own error output, "Executable not found in $PATH", "stdout maxBuffer length
// exceeded". A program is always given its arguments directly, never through a shell.
package run

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// outputBytesMaxDefault caps each of stdout and stderr when Opts sets no cap.
	outputBytesMaxDefault = 1 << 20

	// waitDelay is how long a stopped program's output pipes are read before they are abandoned.
	waitDelay = 2 * time.Second

	// codeNone is the exit code of a program that was killed or never started.
	codeNone = -1

	spawnNotFound = "ENOENT"
	signalTerm    = "SIGTERM"
)

// Opts tune one run. Zero values: no folder change, no time limit, a 1 MiB cap per stream, empty
// input, the server's environment.
type Opts struct {
	Dir            string
	Timeout        time.Duration
	OutputBytesMax int
	Stdin          string
	Env            []string
}

// Result is how a run ended.
type Result struct {
	Stdout string
	Stderr string
	// Code is the exit code; -1 when the program was killed, timed out or never started.
	Code int
	// Err is nil only for exit code 0.
	Err error
	// Spawn is "ENOENT" when the program was not found, another reason when it never started, ""
	// otherwise.
	Spawn string
	// Killed is true when the time limit or the output cap stopped it.
	Killed bool
	// Signal is the signal that ended it ("SIGTERM"), or "".
	Signal string
	// Overflowed names the stream ("stdout" or "stderr") that passed its cap first, or "": the
	// output cap, told apart from the time limit, which also sets Killed.
	Overflowed string
	// OutputLeftOpen is true when the program exited cleanly but something it started kept its
	// output open past waitDelay (exec.ErrWaitDelay). What it printed before is in Stdout.
	OutputLeftOpen bool
}

// Error is a failed run, with the message the server shows.
type Error struct {
	Message string
	Code    int
	Spawn   string
	Killed  bool
}

// Error is the message Conn shows for the failed run.
func (e *Error) Error() string { return e.Message }

// ending is what is known once a program has stopped, before it is described.
type ending struct {
	// err is what exec.Cmd.Run returned.
	err error
	// limitHit is true when the caller's context (the time limit, or a cancel) ended first.
	limitHit bool
	// overflowed names the stream ("stdout" or "stderr") that passed its cap first, or "".
	overflowed string
	stdout     string
	stderr     string
}

// Cmd runs name with args and waits for it.
func Cmd(ctx context.Context, name string, args []string, opts Opts) Result {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	runCtx, stop := context.WithCancel(ctx)
	defer stop()

	bytesMax := opts.OutputBytesMax
	if bytesMax <= 0 {
		bytesMax = outputBytesMaxDefault
	}

	first := &firstOverflow{mu: sync.Mutex{}, stream: "", stop: stop}
	stdout := &capped{
		mu: sync.Mutex{}, text: strings.Builder{}, bytesMax: bytesMax,
		stream: "stdout", first: first,
	}
	stderr := &capped{
		mu: sync.Mutex{}, text: strings.Builder{}, bytesMax: bytesMax,
		stream: "stderr", first: first,
	}

	cmd := exec.CommandContext(runCtx, name, args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = waitDelay
	cmd.Dir = opts.Dir
	cmd.Env = opts.Env
	cmd.Stdin = strings.NewReader(opts.Stdin)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()

	return describe(name, args, ending{
		err:        err,
		limitHit:   ctx.Err() != nil,
		overflowed: first.name(),
		stdout:     stdout.String(),
		stderr:     stderr.String(),
	})
}

// describe turns how a program stopped into the Result Conn reports.
func describe(name string, args []string, end ending) Result {
	result := Result{
		Stdout: end.stdout, Stderr: end.stderr, Code: 0, Err: nil, Spawn: "",
		Killed: false, Signal: "", Overflowed: "", OutputLeftOpen: false,
	}

	// An overflow fails the run even when the program exited by itself before its signal landed.
	if end.overflowed != "" {
		result.Code = codeNone
		result.Killed = true
		result.Signal = signalTerm
		result.Overflowed = end.overflowed
		result.Err = &Error{
			Message: end.overflowed + " maxBuffer length exceeded", Code: codeNone,
			Spawn: "", Killed: true,
		}

		return result
	}

	if end.err == nil {
		return result
	}

	result.Code = codeNone

	if errors.Is(end.err, exec.ErrNotFound) {
		result.Spawn = spawnNotFound
		result.Err = &Error{
			Message: fmt.Sprintf("Executable not found in $PATH: %q", name),
			Code:    codeNone, Spawn: spawnNotFound, Killed: false,
		}

		return result
	}

	exitErr, isExit := errors.AsType[*exec.ExitError](end.err)
	if isExit {
		return failed(name, args, exited(result, exitErr, end.limitHit))
	}

	if !end.limitHit {
		result.OutputLeftOpen = errors.Is(end.err, exec.ErrWaitDelay)
		result.Spawn = end.err.Error()
		result.Err = &Error{
			Message: end.err.Error(), Code: codeNone, Spawn: result.Spawn,
			Killed: false,
		}

		return result
	}

	result.Killed = true
	result.Signal = signalTerm

	return failed(name, args, result)
}

// exited fills in how a program that ran ended: its exit code, or the signal that stopped it.
func exited(result Result, exitErr *exec.ExitError, limitHit bool) Result {
	status, isWaitStatus := exitErr.Sys().(syscall.WaitStatus)
	if !isWaitStatus {
		result.Code = exitErr.ExitCode()

		return result
	}

	if !status.Signaled() {
		result.Code = exitErr.ExitCode()

		return result
	}

	result.Signal = signalName(status.Signal())
	result.Killed = limitHit

	return result
}

// failed adds the "Command failed" error, with the program's error output.
func failed(name string, args []string, result Result) Result {
	message := "Command failed: " + strings.Join(append([]string{name}, args...), " ")
	if result.Stderr != "" {
		message += "\n" + result.Stderr
	}

	result.Err = &Error{Message: message, Code: result.Code, Spawn: "", Killed: result.Killed}

	return result
}

func signalName(signal syscall.Signal) string {
	if signal == syscall.SIGTERM {
		return signalTerm
	}

	if signal == syscall.SIGKILL {
		return "SIGKILL"
	}

	if signal == syscall.SIGINT {
		return "SIGINT"
	}

	if signal == syscall.SIGHUP {
		return "SIGHUP"
	}

	return fmt.Sprintf("SIG%d", int(signal))
}

// FirstLine is the first line of text.
func FirstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")

	return line
}

// firstOverflow records which stream passed its cap first, and stops the program when one does.
type firstOverflow struct {
	mu     sync.Mutex
	stream string
	stop   context.CancelFunc
}

func (f *firstOverflow) record(stream string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.stream != "" {
		return
	}

	f.stream = stream
	f.stop()
}

func (f *firstOverflow) name() string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.stream
}

// capped collects at most bytesMax bytes of one stream and records when more came.
type capped struct {
	mu       sync.Mutex
	text     strings.Builder
	bytesMax int
	stream   string
	first    *firstOverflow
}

// Write keeps what fits and drops the rest. It never fails, so the program is stopped by the
// signal, not by a broken pipe.
func (c *capped) Write(chunk []byte) (int, error) {
	c.mu.Lock()

	room := c.bytesMax - c.text.Len()
	if len(chunk) <= room {
		c.text.Write(chunk)
		c.mu.Unlock()

		return len(chunk), nil
	}

	c.text.Write(chunk[:max(room, 0)])
	c.mu.Unlock()

	c.first.record(c.stream)

	return len(chunk), nil
}

// String is what was kept of the stream.
func (c *capped) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.text.String()
}
