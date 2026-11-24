package reviews_test

// The draft a review session writes (.conn/review.json) is read on the tick, placed on the PR's
// diff, stored on its launch in reviews.json, and set aside once it is no longer the review to
// post.

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
)

const (
	// dCommit is the head the session reviewed.
	dCommit = "0123456789abcdef0123456789abcdef01234567"
	// dHalf is a draft caught half written.
	dHalf = `{"commit":`
)

// errDiffRefused is GitHub refusing the diff.
var errDiffRefused = errors.New("diff refused")

// dDraft is a whole draft with summary: one finding on a line of the diff, one outside it.
func dDraft(summary string) string {
	return `{"commit":"` + dCommit + `","summary":"` + summary + `","verdict":"comment",` +
		`"findings":[` +
		`{"path":"a.go","line":2,"side":"RIGHT","severity":"P2","body":"Cap the retries."},` +
		`{"path":"docs/README.md","line":4,"severity":"P3","body":"Say why."}]}`
}

// dWrite is the session writing content as its review.json, min minutes after the launch.
func dWrite(run *bHarness, minutes float64, content string) {
	worktree := worktreeOf(run.fakes.dir, worldRepo, run.number)
	run.fakes.writeDraft(worktree, worldT0+minutes*minuteMs, content)
}

// dLaunches are the launches as Reviews holds them now (copies, read holding its lock).
func dLaunches(run *bHarness) []reviews.Launch {
	var out []reviews.Launch

	run.tab.WithFile(func(file *reviews.File) {
		entry, found := file.PRs.Get(keyOf(run.number))
		if !found {
			return
		}

		for _, launch := range entry.Launches {
			copied := *launch
			if launch.Draft != nil {
				draft := *launch.Draft
				copied.Draft = &draft
			}

			out = append(out, copied)
		}
	})

	return out
}

// dStored is the stored draft of the PR's first launch, read back from reviews.json.
func dStored(t *testing.T, run *bHarness) *reviews.StoredDraft {
	t.Helper()

	draft := run.launch().Draft
	if draft == nil {
		t.Fatal("no draft is stored")
	}

	return draft
}

func TestADraftIsStoredOnItsLaunch(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	dWrite(run, 1, dDraft("Sound."))
	run.at(2)

	draft := dStored(t, run)

	if draft.Commit != dCommit {
		t.Errorf("commit %q", draft.Commit)
	}

	if draft.Summary != "Sound." {
		t.Errorf("summary %q", draft.Summary)
	}

	if len(draft.Findings) != 2 {
		t.Errorf("findings %d", len(draft.Findings))
	}

	if !slices.Equal(draft.Inline, []bool{true, false}) {
		t.Errorf("inline %v", draft.Inline)
	}

	if draft.FileAt != worldT0+minuteMs {
		t.Errorf("fileAt %v", draft.FileAt)
	}

	bSame(t, "diffs", run.fakes.diffCalls, []string{worldRepo + " development..." + dCommit})
}

func TestAHalfWrittenDraftIsReadAgain(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	dWrite(run, 1, dHalf)
	run.at(2)

	if run.launch().Draft != nil {
		t.Error("a half-written file was stored as a draft")
	}

	if len(run.launch().DraftProblem) == 0 {
		t.Error("the half-written file's problem is not recorded")
	}

	// The same file is not read again.
	run.at(3)

	if reads := run.fakes.draftReads; reads != 1 {
		t.Errorf("the unreadable file was read %d times", reads)
	}

	dWrite(run, 4, dDraft("Whole."))
	run.at(5)

	if dStored(t, run).Summary != "Whole." {
		t.Errorf("summary %q", dStored(t, run).Summary)
	}

	if problems := run.launch().DraftProblem; len(problems) != 0 {
		t.Errorf("problems %q after a readable write", problems)
	}
}

func TestADraftWhoseDiffCannotBeReadPostsEveryFindingInTheBody(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.fakes.with(func(w *world) {
		w.diff = func(string, string, string) (string, error) { return "", errDiffRefused }
	})
	dWrite(run, 1, dDraft("Sound."))
	run.at(2)

	if inline := dStored(t, run).Inline; inline != nil {
		t.Errorf("inline %v, want none", inline)
	}

	// The diff is not asked for again on every tick.
	run.at(3)

	if calls := len(run.fakes.diffCalls); calls != 1 {
		t.Errorf("the diff was asked for %d times", calls)
	}
}

func TestAPostedDraftIsNeverReplaced(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	dWrite(run, 1, dDraft("First."))
	run.at(2)
	dStored(t, run)

	run.tab.WithFile(func(file *reviews.File) {
		entry, _ := file.PRs.Get(keyOf(run.number))
		posted := float64(worldT0 + 2*minuteMs)
		entry.Launches[0].Draft.PostedAt = &posted
	})

	dWrite(run, 3, dDraft("Second."))
	run.at(4)

	if summary := dLaunches(run)[0].Draft.Summary; summary != "First." {
		t.Errorf("summary %q, want the posted one", summary)
	}

	dWrite(run, 5, dHalf)
	run.at(6)

	if problems := dLaunches(run)[0].DraftProblem; len(problems) != 0 {
		t.Errorf("problems %q recorded beside a posted draft", problems)
	}
}

