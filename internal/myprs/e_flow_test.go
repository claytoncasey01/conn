package myprs_test

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/myprs"
)

// The round's flow. The recorded answers (roundOver, askDue) cover the round's end and the ask at
// every boundary; here are readyFor and askedLaunch case by case, which the answers only reach at
// random and always with one Greptile login.

const (
	eMinute = 60_000
	eDevin  = "devin-ai-integration"
)

// eLaunch is round 1 at T0 from head old, armed, not asked.
func eLaunch(change ...func(l *myprs.Launch)) *myprs.Launch {
	out := &myprs.Launch{
		At: worldT0, Model: "sonnet", Name: "self-repo-7", Worktree: "/data/myprs/wt",
		HeadSha: "old", Round: 1, Armed: true, AskedSha: nil, AskedAt: nil, ClosedAt: nil,
		AskTry: nil, Manual: false,
	}

	for _, one := range change {
		one(out)
	}

	return out
}

// eAsked is a launch that asked for sha at at, and is no longer armed.
func eAsked(sha string, at, round float64) *myprs.Launch {
	return eLaunch(func(l *myprs.Launch) {
		l.Round = round
		l.Armed = false
		l.AskedSha = &sha
		l.AskedAt = &at
	})
}

// eFlowPR is a PR at head new; change edits it.
func eFlowPR(change ...func(p *myprs.PR)) *myprs.PR {
	return pr(append([]func(p *myprs.PR){func(p *myprs.PR) { p.HeadSha = "new" }}, change...)...)
}

func eReviewed(by, sha string) func(p *myprs.PR) {
	return func(p *myprs.PR) {
		p.Reviews = append(p.Reviews, myprs.Review{By: by, Sha: sha, At: worldT0 + eMinute})
	}
}

func eThreads(threads ...myprs.Thread) func(p *myprs.PR) {
	return func(p *myprs.PR) { p.Threads = threads }
}

func eBy(by string, resolved bool) myprs.Thread {
	return thread(func(one *myprs.Thread) {
		one.By = by
		one.Resolved = resolved
	})
}

func eCommented(by string, at float64, editedAt *float64) func(p *myprs.PR) {
	return func(p *myprs.PR) {
		p.Comments = append(p.Comments, myprs.Comment{By: by, At: at, EditedAt: editedAt})
	}
}

