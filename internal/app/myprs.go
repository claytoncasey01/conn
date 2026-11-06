package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/claytoncasey01/conn/internal/gh"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/reviews"
	command "github.com/claytoncasey01/conn/internal/run"
	"github.com/claytoncasey01/conn/internal/sessions"
)

const (
	// originTimeout stops the read of a clone's origin.
	originTimeout = 10 * time.Second
	// originBytesMax caps what that read may print.
	originBytesMax = 64 << 10
	// nanosPerMs turns a file time into ms.
	nanosPerMs = 1e6
)

var errNotRegularFile = errors.New("not a regular file")

// newMyPrs is the My PRs tab over this machine: gh, git, cmux, the Slack draft helper and the
// session list's last tick. It shares Reviews' parts, the GitHub budget among them.
func newMyPrs(ctx context.Context, parts reviewsParts, live *sessions.Loop) *myprs.MyPrs {
	cfg := parts.cfg
	self := myprs.Self{Pattern: cfg.SelfNamePattern, Prompt: cfg.SelfReviewPrompt}
	launch := launchDeps(parts, reviews.Names{Pattern: cfg.ReviewNamePattern})
	draft := myprs.DraftConfig{
		Model: cfg.ReviewScanModel, BudgetUSD: cfg.ReviewScanCostUSDMax,
		Timeout: cfg.ReviewScanTimeout,
	}

	return myprs.New(ctx, myprs.Deps{
		Gh:    myprs.Gh{Exec: gh.Large, Trigger: cfg.GreptileTrigger},
		Scope: myprs.ScopeNow(parts.store), File: filepath.Join(cfg.DataDir, "myprs.json"),
		HasClients: func() bool { return live.Hub().Count() > 0 },
		Now:        nowMs, Bump: live.Bump,
		CloneFor: func(repo string) string {
			clone, found := parts.reader.CloneFor(repo, parts.store.Get(), cfg.Home)
			if !found {
				return ""
			}

			return clone.Path
		},
		Sessions:    live.Live,
		RepoMatches: repoMatches,
		Launch: func(ctx context.Context, opts myprs.LaunchOpts) myprs.LaunchResult {
			opts.DataDir = cfg.DataDir

			return self.Launch(ctx, launch, opts)
		},
		Git: gitIn, DataDir: cfg.DataDir,
		Exists: func(path string) bool {
			_, err := os.Stat(path)

			return err == nil
		},
		Writes:    myprs.Writes{Exec: gh.Run, Trigger: cfg.GreptileTrigger},
		RepliesAt: repliesAt, ReadFile: readReplies,
		Draft: func(ctx context.Context, channel, body string) myprs.DraftResult {
			return myprs.RunDraft(ctx, channel, body, parts.runner.Run, draft)
		},
		Record: parts.statuses.Record, Budget: parts.budget, Logger: parts.logger,
		Config: myprs.Config{
			PollMs: millis(cfg.ReviewPoll), Me: cfg.ReviewMe, Dry: cfg.Dry.MyPrs,
			FindingBots: cfg.FindingBots, GreptileBot: cfg.GreptileBot,
			GreptileTrigger: cfg.GreptileTrigger, ProtectedHeads: cfg.ProtectedHeads,
			Self: self,
		},
	})
}

// repoMatches is whether the clone at path has repo as its origin: a wrong clone folder in
// Settings must never push another repo's branch. An error when git cannot say: its first line.
func repoMatches(ctx context.Context, repo, path string) (bool, error) {
	result := command.Cmd(ctx, "git", []string{"-C", path, "remote", "get-url", "origin"},
		command.Opts{
			Dir: "", Timeout: originTimeout, OutputBytesMax: originBytesMax, Stdin: "", Env: nil,
		})
	if result.Err != nil {
		why := command.FirstLine(js.Trim(result.Stderr))
		if why == "" {
			why = result.Err.Error()
		}

		return false, gitError{message: why}
	}

	return reviews.RemoteMatches(js.Trim(result.Stdout), repo), nil
}

// repliesAt is the mtime (ms, floored) of <worktree>/.conn/replies.json; nil when it is not
// there.
func repliesAt(worktree string) *float64 {
	info, err := os.Stat(filepath.Join(worktree, ".conn", "replies.json"))
	if err != nil {
		return nil
	}

	at := math.Floor(float64(info.ModTime().UnixNano()) / nanosPerMs)

	return &at
}

// readReplies is the text of a replies file: a regular file only (not a link, a pipe or a
// device), in a .conn folder of the person's own (not a link a repository committed), and never
// more than the limit plus one byte, so a file the session made huge, or a link pointed
// elsewhere, costs nothing.
func readReplies(path string) (string, bool) {
	err := reviews.CheckOwnFolder(filepath.Dir(path))
	if err != nil {
		return "", false
	}

	data, err := readCapped(path, myprs.RepliesFileBytesMax+1)
	if err != nil {
		return "", false
	}

	return js.DecodeUTF8(data, false), true
}

func readCapped(path string, bytesMax int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("reading the replies file: %w", err)
	}

	if !info.Mode().IsRegular() {
		return nil, errNotRegularFile
	}

	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("opening the replies file: %w", err)
	}

	defer func() { _ = file.Close() }()

	// What was opened is what was looked at: a regular file, whatever happened in between.
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("reading the replies file: %w", err)
	}

	if !opened.Mode().IsRegular() {
		return nil, errNotRegularFile
	}

	data, err := io.ReadAll(io.LimitReader(file, bytesMax))
	if err != nil {
		return nil, fmt.Errorf("reading the replies file: %w", err)
	}

	return data, nil
}
