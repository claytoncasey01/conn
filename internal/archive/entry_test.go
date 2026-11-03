package archive_test

import (
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/archive"
	"github.com/claytoncasey01/conn/internal/sessions"
)

func TestNameFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		registry, title, prompt, want string
	}{
		{"named", "title", "prompt", "named"},
		{"", "title", "prompt", "title"},
		{"", "", "  a \n prompt  ", "a prompt"},
		{"", "", strings.Repeat("x", 70), strings.Repeat("x", 59) + "…"},
		{"", "", "   ", "0f8fad5b"},
	}

	for _, tt := range tests {
		got := archive.NameFor(tt.registry, tt.title, tt.prompt,
			"0f8fad5b-d9cb-469f-a165-70867728950e")
		if got != tt.want {
			t.Errorf("NameFor(%q, %q, %q) = %q", tt.registry, tt.title, tt.prompt, got)
		}
	}
}

func TestOutcome(t *testing.T) {
	t.Parallel()

	if archive.Outcome(sessions.KindAdhoc, &sessions.RunFacts{State: "pr_open", PRState: ""}) !=
		"ended" {
		t.Error("an ad hoc session has a run outcome")
	}

	if archive.Outcome(sessions.KindBuild, &sessions.RunFacts{State: "pr_open", PRState: ""}) !=
		"PR opened" {
		t.Error("an open PR")
	}

	if archive.Outcome(sessions.KindBuild, &sessions.RunFacts{State: "x", PRState: "merged"}) !=
		"merged" {
		t.Error("a merged PR")
	}

	if archive.Outcome(sessions.KindBuild, nil) != "ended" {
		t.Error("no run")
	}
}
