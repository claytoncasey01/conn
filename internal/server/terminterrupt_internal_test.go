package server

import (
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
)

// TestALateReleaseKeepsALaterClaim: a press whose write outlasted the window gives back only its
// own claim, never the claim a later press made in its place.
func TestALateReleaseKeepsALaterClaim(t *testing.T) {
	t.Parallel()

	now := time.Unix(1000, 0)
	record := newInterrupts(func() time.Time { return now })

	const sessionID = pty.ID("0123456789abcdef0123456789abcdef")

	first, claimed := record.claim(sessionID)
	if !claimed {
		t.Fatal("the first press was refused")
	}

	now = now.Add(interruptRepeatWindow)

	_, claimed = record.claim(sessionID)
	if !claimed {
		t.Fatal("a press after the window was refused")
	}

	record.release(sessionID, first)

	_, claimed = record.claim(sessionID)
	if claimed {
		t.Error("the first press's late release gave back the second press's claim")
	}
}
