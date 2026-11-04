package provided_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/provided"
)

func TestRouteOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want provided.RouteKind
		ours bool
	}{
		{"/api/provided", provided.RouteList, true},
		{"/api/provided/install", provided.RouteInstall, true},
		{"/api/provided/remove", provided.RouteRemove, true},
		{"/api/provided/other", provided.RouteNone, true},
		{"/api/providedx", provided.RouteNone, false},
		{"/api/settings", provided.RouteNone, false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()

			got, ours := provided.RouteOf(tt.path)
			if ours != tt.ours {
				t.Errorf("ours = %v, want %v", ours, tt.ours)
			}

			if got != tt.want {
				t.Errorf("route = %v, want %v", got, tt.want)
			}
		})
	}
}

func named(name string) *js.Object {
	body := js.NewObject()
	body.Set("name", name)

	return body
}

func newAPI(t *testing.T, writable bool) (*provided.API, provided.Layout) {
	t.Helper()

	layout := newLayout(t)
	items := []provided.Item{
		agentItem("search", "agent"),
		skillItem(map[string]string{"SKILL.md": "skill"}),
	}

	_, err := provided.Start(layout, items, false)
	if err != nil {
		t.Fatal(err)
	}

	return provided.New(provided.Deps{Layout: layout, Items: items, Writable: writable}), layout
}

func answerOf(t *testing.T, result provided.Result) provided.Answer {
	t.Helper()

	answer, ok := result.Body.(provided.Answer)
	if !ok {
		t.Fatalf("body = %+v (code %d), want an answer", result.Body, result.Code)
	}

	return answer
}

func TestInstallAndRemoveASkill(t *testing.T) {
	t.Parallel()

	api, layout := newAPI(t, true)
	link := filepath.Join(layout.Claude, "skills", "deep-review")

	listed := answerOf(t, api.Handle(provided.RouteList, nil))
	if listed.Skills[0].State != provided.StateOff {
		t.Fatalf("a fresh skill is %q, want notInstalled", listed.Skills[0].State)
	}

	installed := api.Handle(provided.RouteInstall, named("deep-review"))
	if installed.Code != http.StatusOK {
		t.Fatalf("install = %d %+v", installed.Code, installed.Body)
	}

	if answerOf(t, installed).Skills[0].State != provided.StateOn {
		t.Error("the answer to an install does not say installed")
	}

	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("not linked: %v", err)
	}

	if target != filepath.Join(layout.Data, "skills", "deep-review") {
		t.Errorf("links to %s, want the copy in the data folder", target)
	}

	again := api.Handle(provided.RouteInstall, named("deep-review"))
	if again.Code != http.StatusOK {
		t.Errorf("installing twice = %d, want 200", again.Code)
	}

	removed := api.Handle(provided.RouteRemove, named("deep-review"))
	if removed.Code != http.StatusOK {
		t.Fatalf("remove = %d %+v", removed.Code, removed.Body)
	}

	_, err = os.Lstat(link)
	if !os.IsNotExist(err) {
		t.Errorf("the link is still there (err %v)", err)
	}
}

func TestInstallAndRemoveLeaveWhatIsTheUsersAlone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		route provided.RouteKind
	}{
		{"install", provided.RouteInstall},
		{"remove", provided.RouteRemove},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			api, layout := newAPI(t, true)
			mine := filepath.Join(layout.Claude, "skills", "deep-review", "SKILL.md")
			mustWrite(t, mine, "my own deep review")

			result := api.Handle(tt.route, named("deep-review"))
			if result.Code != http.StatusConflict {
				t.Errorf("code = %d, want 409", result.Code)
			}

			if readFile(t, mine) != "my own deep review" {
				t.Error("the user's skill was changed")
			}

			listed := answerOf(t, api.Handle(provided.RouteList, nil))
			if listed.Skills[0].State != provided.StateConflict {
				t.Errorf("state = %q, want conflict", listed.Skills[0].State)
			}

			if listed.Skills[0].Note == "" {
				t.Error("a conflict has no note saying what is in the way")
			}
		})
	}
}

func TestInstallRefusals(t *testing.T) {
	t.Parallel()

	api, _ := newAPI(t, true)
	dry, _ := newAPI(t, false)

	tests := []struct {
		name string
		api  *provided.API
		body *js.Object
		code int
	}{
		{"an unknown skill", api, named("nope"), http.StatusNotFound},
		{"an agent is not a skill", api, named("search"), http.StatusNotFound},
		{"no name", api, js.NewObject(), http.StatusNotFound},
		{"a dry run", dry, named("deep-review"), http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := tt.api.Handle(provided.RouteInstall, tt.body)
			if result.Code != tt.code {
				t.Errorf("code = %d, want %d", result.Code, tt.code)
			}
		})
	}
}

func TestTheListShowsAgentsAndSkillsFromTheDisk(t *testing.T) {
	t.Parallel()

	api, layout := newAPI(t, true)

	_, err := provided.Start(layout, []provided.Item{agentItem("search", "agent")}, true)
	if err != nil {
		t.Fatal(err)
	}

	listed := answerOf(t, api.Handle(provided.RouteList, nil))
	if len(listed.Agents) != 1 {
		t.Fatalf("agents = %v", listed.Agents)
	}

	if listed.Agents[0].State != provided.StateOn {
		t.Errorf("the linked agent is %q, want installed", listed.Agents[0].State)
	}

	if listed.LinkDir != layout.Claude {
		t.Errorf("LinkDir = %q, want %q", listed.LinkDir, layout.Claude)
	}
}
