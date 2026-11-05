package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/gh"
	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/modules"
	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/reviews"
	command "github.com/claytoncasey01/conn/internal/run"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	// gitTimeout stops a git call that names no limit of its own.
	gitTimeout = 120 * time.Second
	// gitBytesMax caps what one git call may print.
	gitBytesMax = 8 << 20
	// gitErrorUnits is how much of git's error output an error carries.
	gitErrorUnits = 400
)

// gitError is a git call that failed: what git printed on stderr, or how the run ended.
type gitError struct {
	message string
}

func (e gitError) Error() string { return e.message }

// gitIn runs git without a shell, in dir ("" is the server's folder); its error's message is
// git's stderr, trimmed and cut, so its first line is the reason.
func gitIn(ctx context.Context, args []string, dir string, timeout time.Duration) (
	string, error,
) {
	return shIn(ctx, "git", args, dir, timeout)
}

// shIn runs a command without a shell, in dir, stopped after timeout (two
// minutes when 0); its error's message is the command's stderr, trimmed and cut at 400
// characters, or how the run ended when it printed nothing.
func shIn(ctx context.Context, name string, args []string, dir string, timeout time.Duration) (
	string, error,
) {
	if timeout == 0 {
		timeout = gitTimeout
	}

	result := command.Cmd(ctx, name, args, command.Opts{
		Dir: dir, Timeout: timeout, OutputBytesMax: gitBytesMax, Stdin: "", Env: nil,
	})
	if result.Err == nil {
		return result.Stdout, nil
	}

	why := result.Stderr
	if why == "" {
		why = result.Err.Error()
	}

	return result.Stdout, gitError{message: js.Slice(js.Trim(why), gitErrorUnits)}
}

// reviewsParts are what Reviews is made of besides the session list.
type reviewsParts struct {
	cfg      config.Config
	store    *settings.Store
	reader   *origin.Reader
	statuses *modules.Statuses
	budget   *ghbudget.Budget
	runner   claude.Runner
	logger   *log.Logger
	// myPrRepos are the repos of the My PRs rows.
	myPrRepos func() []string
}

func millis(duration time.Duration) float64 { return float64(duration.Milliseconds()) }

// newReviews is the Reviews tab over this machine: gh, the Slack helper runs, git, cmux and the
// session list's last tick.
func newReviews(ctx context.Context, parts reviewsParts, live *sessions.Loop) *reviews.Reviews {
	cfg := parts.cfg
	names := reviews.Names{Pattern: cfg.ReviewNamePattern}
	scanConfig := reviews.ScanConfig{
		Model: cfg.ReviewScanModel, WindowDays: cfg.ReviewFirstWindowDays,
		BudgetUSD: cfg.ReviewScanCostUSDMax, Timeout: cfg.ReviewScanTimeout,
	}
	launch := launchDeps(cfg, parts.reader, names)

	return reviews.New(ctx, reviews.Deps{
		Gh: reviews.Gh{Exec: gh.GraphQL},
		Scan: func(ctx context.Context, input reviews.ScanInput) (reviews.ScanResult, error) {
			return reviews.RunScan(ctx, input, parts.runner.Run, scanConfig, nowMs())
		},
		DMScan: func(ctx context.Context, allow []string, since string) ([]reviews.DMFind, error) {
			return reviews.RunDMScan(ctx, allow, since, parts.runner.Run, scanConfig, nowMs())
		},
		File: filepath.Join(cfg.DataDir, "reviews.json"), Settings: parts.store,
		CloneFor: func(repo string) string {
			clone, found := parts.reader.CloneFor(repo, parts.store.Get(), cfg.Home)
			if !found {
				return ""
			}

			return clone.Path
		},
		OriginOf: parts.reader.Of,
		SeenRepos: func() []string {
			return append(seenRepos(live, parts.reader), parts.myPrRepos()...)
		},
		HasClients: func() bool { return live.Hub().Count() > 0 },
		Sessions:   live.Live, Bump: live.Bump, Now: nowMs,
		Launch: func(ctx context.Context, opts reviews.LaunchOpts) reviews.LaunchResult {
			opts.DataDir = cfg.DataDir

			return reviews.Open(ctx, launch, opts)
		},
		Git:        gitIn,
		InputState: live.InputState,
		CloseWorkspace: func(ctx context.Context, pid string) (int, string) {
			reply := live.CloseWorkspace(ctx, pid)
			why, _ := reply.Body.Get("error")

			return reply.Code, js.String(why)
		},
		Exists: nil, WorktreeClone: nil, Record: parts.statuses.Record, Budget: parts.budget,
		Logger: parts.logger,
		Config: reviews.Config{
			PollMs: millis(cfg.ReviewPoll), FirstWindowDays: float64(cfg.ReviewFirstWindowDays),
			DMScanMs:      millis(cfg.ReviewDMScanInterval),
			ChannelScanMs: millis(cfg.ReviewChannelScanInterval),
			CloseAfterMs:  millis(cfg.ReviewCloseAfter),
			CloseCapMs:    millis(cfg.ReviewCloseAfterMax),
			Me:            cfg.ReviewMe, Dry: cfg.Dry.Reviews, NamePattern: cfg.ReviewNamePattern,
		},
	})
}

// launchDeps are the effects of launching a review: real git (no shell, stderr as the error),
// cmux, and files that are private to the owner.
func launchDeps(cfg config.Config, reader *origin.Reader, names reviews.Names) reviews.LaunchDeps {
	return reviews.LaunchDeps{
		Git: func(ctx context.Context, args []string, dir string) (string, error) {
			return gitIn(ctx, args, dir, 0)
		},
		Open:  cmux.Open(cfg.CmuxBin),
		Write: reviews.WriteLaunchFile,
		Exists: func(path string) bool {
			_, err := os.Stat(path)

			return err == nil
		},
		Read: func(path string) (string, error) {
			data, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("reading the exclude file: %w", err)
			}

			return js.DecodeUTF8(data, false), nil
		},
		Origin: reader.Of, Names: names,
		Prompts: reviews.Prompts{Review: cfg.ReviewPrompt, ReReview: cfg.ReReviewLine},
	}
}

// seenRepos are the repos of the live sessions' folders, the same source the Settings page
// suggests from (the My PRs rows are added after them).
func seenRepos(live *sessions.Loop, reader *origin.Reader) []string {
	var out []string

	for _, session := range live.Live() {
		if repo := reader.Of(session.Cwd); repo != "" {
			out = append(out, repo)
		}
	}

	return out
}
