package reviews_test

// A post whose failure leaves unclear whether GitHub took the review is never pressed again on a
// guess: it is held until a poll has read the PR and my reviews of it say.

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
)

const (
	// uTookURL is where the check finds the review the unclear post left.
	uTookURL = "https://github.com/acme/widgets/pull/7#pullrequestreview-9"
	// uOtherCommit is a commit other than the one the draft reviewed.
	uOtherCommit = "fedcba9876543210fedcba9876543210fedcba98"
	// uSentMin is when, in minutes after the launch, the post is sent.
	uSentMin = 2
)

// errCheckFailed is the check's read of my reviews failing.
var errCheckFailed = errors.New("gh: connection reset")

// uMine is one review of mine on commit, submitted minutes after the launch.
func uMine(commit string, minutes float64) []reviews.PostedReview {
	return []reviews.PostedReview{{
		Commit: commit, SubmittedAt: worldT0 + minutes*minuteMs, URL: uTookURL,
	}}
}

// uRow is one failed post, the press again before a poll, the poll's check, and the press
// after it.
type uRow struct {
	postErr error
	mine    []reviews.PostedReview
	mineErr error
	// wantBefore and wantAfter are the answers to the press before the poll and after it.
	wantBefore string
	wantAfter  string
	// wantChecks is how often the check read my reviews.
	wantChecks int
	// wantPosts is how many reviews reached GitHub in all.
	wantPosts int
	// wantURL is the URL the draft holds at the end ("" for none).
	wantURL string
}

func uBusy(why string) string { return `{"error":"` + why + `"}` }

func uRows() map[string]uRow {
	posted := `{"ok":true,"url":"` + worldReviewURL + `"}`

	return map[string]uRow{
		"an unclear failure, then the check finds the post": {
			postErr: errUnclear, mine: uMine(dCommit, uSentMin+0.5),
			wantBefore: uBusy(reviews.TextPostChecking), wantAfter: uBusy(reviews.TextPostPosted),
			wantChecks: 1, wantPosts: 1, wantURL: uTookURL,
		},
		"an unclear failure, then the check finds none": {
			postErr: errUnclear, wantBefore: uBusy(reviews.TextPostChecking),
			wantAfter: posted, wantChecks: 1, wantPosts: 2, wantURL: worldReviewURL,
		},
		"a review of mine on another commit is not the post": {
			postErr: errUnclear, mine: uMine(uOtherCommit, uSentMin+0.5),
			wantBefore: uBusy(reviews.TextPostChecking), wantAfter: posted,
			wantChecks: 1, wantPosts: 2, wantURL: worldReviewURL,
		},
		"a review of mine from well before the post is not the post": {
			postErr: errUnclear, mine: uMine(dCommit, uSentMin-10),
			wantBefore: uBusy(reviews.TextPostChecking), wantAfter: posted,
			wantChecks: 1, wantPosts: 2, wantURL: worldReviewURL,
		},
		"a check that fails keeps the post held": {
			postErr: errUnclear, mineErr: errCheckFailed,
			wantBefore: uBusy(reviews.TextPostChecking),
			wantAfter:  uBusy(reviews.TextPostChecking), wantChecks: 1, wantPosts: 1,
		},
		"a refusal may be pressed again at once": {
			postErr: reviews.RefusedPost(pRefusedText), wantBefore: posted,
			wantAfter:  uBusy(reviews.TextPostPosted),
			wantChecks: 0, wantPosts: 2, wantURL: worldReviewURL,
		},
	}
}

func TestAnUnclearFailureIsCheckedBeforeAnotherPost(t *testing.T) {
	t.Parallel()

	for name, row := range uRows() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
			dWrite(run, 1, dDraft("Sound."))
			run.at(uSentMin)

			failing := func() (string, error) { return "", row.postErr }

			run.fakes.with(func(fakes *world) { fakes.postResult = failing })

			if reply := pPost(t, run.tab, pDraftPost(run)); reply.Code != 502 {
				t.Fatalf("first code %d", reply.Code)
			}

			mine := func() ([]reviews.PostedReview, error) { return row.mine, row.mineErr }

			run.fakes.with(func(fakes *world) {
				fakes.postResult = func() (string, error) { return worldReviewURL, nil }
				fakes.myReviews = mine
			})

			uPress(t, run, "before the poll", row.wantBefore)
			run.refresh()
			run.at(uSentMin + 1)
			uPress(t, run, "after the poll", row.wantAfter)
			uSettled(t, run, row)
		})
	}
}

