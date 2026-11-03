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
	"io/fs"
	"log"
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
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/instance"
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
  stop     end the conn serve running on this data folder ($CONN_DATA, default ~/.conn)
  theme    check theme folders, or build the theme gallery's index
`

var errNotPort = errors.New("CONN_PORT is not a port")

// portTakenError is a port another program holds: not a Conn on this data folder, or the lock
// would have said so.
type portTakenError struct {
	port int
	err  error
}

func (e portTakenError) Error() string {
	return "port " + strconv.Itoa(e.port) +
		" is in use by another program: set CONN_PORT to a free port"
}

func (e portTakenError) Unwrap() error { return e.err }

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
	portNumber, err := port(lookup)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn: %v\n", err)

		return exitError
	}

	cfg := config.FromEnv(lookup)

	held, err := instance.Acquire(cfg.DataDir)
	if errors.Is(err, instance.ErrHeld) {
		return alreadyRunning(ctx, cfg.DataDir, stdout, stderr)
	}

	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn: %v\n", err)

		return exitError
	}

	defer func() { _ = held.Release() }()

	logger := log.New(stderr, "", 0)
	store := loadSettings(cfg.DataDir, logger, stderr)

	listener, err := listen(ctx, portNumber)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn: %v\n", err)

		return exitError
	}

	address := "http://" + listener.Addr().String()

	err = held.Publish(instance.Holder{PID: os.Getpid(), URL: address})
	if err != nil {
		_ = listener.Close()
		_, _ = fmt.Fprintf(stderr, "conn: %v\n", err)

		return exitError
	}

	_, _ = fmt.Fprintf(stdout, "conn listening on %s\n", address)

	handler := newHandler(ctx, cfg, store, logger, ui(lookup))

	return run(ctx, newHTTPServer(handler), listener, stderr)
}

// run serves on listener until ctx ends or the server stops by itself.
func run(
	ctx context.Context, httpServer *http.Server, listener net.Listener, stderr io.Writer,
) int {
	served := make(chan error, 1)

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

// listen opens the server's port on 127.0.0.1. A port another program holds says so.
func listen(ctx context.Context, portNumber int) (net.Listener, error) {
	var listenConfig net.ListenConfig

	address := net.JoinHostPort(server.Host, strconv.Itoa(portNumber))

	listener, err := listenConfig.Listen(ctx, "tcp", address)
	if errors.Is(err, syscall.EADDRINUSE) {
		return nil, portTakenError{port: portNumber, err: err}
	}

	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", address, err)
	}

	return listener, nil
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
func port(lookup config.LookupFunc) (int, error) {
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
func ui(lookup config.LookupFunc) fs.FS {
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
