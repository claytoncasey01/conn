package provided_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/claytoncasey01/conn/internal/provided"
)

func agentItem(name, text string) provided.Item {
	return provided.Item{
		Kind: provided.KindAgent, Name: name,
		Files: []provided.File{{Path: name + ".md", Data: []byte(text)}},
	}
}

func skillItem(files map[string]string) provided.Item {
	item := provided.Item{Kind: provided.KindSkill, Name: "deep-review"}
	for path, text := range files {
		item.Files = append(item.Files, provided.File{Path: path, Data: []byte(text)})
	}

	return item
}

func newLayout(t *testing.T) provided.Layout {
	t.Helper()

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	return provided.Layout{Data: filepath.Join(base, "data"), Claude: filepath.Join(base, "claude")}
}

func mustWrite(t *testing.T, path, text string) {
	t.Helper()

	err := os.MkdirAll(filepath.Dir(path), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(path, []byte(text), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func TestStartLinksAnAgentOnceAndNeverASkill(t *testing.T) {
	t.Parallel()

	layout := newLayout(t)
	items := []provided.Item{
		agentItem("search", "search v1"),
		skillItem(map[string]string{"SKILL.md": "skill v1"}),
	}

	report, err := provided.Start(layout, items, true)
	if err != nil {
		t.Fatal(err)
	}

	if len(report.Linked) != 1 {
		t.Errorf("linked %v, want only search", report.Linked)
	}

	target, err := os.Readlink(filepath.Join(layout.Claude, "agents", "search.md"))
	if err != nil {
		t.Fatalf("the agent is not linked: %v", err)
	}

	if target != filepath.Join(layout.Data, "agents", "search.md") {
		t.Errorf("the agent links to %s, want its copy in the data folder", target)
	}

	_, err = os.Lstat(filepath.Join(layout.Claude, "skills", "deep-review"))
	if !os.IsNotExist(err) {
		t.Errorf("a skill was linked at start (err %v)", err)
	}

	if readFile(t, filepath.Join(layout.Data, "skills", "deep-review", "SKILL.md")) != "skill v1" {
		t.Error("the skill's copy was not written")
	}

	again, err := provided.Start(layout, items, true)
	if err != nil {
		t.Fatal(err)
	}

	if len(again.Linked) != 0 {
		t.Errorf("a second start linked %v, want nothing", again.Linked)
	}
}

func TestStartWithoutLinkingOnlyWritesCopies(t *testing.T) {
	t.Parallel()

	layout := newLayout(t)

	report, err := provided.Start(layout, []provided.Item{agentItem("search", "v1")}, false)
	if err != nil {
		t.Fatal(err)
	}

	if len(report.Linked) != 0 {
		t.Errorf("linked %v, want nothing", report.Linked)
	}

	_, err = os.Lstat(layout.Claude)
	if !os.IsNotExist(err) {
		t.Errorf("Claude's folder was touched (err %v)", err)
	}

	if readFile(t, filepath.Join(layout.Data, "agents", "search.md")) != "v1" {
		t.Error("the copy was not written")
	}
}

func TestStartNeverReplacesWhatIsThere(t *testing.T) {
	t.Parallel()

	layout := newLayout(t)
	mine := filepath.Join(layout.Claude, "agents", "search.md")
	mustWrite(t, mine, "my own search agent")

	report, err := provided.Start(layout, []provided.Item{agentItem("search", "v1")}, true)
	if err != nil {
		t.Fatal(err)
	}

	if len(report.Conflicts) != 1 {
		t.Errorf("conflicts = %v, want search", report.Conflicts)
	}

	if readFile(t, mine) != "my own search agent" {
		t.Error("the user's file was replaced")
	}
}

func TestStartRefreshesCopies(t *testing.T) {
	t.Parallel()

	layout := newLayout(t)
	first := skillItem(map[string]string{"SKILL.md": "v1", "old.md": "old"})
	second := skillItem(map[string]string{"SKILL.md": "v2"})

	_, err := provided.Start(layout, []provided.Item{agentItem("search", "v1"), first}, false)
	if err != nil {
		t.Fatal(err)
	}

	_, err = provided.Start(layout, []provided.Item{agentItem("search", "v2"), second}, false)
	if err != nil {
		t.Fatal(err)
	}

	skill := filepath.Join(layout.Data, "skills", "deep-review")

	tests := []struct{ name, got, want string }{
		{"the agent", readFile(t, filepath.Join(layout.Data, "agents", "search.md")), "v2"},
		{"the skill file", readFile(t, filepath.Join(skill, "SKILL.md")), "v2"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}

	_, err = os.Stat(filepath.Join(skill, "old.md"))
	if !os.IsNotExist(err) {
		t.Errorf("a file the skill no longer has is still in the copy (err %v)", err)
	}
}

func TestStartRemovesOnlyItsOwnStaleLinks(t *testing.T) {
	t.Parallel()

	layout := newLayout(t)
	gone := agentItem("gone", "x")

	_, err := provided.Start(layout, []provided.Item{gone, agentItem("search", "x")}, true)
	if err != nil {
		t.Fatal(err)
	}

	other := filepath.Join(t.TempDir(), "mine.md")
	mustWrite(t, other, "mine")

	err = os.Symlink(other, filepath.Join(layout.Claude, "agents", "mine.md"))
	if err != nil {
		t.Fatal(err)
	}

	report, err := provided.Start(layout, []provided.Item{agentItem("search", "x")}, true)
	if err != nil {
		t.Fatal(err)
	}

	if len(report.Removed) != 1 {
		t.Fatalf("removed %v, want the link to gone.md", report.Removed)
	}

	_, err = os.Lstat(filepath.Join(layout.Claude, "agents", "mine.md"))
	if err != nil {
		t.Errorf("the user's own link was removed: %v", err)
	}
}

func TestAScriptKeepsItsExecutableBit(t *testing.T) {
	t.Parallel()

	layout := newLayout(t)
	skill := skillItem(map[string]string{
		"SKILL.md": "skill", "run.sh": "#!/bin/sh\necho hi\n",
	})

	_, err := provided.Start(layout, []provided.Item{skill}, false)
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(layout.Data, "skills", "deep-review", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("run.sh mode = %v, want the owner's execute bit", info.Mode().Perm())
	}
}
