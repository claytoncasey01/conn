package openwork_test

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/openwork"
	"github.com/claytoncasey01/conn/internal/prstatus"
)

// When the ticket moves to review, and when a run counts as merged: both read only the record.

func TestTheReviewMoveIsDueAsTheSettingSays(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name   string
		policy string
		// opened are the legs with a PR, withCommits the legs with commits or a PR, withPR those
		// of them with a PR.
		opened, withCommits, withPR int
		moved                       bool
		want                        bool
	}{
		{"first: the first PR", "first", 1, 2, 1, false, true},
		{"first: a later PR after a failed move", "first", 2, 2, 2, false, true},
		{"first: moved already", "first", 2, 2, 2, true, false},
		{"first: no PR", "first", 0, 2, 0, false, false},
		{"all: one leg with commits lacks a PR", "all", 1, 2, 1, false, false},
		{"all: every leg with commits has a PR", "all", 2, 2, 2, false, true},
		{"all: a leg with no commits is ignored", "all", 1, 1, 1, false, true},
		{"all: moved already", "all", 2, 2, 2, true, false},
		{"all: no PR", "all", 0, 0, 0, false, false},
		{"never", "never", 2, 2, 2, false, false},
		{"an unknown policy", "sometimes", 1, 1, 1, false, false},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			got := openwork.ReviewDue(row.policy, row.opened, row.withCommits, row.withPR,
				row.moved)
			same(t, "due", got, row.want)
		})
	}
}

// legWith is a leg whose PR is in state ("" no PR, "none" a PR not read yet), with ahead commits.
func legWith(state string, ahead float64) *openwork.Leg {
	leg := &openwork.Leg{Repo: "acme/widgets", Ahead: ahead}

	switch state {
	case "":
	case "none":
		leg.PR = &openwork.PR{URL: "u", Number: 1, Status: nil}
	default:
		leg.PR = &openwork.PR{URL: "u", Number: 1, Status: &prstatus.Status{State: state}}
	}

	return leg
}

func TestARunIsMergedWhenEveryPRIsSettledAndOneMerged(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name  string
		state string
		legs  []*openwork.Leg
		want  bool
	}{
		{"one merged", "pr_open", []*openwork.Leg{legWith("merged", 1)}, true},
		{"one open", "pr_open", []*openwork.Leg{legWith("open", 1)}, false},
		{"one not read yet", "pr_open", []*openwork.Leg{legWith("none", 1)}, false},
		{"not pr_open", "building", []*openwork.Leg{legWith("merged", 1)}, false},
		{"merged and closed", "pr_open", []*openwork.Leg{
			legWith("merged", 1), legWith("closed", 1),
		}, true},
		{"closed only", "pr_open", []*openwork.Leg{
			legWith("closed", 1), legWith("closed", 1),
		}, false},
		{"merged and still open", "pr_open", []*openwork.Leg{
			legWith("merged", 1), legWith("open", 1),
		}, false},
		{"a leg with commits and no PR does not block", "pr_open", []*openwork.Leg{
			legWith("merged", 1), legWith("", 4),
		}, true},
		{"no PR at all", "pr_open", []*openwork.Leg{legWith("", 1), legWith("", 0)}, false},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			record := &openwork.Record{Key: "PROJ-1", State: row.state, Repos: row.legs}
			same(t, "merged", openwork.Merged(record), row.want)
		})
	}
}
