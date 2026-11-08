package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/historyapi"
	"github.com/claytoncasey01/conn/internal/hosted"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/openwork"
	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
)

// hostedLife is what keeps the record of a Server's hosted sessions and resumes them at launch:
// the recorder its modules' sessions are recorded through, each owner's check, and the sessions
// this launch resumed, for the page.
type hostedLife struct {
	recorder *hosted.Recorder
	checks   hosted.Checks
	resumed  *resumedSessions
	// bump moves the snapshot's version, so a snapshot cached before the resumed sessions were
	// set is not shown without them.
	bump func()
	// ticked is closed once the session list the checks read holds what runs now, and
	// tickTimeout bounds the launch's wait for it (firstTickTimeout).
	ticked      <-chan struct{}
	tickTimeout time.Duration
}

// newRecorder is the recorder of host's sessions into the data folder, which takes Claude's id of
// each from the session list's last tick and says what it could not write on stderr.
func newRecorder(dataDir string, host *pty.Host, live *sessions.Loop, stderr io.Writer,
) *hosted.Recorder {
	return hosted.NewRecorder(hosted.RecorderDeps{
		Dir: dataDir, Live: host.Live,
		Linked: func() map[pty.ID]string { return linkedIDs(live.Live()) },
		Write:  hosted.Write,
		Failed: func(err error) {
			_, _ = fmt.Fprintf(stderr, "conn: the record of the hosted sessions: %v\n", err)
		},
	})
}

// linkedIDs is Claude's session id of each hosted session among live, by the host's id.
func linkedIDs(live []sessions.LiveSession) map[pty.ID]string {
	linked := map[pty.ID]string{}

	for _, session := range live {
		if session.HostedID == "" {
			continue
		}

		linked[pty.ID(session.HostedID)] = session.SessionID
	}

	return linked
}

// ownerParts are what the owners' checks read.
type ownerParts struct {
	cfg     config.Config
	store   *settings.Store
	reader  *origin.Reader
	work    *openwork.Work
	history *historyapi.API
	// beside is whether a session named name, or one in folder, runs now (besideIn).
	beside func(name, folder string) bool
	// stores are Reviews' and My PRs' stores as the launch found them.
	stores launchStores
}

// besideIn is whether a session named name in any folder, or one in folder or inside it, runs
// in live (the session list: the person's own claude too) or is in records (the sessions this
// launch has started, before claude registers them).
func besideIn(live []sessions.LiveSession, records []hosted.Record, name, folder string) bool {
	for _, session := range live {
		if session.Name == name {
			return true
		}

		if reviews.Within(session.Cwd, folder) {
			return true
		}
	}

	for _, record := range records {
		if record.Name == name {
			return true
		}

		if reviews.Within(record.Dir, folder) {
			return true
		}
	}

	return false
}

// ownerChecks are each owner's check of a record at launch: Open work's and History's own, and
// the review kinds' worktree rules. History off resumes none of its sessions.
func ownerChecks(parts ownerParts) hosted.Checks {
	reviewRule := parts.reviewRule(reviewKind{
		folder: "reviews", pattern: parts.cfg.ReviewNamePattern, disallowed: nil,
		dry: parts.cfg.Dry.Reviews,
		launched: func(worktree, name, sessionID string) (string, bool) {
			return reviews.LaunchedIn(parts.stores.reviews, worktree, name, sessionID)
		},
	})
	// My PRs' store keeps no conversation per round: the round binds the worktree, the name and
	// the model, and the conversation is the record's, as the other checks bound it.
	selfRule := parts.reviewRule(reviewKind{
		folder: "myprs", pattern: parts.cfg.SelfNamePattern, disallowed: myprs.SelfDisallowed(),
		dry: parts.cfg.Dry.MyPrs,
		launched: func(worktree, name, _ string) (string, bool) {
			return myprs.LaunchedIn(parts.stores.myPrs, worktree, name)
		},
	})

	checks := hosted.Checks{
		hosted.OwnerOpenWork: func(_ context.Context, record hosted.Record) (pty.Spec, bool) {
			return parts.work.ResumeAtLaunch(record.Name, record.Dir)
		},
		hosted.OwnerReviews: func(_ context.Context, record hosted.Record) (pty.Spec, bool) {
			return reviews.ResumeAtLaunch(reviewRule, record.Name, record.Dir,
				record.ClaudeSessionID)
		},
		hosted.OwnerMyPrs: func(_ context.Context, record hosted.Record) (pty.Spec, bool) {
			return reviews.ResumeAtLaunch(selfRule, record.Name, record.Dir,
				record.ClaudeSessionID)
		},
	}

	if parts.history != nil {
		checks[hosted.OwnerHistory] = func(ctx context.Context, record hosted.Record,
		) (pty.Spec, bool) {
			return parts.history.ResumeAtLaunch(ctx, record.ClaudeSessionID, record.Dir)
		}
	}

	return checks
}

