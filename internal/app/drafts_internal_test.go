package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

// TestADraftIsReadCappedFromAConnFolderOfOurOwn: a review session's draft file is read as the
// replies file is, through a .conn folder of ours with no link followed, and never past its cap
// plus one byte, so an oversized one is known to be oversized and costs nothing more.
func TestADraftIsReadCappedFromAConnFolderOfOurOwn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		content   string
		linked    bool
		wantAt    bool
		wantBytes int
	}{
		{"a draft of ours", `{}`, false, true, 2},
		{
			"an oversized one", strings.Repeat("x", reviews.MaxDraftFileBytes+10), false, true,
			reviews.MaxDraftFileBytes + 1,
		},
		{"through a linked .conn", `{}`, true, false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			worktree := draftWorktree(t, tt.content, tt.linked)

			if read := draftAt(worktree) != nil; read != tt.wantAt {
				t.Errorf("a time read: %v, want %v", read, tt.wantAt)
			}

			text, _ := readDraft(filepath.Join(worktree, ".conn", reviews.DraftFile))
			if len(text) != tt.wantBytes {
				t.Errorf("read %d bytes, want %d", len(text), tt.wantBytes)
			}
		})
	}
}

// draftWorktree is a worktree holding content as its draft file, in a .conn folder of its own or
// in one linked to elsewhere.
func draftWorktree(t *testing.T, content string, linked bool) string {
	t.Helper()

	worktree, folder := t.TempDir(), t.TempDir()

	if linked {
		mustMake(t, os.Symlink(folder, filepath.Join(worktree, ".conn")))
	} else {
		folder = filepath.Join(worktree, ".conn")
		mustMake(t, os.Mkdir(folder, 0o700))
	}

	mustMake(t, os.WriteFile(filepath.Join(folder, reviews.DraftFile), []byte(content), 0o600))

	return worktree
}
