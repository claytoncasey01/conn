package myprs_test

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/myprs"
)

const (
	launchedWorktree = "/data/myprs/acme-widgets-12/widgets"
	launchedName     = "self-widgets-12"
	launchedModel    = "opus"
)

// selfLaunch is a self-review round of launchedName in launchedWorktree that still runs.
func selfLaunch() *myprs.Launch {
	return &myprs.Launch{
		At: 1, Model: launchedModel, Name: launchedName, Worktree: launchedWorktree,
		HeadSha: "abc", Round: 1, Armed: false, AskedSha: nil, AskedAt: nil, ClosedAt: nil,
		AskTry: nil, Manual: false,
	}
}

func selfFile(launch *myprs.Launch) *myprs.File {
	file := myprs.EmptyFile()
	file.PRs.Set("acme/widgets#12", &myprs.Entry{Launches: []*myprs.Launch{launch}})

	return file
}

// A launch resumes a self-review only as My PRs launched it, on the model it launched with.
func TestLaunchedIn(t *testing.T) {
	t.Parallel()

	closedAt := 2.0
	closed, manual, elsewhere, renamed := selfLaunch(), selfLaunch(), selfLaunch(), selfLaunch()
	closed.ClosedAt = &closedAt
	manual.Manual = true
	elsewhere.Worktree += "-2"
	renamed.Name = "self-widgets-13"

	tests := []struct {
		name      string
		file      *myprs.File
		wantFound bool
	}{
		{"a round that runs", selfFile(selfLaunch()), true},
		{"closed", selfFile(closed), false},
		{"pasted into a terminal by hand", selfFile(manual), false},
		{"another worktree", selfFile(elsewhere), false},
		{"another name", selfFile(renamed), false},
		{"no launch", myprs.EmptyFile(), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			model, found := myprs.LaunchedIn(tt.file, launchedWorktree, launchedName)
			if found != tt.wantFound {
				t.Fatalf("found %v, want %v", found, tt.wantFound)
			}

			if !found {
				return
			}

			if model != launchedModel {
				t.Errorf("model %q, want %q", model, launchedModel)
			}
		})
	}
}
