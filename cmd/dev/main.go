// Command dev is the development loop: the API server restarts when a Go file changes, and the UI
// updates in place (Vite HMR). Open http://127.0.0.1:5173. Ctrl-C stops both. It does not touch
// a dashboard on :7777.
//
// It keeps its own data folder by default ($HOME/.config/conn-dev), so a dev server never writes
// the history.db or reviews.json the real dashboard uses (two servers on one data folder would
// both poll Reviews and both archive). To develop against the real data, stop the dashboard first
// and run `CONN_DATA=$HOME/.config/conn make dev`.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/claytoncasey01/conn/internal/app"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/desk"
	"github.com/claytoncasey01/conn/internal/terminal"
)

// Exit codes.
const (
	exitOK    = 0
	exitError = 1
)

const (
	defaultAPIPort = "7778"
	defaultUIPort  = "5173"

	// pollInterval is how often the Go files are checked for a change.
	pollInterval = 500 * time.Millisecond

	// stopDelay is how long a process gets to exit after SIGTERM before it is killed.
	stopDelay = 5 * time.Second
)

var errNoHome = errors.New("HOME is not set; set it or CONN_DATA")

type lookupFunc func(key string) (string, bool)

// loop is everything the development loop needs, settled once at the start.
type loop struct {
	root    string
	apiPort string
	uiPort  string
	data    string
	// env is the environment both processes run with.
	env []string
	// bin is where the API binary is built.
	bin    string
	stdout io.Writer
	// terminal says whether stdout is a terminal: only then is the page's secret printed.
	terminal bool
	stderr   io.Writer
}

func main() {
	os.Exit(realMain(os.LookupEnv, os.Environ(), os.Stdout, terminal.Is(os.Stdout), os.Stderr))
}

func realMain(
	lookup lookupFunc, environ []string, stdout io.Writer, stdoutTerminal bool, stderr io.Writer,
) int {
	root, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "dev:", err)

		return exitError
	}

	settings, err := newLoop(root, lookup, environ)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "dev:", err)

		return exitError
	}

	settings.stdout = stdout
	settings.terminal = stdoutTerminal
	settings.stderr = stderr

	defer func() { _ = os.Remove(settings.bin) }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return settings.run(ctx)
}

func newLoop(root string, lookup lookupFunc, environ []string) (loop, error) {
	apiPort := valueOr(lookup, "CONN_PORT", defaultAPIPort)
	uiPort := valueOr(lookup, "CONN_DEV_PORT", defaultUIPort)

	data := valueOr(lookup, "CONN_DATA", "")
	if data == "" {
		home := valueOr(lookup, "HOME", "")
		if home == "" {
			return loop{}, errNoHome
		}

		data = config.DefaultDataDir(config.LookupFunc(lookup)) + "-dev"
	}

	env := append([]string{}, environ...)
	env = append(env,
		"CONN_PORT="+apiPort,
		"CONN_DATA="+data,
		"CONN_DEV_API=http://127.0.0.1:"+apiPort,
		"CONN_DEV_PORT="+uiPort,
		// The API serves the UI from disk, so a dev build never needs a fresh bundle built in.
		"CONN_DIST="+filepath.Join(root, "dist"),
	)

	return loop{
		root:     root,
		apiPort:  apiPort,
		uiPort:   uiPort,
		data:     data,
		env:      env,
		bin:      filepath.Join(os.TempDir(), "conn-dev-"+strconv.Itoa(os.Getpid())),
		stdout:   io.Discard,
		terminal: false,
		stderr:   io.Discard,
	}, nil
}

