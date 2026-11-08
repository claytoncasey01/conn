// Package app is Conn's server put together: the data folder's lock, the settings, every tab
// and the HTTP server over them, for `conn serve` and any other program that runs a Conn.
package app

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"time"

	"github.com/claytoncasey01/conn"
	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/dryseed"
	"github.com/claytoncasey01/conn/internal/gallery"
	"github.com/claytoncasey01/conn/internal/gh"
	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/hosted"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/modules"
	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/openwork"
	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/server"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
	"github.com/claytoncasey01/conn/internal/settingsapi"
	"github.com/claytoncasey01/conn/internal/themes"
)

// seedDry seeds the dry settings into a temp folder when a dry switch is on and there is no
// settings.json (see dryseed), and answers the configuration the run goes on with (every module
// dry in a seeded run) and what removes the seed when the server stops.
func seedDry(cfg config.Config, store *settings.Store, stderr io.Writer) (config.Config, func()) {
	decision := dryseed.Decide(dryseed.FactsOf(cfg, store))
	if !decision.Seed {
		if decision.Why != "" {
			_, _ = fmt.Fprintf(stderr, "conn: %s\n", decision.Why)
		}

		return cfg, func() {}
	}

	dir, err := os.MkdirTemp("", "conn-dry-settings-")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn: could not seed the dry settings: %v\n", err)

		return cfg, func() {}
	}

	// Held for this run only: a save from the Settings page goes there too, and nothing is
	// written into the data folder.
	store.Reload(dir)

	if why := dryseed.Seed(cfg.Dry, dir, store); why != "" {
		_, _ = fmt.Fprintf(stderr, "conn: could not seed the dry settings: %s\n", why)
	}

	cfg.Dry = dryseed.SeededDry()

	return cfg, func() { _ = os.RemoveAll(dir) }
}

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
// until ctx ends. Every session a module starts is recorded through the hosted sessions' recorder
// under that module as its owner; the hostedLife it returns resumes them at launch.
func newHandler(
	ctx context.Context, cfg config.Config, store *settings.Store, logger *log.Logger,
	bundle fs.FS, host *pty.Host,
) (*server.Server, hostedLife) {
	// Read before the modules run, so none has changed its store yet.
	stores := readLaunchStores(cfg.DataDir)
	reader := origin.NewReader()
	opener := newOpener(cfg, store, host)
	archived := openHistory(ctx, cfg, store, reader, logger.Writer())
	resumed := newResumedSessions(host)
	running := &tabs{reviews: nil, myPrs: nil, work: nil}

	live := newSessions(ctx, sessionParts{
		cfg: cfg, store: store, reader: reader, archived: archived, stderr: logger.Writer(),
		host: host,
	}, tabViews{
		reviews: func() any { return running.reviews.Snapshot() },
		myPrs:   func() any { return running.myPrs.Snapshot() },
		runs:    func() []sessions.Run { return running.work.Runs() },
		resumed: resumed.view,
	})

	recorder := newRecorder(cfg.DataDir, host, live, logger.Writer())
	parts := newTabs(ctx, tabParts{
		cfg: cfg, store: store, reader: reader, logger: logger, host: host, live: live,
		opener: opener, recorder: recorder,
	}, running)

	liveIDs := liveIDsOf(live)

	archived.startBackfill(ctx, cfg, store, live, func() map[string]bool { return liveIDs(ctx) })

	api := newSettingsAPI(ctx, parts, live, archived, running.myPrs.Repos)
	historyAPI := archived.newAPI(cfg, store, liveIDs,
		recorder.Opener(hosted.OwnerHistory, opener), reader.Of)
	library, themesAPI := newThemes(cfg, logger)
	checks := ownerChecks(ownerParts{
		cfg: cfg, store: store, reader: reader, work: running.work, history: historyAPI,
		beside: func(name, folder string) bool {
			return besideIn(live.Live(), recorder.Records(), name, folder)
		},
		stores: stores,
	})

	return &server.Server{
		UI: bundle, Clients: live.Hub().Count, Settings: api, Logger: logger, Sessions: live,
		History: historyAPI, HistoryOff: archived.offReason,
		Reviews: running.reviews, MyPrs: running.myPrs, Work: running.work,
		Themes: library, Gallery: themesAPI, Provided: newProvided(cfg, logger),
	}, hostedLife{
		recorder: recorder, checks: checks, resumed: resumed, bump: live.Bump,
		ready: func(ctx context.Context) error {
			return waitForFirstTick(ctx, live.Ticked(), firstTickTimeout)
		},
	}
}

// tabParts are what the tabs beside the session list are made of: each module's sessions open
// through opener, recorded under the module by recorder.
type tabParts struct {
	cfg      config.Config
	store    *settings.Store
	reader   *origin.Reader
	logger   *log.Logger
	host     *pty.Host
	live     *sessions.Loop
	opener   launch.Opener
	recorder *hosted.Recorder
}