// reviewKind is one review kind as a launch resumes it: the data folder's folder of its
// worktrees, its name pattern and disallowed rules, whether it is dry, and its launches.
type reviewKind struct {
	folder     string
	pattern    *regexp.Regexp
	disallowed []string
	dry        bool
	// launched is the model of the kind's open launch of a session (reviews.ResumeRule).
	launched func(worktree, name, sessionID string) (string, bool)
}

// reviewRule is the launch rule of kind: its worktrees under the data folder's folder, its name
// pattern and disallowed rules, on while GitHub is and the kind is not dry, in a repository on the
// allowlist, beside no session, and only as the kind launched it.
func (parts ownerParts) reviewRule(kind reviewKind) reviews.ResumeRule {
	return reviews.ResumeRule{
		Claude: parts.cfg.ClaudeBin, Root: filepath.Join(parts.cfg.DataDir, kind.folder),
		Pattern: kind.pattern, Disallowed: kind.disallowed,
		On: func() bool {
			if kind.dry {
				return false
			}

			return parts.store.ModuleEnabled(settings.ModuleGitHub)
		},
		Allowed: func(worktree string) bool {
			return repos.Allowed(parts.reader.Of(worktree), parts.store.Allow())
		},
		Beside: parts.beside, Launched: kind.launched,
	}
}

// launchResume is what a launch resumes from: the settings now, the record the last launch left
// and why it could not be read, the owners' checks, what starts a session, what carries the
// records not resumed yet in the record, and what waits for the session list the checks read.
type launchResume struct {
	sessions settings.Sessions
	records  []hosted.Record
	readErr  error
	checks   hosted.Checks
	start    hosted.Start
	carry    func([]hosted.Record)
	// ready waits until the session list holds what runs now (waitForFirstTick).
	ready  func(ctx context.Context) error
	stderr io.Writer
}

// firstTickTimeout bounds a launch's wait for the session list's first tick: above its one ps
// call's own limit (10 s).
const firstTickTimeout = 15 * time.Second

// errSessionsNotTicked is a session list that did not tick within firstTickTimeout.
var errSessionsNotTicked = errors.New("the session list did not read the running sessions in time")

// waitForFirstTick waits until ticked is closed, within timeout and while ctx lasts.
func waitForFirstTick(ctx context.Context, ticked <-chan struct{}, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ticked:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for the session list: %w", ctx.Err())
	case <-timer.C:
		return fmt.Errorf("%w: %s", errSessionsNotTicked, timeout)
	}
}