func TestEReadyFor(t *testing.T) {
	t.Parallel()

	asked := eAsked("new", worldT0, 1)
	greptile := eReviewed(greptileBot, "new")
	tests := []struct {
		name     string
		pull     *myprs.PR
		launch   *myprs.Launch
		greptile string
		want     bool
	}{
		{"all clear, Devin never reviewed", eFlowPR(greptile), asked, greptileBot, true},
		{
			"a draft", eFlowPR(greptile, func(p *myprs.PR) { p.Draft = true }), asked, greptileBot,
			false,
		},
		{
			"partial", eFlowPR(greptile, func(p *myprs.PR) { p.Partial = true }), asked,
			greptileBot, false,
		},
		{
			"an older commit reviewed", eFlowPR(eReviewed(greptileBot, "old")), asked,
			greptileBot, false,
		},
		{"no review", eFlowPR(), asked, greptileBot, false},
		{"only Devin reviewed", eFlowPR(eReviewed(eDevin, "new")), asked, greptileBot, false},
		{
			"an open Greptile thread", eFlowPR(greptile, eThreads(eBy(greptileBot, false))),
			asked, greptileBot, false,
		},
		{
			"an open Devin thread", eFlowPR(greptile, eThreads(eBy(eDevin, false))), asked,
			greptileBot, false,
		},
		{
			"a resolved Devin thread", eFlowPR(greptile, eThreads(eBy(eDevin, true))), asked,
			greptileBot, true,
		},
		{
			"human threads", eFlowPR(greptile, eThreads(eBy("sam", false), eBy("", false))),
			asked, greptileBot, true,
		},
		{
			"a summary edited after the ask",
			eFlowPR(eCommented(greptileBot, worldT0-5*eMinute, new(float64(worldT0+eMinute)))),
			asked, greptileBot, true,
		},
		{
			"a summary edited before the ask",
			eFlowPR(eCommented(greptileBot, worldT0-5*eMinute, new(float64(worldT0-eMinute)))),
			asked, greptileBot, false,
		},
		{
			"a summary never edited", eFlowPR(eCommented(greptileBot, worldT0-5*eMinute, nil)),
			asked, greptileBot, false,
		},
		{
			"a comment by someone else", eFlowPR(eCommented("sam", worldT0+eMinute, nil)), asked,
			greptileBot, false,
		},
		{
			"another login's review, read as Greptile's",
			eFlowPR(eReviewed("greptile-other", "new")), asked, greptileBot, false,
		},
		{
			"another login's review, its own login given",
			eFlowPR(eReviewed("greptile-other", "new")), asked, "greptile-other", true,
		},
		{
			"Greptile's review, another login given", eFlowPR(greptile), asked, "greptile-other",
			false,
		},
		{"no launch", eFlowPR(greptile), nil, greptileBot, false},
		{
			"a launch that never asked",
			eFlowPR(greptile), eLaunch(func(l *myprs.Launch) { l.Armed = false }), greptileBot,
			false,
		},
		{"asked with no time", eFlowPR(greptile), eLaunch(func(l *myprs.Launch) {
			sha := "new"
			l.AskedSha = &sha
		}), greptileBot, false},
		{"the head moved on after the ask", eFlowPR(func(p *myprs.PR) { p.HeadSha = "newer" },
			eReviewed(greptileBot, "newer")), asked, greptileBot, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := myprs.ReadyFor(tt.pull, tt.launch, defaults().FindingBots, tt.greptile)
			if got != tt.want {
				t.Errorf("ready %v", got)
			}
		})
	}
}

func TestEAskedLaunch(t *testing.T) {
	t.Parallel()

	first := eAsked("new", worldT0, 1)
	second := eAsked("new", worldT0+eMinute, 2)
	pushedNothing := func(round float64) *myprs.Launch {
		return eLaunch(func(l *myprs.Launch) {
			l.Round = round
			l.HeadSha = "new"
			l.Armed = false
		})
	}
	armed := func(round float64) *myprs.Launch {
		return eLaunch(func(l *myprs.Launch) {
			l.Round = round
			l.HeadSha = "new"
		})
	}
	neverPasted := eLaunch(func(l *myprs.Launch) {
		l.Round = 2
		l.HeadSha = "new"
		l.Manual = true
	})
	tests := []struct {
		name     string
		launches []*myprs.Launch
		head     string
		want     *myprs.Launch
	}{
		{"the one that asked", []*myprs.Launch{first}, "new", first},
		{
			"past rounds with nothing pushed",
			[]*myprs.Launch{first, pushedNothing(2), pushedNothing(3)},
			"new", first,
		},
		{"not past one still armed", []*myprs.Launch{first, armed(2)}, "new", nil},
		{
			"not past one that asked for another head",
			[]*myprs.Launch{first, eAsked("other", worldT0, 2)},
			"new", nil,
		},
		{"the newest that asked", []*myprs.Launch{first, second}, "new", second},
		{"another head", []*myprs.Launch{first}, "newer", nil},
		{"none", []*myprs.Launch{}, "new", nil},
		{
			"past an armed round that is not the newest",
			[]*myprs.Launch{first, neverPasted, pushedNothing(3)},
			"new", first,
		},
		{
			"not past the newest still armed",
			[]*myprs.Launch{first, pushedNothing(3), armed(4)},
			"new", nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if myprs.AskedLaunch(tt.launches, tt.head) != tt.want {
				t.Error("another launch")
			}
		})
	}
}
