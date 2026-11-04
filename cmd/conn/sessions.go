package main

import (
	"context"
	"io"
	"time"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
)

// newSessions is the session list over this machine: ps, the registry under $HOME, gh.
func newSessions(ctx context.Context, cfg config.Config, store *settings.Store,
	reader *origin.Reader, archived *historyParts, stderr io.Writer, views tabViews,
) *sessions.Loop {
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
		Hooks:    sessionHooks(store, archived.status(ctx, stderr), views),
		Now:      time.Now,
	})
}

// tabViews are the Reviews and My PRs views the snapshot holds.
type tabViews struct {
	reviews, myPrs func() any
}

// sessionHooks are what the rest of the server puts in the snapshot. Open work is not in yet:
// it gives what it shows before it has started.
func sessionHooks(store *settings.Store, history func() any, views tabViews) sessions.Hooks {
	return sessions.Hooks{
		Runs:    func() []sessions.Run { return []sessions.Run{} },
		History: history,
		Reviews: views.reviews,
		MyPrs:   views.myPrs,
		Setup:   store.Setup,
		// Open work is on while the Atlassian module is and a Jira project is listed.
		OpenWork: func() bool {
			if !store.ModuleEnabled(settings.ModuleAtlassian) {
				return false
			}

			return len(store.Get().Atlassian.Jira.Projects) > 0
		},
		RepoVisible: func(repo string) bool { return repos.Allowed(repo, store.Allow()) },
		GithubOn:    func() bool { return store.ModuleEnabled(settings.ModuleGitHub) },
	}
}
