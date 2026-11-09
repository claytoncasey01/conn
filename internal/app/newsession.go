package app

import (
	"time"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/newsession"
	"github.com/claytoncasey01/conn/internal/openwork"
	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/realpath"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
)

// newSessionParts are what the Sessions page's New session reads and starts through.
type newSessionParts struct {
	cfg    config.Config
	store  *settings.Store
	reader *origin.Reader
	host   *pty.Host
	live   *sessions.Loop
	// open is History's opener: a new session is recorded as a History session on the
	// conversation id Conn chose, which History's check resumes at launch.
	open launch.Opener
	// myPrRepos are the repos of the My PRs rows.
	myPrRepos func() []string
}

// newNewSession is New session over this machine: the repositories Open work would offer, their
// clones as Open work finds them, each a real folder of the person's own, and the host's count of
// running sessions. Any dry switch makes it dry: a dry run starts nothing real.
func newNewSession(parts newSessionParts) *newsession.API {
	seen := seenWorkRepos(workParts{
		cfg: parts.cfg, store: parts.store, reader: parts.reader, myPrRepos: parts.myPrRepos,
	}, func() []string { return seenRepos(parts.live, parts.reader) })

	return newsession.New(newsession.Deps{
		Settings: parts.store.Get,
		Repos: func(current settings.Settings) []string {
			return openwork.AllowedRepos(current, seen())
		},
		CloneFor: func(repo string, current settings.Settings) (string, bool) {
			clone, found := parts.reader.CloneFor(repo, current, parts.cfg.Home)

			return clone.Path, found
		},
		CheckClone: func(folder string) error {
			return newsession.CheckClone(folder, reviews.CheckSafeFolder)
		},
		Resolve: realpath.Resolve, Open: parts.open,
		Claude: parts.cfg.ClaudeBin, Dry: parts.cfg.Dry.Any(), Room: parts.host.Room,
		NewID: newsession.NewID, Now: time.Now, Cooldown: 0,
	})
}
