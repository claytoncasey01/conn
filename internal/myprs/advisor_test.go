package myprs_test

import (
	"context"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/settings"
)

// A press starts the round on the advisor it chose, else on the one Settings saved.
func TestARoundStartsTheAdvisorItChoseElseTheSaved(t *testing.T) {
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
			name: "Claude Code's own over the saved one", saved: "sonnet", chosen: "claude-code",
			want: "claude-code",
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			world := newWorld(t, pr())
			world.Scope.Advisor = row.saved
			poller, _ := world.New()
			tick(poller)

			reply := poller.Start(context.Background(), "org/repo", 7.0, "sonnet", row.chosen)
			if reply.Code != 200 {
				t.Fatalf("code = %d (%s), want 200", reply.Code, js.Stringify(reply.Body))
			}

			launched := aLaunched(world)
			if len(launched) != 1 {
				t.Fatalf("launched %d rounds, want 1", len(launched))
			}

			if launched[0].Advisor != row.want {
				t.Errorf("advisor = %q, want %q", launched[0].Advisor, row.want)
			}
		})
	}
}

// A press whose advisor is not a choice is refused before anything is launched.
func TestARoundWithAnAdvisorThatIsNoChoiceIsRefused(t *testing.T) {
	t.Parallel()

	for _, chosen := range []any{"claude-opus-5-5", "", 3.0} {
		world, poller, _ := aPolled(t, pr())

		reply := poller.Start(context.Background(), "org/repo", 7.0, "sonnet", chosen)
		if reply.Code != 400 {
			t.Errorf("%v: code = %d, want 400", chosen, reply.Code)
		}

		want := `{"error":"advisor must be claude-code, fable, opus or sonnet"}`
		if got := js.Stringify(reply.Body); got != want {
			t.Errorf("%v: body = %s, want %s", chosen, got, want)
		}

		if launched := aLaunched(world); len(launched) != 0 {
			t.Errorf("%v: launched %+v, want none", chosen, launched)
		}
	}
}

// The scope carries the sessions' saved advisor.
func TestTheScopeCarriesTheSavedAdvisor(t *testing.T) {
	t.Parallel()

	current := settings.Defaults()
	current.Sessions.Advisor = "fable"

	scope := myprs.ScopeOf(current, false, func(string) bool { return true })
	if scope.Advisor != "fable" {
		t.Errorf("Advisor = %q, want fable", scope.Advisor)
	}
}
