package reviews_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

// Bun's worktreeClone, real and within (reviews.ts), on folders made as git makes them: the
// worktree's .git is a pointer file, and the gitdir's commondir leads back to <clone>/.git.

// dWorktreeOf makes a clone folder with a gitdir for one worktree, and the worktree with pointer
// as its .git file; commondir is written when not nil. It answers the clone and the worktree.
func dWorktreeOf(t *testing.T, pointer func(gitDir string) string, commondir *string) (
	string, string,
) {
	t.Helper()

	root := t.TempDir()
	clone := filepath.Join(root, "clone")
	gitDir := filepath.Join(clone, ".git", "worktrees", "wt")
	worktree := filepath.Join(root, "wt")

	dMust(t, os.MkdirAll(gitDir, 0o755))
	dMust(t, os.MkdirAll(worktree, 0o755))

	if commondir != nil {
		dMust(t, os.WriteFile(filepath.Join(gitDir, "commondir"), []byte(*commondir), 0o644))
	}

	if pointer != nil {
		dMust(t, os.WriteFile(filepath.Join(worktree, ".git"), []byte(pointer(gitDir)), 0o644))
	}

	return clone, worktree
}

// TestDWorktreeCloneFollowsThePointer: the clone a worktree was made from, or "" when the
// folder is not such a worktree.
func TestDWorktreeCloneFollowsThePointer(t *testing.T) {
	t.Parallel()

	parent := "../..\n"
	empty := ""
	other := "../../../elsewhere\n"
	absolute := func(gitDir string) string { return "gitdir: " + gitDir + "\n" }

	tests := []struct {
		name      string
		pointer   func(gitDir string) string
		commondir *string
		found     bool
	}{
		{"an absolute gitdir", absolute, &parent, true},
		{
			"a relative gitdir",
			func(string) string { return "gitdir: ../clone/.git/worktrees/wt\n" }, &parent, true,
		},
		{
			"CRLF and spaces",
			func(gitDir string) string { return "gitdir:   " + gitDir + "  \r\nx\n" },
			&parent, true,
		},
		{"no gitdir line", func(string) string { return "not a pointer\n" }, &parent, false},
		{"no commondir", absolute, nil, false},
		{"an empty commondir", absolute, &empty, false},
		{"a commondir that is not a .git", absolute, &other, false},
		{"no .git", nil, &parent, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clone, worktree := dWorktreeOf(t, tt.pointer, tt.commondir)

			want := ""
			if tt.found {
				want = clone
			}

			if got := reviews.WorktreeClone(worktree); got != want {
				t.Errorf("WorktreeClone = %q, want %q", got, want)
			}
		})
	}
}

// TestDWorktreeCloneOfAFolderGitDirIsNone: a clone's own .git folder is no pointer.
func TestDWorktreeCloneOfAFolderGitDirIsNone(t *testing.T) {
	t.Parallel()

	clone, _ := dWorktreeOf(t, nil, nil)

	if got := reviews.WorktreeClone(clone); got != "" {
		t.Errorf("WorktreeClone of a clone = %q", got)
	}
}

// TestDRealResolvesLinksAndKeepsTheMissingTail: links are followed; a part that does not exist
// is kept as written after the deepest part that does.
func TestDRealResolvesLinksAndKeepsTheMissingTail(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "link")

	dMust(t, os.MkdirAll(target, 0o755))
	dMust(t, os.Symlink(target, link))

	resolved, err := filepath.EvalSymlinks(target)
	dMust(t, err)

	tests := []struct{ path, want string }{
		{link, resolved},
		{filepath.Join(link, "missing", "x"), filepath.Join(resolved, "missing", "x")},
		{"/", "/"},
	}

	for _, tt := range tests {
		if got := reviews.Real(tt.path); got != tt.want {
			t.Errorf("Real(%s) = %s, want %s", tt.path, got, tt.want)
		}
	}
}

// TestDWithinIsTheFolderOrBelowIt: never a sibling that shares its name's start.
func TestDWithinIsTheFolderOrBelowIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		cwd  string
		want bool
	}{
		{"/data/reviews/pr-1", true},
		{"/data/reviews/pr-1/widgets", true},
		{"/data/reviews/pr-10", false},
		{"/data/reviews", false},
	}

	for _, tt := range tests {
		if got := reviews.Within(tt.cwd, "/data/reviews/pr-1"); got != tt.want {
			t.Errorf("Within(%s) = %v", tt.cwd, got)
		}
	}
}
