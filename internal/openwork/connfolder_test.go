package openwork_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/claytoncasey01/conn/internal/openwork"
)

// TestTheBriefIsWrittenOnlyIntoAConnFolderOfOurOwn: a worktree whose .conn is a link writes no
// brief, and nothing where the link points; a brief.md that is a link is replaced, never written
// through.
func TestTheBriefIsWrittenOnlyIntoAConnFolderOfOurOwn(t *testing.T) {
	t.Parallel()

	worktree, elsewhere := t.TempDir(), t.TempDir()

	err := os.Symlink(elsewhere, filepath.Join(worktree, ".conn"))
	if err != nil {
		t.Fatal(err)
	}

	slot := &openwork.Leg{Repo: "acme/widgets", Worktree: worktree}
	record := &openwork.Record{Key: "PROJ-1", Repos: []*openwork.Leg{slot}, Layout: "shared"}

	err = openwork.WriteBrief(record, slot)
	if err == nil {
		t.Fatal("the brief was written")
	}

	entries, _ := os.ReadDir(elsewhere)
	if len(entries) != 0 {
		t.Errorf("written where the link points: %v", entries)
	}

	own := t.TempDir()
	target := filepath.Join(t.TempDir(), "target.md")

	err = os.WriteFile(target, []byte("theirs"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.MkdirAll(filepath.Join(own, ".conn"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(target, filepath.Join(own, ".conn", "brief.md"))
	if err != nil {
		t.Fatal(err)
	}

	ownSlot := &openwork.Leg{Repo: "acme/widgets", Worktree: own}

	err = openwork.WriteBrief(&openwork.Record{
		Key: "PROJ-2", Repos: []*openwork.Leg{ownSlot}, Layout: "shared",
	}, ownSlot)
	if err != nil {
		t.Fatal(err)
	}

	theirs, _ := os.ReadFile(target)
	if string(theirs) != "theirs" {
		t.Errorf("the brief was written through a link: %q", theirs)
	}
}
