package reviews_test

// The poll cadence: from what GitHub says the searches cost, never from the pattern count.

import (
	"strconv"
	"testing"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/reviews"
)

func TestOrchCPollIntervalFollowsTheMeasuredCost(t *testing.T) {
	t.Parallel()

	rate := func(cost, remaining, resetAt float64) *ghbudget.RateInfo {
		return &ghbudget.RateInfo{Cost: cost, Remaining: remaining, ResetAt: resetAt}
	}

	tests := []struct {
		name string
		rate *ghbudget.RateInfo
		want float64
	}{
		{name: "nothing measured yet", rate: nil, want: pollMs},
		{name: "free", rate: rate(0, 5000, worldT0+hourMs), want: pollMs},
		// A cheap poll (25 "Requests me" searches, six to a call, a few points each).
		{name: "cheap", rate: rate(5, 4990, worldT0+hourMs), want: pollMs},
		// An hour of polls at 100 points each must stay under 2500: one every 144 s at most,
		// so the base (180 s) still holds; 250 points: every 6 minutes.
		{name: "100 points", rate: rate(100, 4900, worldT0+hourMs), want: pollMs},
		{name: "250 points", rate: rate(250, 5000, worldT0+hourMs), want: 360_000},
		// Half of 300 points left for the next hour at 50 a poll: every 20 minutes, capped at
		// 15.
		{name: "little left", rate: rate(50, 300, worldT0+hourMs), want: ghbudget.PollCapMs},
		// Half of 1000 left for half an hour at 200 a poll: every 12 minutes.
		{name: "half an hour", rate: rate(200, 1000, worldT0+hourMs/2), want: 720_000},
		{name: "nothing left", rate: rate(5000, 0, worldT0+hourMs), want: ghbudget.PollCapMs},
		// A reset already past says nothing about what is left.
		{name: "reset past", rate: rate(5, 0, worldT0-1), want: pollMs},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cIs(t, "interval", reviews.PollEvery(tt.rate, worldT0, pollMs), tt.want)
		})
	}
}

func TestOrchCManyPatternsPollAtTheBaseCadenceWhenCheapAndSlowerWhenDear(t *testing.T) {
	t.Parallel()

	allow := make([]string, 0, 26)
	for index := range 25 {
		allow = append(allow, "org"+strconv.Itoa(index)+"/*")
	}

	allow = append(allow, "acme/*")
	tab, fakes := setup(t, setupOpts{
		noClient: true, settings: cSettings(settingsOpts{allow: allow, slack: new(false)}),
	})

	polls := func() int { return fakes.snapshotCalls().list }

	fakes.with(func(state *world) {
		state.rate = &ghbudget.RateInfo{Cost: 5, Remaining: 4995, ResetAt: worldT0 + hourMs}
	})
	tickAndSettle(tab)
	cIs(t, "polls", polls(), 1)
	cIs(t, "one \"Requests me\" search per pattern, and nothing else",
		len(fakes.snapshotCalls().search[0]), 26)

	fakes.advance(pollMs)
	tickAndSettle(tab)
	cIs(t, "the base interval later it polls again", polls(), 2)

	// The same list made dear: GitHub says each poll costs 400 points, so every 9.6 minutes.
	fakes.with(func(state *world) {
		state.rate = &ghbudget.RateInfo{Cost: 400, Remaining: 4000, ResetAt: worldT0 + hourMs}
	})
	fakes.advance(pollMs)
	tickAndSettle(tab)
	cIs(t, "polls", polls(), 3)

	fakes.advance(pollMs)
	tickAndSettle(tab)
	cIs(t, "not after the base interval", polls(), 3)

	fakes.advance(576_000 - pollMs)
	tickAndSettle(tab)
	cIs(t, "after 9.6 minutes", polls(), 4)
}
