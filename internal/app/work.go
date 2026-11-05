package app

import (
	"context"
	"log"
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
}

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
		OriginOf: parts.reader.Of, Sh: shIn, Helper: runner.Run,
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
	return func(name, cwd string) *openwork.Found {
		for _, session := range live.Live() {
			if session.Name != name {
				if session.Cwd != cwd {
					continue
				}
			}

			return &openwork.Found{
				ID: js.Number(session.PID), PID: session.PID, Status: session.Shown,
				SessionID: session.SessionID,
			}
		}

		return nil
	}
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
