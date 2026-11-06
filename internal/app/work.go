package app

import (
	"context"
	"log"
	"path/filepath"
	"time"

	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/modules"
	"github.com/claytoncasey01/conn/internal/openwork"
	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
)

// workParts are what Open work is made of besides the session list.
type workParts struct {
	cfg      config.Config
	store    *settings.Store
	reader   *origin.Reader
	runner   claude.Runner
	statuses *modules.Statuses
	logger   *log.Logger
	// myPrRepos are the repos of the My PRs rows.
	myPrRepos func() []string
}

// cloneFoldersMax is the most folders the clone folder and ~/projects are read for when Open
// work looks for the repositories cloned on this machine.
const cloneFoldersMax = 500

// newWork is Open work over this machine: git and gh run without a shell, the Jira and
// research helper runs, cmux, and the session list's last tick. It makes the runs folder, even
// while Open work is off.
func newWork(ctx context.Context, parts workParts, live *sessions.Loop) *openwork.Work {
	cfg, store, runner := parts.cfg, parts.store, parts.runner

	return openwork.New(ctx, openwork.Deps{
		DataDir: cfg.DataDir, Dry: cfg.Dry.Work, Settings: store.Get,
		CloneFor: func(repo string, current settings.Settings) (string, bool) {
			clone, found := parts.reader.CloneFor(repo, current, cfg.Home)

			return clone.Path, found
		},
		SeenRepos: seenWorkRepos(parts, func() []string { return seenRepos(live, parts.reader) }),
		OriginOf:  parts.reader.Of, Sh: shIn, Helper: runner.Run,
		FetchSprint: func(ctx context.Context, hooks openwork.Hooks) (jira.Sprint, error) {
			return jira.FetchSprint(ctx, jira.SprintDeps{
				Settings: store.Get(), DataDir: cfg.DataDir, Run: runner.Run, Save: nil,
				OnProgress: hooks.OnProgress, OnAuth: hooks.OnAuth,
			})
		},
		Transition: func(ctx context.Context, key string, move jira.Move) (jira.Moved, error) {
			return jira.Transition(ctx, jira.MoveDeps{
				Settings: store.Get(), DataDir: cfg.DataDir, Run: runner.Run,
			}, key, move)
		},
		Open: cmux.Open(cfg.CmuxBin), Record: parts.statuses.Record,
		FindSession: findSession(live),
		Reply: func(ctx context.Context, sessionID, text string) openwork.Reply {
			reply := live.Reply(ctx, sessionID, text)

			return openwork.Reply{Code: reply.Code, Body: reply.Body}
		},
		Bump: live.Bump, Now: nowMs, Sleep: sleepFor, Logger: parts.logger,
	})
}

// findSession is the live session named name or in cwd, as the session list's last tick left
// it: a hook of the session list reads Open work, so Open work never waits on it.
func findSession(live *sessions.Loop) func(name, cwd string) *openwork.Found {
	return func(name, cwd string) *openwork.Found { return foundIn(live.Live(), name, cwd) }
}

// foundIn is the first of the live sessions named name or in cwd; a name of "" finds by cwd
// alone, so a session with no name is not taken for the one asked for.
func foundIn(live []sessions.LiveSession, name, cwd string) *openwork.Found {
	for _, session := range live {
		if !sessionIs(session, name, cwd) {
			continue
		}

		return &openwork.Found{
			ID: js.Number(session.PID), PID: session.PID, Status: session.Shown,
			SessionID: session.SessionID,
		}
	}

	return nil
}

func sessionIs(session sessions.LiveSession, name, cwd string) bool {
	if session.Cwd == cwd {
		return true
	}

	if name == "" {
		return false
	}

	return session.Name == name
}

// sleepFor waits for wait, or until ctx ends.
func sleepFor(ctx context.Context, wait time.Duration) {
	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// seenWorkRepos are the repositories Open work may offer besides the ones Settings names: the
// clones in the clone folder and in ~/projects, as CloneFor finds them, then the repositories of
// the live sessions (sessionRepos) and the My PRs rows, as Reviews sees them. Open work keeps
// those the allowlist covers.
func seenWorkRepos(parts workParts, sessionRepos func() []string) func() []string {
	return func() []string {
		parents := []string{}
		if root := parts.store.Get().Repos.CloneRoot; root != nil {
			parents = append(parents, *root)
		}

		parents = append(parents, filepath.Join(parts.cfg.Home, "projects"))

		found := parts.reader.ClonesIn(parents, cloneFoldersMax)
		found = append(found, sessionRepos()...)

		if parts.myPrRepos != nil {
			found = append(found, parts.myPrRepos()...)
		}

		return found
	}
}
