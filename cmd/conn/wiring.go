package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"time"

	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/gh"
	"github.com/claytoncasey01/conn/internal/instance"
	"github.com/claytoncasey01/conn/internal/modules"
	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/server"
	"github.com/claytoncasey01/conn/internal/settings"
	"github.com/claytoncasey01/conn/internal/settingsapi"
)

const (
	// holderWait bounds the wait for a Conn that holds the data folder to say where it listens:
	// it takes the lock a moment before it opens its port.
	holderWait = 5 * time.Second
	// holderPoll is how often the lock file is read while waiting.
	holderPoll = 50 * time.Millisecond
)

// loadSettings reads settings.json once, at boot: a hand edit or a deletion takes a restart, and
// a PUT replaces it in memory and on disk. A bad file is the defaults (setup), never a failed
// boot; what could not be read is said here and on the Settings page.
func loadSettings(dataDir string, logger *log.Logger, stderr io.Writer) *settings.Store {
	store := settings.NewStore(logger)
	store.Reload(dataDir)

	for _, why := range store.Dropped() {
		_, _ = fmt.Fprintf(stderr, "conn: settings.json: %s\n", why)
	}

	return store
}

// newHandler builds the server and everything it reads.
func newHandler(
	cfg config.Config, store *settings.Store, logger *log.Logger, bundle fs.FS,
) *server.Server {
	reader := origin.NewReader()
	runner := claude.Runner{Bin: cfg.ClaudeBin}

	// The module statuses are read from memory, filled by helper runs and by a CHECK press, the
	// only thing that runs a probe.
	statuses := modules.NewStatuses(modules.Deps{
		Enabled: func(name string) bool { return store.Get().Switch(name) },
		Probes: map[string]modules.Probe{
			settings.ModuleGitHub:    modules.GitHubProbe(modules.GhAuthStatus),
			settings.ModuleSlack:     modules.ConnectorProbe(modules.Slack, runner.Run),
			settings.ModuleAtlassian: modules.ConnectorProbe(modules.Atlassian, runner.Run),
		},
		Now: time.Now,
	})

	api := settingsapi.New(settingsapi.Deps{
		Settings:     store,
		Sessions:     nil,
		MyPrRepos:    nil,
		HistoryRepos: nil,
		OriginOf:     reader.Of,
		CloneFor: func(repo string) (origin.Clone, bool) {
			return reader.CloneFor(repo, store.Get(), cfg.Home)
		},
		Gh:      gh.Run,
		Modules: statuses,
	})

	return &server.Server{UI: bundle, Clients: nil, Settings: api, Logger: logger}
}

// alreadyRunning says where the Conn holding the data folder answers.
func alreadyRunning(ctx context.Context, dataDir string, stdout, stderr io.Writer) int {
	holder, err := waitForHolder(ctx, dataDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn: %v\n", err)

		return exitError
	}

	_, _ = fmt.Fprintf(stdout, "conn is already running on %s\n", holder.URL)

	return exitOK
}

// waitForHolder is the Conn holding the data folder, once it has said where it listens.
func waitForHolder(ctx context.Context, dataDir string) (instance.Holder, error) {
	deadline := time.Now().Add(holderWait)

	for {
		holder, running, err := instance.Running(dataDir)
		if err != nil {
			return instance.Holder{}, fmt.Errorf("reading the lock file: %w", err)
		}

		if !running {
			return instance.Holder{}, errStoppedMeanwhile
		}

		if holder.URL != "" {
			return holder, nil
		}

		if time.Now().After(deadline) {
			return instance.Holder{}, fmt.Errorf("%w within %s", errNoAddress, holderWait)
		}

		select {
		case <-ctx.Done():
			return instance.Holder{}, fmt.Errorf("waiting for the running conn: %w", ctx.Err())
		case <-time.After(holderPoll):
		}
	}
}