// dStore is a draft written at minute minutes and stored on the tick a minute later.
func dStore(run *bHarness, minutes float64) {
	dWrite(run, minutes, dDraft("Sound."))
	run.at(minutes + 1)
}

// dRelaunch is REVIEW pressed again after the first launch's session went away.
func dRelaunch(t *testing.T, run *bHarness, minutes float64) {
	t.Helper()

	run.fakes.with(func(w *world) { w.sessions = []sessions.LiveSession{} })
	run.at(minutes)

	if reply := run.start(bModel); reply.Code != 200 {
		t.Fatalf("the second launch answered %d %s", reply.Code, reply.Body)
	}
}

// dByHand makes the next launch one whose session the person starts from a command.
func dByHand(run *bHarness) {
	run.fakes.with(func(w *world) {
		w.launchResult = func(opts reviews.LaunchOpts) reviews.LaunchResult {
			return reviews.LaunchResult{
				OK: false, Worktree: worktreeOf(w.dir, opts.Repo, opts.Number),
				ManualCommand: "claude", Error: "start it yourself",
			}
		}
	})
}

func TestADraftIsSetAside(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		act  func(t *testing.T, run *bHarness)
		want *string
	}{
		{
			"my review after the draft", func(_ *testing.T, run *bHarness) {
				dStore(run, 1)
				run.review(3)
			}, textOf(reviews.SetAsideReviewed),
		},
		{
			"my review before the draft", func(_ *testing.T, run *bHarness) {
				run.review(1)
				dStore(run, 2)
			}, nil,
		},
		{
			"a newer launch", func(t *testing.T, run *bHarness) {
				t.Helper()
				dStore(run, 1)
				dRelaunch(t, run, 10)
			}, textOf(reviews.SetAsideNewer),
		},
		{
			"a newer launch by hand, once its draft is stored", func(t *testing.T, run *bHarness) {
				t.Helper()
				dStore(run, 1)
				dByHand(run)
				dRelaunch(t, run, 10)
				dStore(run, 11)
			}, textOf(reviews.SetAsideNewer),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
			test.act(t, run)

			got := dStored(t, run).SetAside
			if !equalText(got, test.want) {
				t.Errorf("set aside %v, want %v", textValue(got), textValue(test.want))
			}
		})
	}
}

func TestAStoredDraftCountsAsALandedReview(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		content    string
		wantClosed bool
	}{
		{"a readable draft", dDraft("Sound."), true},
		{"an unreadable file", dHalf, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
			dWrite(run, 1, test.content)
			run.at(2)
			run.at(bAfterMin + 5)

			if closed := run.launch().ClosedAt != nil; closed != test.wantClosed {
				t.Errorf("closed %v, want %v", closed, test.wantClosed)
			}
		})
	}
}

func equalText(got, want *string) bool {
	if got == nil || want == nil {
		return got == want
	}

	return *got == *want
}

func textValue(value *string) string {
	if value == nil {
		return "<nil>"
	}

	return *value
}

func TestTheDiffIsTheCompareAsADiffWithinItsCap(t *testing.T) {
	t.Parallel()

	tooLarge := strings.Repeat("x", reviews.MaxDiffBytes+1)
	tests := []struct {
		name      string
		baseRef   string
		answer    dStep
		wantOut   string
		wantErr   error
		wantCalls int
	}{
		{"the diff", "development", dStep{out: worldDiff, failed: nil}, worldDiff, nil, 1},
		{
			"over the cap", "development",
			dStep{out: tooLarge, failed: nil},
			"",
			reviews.ErrDiffTooLarge, 1,
		},
		{"gh fails", "development", dStep{out: "partial", failed: errDBad}, "", errDBad, 1},
		{
			"an unsafe base", "../main",
			dStep{out: worldDiff, failed: nil},
			"",
			reviews.ErrDiffUnsafe, 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source := &dGh{steps: []dStep{test.answer}, calls: nil}
			out, err := reviews.Gh{Exec: nil, Large: source.exec}.Diff(t.Context(), worldRepo,
				test.baseRef, dCommit)

			if !errors.Is(err, test.wantErr) {
				t.Errorf("error %v, want %v", err, test.wantErr)
			}

			if out != test.wantOut {
				t.Errorf("out %d bytes, want %d", len(out), len(test.wantOut))
			}

			if len(source.calls) != test.wantCalls {
				t.Fatalf("calls %d, want %d", len(source.calls), test.wantCalls)
			}

			if test.wantCalls == 0 {
				return
			}

			want := []string{
				"api", "-H", "Accept: application/vnd.github.diff",
				"repos/" + worldRepo + "/compare/development..." + dCommit,
			}
			bSame(t, "args", source.calls[0], want)
		})
	}
}
