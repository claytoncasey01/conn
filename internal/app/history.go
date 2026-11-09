package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/claytoncasey01/conn/internal/archive"
	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/history"
	"github.com/claytoncasey01/conn/internal/historyapi"
	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	// reasonUnits is how much of an error a banner shows.
	reasonUnits = 160
	// privateMask makes the archive owner-only whatever the process umask.
	privateMask = 0o077
	// backfillDelay lets the first paint come before the backfill.
	backfillDelay = 2 * time.Second
)

// historyParts are History's pieces, or only why it is off.
type historyParts struct {
	db         *history.DB
	summarizer *archive.Summarizer
	archiver   *archive.Archiver
	leads      *sessions.Leads
	env        archive.Env
	gate       *archive.Gate
	// offReason says why History is off ("" when it is on).
	offReason string
	mutex     sync.Mutex
	// backfill is the running pass's progress; nil when none is running.
	backfill *backfillState
	logged   string
}

type backfillState struct {
	Total int `json:"total"`
	Done  int `json:"done"`
}

// reason is the first line of an error, capped: what a banner can show.
func reason(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")

	line = js.Cut(js.Trim(line), reasonUnits)
	if line == "" {
		return "unknown error"
	}

	return line
}

// openHistory opens the archive without letting it take the dashboard down: a file that is not a
// database, a newer one, or a folder that cannot be written leaves History off, with a reason, and
// the file where it is.
func openHistory(ctx context.Context, cfg config.Config, store *settings.Store,
	reader *origin.Reader, stderr io.Writer,
) *historyParts {
	path := filepath.Join(cfg.DataDir, "history.db")
	repoOf := func(folder string) string {
		info, err := os.Stat(folder)
		if err != nil {
			return ""
		}

		if !info.IsDir() {
			return ""
		}

		return reader.Of(folder)
	}

	// Private from the first byte; put back at once, so nothing started later inherits it.
	mask := syscall.Umask(privateMask)
	opened, err := history.Open(ctx, path, history.Options{OriginOf: repoOf, Now: nowMs})

	syscall.Umask(mask)

	parts := &historyParts{mutex: sync.Mutex{}}

	if err != nil {
		parts.offReason = filepath.Base(path) + " could not be opened: " + reason(err)
		_, _ = fmt.Fprintf(stderr, "conn: History is off: %s. The live views are not affected, "+
			"and the file was left as it is.\n", parts.offReason)

		return parts
	}

	parts.db = opened
	parts.leads = sessions.NewLeads()
	parts.env = archive.Env{RepoOf: repoOf, TicketKey: ticketKey(store)}
	parts.gate = &archive.Gate{Setup: store.Setup, Allow: store.Allow}
	parts.summarizer = archive.NewSummarizer(opened,
		archive.ClaudeSummary(claude.Runner{Bin: cfg.ClaudeBin}, cfg.SummaryCostUSDMax,
			cfg.SummaryTimeout), parts.gate, cfg.SummaryModel,
		func() context.Context { return context.WithoutCancel(ctx) })

	// The queue lives in memory: a summary a dead process left queued or running stays that way.
	_, err = opened.ResetInterruptedSummaries(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn: could not reset interrupted summaries: %v\n", err)
	}

	parts.archiver = archive.NewArchiver(opened, parts.env, parts.gate, parts.leads,
		parts.summarizer.Captured)

	go parts.archiver.Run(ctx)

	return parts
}

func nowMs() float64 { return float64(time.Now().UnixMilli()) }

// ticketKey is the ticket a session's name is, while Open work is on.
func ticketKey(store *settings.Store) func(name string) string {
	return func(name string) string { return jira.KeyOf(store.Get(), name) }
}

type historyFooterOn struct {
	Failed   float64        `json:"failed"`
	Backfill *backfillState `json:"backfill"`
	Error    *string        `json:"error"`
}

// status is what the snapshot says about History. A read that fails at run time is reported here,
// and logged once per distinct message. The read is never cut short by ctx ending.
func (h *historyParts) status(ctx context.Context, stderr io.Writer) func() any {
	detached := context.WithoutCancel(ctx)

	return func() any {
		h.mutex.Lock()
		backfill := h.backfill
		h.mutex.Unlock()

		if h.db == nil {
			why := h.offReason

			return historyFooterOn{Failed: 0, Backfill: nil, Error: &why}
		}

		failed, err := h.db.FailureCount(detached)
		if err == nil {
			return historyFooterOn{Failed: failed, Backfill: backfill, Error: nil}
		}

		why := reason(err)

		h.mutex.Lock()
		if why != h.logged {
			h.logged = why
			_, _ = fmt.Fprintf(stderr, "conn: History could not be read: %s\n", why)
		}
		h.mutex.Unlock()

		message := "History could not be read: " + why

		return historyFooterOn{Failed: 0, Backfill: backfill, Error: &message}
	}
}

// startBackfill runs the startup backfill a little after the server starts, and again whenever
// the allowlist changes (the first one ends setup; a repo added later brings in its sessions). A
// pass is never cut short by ctx ending.
func (h *historyParts) startBackfill(ctx context.Context, cfg config.Config,
	store *settings.Store, live *sessions.Loop, liveIDs func() map[string]bool,
) {
	if h.db == nil {
		return
	}

	backfill := &archive.Backfill{
		DB: h.db, Root: cfg.TranscriptsRoot, Env: h.env, Gate: h.gate, Leads: h.leads,
		LiveIDs: liveIDs, MinAgeMs: 0, Now: nowMs,
		OnProgress: func(progress archive.Progress) {
			h.mutex.Lock()
			h.backfill = &backfillState{Total: progress.Total, Done: progress.Done}
			h.mutex.Unlock()
			live.Bump()
		},
	}

	runner := archive.NewRunner(func() int {
		_, tooNew := backfill.Pass(context.WithoutCancel(ctx))

		h.mutex.Lock()
		h.backfill = nil
		h.mutex.Unlock()
		live.Bump()

		return tooNew
	}, time.Duration(archive.MinAgeMs+archive.FollowUpSlackMs)*time.Millisecond)

	time.AfterFunc(backfillDelay, runner.Request)

	lastAllow := string(js.Marshal(store.Get().Repos.Allow))

	store.OnChange(func(next settings.Settings) {
		allow := string(js.Marshal(next.Repos.Allow))
		if allow == lastAllow {
			return
		}

		lastAllow = allow

		runner.Request()
	})
}

// repos are every repo History holds, hidden ones too: the Settings page must be able to offer one
// that was removed, so it can be added back.
func (h *historyParts) repos(ctx context.Context) func() []string {
	return func() []string {
		if h.db == nil {
			return []string{}
		}

		found, err := h.db.Repos(ctx)
		if err != nil {
			return []string{}
		}

		return found
	}
}

// newAPI is the History API, nil while History is off.
func (h *historyParts) newAPI(cfg config.Config, store *settings.Store,
	liveIDs func(ctx context.Context) map[string]bool, open launch.Opener,
	repoOf func(folder string) string,
) *historyapi.API {
	if h.db == nil {
		return nil
	}

	return historyapi.New(historyapi.Deps{
		DB: h.db, Summarizer: h.summarizer, LiveIDs: liveIDs, Allow: store.Allow,
		RepoOf: repoOf, Open: open, CheckFolder: reviews.CheckSafeFolder, Claude: cfg.ClaudeBin,
		Dry: func() bool { return os.Getenv("CONN_WORK_DRY") == "1" },
		Now: time.Now, ResumeCooldown: 0,
	})
}
