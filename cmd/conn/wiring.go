package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"time"

	"github.com/claytoncasey01/conn"
	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/gallery"
	"github.com/claytoncasey01/conn/internal/gh"
	"github.com/claytoncasey01/conn/internal/instance"
	"github.com/claytoncasey01/conn/internal/modules"
	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/server"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
	"github.com/claytoncasey01/conn/internal/settingsapi"
	"github.com/claytoncasey01/conn/internal/themes"
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

// newHandler builds the server and everything it reads, and starts the session list, which runs
// until ctx ends.
func newHandler(
	ctx context.Context, cfg config.Config, store *settings.Store, logger *log.Logger,
	bundle fs.FS,
) *server.Server {
	reader := origin.NewReader()
	runner := claude.Runner{Bin: cfg.ClaudeBin}
	archived := openHistory(ctx, cfg, store, reader, logger.Writer())
	live := newSessions(ctx, cfg, store, reader, archived, logger.Writer())

	// The snapshot is cached by version: a change of the allowlist must reach the page (the
	// first-launch redirect) at once.
	store.OnChange(func(settings.Settings) { live.Bump() })

	go live.Run(ctx)

	liveIDs := func(ctx context.Context) map[string]bool {
		ids := map[string]bool{}
		for _, session := range live.LiveSessions(ctx) {
			ids[session.SessionID] = true
		}

		return ids
	}

	archived.startBackfill(ctx, cfg, store, live, func() map[string]bool { return liveIDs(ctx) })

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
		Sessions:     liveFolders(ctx, live),
		MyPrRepos:    nil,
		HistoryRepos: archived.repos(ctx),
		OriginOf:     reader.Of,
		CloneFor: func(repo string) (origin.Clone, bool) {
			return reader.CloneFor(repo, store.Get(), cfg.Home)
		},
		Gh:      gh.Run,
		Modules: statuses,
	})

	builtin := themes.Embedded(conn.Themes())
	now := func() int64 { return time.Now().UnixMilli() }

	// The gallery's deps are read per call (the built-in ids and Default), so nothing is fetched
	// at boot, and with CONN_THEME_INDEX off the API answers before it calls any of them.
	themesGallery := gallery.New(gallery.Deps{
		Transport: nil, Now: now, Builtin: builtin, User: cfg.ThemesUser, Repo: cfg.ThemeIndex,
		Timeout: 0, Files: nil,
	})

	return &server.Server{
		UI: bundle, Clients: live.Hub().Count, Settings: api, Logger: logger, Sessions: live,
		History: archived.newAPI(cfg, store, liveIDs), HistoryOff: archived.offReason,
		Themes:  themes.New(builtin, themes.Disk(cfg.ThemesUser), logger),
		Gallery: gallery.NewAPI(themesGallery, cfg.ThemeIndex, now),
	}
}

// liveFolders are the live sessions' folders, for the Settings page's repo suggestions.
func liveFolders(ctx context.Context, live *sessions.Loop) func() []string {
	return func() []string {
		live := live.LiveSessions(ctx)
		folders := make([]string, 0, len(live))

		for _, session := range live {
			folders = append(folders, session.Cwd)
		}

		return folders
	}
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
