package app

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/hosted"
	"github.com/claytoncasey01/conn/internal/sessions"
)

// Reviews and My PRs read the launch's own settle: no "gone" before the resume is done, and
// from when it was done after.
func TestReviewsAndMyPrsReadTheLaunchsSettle(t *testing.T) {
	t.Parallel()

	const doneAt = 1_800_000_000_000

	tests := []struct {
		name    string
		settled func(parts reviewsParts, live *sessions.Loop) func() (float64, bool)
	}{
		{"Reviews", func(parts reviewsParts, live *sessions.Loop) func() (float64, bool) {
			return reviewsDeps(parts, live).Settled
		}},
		{"My PRs", func(parts reviewsParts, live *sessions.Loop) func() (float64, bool) {
			return myPrsDeps(parts, live).Settled
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settle := newResumeSettle()
			parts := reviewsParts{
				cfg:     config.FromEnv(func(string) (string, bool) { return "", false }),
				settled: settle.settled,
			}
			settled := tt.settled(parts, sessions.NewLoop(sessions.Deps{}))

			if _, done := settled(); done {
				t.Error("settled before the resume was done")
			}

			settle.finish(doneAt)

			settledAt, done := settled()
			if !done {
				t.Fatal("not settled after the resume was done")
			}

			if settledAt != doneAt {
				t.Errorf("settled at %v, want %v", settledAt, float64(doneAt))
			}
		})
	}
}

// My PRs reads which folders the last quit's record named: a round there may be resuming.
func TestMyPrsReadsTheFoldersTheRecordNamed(t *testing.T) {
	t.Parallel()

	settle := newResumeSettle()
	settle.recordedFrom([]hosted.Record{resumeRecord()})

	parts := reviewsParts{
		cfg:      config.FromEnv(func(string) (string, bool) { return "", false }),
		settled:  settle.settled,
		recorded: settle.recordedIn,
	}
	recorded := myPrsDeps(parts, sessions.NewLoop(sessions.Deps{})).Recorded

	if !recorded(resumeRecord().Dir) {
		t.Error("the record's folder does not read as recorded")
	}

	if recorded("/elsewhere") {
		t.Error("a folder the record did not name reads as recorded")
	}
}
