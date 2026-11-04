package realpath_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/claytoncasey01/conn/internal/realpath"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	folder := filepath.Join(base, "folder")
	link := filepath.Join(base, "link")

	err = os.Mkdir(folder, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(folder, link)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, path, want string
	}{
		{"a folder", folder, folder},
		{"a link to it", link, folder},
		{"with a trailing slash", folder + "/", folder},
		{"through ..", filepath.Join(base, "x", "..", "folder"), folder},
		{"a path that does not exist", filepath.Join(base, "none"), filepath.Join(base, "none")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := realpath.Resolve(tt.path)
			if got != tt.want {
				t.Errorf("Resolve(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
