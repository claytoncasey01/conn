package reviews_test

import (
	"regexp"
	"slices"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

// aRequestedPR is PR 7 of acme/widgets, requesting me.
func aRequestedPR() *reviews.PR {
	return prOf(func(pr *reviews.PR) { pr.Requested = []string{worldMe} })
}

// A press starts the review on the advisor it chose, else on the one Settings saved.
func TestAPressStartsTheAdvisorItChoseElseTheSaved(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name   string
		saved  string
		chosen any
		want   string
	}{
		{name: "none chosen: the saved one", saved: "opus", chosen: nil, want: "opus"},
		{name: "a choice over the saved one", saved: "opus", chosen: "fable", want: "fable"},
		{
			name: "Claude Code's own over the saved one", saved: "fable", chosen: "claude-code",
			want: "claude-code",
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			current := settingsOf(settingsOpts{})
			current.Sessions.Advisor = row.saved
			tab, fakes := aReady(t, setupOpts{
				settings: &current, open: []*reviews.PR{aRequestedPR()},
			})

			reply, err := tab.Start(t.Context(), worldRepo, "7", aSonnet, row.chosen)
			if err != nil {
				t.Fatal(err)
			}

			aEqual(t, "code", reply.Code, 200)
			fakes.with(func(fakes *world) {
				aEqual(t, "launches", len(fakes.launches), 1)
				aEqual(t, "advisor", fakes.launches[0].Advisor, row.want)
			})
		})
	}
}

// A press whose advisor is not a choice is refused before anything is launched.
func TestAPressWithAnAdvisorThatIsNoChoiceIsRefused(t *testing.T) {
	t.Parallel()

	for _, chosen := range []any{"claude-opus-5-5", "", 3.0, []any{"opus"}} {
		tab, fakes := aReady(t, setupOpts{open: []*reviews.PR{aRequestedPR()}})

		reply, err := tab.Start(t.Context(), worldRepo, "7", aSonnet, chosen)
		if err != nil {
			t.Fatal(err)
		}

		aEqual(t, "code", reply.Code, 400)
		aEqual(t, "body", aBody(reply),
			`{"error":"advisor must be claude-code, fable, opus or sonnet"}`)
		aEqual(t, "launches", aLaunchCount(fakes), 0)
	}
}

// The advisor's settings go after the model and before --, so the prompt is still the only
// argument after it.
func TestTheReviewArgumentsCarryTheAdvisor(t *testing.T) {
	t.Parallel()

	pattern := regexp.MustCompile(`^pr-acme-widgets-7$`)
	rows := []struct {
		choice string
		want   []string
	}{
		{choice: "claude-code", want: []string{
			"--name", "pr-acme-widgets-7", "--model", "sonnet", "--",
		}},
		{choice: "fable", want: []string{
			"--name", "pr-acme-widgets-7", "--model", "sonnet",
			"--settings", `{"advisorModel":"fable"}`, "--",
		}},
	}

	for _, row := range rows {
		t.Run(row.choice, func(t *testing.T) {
			t.Parallel()

			args, err := reviews.ClaudeArgs("pr-acme-widgets-7", "sonnet", row.choice, pattern, nil)
			if err != nil {
				t.Fatal(err)
			}

			start := slices.Index(args, "--name")
			if start < 0 {
				t.Fatalf("no --name in %q", args)
			}

			got := args[start : len(args)-1]
			if !slices.Equal(got, row.want) {
				t.Errorf("args = %q, want %q", got, row.want)
			}
		})
	}
}

func TestTheReviewArgumentsRefuseWhatIsNoAdvisor(t *testing.T) {
	t.Parallel()

	pattern := regexp.MustCompile(`^pr-acme-widgets-7$`)

	_, err := reviews.ClaudeArgs("pr-acme-widgets-7", "sonnet", "haiku", pattern, nil)
	if err == nil {
		t.Fatal("ClaudeArgs took the advisor haiku")
	}
}
