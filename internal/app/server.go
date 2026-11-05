package app

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

const (
	portDefault = 7777
	portMax     = 65535
)

// ShutdownTimeout is how long a stopping server gives open requests to finish before it closes
// them. The app waits a little longer than this at quit, so it is exported.
const ShutdownTimeout = 2 * time.Second

// The HTTP server's limits. writeTimeout is 0 on purpose: a page's live-update stream stays open
// for as long as the page does, and a write deadline would cut it.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 0
	idleTimeout       = 60 * time.Second

	// headerBytesMax is net/http's default (http.DefaultMaxHeaderBytes), set here so the limit is
	// a decision in this file rather than a default nobody looked at.
	headerBytesMax = 1 << 20
)

var errNotPort = errors.New("CONN_PORT is not a port")

// PortTakenError is a port another program holds: not a Conn on this data folder, or the lock
// would have said so.
type PortTakenError struct {
	// Port is the port Conn was asked to listen on.
	Port int

	err error
}

func (e PortTakenError) Error() string {
	return "port " + strconv.Itoa(e.Port) +
		" is in use by another program: set CONN_PORT to a free port"
}

func (e PortTakenError) Unwrap() error { return e.err }

// Server is a Conn serving on a data folder, started by Start.
type Server struct {
	// URL is where it answers: http://127.0.0.1:PORT.
	URL string

	done chan struct{}
	err  error
}

// Done is closed after the server has shut down and let the data folder go.
func (s *Server) Done() <-chan struct{} { return s.done }

// Err is why the server ended: nil after a clean shutdown, and nil while it still runs.
func (s *Server) Err() error {
	select {
	case <-s.done:
		return s.err
	default:
		return nil
	}
}

// Start takes the data folder's lock, loads settings, listens on Port(lookup), publishes
// {PID, URL} in the lock and serves until ctx ends, then shuts down within ShutdownTimeout.
// It returns instance.ErrHeld (wrapped) when another Conn holds the folder, and a PortTakenError
// when another program holds the port.
func Start(ctx context.Context, lookup config.LookupFunc, stderr io.Writer) (*Server, error) {
	portNumber, err := Port(lookup)
	if err != nil {
		return nil, err
	}

	cfg := config.FromEnv(lookup)

	held, err := instance.Acquire(cfg.DataDir)
	if err != nil {
		return nil, err
	}

	logger := log.New(stderr, "", 0)
	store := loadSettings(cfg.DataDir, logger, stderr)

	cfg, removeSeed := seedDry(cfg, store, stderr)
	release := func() {
		removeSeed()

		_ = held.Release()
	}

	listener, address, err := listenAndPublish(ctx, held, portNumber)
	if err != nil {
		release()

		return nil, err
	}

	serveCtx, cancel := context.WithCancel(ctx)
	running := &Server{URL: address, done: make(chan struct{}), err: nil}
	handler := newHandler(serveCtx, cfg, store, logger, ui(lookup))

	go running.serve(serveCtx, newHTTPServer(handler), listener, func() {
		cancel()
		release()
	})

	return running, nil
}

// listenAndPublish opens the port and writes where it answers into the lock.
func listenAndPublish(
	ctx context.Context, held *instance.Lock, portNumber int,
) (net.Listener, string, error) {
	listener, err := listen(ctx, portNumber)
	if err != nil {
		return nil, "", err
	}

	address := "http://" + listener.Addr().String()

	err = held.Publish(instance.Holder{PID: os.Getpid(), URL: address})
	if err != nil {
		_ = listener.Close()

		return nil, "", err
	}

	return listener, address, nil
}

// serve serves on listener until ctx ends or the server stops by itself, then runs end and
// closes Done. A server that outlives ShutdownTimeout (a page's live-update stream) is closed.
func (s *Server) serve(
	ctx context.Context, httpServer *http.Server, listener net.Listener, end func(),
) {
	defer close(s.done)
	defer end()

	served := make(chan error, 1)

	go func() { served <- httpServer.Serve(listener) }()

	select {
	case err := <-served:
		_ = httpServer.Close()

		if !errors.Is(err, http.ErrServerClosed) {
			s.err = err
		}

		return
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownTimeout)
	defer cancel()

	err := httpServer.Shutdown(shutdownCtx)
	if err != nil {
		_ = httpServer.Close()
	}
}

// listen opens the server's port on 127.0.0.1. A port another program holds says so.
func listen(ctx context.Context, portNumber int) (net.Listener, error) {
	var listenConfig net.ListenConfig

	address := net.JoinHostPort(server.Host, strconv.Itoa(portNumber))

	listener, err := listenConfig.Listen(ctx, "tcp", address)
	if errors.Is(err, syscall.EADDRINUSE) {
		return nil, PortTakenError{Port: portNumber, err: err}
	}

	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", address, err)
	}

	return listener, nil
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

// Port is CONN_PORT, or 7777 when it is unset or blank. 0 lets the system pick a free port.
func Port(lookup config.LookupFunc) (int, error) {
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
