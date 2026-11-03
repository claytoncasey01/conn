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

// refresh is the Reviews tab's refresh counter.
type refresh struct {
	Seq  int `json:"seq"`
	Done int `json:"done"`
}

// reviewsOff is the Reviews tab switched off for a reason, with nothing listed, until Reviews is
// in.
type reviewsOff struct {
	Channel        string  `json:"channel"`
	Enabled        bool    `json:"enabled"`
	DisabledReason string  `json:"disabledReason"`
	Me             any     `json:"me"`
	Rows           []any   `json:"rows"`
	LastPollAt     any     `json:"lastPollAt"`
	LastScanAt     any     `json:"lastScanAt"`
	Error          any     `json:"error"`
	Slack          string  `json:"slack"`
	SlackError     any     `json:"slackError"`
	DMError        any     `json:"dmError"`
	Refresh        refresh `json:"refresh"`
}

// newSessions is the session list over this machine: ps, the registry under $HOME, gh.
func newSessions(ctx context.Context, cfg config.Config, store *settings.Store,
	reader *origin.Reader, archived *historyParts, stderr io.Writer,
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
		Hooks:    sessionHooks(store, archived.status(ctx, stderr)),
		Now:      time.Now,
	})
}

// sessionHooks are what the rest of the server puts in the snapshot. Open work, History,
// Reviews and My PRs are not in yet: they give what Bun gives before each has started.
func sessionHooks(store *settings.Store, history func() any) sessions.Hooks {
	return sessions.Hooks{
		Runs:    func() []sessions.Run { return []sessions.Run{} },
		History: history,
		Reviews: func() any {
			return reviewsOff{
				Channel: "", Enabled: false, DisabledReason: "not started", Me: nil,
				Rows: []any{}, LastPollAt: nil, LastScanAt: nil, Error: nil, Slack: "unused",
				SlackError: nil, DMError: nil, Refresh: refresh{Seq: 0, Done: 0},
			}
		},
		MyPrs: nil,
		Setup: store.Setup,
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
