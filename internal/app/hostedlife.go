package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sync"

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
}

// ownerChecks are each owner's check of a record at launch: Open work's and History's own, and
// the review kinds' worktree rules. History off resumes none of its sessions.
func ownerChecks(parts ownerParts) hosted.Checks {
	reviewRule := parts.reviewRule("reviews", parts.cfg.ReviewNamePattern, nil,
		parts.cfg.Dry.Reviews)
	selfRule := parts.reviewRule("myprs", parts.cfg.SelfNamePattern, myprs.SelfDisallowed(),
		parts.cfg.Dry.MyPrs)

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

// reviewRule is the launch rule of one review kind: its worktrees under the data folder's
// folder, its name pattern and disallowed rules, on while GitHub is and the kind is not dry, in a
// repository on the allowlist.
func (parts ownerParts) reviewRule(folder string, pattern *regexp.Regexp, disallowed []string,
	dry bool,
) reviews.ResumeRule {
	return reviews.ResumeRule{
		Claude: parts.cfg.ClaudeBin, Root: filepath.Join(parts.cfg.DataDir, folder),
		Pattern: pattern, Disallowed: disallowed,
		On: func() bool {
			if dry {
				return false
			}

			return parts.store.ModuleEnabled(settings.ModuleGitHub)
		},
		Allowed: func(worktree string) bool {
			return repos.Allowed(parts.reader.Of(worktree), parts.store.Allow())
		},
	}
}

// launchResume is what a launch resumes from: the settings now, the record the last launch left
// and why it could not be read, the owners' checks, what starts a session, and what carries the
// records not resumed yet in the record.
type launchResume struct {
	sessions settings.Sessions
	records  []hosted.Record
	readErr  error
	checks   hosted.Checks
	start    hosted.Start
	carry    func([]hosted.Record)
	stderr   io.Writer
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

// resumeStart starts a resumed session inside Conn, whatever the settings say, and records it
// in the record's place: for the record's owner, on the conversation the owner's check resumed.
func resumeStart(host *pty.Host, recorder *hosted.Recorder) hosted.Start {
	inside := launch.New(launch.Deps{
		Sessions: func() settings.Sessions {
			return settings.Sessions{Where: settings.WhereInside, ResumeOnLaunch: true}
		},
		Start: host.Start, Cmux: nil, LookPath: exec.LookPath, Environ: os.Environ,
	})

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