// resumeAtLaunch resumes the recorded sessions, as each owner's check rebuilds them, when the
// settings resume on launch and sessions open inside Conn; otherwise it resumes none. It says
// what it did on stderr, never more than the counts and the reason a record was not read.
func resumeAtLaunch(ctx context.Context, plan launchResume) hosted.Outcome {
	if plan.readErr != nil {
		_, _ = fmt.Fprintf(plan.stderr, "conn: no hosted session is resumed: %v\n", plan.readErr)
	}

	if !resumeWanted(plan.sessions) {
		return hosted.Outcome{
			Resumed: []hosted.Resumed{}, Skipped: len(plan.records), Unreached: nil,
		}
	}

	// Until each is resumed or refused, the record keeps the last launch's records as they were.
	plan.carry(plan.records)

	// An owner's check that reads the session list before its first tick sees nothing run: a
	// session the person runs in the folder would be missed. Without it, nothing is resumed, and
	// every record is kept for the next launch.
	err := plan.ready(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(plan.stderr, "conn: no hosted session is resumed: %v\n", err)

		return hosted.Outcome{
			Resumed: []hosted.Resumed{}, Skipped: len(plan.records), Unreached: plan.records,
		}
	}

	outcome := hosted.Resume(ctx, plan.records, plan.checks, plan.start)
	if len(plan.records) > 0 {
		_, _ = fmt.Fprintf(plan.stderr, "conn: resumed %d hosted sessions; %d not resumed\n",
			len(outcome.Resumed), outcome.Skipped)
	}

	return outcome
}

// resumeWanted is whether a launch resumes the recorded sessions: only when Settings resumes on
// launch, and only while sessions open inside Conn, since the record is of sessions Conn hosted.
func resumeWanted(current settings.Sessions) bool {
	if !current.ResumeOnLaunch {
		return false
	}

	return current.Where == settings.WhereInside
}

// insideOpener opens every session inside Conn on host, whatever the settings say: a resume never
// opens cmux or prints a command.
func insideOpener(host *pty.Host) launch.Opener {
	return launch.New(launch.Deps{
		Sessions: func() settings.Sessions {
			return settings.Sessions{Where: settings.WhereInside, ResumeOnLaunch: true}
		},
		Start: host.Start, Cmux: nil, LookPath: exec.LookPath, Environ: os.Environ,
	})
}

// resumeStart starts a resumed session through inside, and records it in the record's place: for
// the record's owner, on the conversation the owner's check resumed.
func resumeStart(inside launch.Opener, recorder *hosted.Recorder) hosted.Start {
	return func(ctx context.Context, record hosted.Record, spec pty.Spec) (pty.ID, bool) {
		opened := inside(ctx, spec)
		if opened.HostedID == "" {
			return "", false
		}

		recorder.Resumed(record, opened.HostedID, spec)

		return opened.HostedID, true
	}
}

// resumedSessions are the sessions this launch resumed, for the page's banner.
type resumedSessions struct {
	mu       sync.Mutex
	sessions []hosted.Resumed
	host     *pty.Host
}

func newResumedSessions(host *pty.Host) *resumedSessions {
	return &resumedSessions{mu: sync.Mutex{}, sessions: nil, host: host}
}

func (r *resumedSessions) set(resumed []hosted.Resumed) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.sessions = slices.Clone(resumed)
}

// view is the resumed sessions that still run, for the snapshot, or nil for none.
func (r *resumedSessions) view() any {
	r.mu.Lock()
	resumed := slices.Clone(r.sessions)
	r.mu.Unlock()

	return resumedView(resumed, r.host.Live())
}

// resumedView is the snapshot's "resumed": the resumed sessions still running in live, in the
// order they were resumed, or nil (the field left out) when none is.
func resumedView(resumed []hosted.Resumed, live []pty.Info) any {
	running := map[pty.ID]bool{}

	for _, info := range live {
		if info.ExitCode == nil {
			running[info.ID] = true
		}
	}

	shown := []hosted.Resumed{}

	for _, session := range resumed {
		if running[session.ID] {
			shown = append(shown, session)
		}
	}

	if len(shown) == 0 {
		return nil
	}

	return resumedJSON{Sessions: shown}
}

// resumedJSON is the snapshot's "resumed".
type resumedJSON struct {
	Sessions []hosted.Resumed `json:"sessions"`
}
