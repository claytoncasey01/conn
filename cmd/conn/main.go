// Command conn is Conn: a local dashboard for Claude Code.
//
//	conn serve    run the dashboard server on 127.0.0.1:$CONN_PORT (default 7777) for a browser
//	conn stop     end the conn serve running on this data folder
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/claytoncasey01/conn"
	"github.com/claytoncasey01/conn/internal/app"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/desk"
	"github.com/claytoncasey01/conn/internal/instance"
	"github.com/claytoncasey01/conn/internal/secret"
)

// Exit codes. 2 is a usage error, as in the flag package.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const usage = `usage: conn <command>

commands:
  serve    run the dashboard server on 127.0.0.1:$CONN_PORT (default 7777) for a browser
  stop     end the conn serve running on this data folder ($CONN_DATA, default ~/.config/conn)
  theme    check theme folders, or build the theme gallery's index
  version  print the version of this build
`

// main is the only place that touches the process: its arguments, environment, output streams
// and exit code, and whether stdout is a terminal. Everything below takes them as arguments, so
// it can be tested.
func main() {
	os.Exit(realMain(os.Args[1:], os.LookupEnv, os.Stdout, terminal(os.Stdout), os.Stderr))
}

// terminal says whether file is a character device: a terminal, or a sink such as /dev/null
// that keeps nothing. A pipe, a file, a socket or a log is not, so the page's secret, which is
// printed only to a terminal, is never written where it would be kept or read by another program.
func terminal(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}

// realMain runs the command args name and returns its exit code. No command, or one it does not
// know, is a usage error. stdoutTerminal says whether stdout is a terminal.
func realMain(
	args []string, lookup config.LookupFunc, stdout io.Writer, stdoutTerminal bool,
	stderr io.Writer,
) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)

		return exitUsage
	}

	switch args[0] {
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		return serve(ctx, lookup, stdout, stdoutTerminal, stderr)
	case "stop":
		return stop(context.Background(), lookup, stdout, stderr)
	case "theme":
		return themeCommand(args[1:], lookup, stdout, stderr)
	case "version", "-v", "--version":
		_, _ = fmt.Fprintf(stdout, "conn %s\n", conn.Version())

		return exitOK
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(stdout, usage)

		return exitOK
	}

	_, _ = fmt.Fprintf(stderr, "conn: unknown command %q\n\n%s", args[0], usage)

	return exitUsage
}

// serve runs the dashboard server until ctx ends, then lets open requests finish. Another Conn
// already running on the data folder is not an error: serve says where it is and exits. Where it
// says the page is follows sayWhere: with the secret only on a terminal.
func serve(
	ctx context.Context, lookup config.LookupFunc, stdout io.Writer, stdoutTerminal bool,
	stderr io.Writer,
) int {
	dataDir := config.FromEnv(lookup).DataDir
	where := place{stdout: stdout, terminal: stdoutTerminal, dataDir: dataDir}

	running, err := app.Start(ctx, lookup, stderr)
	if errors.Is(err, instance.ErrHeld) {
		return alreadyRunning(ctx, where, stderr)
	}

	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn: %v\n", err)

		return exitError
	}

	sayWhere(where, "conn listening on", running.URL, running.Secret)

	// Done follows ctx within the server's shutdown timeout, and comes after the folder is let
	// go, so the process never exits while the server still holds it.
	<-running.Done()

	return serveEnded(running.Err(), stderr)
}

// alreadyRunning says where the Conn holding the data folder answers.
func alreadyRunning(ctx context.Context, where place, stderr io.Writer) int {
	holder, err := app.WaitForHolder(ctx, where.dataDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn: %v\n", err)

		return exitError
	}

	sayWhere(where, "conn is already running on", holder.URL, holder.Secret)

	return exitOK
}

// place is where serve says where the page is: stdout, whether it is a terminal, and the data
// folder whose lock file holds the secret.
type place struct {
	stdout   io.Writer
	terminal bool
	dataDir  string
}

// sayWhere prints said and the address of the page of the Conn at url. On a terminal that is the
// page with key in its fragment, ready to open. Anywhere else, where the line could be kept or
// read by another program, it is url alone and a line naming the lock file the secret is in.
func sayWhere(where place, said, url string, key secret.Secret) {
	if where.terminal {
		_, _ = fmt.Fprintf(where.stdout, "%s %s\n", said, desk.PageURL(url, key))

		return
	}

	_, _ = fmt.Fprintf(where.stdout, "%s %s\n", said, url)

	if key.IsZero() {
		return
	}

	_, _ = fmt.Fprintf(where.stdout, "conn: open %s/#k=<secret>, with the \"secret\" in %s\n",
		url, filepath.Join(where.dataDir, instance.FileName))
}

// serveEnded is the exit code for a server that has stopped: nil is a clean shutdown.
func serveEnded(err error, stderr io.Writer) int {
	if err == nil {
		return exitOK
	}

	_, _ = fmt.Fprintf(stderr, "conn: %v\n", err)

	return exitError
}
