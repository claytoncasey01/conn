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

const usage = `usage: conn <command>

commands:
  serve    run the dashboard server on 127.0.0.1:$CONN_PORT (default 7777) for a browser
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "serve":
		return serve(stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "conn: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

// port is CONN_PORT, or 7777 when it is unset or blank.
func port() (int, error) {
	v := strings.TrimSpace(os.Getenv("CONN_PORT"))
	if v == "" {
		return 7777, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 65535 {
		return 0, fmt.Errorf("CONN_PORT is not a port: %q", v)
	}
	return n, nil
}

// ui is the page bundle: the folder CONN_DIST names, else the one built into this binary.
func ui() fs.FS {
	if d := strings.TrimSpace(os.Getenv("CONN_DIST")); d != "" {
		abs, err := filepath.Abs(d)
		if err == nil {
			d = abs
		}
		return os.DirFS(d)
	}
	return conn.UI()
}

func serve(stdout, stderr io.Writer) int {
	p, err := port()
	if err != nil {
		fmt.Fprintf(stderr, "conn: %v\n", err)
		return 1
	}
	addr := net.JoinHostPort(server.Host, strconv.Itoa(p))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(stderr, "conn: %v\n", err)
		return 1
	}
	srv := &http.Server{Handler: &server.Server{UI: ui()}, ReadHeaderTimeout: 10 * time.Second}
	fmt.Fprintf(stdout, "conn listening on http://%s\n", addr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(stderr, "conn: %v\n", err)
			return 1
		}
	case <-ctx.Done():
		shut, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}
	return 0
}
