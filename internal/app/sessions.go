package app

import (
	"context"
	"io"
	"time"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
)

// sessionParts are what the session list is made from.
type sessionParts struct {
	cfg      config.Config
	store    *settings.Store
	reader   *origin.Reader
	archived *historyParts
	stderr   io.Writer
	// host runs the sessions Conn hosts.
	host *pty.Host
}

// newSessions is the session list over this machine: ps, the registry under $HOME, gh, and the
// sessions Conn hosts.
func newSessions(ctx context.Context, parts sessionParts, views tabViews) *sessions.Loop {
	cfg, reader, archived := parts.cfg, parts.reader, parts.archived

	var archive sessions.Archive
	if archived.archiver != nil {
		archive = archived.archiver
	}

	return sessions.NewLoop(sessions.Deps{
		Home:    cfg.Home,
		DataDir: cfg.DataDir,
		Kinds: sessions.KindRules{
			ReviewName: cfg.ReviewNamePattern, SelfName: cfg.SelfNamePattern, ReviewsRoot: "",
		},
		Procs:    sessions.Procs,
		Env:      sessions.Env,
		PRView:   sessions.PRView,
		Cmux:     sessions.Cmux(cfg.CmuxBin),
		Stat:     sessions.Stat,
		Kill:     sessions.Kill,
		RepoOf:   reader.Of,
		Upstream: origin.UpstreamOf,
		Archive:  archive,
		Hooks:    sessionHooks(parts.store, archived.status(ctx, parts.stderr), views),
		Now:      time.Now,
		Host:     parts.host,
	})
}

// tabViews are the Reviews and My PRs views and the Open work runs the snapshot holds.
type tabViews struct {
	reviews, myPrs func() any
	runs           func() []sessions.Run
	// resumed are the sessions this launch resumed that still run, for the banner.
	resumed func() any
}

// sessionHooks are what the rest of the server puts in the snapshot.
func sessionHooks(store *settings.Store, history func() any, views tabViews) sessions.Hooks {
	return sessions.Hooks{
		// Open work off (the module is off, in setup, or lists no project): no runs, so no
		// ticket sessions.
		Runs:    views.runs,
		History: history,
		Reviews: views.reviews,
		MyPrs:   views.myPrs,
		Setup:   store.Setup,
		// Open work is on while the Atlassian module is and a Jira project is listed.
		OpenWork:    func() bool { return jira.On(store.Get()) },
		RepoVisible: func(repo string) bool { return repos.Allowed(repo, store.Allow()) },
		GithubOn:    func() bool { return store.ModuleEnabled(settings.ModuleGitHub) },
		Resumed:     views.resumed,
	}
}
