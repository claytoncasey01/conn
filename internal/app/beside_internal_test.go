package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/claytoncasey01/conn/internal/hosted"
	"github.com/claytoncasey01/conn/internal/sessions"
)

// A launch resumes no review beside a session of its name in any folder, or one in its worktree:
// the person's own claude, or one this launch has started already.
func TestBesideIn(t *testing.T) {
	t.Parallel()

	const (
		name     = "pr-12"
		worktree = "/data/reviews/pr-12/widgets"
	)

	tests := besideRows(name, worktree)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := besideIn(tt.live, tt.records, name, worktree)
			if got != tt.want {
				t.Errorf("beside %v, want %v", got, tt.want)
			}
		})
	}
}

// besideRow is one case of besideIn: what runs, what this launch started, and the answer.
type besideRow struct {
	name    string
	live    []sessions.LiveSession
	records []hosted.Record
	want    bool
}

func besideRows(name, worktree string) []besideRow {
	return []besideRow{
		{"nothing runs", nil, nil, false},
		{
			"another session elsewhere",
			[]sessions.LiveSession{{Name: "pr-13", Cwd: "/elsewhere"}},
			nil, false,
		},
		{
			"a session of its name elsewhere",
			[]sessions.LiveSession{{Name: name, Cwd: "/x"}},
			nil,
			true,
		},
		{
			"a session in its worktree",
			[]sessions.LiveSession{{Name: "", Cwd: worktree}},
			nil,
			true,
		},
		{"a session inside its worktree", []sessions.LiveSession{{
			Name: "", Cwd: worktree + "/src",
		}}, nil, true},
		{"a session in a folder beside it", []sessions.LiveSession{{
			Name: "", Cwd: worktree + "-2",
		}}, nil, false},
		{
			"one this launch started there", nil,
			[]hosted.Record{{Name: "pr-99", Dir: worktree}},
			true,
		},
		{
			"one this launch started of its name", nil,
			[]hosted.Record{{Name: name, Dir: "/x"}},
			true,
		},
	}
}

// A data folder under a link: claude's working folder is the physical one, the record's the
// linked one. Both are resolved before they are compared, so the person's claude is seen.
func TestBesideInResolvesLinks(t *testing.T) {
	t.Parallel()

	physical := t.TempDir()
	worktree := filepath.Join(physical, "reviews", "pr-12", "widgets")

	err := os.MkdirAll(worktree, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	linked := filepath.Join(t.TempDir(), "data")

	err = os.Symlink(physical, linked)
	if err != nil {
		t.Fatal(err)
	}

	recorded := filepath.Join(linked, "reviews", "pr-12", "widgets")
	live := []sessions.LiveSession{{Name: "", Cwd: worktree}}

	if !besideIn(live, nil, "pr-12", recorded) {
		t.Error("a session in the worktree through a link was not seen")
	}

	if !besideIn(nil, []hosted.Record{{Name: "", Dir: worktree}}, "pr-12", recorded) {
		t.Error("a session this launch started in the worktree through a link was not seen")
	}
}