// uPress is Post review pressed again, answering want.
func uPress(t *testing.T, run *bHarness, when, want string) {
	t.Helper()

	reply := pPost(t, run.tab, pDraftPost(run))
	if got := string(js.Marshal(reply.Body)); got != want {
		t.Errorf("%s: %s", when, got)
	}
}

// uSettled checks what reached GitHub and what the draft holds at the end.
func uSettled(t *testing.T, run *bHarness, row uRow) {
	t.Helper()

	run.fakes.with(func(fakes *world) {
		if fakes.myReviewsCalls != row.wantChecks {
			t.Errorf("checks %d", fakes.myReviewsCalls)
		}

		if len(fakes.posts) != row.wantPosts {
			t.Errorf("posts %d", len(fakes.posts))
		}
	})

	url := ""
	if draft := dLaunches(run)[0].Draft; draft.URL != nil {
		url = *draft.URL
	}

	if url != row.wantURL {
		t.Errorf("url %q", url)
	}
}

// TestAPollDuringAPostDoesNotSetItAside: my review landing while the post is out is the post
// itself, so the draft ends posted, not set aside as reviewed by hand.
func TestAPollDuringAPostDoesNotSetItAside(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	dWrite(run, 1, dDraft("Sound."))
	run.at(uSentMin)

	held := newGate()

	run.fakes.with(func(w *world) { w.postGate = held })

	replies := pPostLater(t, run.tab, pDraftPost(run))
	held.reached(t)
	run.review(uSentMin + 1)

	if why := dLaunches(run)[0].Draft.SetAside; why != nil {
		t.Errorf("while posting the draft is set aside: %s", *why)
	}

	held.open()

	if reply := <-replies; reply.Code != 200 {
		t.Errorf("code %d", reply.Code)
	}

	draft := dLaunches(run)[0].Draft
	if draft.PostedAt == nil {
		t.Error("the draft is not posted")
	}

	if draft.SetAside != nil {
		t.Errorf("the draft is set aside: %s", *draft.SetAside)
	}
}

// TestGhMyReviews: the check reads my newest submitted reviews of one PR, with the PR and the
// login as variables, and calls nothing for a login or repo that is not safe.
func TestGhMyReviews(t *testing.T) {
	t.Parallel()

	answer := `{"data":{"repository":{"pullRequest":{"reviews":{"nodes":[` +
		`{"url":"` + uTookURL + `","state":"COMMENTED","submittedAt":"2027-01-15T08:00:00Z",` +
		`"commit":{"oid":"` + dCommit + `"}},` +
		`{"url":"","state":"PENDING","submittedAt":null,"commit":{"oid":"` + dCommit + `"}}` +
		`]}}}}}`

	for name, row := range map[string]struct {
		repo, login string
		wantErr     error
		wantFound   int
		wantCalls   int
	}{
		"my submitted reviews": {
			repo: worldRepo, login: worldMe, wantFound: 1, wantCalls: 1,
		},
		"no login calls nothing": {repo: worldRepo, wantErr: reviews.ErrLookupUnsafe},
		"a repo not safe calls nothing": {
			repo: "../x", login: worldMe, wantErr: reviews.ErrLookupUnsafe,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var args []string

			calls := 0
			exec := func(_ context.Context, given []string) (string, error) {
				calls++
				args = given

				return answer, nil
			}

			found, err := reviews.Gh{Exec: exec}.MyReviews(t.Context(), row.repo, 7, row.login)
			if !errors.Is(err, row.wantErr) {
				t.Errorf("error %v", err)
			}

			if len(found) != row.wantFound {
				t.Errorf("found %d", len(found))
			}

			if calls != row.wantCalls {
				t.Errorf("calls %d", calls)
			}

			if row.wantFound > 0 && found[0].URL != uTookURL {
				t.Errorf("url %q", found[0].URL)
			}

			if row.wantCalls > 0 && !slices.Contains(args, "a="+worldMe) {
				t.Errorf("args %v", args)
			}
		})
	}
}
