package reviews_test

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

// aReviewedOn is PR 7 with my review on commit (state, unless empty), requesting me when asked.
func aReviewedOn(commit, state string, requested bool) *reviews.PR {
	return prOf(func(pr *reviews.PR) {
		if requested {
			pr.Requested = []string{worldMe}
		}

		pr.Reviews = []reviews.Review{myReview(func(review *reviews.Review) {
			review.Commit = commit
			if state != "" {
				review.State = state
			}
		})}
	})
}

// aReReview is the re-review commit of the nth launch ("<nil>" for none).
func aReReview(t *testing.T, fakes *world, nth int) string {
	t.Helper()

	var sha *string

	fakes.with(func(fakes *world) {
		if nth >= len(fakes.launches) {
			t.Fatalf("no launch %d", nth)
		}

		sha = fakes.launches[nth].ReReviewSha
	})

	return aText(sha)
}

// aReReviewCase is one start (or two) of a re-review.
type aReReviewCase struct {
	name string
	// open is PR 7 as GitHub has it; nil for one that requests me, unreviewed.
	open *reviews.PR
	// later is PR 7 as GitHub has it at a second start 5 minutes on; nil for no second start.
	later *reviews.PR
	state string
	want  string
}

func aReReviewCases() []aReReviewCase {
	return []aReReviewCase{
		// State rereview: reviewed, and asked again on GitHub.
		{
			name: "asked again", open: aReviewedOn("abc1234", "", true), later: nil,
			state: "rereview", want: "abc1234",
		},
		// A pending (unsent) review is not a review.
		{
			name: "pending", open: aReviewedOn("abc1234", "PENDING", true), later: nil, state: "",
			want: "<nil>",
		},
		// A previous launch also makes this a re-review, even when GitHub no longer shows the PR
		// as asked again.
		{
			name: "launched before", open: aReviewedOn("def5678", "", true),
			later: aReviewedOn("def5678", "", false), state: "", want: "def5678",
		},
		// A previous launch and no review by Jane: no line rather than a wrong one.
		{name: "no review", open: nil, later: aRequested(7), state: "", want: "<nil>"},
		// A commit that is not a sha: no line.
		{
			name: "not a sha", open: aReviewedOn("not a sha; rm -rf", "", true), later: nil,
			state: "", want: "<nil>",
		},
	}
}

// A re-review passes the commit Jane last reviewed; an unknown commit sends no line.
func TestReReviewPassesTheLastReviewedCommit(t *testing.T) {
	t.Parallel()

	for _, tt := range aReReviewCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			aReReviewRun(t, tt)
		})
	}
}

func aReReviewRun(t *testing.T, tt aReReviewCase) {
	t.Helper()

	opts := setupOpts{}
	if tt.open != nil {
		opts.open = []*reviews.PR{tt.open}
	}

	tab, fakes := aReady(t, opts)

	if tt.state != "" {
		aEqual(t, "state", view(t, tab).Rows[0].State, tt.state)
	}

	aStart(t, tab, worldRepo, "7", aSonnet)

	if tt.later == nil {
		aEqual(t, "re-review", aReReview(t, fakes, 0), tt.want)

		return
	}

	fakes.with(func(fakes *world) {
		fakes.now += 5 * minuteMs
		fakes.open = []*reviews.PR{tt.later}
	})
	aStart(t, tab, worldRepo, "7", aSonnet)
	aEqual(t, "re-review", aReReview(t, fakes, 1), tt.want)
	fakes.with(func(fakes *world) { aEqual(t, "name", fakes.launches[1].Name, aName7b) })
}
