package reviews_test

// Drafts taken while GitHub may not be called, before the PR is read, and rewritten badly.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
)

// errListDown is GitHub not answering the searches.
var errListDown = errors.New("GitHub is down")

// TestADraftWrittenWhileConnWasDownIsNeverLost: Conn restarts after the session wrote its draft
// and went away, the polls fail until its launch is judged gone and closed, and the draft is
// still taken once a poll reads the PR; the worktree sweep leaves it until then.
func TestADraftWrittenWhileConnWasDownIsNeverLost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		ended      bool
		wantInline int
	}{
		{"the PR is still open", false, 2},
		{"the PR ended meanwhile", true, 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
			restarted := dRestartOffline(t, run)

			run.fakes.with(func(w *world) {
				w.listErr = nil
				if test.ended {
					w.open = []*reviews.PR{}
				}
			})
			dRefresh(t, restarted)
			restarted.Tick()
			restarted.Settle()

			if removed := dRemoves(run.fakes); len(removed) != 0 {
				t.Errorf("the worktree was removed before its draft was taken: %q", removed)
			}

			restarted.Tick()
			restarted.Settle()

			if inline := dStored(t, run).Inline; len(inline) != test.wantInline {
				t.Errorf("inline %v, want %d placements", inline, test.wantInline)
			}
		})
	}
}

// dRestartOffline is Conn restarted after the session wrote its draft and left, its polls
// failing until the launch is judged gone and closed.
func dRestartOffline(t *testing.T, run *bHarness) *reviews.Reviews {
	t.Helper()

	dWrite(run, 1, dDraft("Sound."))
	run.fakes.with(func(w *world) {
		w.sessions = []sessions.LiveSession{}
		w.listErr = errListDown
		w.now = worldT0 + 6*minuteMs
	})

	restarted := newReviews(t, run.fakes, setupOpts{})
	restarted.Tick()
	restarted.Settle()

	if run.launch().ClosedAt == nil {
		t.Fatal("the launch is not closed")
	}

	if run.launch().Draft != nil {
		t.Fatal("the draft was taken while the PR was not read")
	}

	return restarted
}

func dRefresh(t *testing.T, tab *reviews.Reviews) {
	t.Helper()

	_, err := tab.Refresh()
	if err != nil {
		t.Fatal(err)
	}
}

// dRemoves are the worktree removals git was asked for.
func dRemoves(fakes *world) []string {
	var out []string

	for _, call := range bGits(fakes) {
		if strings.HasPrefix(call, "worktree remove") {
			out = append(out, call)
		}
	}

	return out
}

// TestADiffReadCutShortStoresNothing: a diff read that Conn stopping cut short stores no draft,
// so the file is read again and its findings placed.
func TestADiffReadCutShortStoresNothing(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.fakes.with(func(w *world) {
		w.diff = func(string, string, string) (string, error) { return "", context.Canceled }
	})
	dWrite(run, 1, dDraft("Sound."))
	run.at(2)

	if run.launch().Draft != nil {
		t.Fatal("a draft was stored from a cut-short read")
	}

	run.fakes.with(func(w *world) {
		w.diff = func(string, string, string) (string, error) { return worldDiff, nil }
	})
	run.at(3)

	if inline := dStored(t, run).Inline; len(inline) != 2 {
		t.Errorf("inline %v, want the findings placed", inline)
	}
}

func TestADraftIsStoredWithoutCallingGitHubWhileItMayNotBeCalled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		block func(run *bHarness)
	}{
		{"GitHub is off", func(run *bHarness) {
			run.fakes.setSettings(settingsOf(settingsOpts{github: new(false)}))
		}},
		{"the rate limit is used up", func(run *bHarness) {
			run.tab.SetPausedUntil(worldT0 + 1000*hourMs)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
			test.block(run)
			dWrite(run, 1, dDraft("Sound."))
			run.at(2)

			if inline := dStored(t, run).Inline; inline != nil {
				t.Errorf("inline %v, want none", inline)
			}

			if calls := run.fakes.diffCalls; len(calls) != 0 {
				t.Errorf("the diff was asked for: %q", calls)
			}
		})
	}
}

// TestADraftWaitsForThePRToBeRead: after a restart, a draft is not read before the poll has read
// its PR (its base is not known yet), and is placed on the diff once it has.
func TestADraftWaitsForThePRToBeRead(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	dWrite(run, 1, dDraft("Sound."))
	run.fakes.advance(2 * minuteMs)

	restarted := newReviews(t, run.fakes, setupOpts{})
	restarted.Tick()

	if reads := run.fakes.snapshotReads(); reads != 0 {
		t.Errorf("read %d times before the PR was", reads)
	}

	restarted.Settle()
	tickAndSettle(restarted)

	if inline := dStored(t, run).Inline; len(inline) != 2 {
		t.Errorf("inline %v, want the findings placed", inline)
	}
}

func TestAnUnreadableRewriteKeepsTheDraft(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	dWrite(run, 1, dDraft("Sound."))
	run.at(2)
	dWrite(run, 3, dHalf)
	run.at(4)

	if summary := dStored(t, run).Summary; summary != "Sound." {
		t.Errorf("summary %q, want the readable draft kept", summary)
	}

	if problems := run.launch().DraftProblem; len(problems) == 0 {
		t.Error("the unreadable rewrite's problem is not recorded")
	}
}

// snapshotReads is how often a draft file was read so far.
func (w *world) snapshotReads() int {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.draftReads
}
