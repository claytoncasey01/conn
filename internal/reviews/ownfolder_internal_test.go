package reviews

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestAFolderIsOursOnlyWhenEveryFactSaysSo: a link, something other than a folder, or a folder
// another user owns is refused, each for its own reason.
func TestAFolderIsOursOnlyWhenEveryFactSaysSo(t *testing.T) {
	t.Parallel()

	const own, other = 501, 502

	tests := []struct {
		name    string
		facts   folderFacts
		wantErr error
	}{
		{"a folder of our own", folderFacts{link: false, folder: true, owner: own}, nil},
		{"a link", folderFacts{link: true, folder: false, owner: own}, ErrNotOwnFolder},
		{"a file", folderFacts{link: false, folder: false, owner: own}, ErrNotOwnFolder},
		{"another user's", folderFacts{link: false, folder: true, owner: other}, ErrNotOwnFolder},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := checkFolderFacts("/w/.conn", tt.facts, own)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestTheFolderIsHeldByItsDescriptor: the steps after the folder's first look work on the folder
// they opened, never on a path looked up again, so a link put in its place meanwhile is refused
// by those steps on their own: the folder's open, a write and a read.
func TestTheFolderIsHeldByItsDescriptor(t *testing.T) {
	t.Parallel()

	worktree, elsewhere := t.TempDir(), t.TempDir()
	conn := filepath.Join(worktree, ".conn")

	err := os.WriteFile(filepath.Join(elsewhere, "secret.md"), []byte("secret"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(elsewhere, conn)
	if err != nil {
		t.Fatal(err)
	}

	_, err = openOwnFolder(conn)
	if !errors.Is(err, ErrNotOwnFolder) {
		t.Errorf("open: %v", err)
	}

	err = writeNewPrivate(filepath.Join(conn, "prompt.md"), "the prompt")
	if !errors.Is(err, ErrNotOwnFolder) {
		t.Errorf("write: %v", err)
	}

	_, err = os.Lstat(filepath.Join(elsewhere, "prompt.md"))
	if err == nil {
		t.Error("the prompt was written where the link points")
	}

	_, err = openInFolder(conn, "secret.md")
	if !errors.Is(err, ErrNotOwnFolder) {
		t.Errorf("read: %v", err)
	}
}

// TestAFileInTheFolderIsOpenedOnlyIfItIsNoLink: a link at the file is refused, a missing file
// says it does not exist, and a regular file opens.
func TestAFileInTheFolderIsOpenedOnlyIfItIsNoLink(t *testing.T) {
	t.Parallel()

	conn, elsewhere := t.TempDir(), t.TempDir()

	err := os.WriteFile(filepath.Join(elsewhere, "secret.md"), []byte("secret"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(filepath.Join(elsewhere, "secret.md"), filepath.Join(conn, "linked.md"))
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(conn, "own.md"), []byte("own"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	_, err = OpenInOwnFolder(conn, "linked.md")
	if err == nil {
		t.Error("a link was opened")
	}

	_, err = OpenInOwnFolder(conn, "missing.md")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}

	file, err := OpenInOwnFolder(conn, "own.md")
	if err != nil {
		t.Fatalf("own: %v", err)
	}

	_ = file.Close()
}

// TestAFileIsRemovedOnlyThroughAFolderOfOurOwn: a file or a link in the folder is removed (what
// the link points to is kept), a missing one is nothing to do, and a folder that is a link is
// refused with nothing removed where it points.
func TestAFileIsRemovedOnlyThroughAFolderOfOurOwn(t *testing.T) {
	t.Parallel()

	conn, elsewhere := t.TempDir(), t.TempDir()
	target := filepath.Join(elsewhere, "replies.json")
	mustWrite(t, target, "kept")
	mustWrite(t, filepath.Join(conn, "own.json"), "own")

	err := os.Symlink(target, filepath.Join(conn, "linked.json"))
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"own.json", "linked.json", "missing.json"} {
		err = UnlinkInOwnFolder(conn, name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}

		_, err = os.Lstat(filepath.Join(conn, name))
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s is still there", name)
		}
	}

	linked := filepath.Join(t.TempDir(), ".conn")

	err = os.Symlink(elsewhere, linked)
	if err != nil {
		t.Fatal(err)
	}

	err = UnlinkInOwnFolder(linked, "replies.json")
	if !errors.Is(err, ErrNotOwnFolder) {
		t.Errorf("through a linked folder: %v", err)
	}

	kept, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}

	if string(kept) != "kept" {
		t.Errorf("the target is now %q", kept)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()

	err := os.WriteFile(path, []byte(content), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}
