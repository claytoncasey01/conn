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
	"syscall"

	"github.com/claytoncasey01/conn"
	"github.com/claytoncasey01/conn/internal/app"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/instance"
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
// and exit code. Everything below takes them as arguments, so it can be tested.
func main() {
	os.Exit(realMain(os.Args[1:], os.LookupEnv, os.Stdout, os.Stderr))
}

// realMain runs the command args name and returns its exit code. No command, or one it does not
// know, is a usage error.
func realMain(args []string, lookup config.LookupFunc, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)

		return exitUsage
	}

	switch args[0] {
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		return serve(ctx, lookup, stdout, stderr)
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
// already running on the data folder is not an error: serve says where it is and exits.
func serve(ctx context.Context, lookup config.LookupFunc, stdout, stderr io.Writer) int {
	running, err := app.Start(ctx, lookup, stderr)
	if errors.Is(err, instance.ErrHeld) {
		return alreadyRunning(ctx, config.FromEnv(lookup).DataDir, stdout, stderr)
	}

	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn: %v\n", err)

		return exitError
	}

	_, _ = fmt.Fprintf(stdout, "conn listening on %s\n", running.URL)

	// Done follows ctx within the server's shutdown timeout, and comes after the folder is let
	// go, so the process never exits while the server still holds it.
	<-running.Done()

	return serveEnded(running.Err(), stderr)
}

// alreadyRunning says where the Conn holding the data folder answers.
func alreadyRunning(ctx context.Context, dataDir string, stdout, stderr io.Writer) int {
	holder, err := app.WaitForHolder(ctx, dataDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn: %v\n", err)

		return exitError
	}

	_, _ = fmt.Fprintf(stdout, "conn is already running on %s\n", holder.URL)

	return exitOK
}

// serveEnded is the exit code for a server that has stopped: nil is a clean shutdown.
func serveEnded(err error, stderr io.Writer) int {
	if err == nil {
		return exitOK
	}

	_, _ = fmt.Fprintf(stderr, "conn: %v\n", err)

	return exitError
}
