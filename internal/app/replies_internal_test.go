package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRepliesAreReadOnlyFromAConnFolderOfOurOwn: a replies file in a .conn that is a link (a
// repository can commit one) or that is itself a link is not read, so a secret it points to never
// becomes replies.
func TestRepliesAreReadOnlyFromAConnFolderOfOurOwn(t *testing.T) {
	t.Parallel()

	elsewhere := t.TempDir()

	err := os.WriteFile(filepath.Join(elsewhere, "replies.json"), []byte(`["secret"]`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	linkedFolder := t.TempDir()

	err = os.Symlink(elsewhere, filepath.Join(linkedFolder, ".conn"))
	if err != nil {
		t.Fatal(err)
	}

	linkedFile := t.TempDir()

	err = os.MkdirAll(filepath.Join(linkedFile, ".conn"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(filepath.Join(elsewhere, "replies.json"),
		filepath.Join(linkedFile, ".conn", "replies.json"))
	if err != nil {
		t.Fatal(err)
	}

	for _, worktree := range []string{linkedFolder, linkedFile} {
		_, read := readReplies(filepath.Join(worktree, ".conn", "replies.json"))
		if read {
			t.Errorf("read through a link in %s", worktree)
		}
	}

	own := ownReplies(t)

	text, read := readReplies(filepath.Join(own, ".conn", "replies.json"))
	if !read {
		t.Fatal("our own replies were not read")
	}

	if text != "[]" {
		t.Errorf("text %q", text)
	}
}

// ownReplies is a worktree whose .conn is a folder of ours holding replies.json, [].
func ownReplies(t *testing.T) string {
	t.Helper()

	own := t.TempDir()

	err := os.MkdirAll(filepath.Join(own, ".conn"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(own, ".conn", "replies.json"), []byte(`[]`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return own
}
