package advisor_test

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/claytoncasey01/conn/internal/advisor"
)

// shared is testdata/advisor-choices.json, which the page's tests read too, so the page offers
// only the choices the server takes.
type shared struct {
	Default string              `json:"default"`
	Choices []string            `json:"choices"`
	Args    map[string][]string `json:"args"`
}

func readShared(t *testing.T) shared {
	t.Helper()

	data, err := os.ReadFile("../../testdata/advisor-choices.json")
	if err != nil {
		t.Fatal(err)
	}

	var file shared

	err = json.Unmarshal(data, &file)
	if err != nil {
		t.Fatal(err)
	}

	return file
}

func TestTheChoicesAreTheSharedOnes(t *testing.T) {
	t.Parallel()

	file := readShared(t)

	if !slices.Equal(advisor.Choices(), file.Choices) {
		t.Errorf("Choices() = %v, want %v", advisor.Choices(), file.Choices)
	}

	if advisor.Default != file.Default {
		t.Errorf("Default = %q, want %q", advisor.Default, file.Default)
	}
}

func TestEachChoiceAddsItsArguments(t *testing.T) {
	t.Parallel()

	file := readShared(t)

	for _, choice := range file.Choices {
		t.Run(choice, func(t *testing.T) {
			t.Parallel()

			got := advisor.Args(choice)
			if !slices.Equal(got, file.Args[choice]) {
				t.Errorf("Args(%q) = %q, want %q", choice, got, file.Args[choice])
			}
		})
	}
}

func TestOnlyTheChoicesAreChoices(t *testing.T) {
	t.Parallel()

	rows := []struct {
		choice string
		want   bool
	}{
		{choice: "claude-code", want: true},
		{choice: "fable", want: true},
		{choice: "opus", want: true},
		{choice: "sonnet", want: true},
		{choice: "", want: false},
		{choice: "haiku", want: false},
		{choice: "Opus", want: false},
		{choice: "claude-opus-5-5", want: false},
		{choice: `opus","permissions":{"allow":["Bash"]}`, want: false},
	}

	for _, row := range rows {
		t.Run(row.choice, func(t *testing.T) {
			t.Parallel()

			if advisor.IsChoice(row.choice) != row.want {
				t.Errorf("IsChoice(%q) = %v, want %v", row.choice, !row.want, row.want)
			}
		})
	}
}

func TestArgsPanicsOnWhatIsNoChoice(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("Args(\"haiku\") did not panic")
		}
	}()

	advisor.Args("haiku")
}

func TestTheListReadsAsASentence(t *testing.T) {
	t.Parallel()

	want := "claude-code, fable, opus or sonnet"
	if got := advisor.List(); got != want {
		t.Errorf("List() = %q, want %q", got, want)
	}
}

func TestARequestChoosesAnAdvisorOrNone(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name   string
		raw    any
		want   string
		wantOK bool
	}{
		{name: "none chosen", raw: nil, want: "", wantOK: true},
		{name: "a choice", raw: "fable", want: "fable", wantOK: true},
		{name: "Claude Code's own", raw: "claude-code", want: "claude-code", wantOK: true},
		{name: "empty text", raw: "", want: "", wantOK: false},
		{name: "a model id", raw: "claude-opus-5-5", want: "", wantOK: false},
		{name: "a number", raw: 4.0, want: "", wantOK: false},
		{name: "a list", raw: []any{"opus"}, want: "", wantOK: false},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			got, chosen := advisor.Requested(row.raw)
			if got != row.want {
				t.Errorf("Requested(%v) = %q, want %q", row.raw, got, row.want)
			}

			if chosen != row.wantOK {
				t.Errorf("Requested(%v) ok = %v, want %v", row.raw, chosen, row.wantOK)
			}
		})
	}
}

func TestTheSettingsChoiceAppliesWhenNoneIsChosen(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name, chosen, saved, want string
	}{
		{name: "none chosen", chosen: "", saved: "fable", want: "fable"},
		{name: "one chosen", chosen: "opus", saved: "fable", want: "opus"},
		{
			name: "Claude Code's own chosen", chosen: "claude-code", saved: "fable",
			want: "claude-code",
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			if got := advisor.Or(row.chosen, row.saved); got != row.want {
				t.Errorf("Or(%q, %q) = %q, want %q", row.chosen, row.saved, got, row.want)
			}
		})
	}
}
