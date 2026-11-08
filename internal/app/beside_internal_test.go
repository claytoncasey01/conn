package app

import (
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

	tests := []struct {
		name    string
		live    []sessions.LiveSession
		records []hosted.Record
		want    bool
	}{
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
