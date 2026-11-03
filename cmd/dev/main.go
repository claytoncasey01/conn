// Command dev is the development loop: the API server restarts when a Go file changes, and the UI updates in place (Vite HMR).
// Open http://127.0.0.1:5173. Ctrl-C stops both. It does not touch a dashboard on :7777.
//
// It keeps its own data dir by default ($HOME/.conn-dev), so a dev server never writes the history.db or reviews.json the real
// dashboard uses (two servers on one data dir would both poll Reviews and both archive). To develop against the real data, stop the
// dashboard first and run `CONN_DATA=$HOME/.conn make dev`.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fail(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fail(err)
	}
	apiPort := envOr("CONN_PORT", "7778")
	uiPort := envOr("CONN_DEV_PORT", "5173")
	data := envOr("CONN_DATA", filepath.Join(home, ".conn-dev"))
	env := append(os.Environ(), "CONN_PORT="+apiPort, "CONN_DATA="+data, "CONN_DEV_API=http://127.0.0.1:"+apiPort, "CONN_DEV_PORT="+uiPort,
		// the API serves the UI from disk, so a dev build never needs the bundle built into the binary to be fresh
		"CONN_DIST="+filepath.Join(root, "dist"))

	bin := filepath.Join(os.TempDir(), fmt.Sprintf("conn-dev-%d", os.Getpid()))
	defer os.Remove(bin)

	ui := exec.Command("bun", "--bun", "node_modules/vite/bin/vite.js", "--host", "127.0.0.1", "--port", uiPort, "--strictPort")
	ui.Dir, ui.Env, ui.Stdout, ui.Stderr = root, env, os.Stdout, os.Stderr
	if err := ui.Start(); err != nil {
		fail(err)
	}
	uiDone := make(chan struct{})
	go func() { _ = ui.Wait(); close(uiDone) }()

	fmt.Printf("dev: UI http://127.0.0.1:%s  API :%s  data %s\n", uiPort, apiPort, data)

	var api *exec.Cmd
	start := func() {
		build := exec.Command("go", "build", "-o", bin, "./cmd/conn")
		build.Dir, build.Stdout, build.Stderr = root, os.Stdout, os.Stderr
		if err := build.Run(); err != nil {
			fmt.Println("dev: build failed; waiting for a change")
			return
		}
		api = exec.Command(bin, "serve")
		api.Dir, api.Env, api.Stdout, api.Stderr = root, env, os.Stdout, os.Stderr
		if err := api.Start(); err != nil {
			fmt.Println("dev:", err)
			api = nil
		}
	}
	stopAPI := func() {
		if api != nil && api.Process != nil {
			_ = api.Process.Signal(syscall.SIGTERM)
			_ = api.Wait()
		}
		api = nil
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	last := stamp(root)
	start()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-sig:
			stopAPI()
			_ = ui.Process.Signal(syscall.SIGTERM)
			<-uiDone
			return
		case <-uiDone:
			// either process ending ends the other, so nothing is left running on a port
			stopAPI()
			os.Exit(1)
		case <-tick.C:
			if s := stamp(root); s != last {
				last = s
				fmt.Println("dev: Go files changed, restarting the API")
				stopAPI()
				start()
			}
		}
	}
}

// stamp sums the sizes and modification times of the Go files and go.mod, so any edit, add or delete changes it.
func stamp(root string) string {
	var n, size int64
	var newest time.Time
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "dist", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") || d.Name() == "go.mod" || d.Name() == "go.sum" {
			if info, err := d.Info(); err == nil {
				n++
				size += info.Size()
				if info.ModTime().After(newest) {
					newest = info.ModTime()
				}
			}
		}
		return nil
	})
	return fmt.Sprintf("%d/%d/%d", n, size, newest.UnixNano())
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "dev:", err)
	os.Exit(1)
}
