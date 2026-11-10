package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The icon is written whole over the one before, in a folder made for it that only this user
// can open, with nothing left beside it.
func TestWriteIconReplacesTheIconWhole(t *testing.T) {
	t.Parallel()

	folder := filepath.Join(t.TempDir(), "io.example.conn")
	path := filepath.Join(folder, "DockIcon.png")

	for _, icon := range []string{"the first icon", "the second"} {
		err := writeIcon(path, []byte(icon))
		if err != nil {
			t.Fatalf("writeIcon: %v", err)
		}

		written, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading the icon: %v", err)
		}

		if string(written) != icon {
			t.Errorf("the icon holds %q, want %q", written, icon)
		}
	}

	entries, err := os.ReadDir(folder)
	if err != nil {
		t.Fatalf("reading the folder: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("the folder holds %d files, want only the icon", len(entries))
	}

	info, err := os.Stat(folder)
	if err != nil {
		t.Fatalf("reading the folder: %v", err)
	}

	if info.Mode().Perm() != iconFolderMode {
		t.Errorf("the folder's mode is %v, want %v", info.Mode().Perm(),
			os.FileMode(iconFolderMode))
	}
}
