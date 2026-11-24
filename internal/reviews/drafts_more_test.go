package reviews_test

// Drafts taken while GitHub may not be called, before the PR is read, and rewritten badly.

import "testing"

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
