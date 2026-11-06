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
	"github.com/claytoncasey01/conn/internal/drysession"
	"github.com/claytoncasey01/conn/internal/instance"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/server"
)

const (
	portDefault = 7777
	portMax     = 65535
)

// ShutdownTimeout is how long a stopping server gives open requests to finish before it closes
// them.
const ShutdownTimeout = 2 * time.Second

// quitStopTimeout bounds the stop of the hosted sessions at quit: above the host's grace for a
// polite stop (5 s), after which every session still running is killed at once.
const quitStopTimeout = 7 * time.Second

// QuitTimeout is the most a stopping server takes to let its data folder go: its requests'
// shutdown, its terminals' close, then its hosted sessions' stop. Whatever waits for it to stop
// waits a little longer.
const QuitTimeout = ShutdownTimeout + server.TerminalsCloseTimeout + quitStopTimeout

// The HTTP server's limits. writeTimeout is 0 on purpose: a page's live-update stream stays open
// for as long as the page does, and a write deadline would cut it. readHeaderTimeout is the
// terminal's handshake deadline, since a WebSocket handshake is a request: its headers come from
// this machine, so they take far less.
const (
	readHeaderTimeout = server.HandshakeTimeout
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
	// URL is where it answers: http://127.0.0.1:PORT. It never holds the secret.
	URL string
	// Secret is what its API asks for, made for this launch.
	Secret secret.Secret

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

// Ended is a Server at url that has already shut down with err (nil for a clean shutdown). A
// running server ends with an error only when its listener fails, which no caller can make
// happen, so the code that reacts to such an end is tested with this.
func Ended(url string, err error) *Server {
	done := make(chan struct{})
	close(done)

	return &Server{URL: url, Secret: secret.Secret{}, done: done, err: err}
}

// Start takes the data folder's lock, loads settings, makes this launch's secret, listens on
// port(lookup), publishes {PID, URL, Secret} in the lock and serves until ctx ends, then shuts
// down within ShutdownTimeout.
// It returns instance.ErrHeld (wrapped) when another Conn holds the folder, and a PortTakenError
// when another program holds the port. Its hosted sessions run on real terminals, but for the dry
// run's stand-in sessions (drysession), which run nothing.
func Start(ctx context.Context, lookup config.LookupFunc, stderr io.Writer) (*Server, error) {
	return StartWith(ctx, lookup, stderr, pty.NewHost(pty.Config{
		Spawner: drysession.Spawner(pty.Spawn), Limits: pty.DefaultLimits(),
		Answers: pty.Answers{}, TurnWaited: nil,
	}))
}

// StartWith is Start with host running the hosted sessions: the server serves their terminals
// and stops every one of them when it stops, before it lets the data folder go.
func StartWith(
	ctx context.Context, lookup config.LookupFunc, stderr io.Writer, host *pty.Host,
) (*Server, error) {
	portNumber, err := port(lookup)
	if err != nil {
		return nil, err
	}

	cfg := config.FromEnv(lookup)

	key, err := secret.New()
	if err != nil {
		return nil, fmt.Errorf("starting conn: %w", err)
	}

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

	listener, address, err := listenAndPublish(ctx, held, portNumber, key)
	if err != nil {
		release()

		return nil, err
	}

	serveCtx, cancel := context.WithCancel(ctx)
	running := &Server{URL: address, Secret: key, done: make(chan struct{}), err: nil}
	handler := newHandler(serveCtx, cfg, store, logger, ui(lookup), host)
	handler.Secret = key
	// The terminals are closed by the quit below rather than when ctx ends, so the quit alone
	// orders their closing and the sessions' stop.
	terminals := server.NewTerminals(context.WithoutCancel(ctx), host)
	handler.Terminals = terminals

	go running.serve(serveCtx, newHTTPServer(handler), listener, func() {
		// The terminals close first, as going away, so none sees its program stopped or races
		// a reconnect against the stop; then the sessions stop; then the folder is let go.
		terminals.Close()
		cancel()
		stopHosted(context.WithoutCancel(ctx), host, stderr)
		release()
	})

	return running, nil
}

// stopHosted stops every hosted session, within quitStopTimeout, and says what did not stop.
func stopHosted(ctx context.Context, host *pty.Host, stderr io.Writer) {
	ctx, cancel := context.WithTimeout(ctx, quitStopTimeout)
	defer cancel()

	err := host.StopAll(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn: hosted sessions did not all stop: %v\n", err)
	}
}

// listenAndPublish opens the port and writes where it answers, and the secret it asks for, into
// the lock.
func listenAndPublish(
	ctx context.Context, held *instance.Lock, portNumber int, key secret.Secret,
) (net.Listener, string, error) {
	listener, err := listen(ctx, portNumber)
	if err != nil {
		return nil, "", err
	}

	address := "http://" + listener.Addr().String()

	err = held.Publish(instance.Holder{PID: os.Getpid(), URL: address, Secret: key})
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

// port is CONN_PORT, or 7777 when it is unset or blank. 0 lets the system pick a free port.
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
