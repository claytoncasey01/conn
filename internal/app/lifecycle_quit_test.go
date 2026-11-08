package app_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/app"
	"github.com/claytoncasey01/conn/internal/hosted"
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

// A launch removes the temporary file a write the last launch's crash cut short left behind.
func TestALaunchRemovesALeftoverTemporaryRecord(t *testing.T) {
	t.Parallel()

	world := newLifeWorld(t, "")
	leftover := filepath.Join(world.data(), ".hosted-4242.json")
	writeLifeFile(t, leftover, "{}")

	launch(t, world, newSpawns(world.data()))

	_, err := os.Lstat(leftover)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the leftover is still there: %v", err)
	}
}

// With sessions opening in a terminal app, a launch resumes nothing and keeps the record for a
// launch whose sessions open inside Conn again, which resumes them.
func TestALaunchWithSessionsElsewhereKeepsTheRecord(t *testing.T) {
	t.Parallel()

	world := newLifeWorld(t, `,"sessions":{"where":"terminal","resumeOnLaunch":true}`,
		sessionOne)
	writeRecords(t, world.data(), world.record(0))

	spawned := newSpawns(world.data())
	_, quit := launch(t, world, spawned)

	if len(spawned.started()) != 0 {
		t.Errorf("started %+v with sessions opening elsewhere", spawned.started())
	}

	quit()

	if !slices.Equal(readRecords(t, world.data()), []hosted.Record{world.record(0)}) {
		t.Errorf("the record is %+v, want it kept as it was", readRecords(t, world.data()))
	}
}

// The launch answers while it still resumes: it published where it listens, so a window that
// attached to it is not left without an answer while the resume waits or checks.
func TestALaunchAnswersWhileItResumes(t *testing.T) {
	t.Parallel()

	world := newLifeWorld(t, "", sessionOne)
	writeRecords(t, world.data(), world.record(0))

	resuming := make(chan struct{})
	spawned := newSpawns(world.data())
	// The resume holds at its start until the page has had its answer.
	spawned.atSpawn = func() { <-resuming }

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	started := make(chan *app.Server, 1)

	go func() {
		server, err := app.StartWith(ctx, lookupFrom(world.env), io.Discard, spawned.host())
		if err != nil {
			t.Error(err)
		}

		started <- server
	}()

	var server *app.Server

	select {
	case server = <-started:
	case <-time.After(testtime.Scaled(lifeWait)):
		close(resuming)
		t.Fatal("the launch did not serve before its resume was done")
	}

	snapshotResumed(t, server)
	close(resuming)
	cancel()

	select {
	case <-server.Done():
	case <-time.After(testtime.Scaled(lifeWait)):
		t.Fatal("the server did not stop")
	}
}
