package sessions

import (
	"context"
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/run"
)

const (
	// procsTimeout bounds the one ps call for the whole machine.
	procsTimeout = 10 * time.Second
	procsBytes   = 16 << 20
	// envTimeout bounds `ps eww` of one process.
	envTimeout = 3 * time.Second
	envBytes   = 4 << 20
	// prViewBytes is execFile's default cap on gh's output.
	prViewBytes = 1 << 20
)

// Procs is `ps -A -o pid=,ppid=,comm=`: every process, its parent and its executable.
func Procs() (string, error) {
	result := run.Cmd(context.Background(), "ps", []string{"-A", "-o", "pid=,ppid=,comm="},
		run.Opts{Dir: "", Timeout: procsTimeout, OutputBytesMax: procsBytes, Stdin: "", Env: nil})

	return result.Stdout, result.Err
}

// Env is a process's environment as `ps eww` prints it after the command line, without the
// command line: what a command line holds (a prompt, a ticket's text) is never read as its
// environment.
func Env(pid float64) (string, error) {
	opts := run.Opts{Dir: "", Timeout: envTimeout, OutputBytesMax: envBytes, Stdin: "", Env: nil}

	command := run.Cmd(context.Background(), "ps",
		[]string{"-ww", "-o", "command=", "-p", js.Number(pid)}, opts)
	if command.Err != nil {
		return "", command.Err
	}

	withEnvironment := run.Cmd(context.Background(), "ps",
		[]string{"eww", "-o", "command=", "-p", js.Number(pid)}, opts)
	if withEnvironment.Err != nil {
		return "", withEnvironment.Err
	}

	return environmentPart(withEnvironment.Stdout, command.Stdout), nil
}

// environmentPart is what `ps eww -o command=` printed after the command line `ps -o command=`
// printed, or "" when the two do not start alike (the process changed between them).
func environmentPart(withEnvironment, command string) string {
	command = strings.TrimRight(command, "\n")
	if command == "" {
		return ""
	}

	environment, found := strings.CutPrefix(withEnvironment, command)
	if !found {
		return ""
	}

	return environment
}

// PRView runs gh in a folder and gives what it printed.
func PRView(ctx context.Context, cwd string, args []string) ([]byte, error) {
	result := run.Cmd(ctx, "gh", args, run.Opts{
		Dir: cwd, Timeout: 0, OutputBytesMax: prViewBytes, Stdin: "", Env: nil,
	})

	return []byte(result.Stdout), result.Err
}

const (
	// cmuxTimeout bounds one cmux call.
	cmuxTimeout = 10 * time.Second
	cmuxBytes   = 1 << 20
	// statTimeout bounds `ps -o stat=` of one process.
	statTimeout = 5 * time.Second
)

// Cmux runs the cmux program bin.
func Cmux(bin string) func(ctx context.Context, args []string) run.Result {
	return func(ctx context.Context, args []string) run.Result {
		return run.Cmd(ctx, bin, args, run.Opts{
			Dir: "", Timeout: cmuxTimeout, OutputBytesMax: cmuxBytes, Stdin: "", Env: nil,
		})
	}
}

// Stat is `ps -o stat= -p <pid>`: the process's state letters.
func Stat(ctx context.Context, pid float64) run.Result {
	return run.Cmd(ctx, "ps", []string{"-o", "stat=", "-p", js.Number(pid)}, run.Opts{
		Dir: "", Timeout: statTimeout, OutputBytesMax: cmuxBytes, Stdin: "", Env: nil,
	})
}

// Kill sends SIGTERM to a process.
func Kill(pid float64) error {
	err := syscall.Kill(int(pid), syscall.SIGTERM)
	if err != nil {
		return fmt.Errorf("kill: %w", err)
	}

	return nil
}
