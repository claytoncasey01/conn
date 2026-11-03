// Command conn is Conn: a local dashboard for Claude Code.
//
//	conn serve    run the dashboard server on 127.0.0.1:$CONN_PORT (default 7777) for a browser
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/claytoncasey01/conn"
	"github.com/claytoncasey01/conn/internal/server"
)

// Exit codes. 2 is a usage error, as in the flag package.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const (
	portDefault = 7777
	portMax     = 65535
)

// The HTTP server's limits. writeTimeout is 0 on purpose: a page's live-update stream stays open
// for as long as the page does, and a write deadline would cut it.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 0
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 2 * time.Second

	// headerBytesMax is net/http's default (http.DefaultMaxHeaderBytes), set here so the limit is
	// a decision in this file rather than a default nobody looked at.
	headerBytesMax = 1 << 20
)

const usage = `usage: conn <command>

commands:
  serve    run the dashboard server on 127.0.0.1:$CONN_PORT (default 7777) for a browser
`

var errNotPort = errors.New("CONN_PORT is not a port")

// lookupFunc reads one environment variable: os.LookupEnv, or a map in a test.
type lookupFunc func(key string) (string, bool)

// main is the only place that touches the process: its arguments, environment, output streams
// and exit code. Everything below takes them as arguments, so it can be tested.
func main() {
	os.Exit(realMain(os.Args[1:], os.LookupEnv, os.Stdout, os.Stderr))
}

// realMain runs the command args name and returns its exit code. No command, or one it does not
// know, is a usage error.
func realMain(args []string, lookup lookupFunc, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)

		return exitUsage
	}

	switch args[0] {
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		return serve(ctx, lookup, stdout, stderr)
	case "help", "-h", "--help":
		_, _ = fmt.Fprint(stdout, usage)

		return exitOK
	}

	_, _ = fmt.Fprintf(stderr, "conn: unknown command %q\n\n%s", args[0], usage)

	return exitUsage
}

// serve runs the dashboard server until ctx ends, then lets open requests finish.
func serve(ctx context.Context, lookup lookupFunc, stdout, stderr io.Writer) int {
	portNumber, err := port(lookup)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn: %v\n", err)

		return exitError
	}

	var listenConfig net.ListenConfig

	address := net.JoinHostPort(server.Host, strconv.Itoa(portNumber))

	listener, err := listenConfig.Listen(ctx, "tcp", address)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn: %v\n", err)

		return exitError
	}

	httpServer := newHTTPServer(&server.Server{UI: ui(lookup), Clients: nil})
	served := make(chan error, 1)

	_, _ = fmt.Fprintf(stdout, "conn listening on http://%s\n", listener.Addr())

	go func() { served <- httpServer.Serve(listener) }()

	select {
	case err := <-served:
		return serveEnded(err, stderr)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	_ = httpServer.Shutdown(shutdownCtx)

	return exitOK
}

// serveEnded is the exit code for a server that stopped by itself.
func serveEnded(err error, stderr io.Writer) int {
	if errors.Is(err, http.ErrServerClosed) {
		return exitOK
	}

	_, _ = fmt.Fprintf(stderr, "conn: %v\n", err)

	return exitError
}

func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    headerBytesMax,
	}
}

// port is CONN_PORT, or 7777 when it is unset or blank.
func port(lookup lookupFunc) (int, error) {
	value, _ := lookup("CONN_PORT")
	value = strings.TrimSpace(value)

	if value == "" {
		return portDefault, nil
	}

	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", errNotPort, value)
	}

	if number < 0 {
		return 0, fmt.Errorf("%w: %q", errNotPort, value)
	}

	if number > portMax {
		return 0, fmt.Errorf("%w: %q", errNotPort, value)
	}

	return number, nil
}

// ui is the page bundle: the folder CONN_DIST names, else the one built into this binary.
func ui(lookup lookupFunc) fs.FS {
	folder, _ := lookup("CONN_DIST")
	folder = strings.TrimSpace(folder)

	if folder == "" {
		return conn.UI()
	}

	absolute, err := filepath.Abs(folder)
	if err == nil {
		folder = absolute
	}

	return os.DirFS(folder)
}
