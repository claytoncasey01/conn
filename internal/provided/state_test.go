package provided_test

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/provided"
)

func TestJudge(t *testing.T) {
	t.Parallel()

	const want = "/data/skills/x"

	tests := []struct {
		name  string
		found provided.Found
		state provided.State
	}{
		{"nothing there", provided.Found{}, provided.StateOff},
		{"a file", provided.Found{Exists: true}, provided.StateConflict},
		{
			"a link to the copy",
			provided.Found{Exists: true, IsLink: true, Target: want},
			provided.StateOn,
		},
		{
			"a link to the copy, unclean",
			provided.Found{Exists: true, IsLink: true, Target: "/data/skills/./x/"},
			provided.StateOn,
		},
		{
			"a link somewhere else",
			provided.Found{Exists: true, IsLink: true, Target: "/elsewhere/x"},
			provided.StateConflict,
		},
		{
			"a link into another Conn folder",
			provided.Found{Exists: true, IsLink: true, Target: "/other-data/skills/x"},
			provided.StateConflict,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := provided.Judge(tt.found, want)
			if got != tt.state {
				t.Errorf("Judge = %q, want %q", got, tt.state)
			}
		})
	}
}

func TestInside(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, folder, target string
		want                 bool
	}{
		{"a file in it", "/d/agents", "/d/agents/a.md", true},
		{"deeper", "/d/agents", "/d/agents/x/a.md", true},
		{"the folder itself", "/d/agents", "/d/agents", false},
		{"a sibling", "/d/agents", "/d/skills/a.md", false},
		{"a folder with the same prefix", "/d/agents", "/d/agents-old/a.md", false},
		{"a name that starts with dots", "/d/agents", "/d/agents/..a.md", true},
		{"outside", "/d/agents", "/elsewhere/a.md", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := provided.Inside(tt.folder, tt.target)
			if got != tt.want {
				t.Errorf("Inside(%q, %q) = %v, want %v", tt.folder, tt.target, got, tt.want)
			}
		})
	}
}

func TestAutoLinks(t *testing.T) {
	t.Parallel()

	agent := func(name string) provided.Item {
		return provided.Item{Kind: provided.KindAgent, Name: name}
	}
	skill := provided.Item{Kind: provided.KindSkill, Name: "deep-review"}

	seen := []provided.Seen{
		{Item: agent("off"), State: provided.StateOff},
		{Item: agent("on"), State: provided.StateOn},
		{Item: agent("conflict"), State: provided.StateConflict},
		{Item: skill, State: provided.StateOff},
	}

	links := provided.AutoLinks(seen)
	if len(links) != 1 {
		t.Fatalf("AutoLinks = %v, want only the agent with nothing in its place", links)
	}

	if links[0].Name != "off" {
		t.Errorf("linked %q, want off", links[0].Name)
	}
}

func TestStaleLinks(t *testing.T) {
	t.Parallel()

	layout := provided.Layout{Data: "/d", Claude: "/c"}
	items := []provided.Item{{Kind: provided.KindAgent, Name: "search"}}
	entries := []provided.LinkEntry{
		{Path: "/c/agents/search.md", Name: "search.md", Target: "/d/agents/search.md"},
		{Path: "/c/agents/gone.md", Name: "gone.md", Target: "/d/agents/gone.md"},
		{Path: "/c/agents/mine.md", Name: "mine.md", Target: "/home/me/mine.md"},
		{Path: "/c/agents/dev.md", Name: "dev.md", Target: "/dev-data/agents/dev.md"},
	}

	got := provided.StaleLinks(layout, provided.KindAgent, entries, items)
	if len(got) != 1 {
		t.Fatalf("StaleLinks = %v, want only the stale link of Conn's", got)
	}

	if got[0] != "/c/agents/gone.md" {
		t.Errorf("stale = %q, want /c/agents/gone.md", got[0])
	}
}
