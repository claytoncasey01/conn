package reviews_test

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

// launchedFile is a reviews.json whose one PR has launches.
func launchedFile(launches ...*reviews.Launch) *reviews.File {
	file := reviews.EmptyFile()
	file.PRs.Set("acme/widgets#12", &reviews.Entry{Launches: launches})

	return file
}

// openLaunch is a launch of the session resumeName in worktree that still runs.
func openLaunch(worktree string, sessionID *string) *reviews.Launch {
	return &reviews.Launch{
		At: 1, Model: resumeModel, Name: resumeName, HeadSha: "abc", Worktree: worktree,
		SessionID: sessionID, ClosedAt: nil, Clone: "",
	}
}

// A launch resumes a review only as the module launched it, on the model it launched with.
func TestLaunchedIn(t *testing.T) {
	t.Parallel()

	const (
		worktree = "/data/reviews/acme-widgets-12/widgets"
		other    = "7a1e9c2b-5f3d-4e6a-8b7c-998877665544"
	)

	pinned, otherID, closedAt := resumeID, other, 2.0
	closed := openLaunch(worktree, &pinned)
	closed.ClosedAt = &closedAt
	renamed := openLaunch(worktree, nil)
	renamed.Name = "pr-13"

	tests := []struct {
		name      string
		file      *reviews.File
		wantFound bool
	}{
		{"its conversation pinned", launchedFile(openLaunch(worktree, &pinned)), true},
		{"its conversation not pinned yet", launchedFile(openLaunch(worktree, nil)), true},
		{"another conversation pinned", launchedFile(openLaunch(worktree, &otherID)), false},
		{"another worktree", launchedFile(openLaunch(worktree+"-2", nil)), false},
		{"another name", launchedFile(renamed), false},
		{"closed", launchedFile(closed), false},
		{"no launch", reviews.EmptyFile(), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			model, found := reviews.LaunchedIn(tt.file, worktree, resumeName, resumeID)
			if found != tt.wantFound {
				t.Fatalf("found %v, want %v", found, tt.wantFound)
			}

			if !found {
				return
			}

			if model != resumeModel {
				t.Errorf("model %q, want %q", model, resumeModel)
			}
		})
	}
}