// run starts the UI and the API, and restarts the API on every Go change until ctx ends or the
// UI exits. Either process ending ends the other, so nothing is left running on a port.
func (l loop) run(ctx context.Context) int {
	uiProcess := l.command(ctx, "bun", "--bun", "node_modules/vite/bin/vite.js",
		"--host", "127.0.0.1", "--port", l.uiPort, "--strictPort")

	err := uiProcess.Start()
	if err != nil {
		_, _ = fmt.Fprintln(l.stderr, "dev:", err)

		return exitError
	}

	uiDone := make(chan struct{})

	go func() {
		_ = uiProcess.Wait()

		close(uiDone)
	}()

	_, _ = fmt.Fprintf(l.stdout, "dev: UI http://127.0.0.1:%s  API :%s  data %s\n",
		l.uiPort, l.apiPort, l.data)

	last := stamp(l.root)
	api := l.startAPI(ctx)
	ticker := time.NewTicker(pollInterval)

	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			stopProcess(api)
			<-uiDone

			return exitOK
		case <-uiDone:
			stopProcess(api)

			return exitError
		case <-ticker.C:
			now := stamp(l.root)
			if now == last {
				continue
			}

			last = now

			_, _ = fmt.Fprintln(l.stdout, "dev: Go files changed, restarting the API")

			stopProcess(api)
			api = l.startAPI(ctx)
		}
	}
}

// startAPI builds the API and starts it, or says the build failed and returns nil.
func (l loop) startAPI(ctx context.Context) *exec.Cmd {
	build := l.command(ctx, "go", "build", "-o", l.bin, "./cmd/conn")

	err := build.Run()
	if err != nil {
		_, _ = fmt.Fprintln(l.stdout, "dev: build failed; waiting for a change")

		return nil
	}

	api := l.command(ctx, l.bin, "serve")

	err = api.Start()
	if err != nil {
		_, _ = fmt.Fprintln(l.stdout, "dev:", err)

		return nil
	}

	l.sayPage(ctx)

	return api
}

// sayPage prints the UI's address with the new API's secret, which every start makes anew, so
// the page is opened again after a restart. It is printed only to a terminal; elsewhere the line
// says the secret is in the dev data folder's lock file.
func (l loop) sayPage(ctx context.Context) {
	holder, err := app.WaitForHolder(ctx, l.data)
	if err != nil {
		_, _ = fmt.Fprintln(l.stdout, "dev:", err)

		return
	}

	page := "http://127.0.0.1:" + l.uiPort
	if l.terminal {
		page = desk.PageURL(page, holder.Secret)
	}

	_, _ = fmt.Fprintf(l.stdout, "dev: open %s (the secret is new at each API start)\n", page)
}

// command runs name in the repository folder with the loop's environment and output. Ending ctx
// sends it SIGTERM, and SIGKILL stopDelay later.
func (l loop) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = l.root
	cmd.Env = l.env
	cmd.Stdout = l.stdout
	cmd.Stderr = l.stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = stopDelay

	return cmd
}

func stopProcess(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}

	if cmd.Process == nil {
		return
	}

	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = cmd.Wait()
}

// stamp sums the sizes and modification times of the Go files, the module files and the
// built-in theme files (the server carries them), so any edit, add or delete changes it.
func stamp(root string) string {
	var count, size, newest int64

	_ = filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		// WalkDir reports an error only for a folder it cannot read: leave that folder out.
		if err != nil {
			return filepath.SkipDir
		}

		if entry.IsDir() {
			return skipped(entry.Name())
		}

		if !watched(name) {
			return nil
		}

		// A file that vanished mid-walk is not counted; the next stamp sees it gone.
		info, infoErr := entry.Info()
		if infoErr == nil {
			count++
			size += info.Size()
			newest = max(newest, info.ModTime().UnixNano())
		}

		return nil
	})

	return fmt.Sprintf("%d/%d/%d", count, size, newest)
}

// skipped is filepath.SkipDir for a folder that never holds the server's Go code.
func skipped(folder string) error {
	switch folder {
	case ".git", "node_modules", "dist", "bin":
		return filepath.SkipDir
	}

	return nil
}

func watched(name string) bool {
	if strings.HasSuffix(name, ".go") {
		return true
	}

	switch filepath.Base(name) {
	case "go.mod", "go.sum":
		return true
	case "theme.json", "theme.css":
		// themes/<id>/theme.json: a built-in, which the server embeds.
		return filepath.Base(filepath.Dir(filepath.Dir(name))) == "themes"
	}

	return false
}

// valueOr is the variable key, or fallback when it is unset or empty.
func valueOr(lookup lookupFunc, key, fallback string) string {
	value, _ := lookup(key)
	if value == "" {
		return fallback
	}

	return value
}
