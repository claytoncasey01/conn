package reviews_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

// TestThePromptIsWrittenOnlyIntoAConnFolderOfOurOwn: a worktree whose .conn is a link (a
// repository can commit one) or a file gets no prompt, and nothing is written where the link
// points.
func TestThePromptIsWrittenOnlyIntoAConnFolderOfOurOwn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		make func(t *testing.T, conn, elsewhere string)
		// says is in the refusal.
		says string
	}{
		{"a link to another folder", func(t *testing.T, conn, elsewhere string) {
			t.Helper()

			err := os.Symlink(elsewhere, conn)
			if err != nil {
				t.Fatal(err)
			}
		}, "is a link"},
		{"a file", func(t *testing.T, conn, _ string) {
			t.Helper()

			err := os.WriteFile(conn, []byte("x"), 0o600)
			if err != nil {
				t.Fatal(err)
			}
		}, "not a directory"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			worktree, elsewhere := t.TempDir(), t.TempDir()
			conn := filepath.Join(worktree, ".conn")
			tt.make(t, conn, elsewhere)

			err := reviews.WriteLaunchFile(filepath.Join(conn, "prompt.md"), "the prompt", false)
			if err == nil {
				t.Fatal("the prompt was written")
			}

			if !strings.Contains(err.Error(), tt.says) {
				t.Errorf("error %q does not say %q", err, tt.says)
			}

			_, err = os.Stat(filepath.Join(elsewhere, "prompt.md"))
			if err == nil {
				t.Error("the prompt was written where the link points")
			}
		})
	}

	worktree := t.TempDir()

	err := reviews.WriteLaunchFile(filepath.Join(worktree, ".conn", "prompt.md"), "p", false)
	if err != nil {
		t.Fatalf("a folder of our own: %v", err)
	}
}