// newTabs makes Open work, Reviews and My PRs into running, starts them, and is the parts Reviews
// was made from.
func newTabs(ctx context.Context, made tabParts, running *tabs) reviewsParts {
	cfg, store, live := made.cfg, made.store, made.live
	runner := claude.Runner{Bin: cfg.ClaudeBin}
	statuses := newStatuses(store, runner)
	// Open work makes the runs folder at boot, even while it is off.
	running.work = newWork(ctx, workParts{
		cfg: cfg, store: store, reader: made.reader, runner: runner, statuses: statuses,
		logger: made.logger, myPrRepos: func() []string { return running.myPrs.Repos() },
		open: made.recorder.Opener(hosted.OwnerOpenWork, made.opener), host: made.host,
	}, live)

	// My PRs and Reviews poll GitHub as the same account: they share its hourly budget and a
	// rate limit that either runs into. Reviews places an old record against the repos Conn has
	// seen, My PRs' rows among them.
	parts := reviewsParts{
		cfg: cfg, store: store, reader: made.reader, statuses: statuses, budget: ghbudget.New(),
		runner: runner, logger: made.logger,
		myPrRepos: func() []string { return running.myPrs.Repos() },
		open:      made.recorder.Opener(hosted.OwnerReviews, made.opener),
		selfOpen:  made.recorder.Opener(hosted.OwnerMyPrs, made.opener),
	}
	running.reviews = newReviews(ctx, parts, live)
	running.myPrs = newMyPrs(ctx, parts, live)

	startTabs(ctx, store, live, *running)

	return parts
}

// tabs are the parts of the server that tick beside the session list.
type tabs struct {
	reviews *reviews.Reviews
	myPrs   *myprs.MyPrs
	work    *openwork.Work
}

// newSettingsAPI is /api/settings over the store, the statuses and the repos Conn has seen.
func newSettingsAPI(ctx context.Context, parts reviewsParts, live *sessions.Loop,
	archived *historyParts, myPrRepos func() []string,
) *settingsapi.API {
	return settingsapi.New(settingsapi.Deps{
		Settings:     parts.store,
		Sessions:     liveFolders(ctx, live),
		MyPrRepos:    myPrRepos,
		HistoryRepos: archived.repos(ctx),
		OriginOf:     parts.reader.Of,
		CloneFor: func(repo string) (origin.Clone, bool) {
			return parts.reader.CloneFor(repo, parts.store.Get(), parts.cfg.Home)
		},
		Gh:      gh.Run,
		Modules: parts.statuses,
	})
}

// startTabs has every settings save reach the snapshot and the tabs, and starts the session list
// and the tabs, which run until ctx ends.
func startTabs(ctx context.Context, store *settings.Store, live *sessions.Loop, running tabs) {
	reviewsTab, myPrsTab := running.reviews, running.myPrs
	// A change in Settings reaches Open work's next call: turned off, nothing Atlassian is
	// running; other projects, no old list.
	store.OnChange(func(settings.Settings) { running.work.SettingsChanged() })
	// The snapshot is cached by version: a change of the allowlist must reach the page (the
	// first-launch redirect) at once. A saved allowlist, source or channel reaches Reviews at
	// once too (and the first allowlist migrates an old reviews.json).
	store.OnChange(func(settings.Settings) { live.Bump() })
	store.OnChange(func(settings.Settings) { reviewsTab.SettingsChanged() })
	// A change of the allowlist or a switch reaches My PRs at once: a removed repo's PRs leave
	// the list, and a poll runs for an added one.
	store.OnChange(func(settings.Settings) { myPrsTab.SettingsChanged() })

	go live.Run(ctx)
	go reviewsTab.Run(ctx)
	go myPrsTab.Run(ctx)
	go running.work.Run(ctx)
}

// newStatuses are the module statuses: read from memory, filled by helper runs and by a CHECK
// press, the only thing that runs a probe.
func newStatuses(store *settings.Store, runner claude.Runner) *modules.Statuses {
	return modules.NewStatuses(modules.Deps{
		Enabled: func(name string) bool { return store.Get().Switch(name) },
		Probes: map[string]modules.Probe{
			settings.ModuleGitHub:    modules.GitHubProbe(modules.GhAuthStatus),
			settings.ModuleSlack:     modules.ConnectorProbe(modules.Slack, runner.Run),
			settings.ModuleAtlassian: modules.ConnectorProbe(modules.Atlassian, runner.Run),
		},
		Now: time.Now,
	})
}

// liveIDsOf answers the ids of the live sessions, read when it is called.
func liveIDsOf(live *sessions.Loop) func(ctx context.Context) map[string]bool {
	return func(ctx context.Context) map[string]bool {
		ids := map[string]bool{}
		for _, session := range live.LiveSessions(ctx) {
			ids[session.SessionID] = true
		}

		return ids
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

// newThemes are the themes library and the gallery API over it.
func newThemes(cfg config.Config, logger *log.Logger) (*themes.Library, *gallery.API) {
	builtin := themes.Embedded(conn.Themes())
	now := func() int64 { return time.Now().UnixMilli() }

	// The gallery's deps are read per call (the built-in ids and Default), so nothing is fetched
	// at boot, and with CONN_THEME_INDEX off the API answers before it calls any of them.
	themesGallery := gallery.New(gallery.Deps{
		Transport: nil, Now: now, Builtin: builtin, User: cfg.ThemesUser, Repo: cfg.ThemeIndex,
		Timeout: 0, Files: nil,
	})

	return themes.New(builtin, themes.Disk(cfg.ThemesUser), logger),
		gallery.NewAPI(themesGallery, cfg.ThemeIndex, now)
}
