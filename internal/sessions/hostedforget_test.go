package sessions_test

import (
	"slices"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/sessions"
)

// A hosted session a person ends on purpose leaves the list: a stop from its row, whether its
// program still runs or has already ended by itself, and a close from Reviews. One that ends by
// itself stays, ended, until then (TestAHostedSessionThatEndsCanNoLongerBeRepliedTo).
func TestAHostedSessionEndedOnPurposeLeavesTheList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		end  func(t *testing.T, loop *sessions.Loop, rowID string, held *program) sessions.Reply
	}{
		{"stopped from its row while it runs", stopFromRow},
		{"stopped from its row once it has ended", stopEndedRow},
		{"closed from Reviews", closeFromReviews},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := newWorld(t)
			stamp := float64(time.Now().UnixMilli() - 5000)
			hostedID, held := fake.hostedSession(70, "build-7", "idle", "", idleBox, stamp)
			loop := start(t, fake)

			rowOf(t, loop, hostedID, anyRow)

			reply := tt.end(t, loop, hostedID, held)
			if reply.Code != 200 {
				t.Fatalf("end: %d %s", reply.Code, body(reply))
			}

			waitRowGone(t, loop, hostedID)
		})
	}
}

func stopFromRow(t *testing.T, loop *sessions.Loop, rowID string, _ *program) sessions.Reply {
	t.Helper()

	return loop.Stop(t.Context(), rowID, false)
}

func stopEndedRow(t *testing.T, loop *sessions.Loop, rowID string, held *program) sessions.Reply {
	t.Helper()

	held.exit()
	rowWithin(t, loop, rowID, func(row detail) bool { return row.ExitCode != nil }, endShownWithin)

	return loop.Stop(t.Context(), rowID, false)
}

func closeFromReviews(t *testing.T, loop *sessions.Loop, rowID string, _ *program) sessions.Reply {
	t.Helper()

	time.Sleep(testSettle())

	return loop.CloseWorkspace(t.Context(), rowID)
}

// waitRowGone waits, at most endShownWithin, until the snapshot has no row rowID.
func waitRowGone(t *testing.T, loop *sessions.Loop, rowID string) {
	t.Helper()

	deadline := time.Now().Add(endShownWithin)

	for time.Now().Before(deadline) {
		got, _ := read(t, loop)
		if !slices.Contains(ids(got), rowID) {
			return
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("the row %s is still listed", rowID)
}
