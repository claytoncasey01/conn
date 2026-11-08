package app_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/app"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

const sessionThree = "9b2d4c6e-8f10-4a3b-8c5d-6e7f8091a2b3"

// A quit that comes while the launch still resumes keeps every record: the session it started
// ran at the quit, so it stays recorded, and the records it had not reached are kept as they
// were. The next launch resumes all of them.
func TestAQuitDuringTheResumeKeepsEveryRecord(t *testing.T) {
	t.Parallel()

	world := newLifeWorld(t, "", sessionOne, sessionTwo, sessionThree)
	writeRecords(t, world.data(), world.record(0), world.record(1), world.record(2))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	first := newSpawns(world.data())
	// The person quits as the first resumed session starts.
	first.atSpawn = cancel

	server, err := app.StartWith(ctx, lookupFrom(world.env), io.Discard, first.host())
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-server.Done():
	case <-time.After(testtime.Scaled(lifeWait)):
		t.Fatal("the server did not stop")
	}

	if len(first.started()) != 1 {
		t.Fatalf("started %d before the quit, want 1", len(first.started()))
	}

	recorded := readRecords(t, world.data())
	if len(recorded) != len(world.sessions) {
		t.Fatalf("the record after the quit holds %d of %d", len(recorded), len(world.sessions))
	}

	second := newSpawns(world.data())
	launch(t, world, second)
	sameSpecs(t, second.started(), []pty.Spec{
		world.resumeSpec(0), world.resumeSpec(1), world.resumeSpec(2),
	})
}
