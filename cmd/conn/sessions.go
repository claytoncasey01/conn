package main

import (
	"time"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
)

// historyFooter is the archive's footer state until History is in: nothing failed, no backfill.
type historyFooter struct {
	Failed   int `json:"failed"`
	Backfill any `json:"backfill"`
	Error    any `json:"error"`
}

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
func newSessions(cfg config.Config, store *settings.Store, reader *origin.Reader) *sessions.Loop {
	return sessions.NewLoop(sessions.Deps{
		Home:    cfg.Home,
		DataDir: cfg.DataDir,
		Kinds: sessions.KindRules{
			ReviewName: cfg.ReviewNamePattern, SelfName: cfg.SelfNamePattern, ReviewsRoot: "",
		},
		Procs:    sessions.Procs,
		Env:      sessions.Env,
		PRView:   sessions.PRView,
		RepoOf:   reader.Of,
		Upstream: origin.UpstreamOf,
		Hooks:    sessionHooks(store),
		Now:      time.Now,
	})
}

// sessionHooks are what the rest of the server puts in the snapshot. Open work, History,
// Reviews and My PRs are not in yet: they give what Bun gives before each has started.
func sessionHooks(store *settings.Store) sessions.Hooks {
	return sessions.Hooks{
		Runs: func() []sessions.Run { return []sessions.Run{} },
		History: func() any {
			return historyFooter{Failed: 0, Backfill: nil, Error: nil}
		},
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
