package app

import (
	"os"
	"path/filepath"
	"syscall"
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

// TestARepliesTimeIsReadOnlyFromARegularFileOfOurOwn: the time a replies file was written ends a
// round, so it is read only from a regular file in a .conn folder of ours, never through a link
// a repository committed (which would give the time of whatever it points to).
func TestARepliesTimeIsReadOnlyFromARegularFileOfOurOwn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		make func(t *testing.T, worktree, elsewhere string)
		want bool
	}{
		{"a regular file of ours", func(t *testing.T, worktree, _ string) {
			t.Helper()
			mustMake(t, os.Mkdir(filepath.Join(worktree, ".conn"), 0o700))
			mustMake(t, os.WriteFile(filepath.Join(worktree, ".conn", "replies.json"),
				[]byte(`[]`), 0o600))
		}, true},
		{"no file", func(t *testing.T, worktree, _ string) {
			t.Helper()
			mustMake(t, os.Mkdir(filepath.Join(worktree, ".conn"), 0o700))
		}, false},
		{"the file is a link", func(t *testing.T, worktree, elsewhere string) {
			t.Helper()
			mustMake(t, os.Mkdir(filepath.Join(worktree, ".conn"), 0o700))
			mustMake(t, os.Symlink(filepath.Join(elsewhere, "replies.json"),
				filepath.Join(worktree, ".conn", "replies.json")))
		}, false},
		{".conn is a link", func(t *testing.T, worktree, elsewhere string) {
			t.Helper()
			mustMake(t, os.Symlink(elsewhere, filepath.Join(worktree, ".conn")))
		}, false},
		{"the file is a pipe", func(t *testing.T, worktree, _ string) {
			t.Helper()
			mustMake(t, os.Mkdir(filepath.Join(worktree, ".conn"), 0o700))
			mustMake(t, syscall.Mkfifo(filepath.Join(worktree, ".conn", "replies.json"), 0o600))
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			worktree, elsewhere := t.TempDir(), t.TempDir()
			mustMake(t, os.WriteFile(filepath.Join(elsewhere, "replies.json"), []byte(`[]`),
				0o600))
			tt.make(t, worktree, elsewhere)

			at := repliesAt(worktree)
			if read := at != nil; read != tt.want {
				t.Errorf("a time read: %v, want %v", read, tt.want)
			}
		})
	}
}

func mustMake(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}
