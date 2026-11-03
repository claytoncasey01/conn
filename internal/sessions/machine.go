package sessions

import (
	"context"
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

// Env is `ps eww -p <pid>`: the process with its environment.
func Env(pid float64) (string, error) {
	result := run.Cmd(context.Background(), "ps", []string{"eww", "-p", js.Number(pid)},
		run.Opts{Dir: "", Timeout: envTimeout, OutputBytesMax: envBytes, Stdin: "", Env: nil})

	return result.Stdout, result.Err
}

// PRView runs gh in a folder and gives what it printed.
func PRView(ctx context.Context, cwd string, args []string) ([]byte, error) {
	result := run.Cmd(ctx, "gh", args, run.Opts{
		Dir: cwd, Timeout: 0, OutputBytesMax: prViewBytes, Stdin: "", Env: nil,
	})

	return []byte(result.Stdout), result.Err
}
