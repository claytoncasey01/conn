package app_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/hosted"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

const (
	sessionOne = "0f8fad5b-d9cb-469f-a165-70867728950e"
	sessionTwo = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	// recordPoll is how often a test looks at the record while it waits for it to change.
	recordPoll = 20 * time.Millisecond
)

// Quit stops every session and leaves the record of those that ran; the next launch resumes them
// on their own conversations, and the page says so.
func TestQuitThenLaunchResumesTheSessionsThatRan(t *testing.T) {
	t.Parallel()

	world := newLifeWorld(t, "", sessionOne)
	writeRecords(t, world.data(), world.record(0))

	first := newSpawns(world.data())
	server, quit := launch(t, world, first)

	sameSpecs(t, first.started(), []pty.Spec{world.resumeSpec(0)})

	recorded := readRecords(t, world.data())
	if len(recorded) != 1 {
		t.Fatalf("the record holds %d sessions, want the resumed one", len(recorded))
	}

	if recorded[0].ClaudeSessionID != sessionOne {
		t.Errorf("the record resumes %q, want %q", recorded[0].ClaudeSessionID, sessionOne)
	}

	shown := snapshotResumed(t, server)
	if len(shown) != 1 {
		t.Fatalf("the page lists %d resumed sessions, want 1", len(shown))
	}

	if string(shown[0].ID) != recorded[0].ID {
		t.Errorf("the page lists %q, want the recorded %q", shown[0].ID, recorded[0].ID)
	}

	quit()

	if !slices.Equal(first.recordAtStop, []bool{true}) {
		t.Errorf("the record named the session at its stop: %v, want it recorded first",
			first.recordAtStop)
	}

	if len(readRecords(t, world.data())) != 1 {
		t.Errorf("the quit left %d sessions in the record, want 1",
			len(readRecords(t, world.data())))
	}

	second := newSpawns(world.data())
	launch(t, world, second)
	sameSpecs(t, second.started(), []pty.Spec{world.resumeSpec(0)})
}

// A session that ended before the quit is dropped from the record and not resumed.
func TestASessionThatEndedBeforeTheQuitIsNotResumed(t *testing.T) {
	t.Parallel()

	world := newLifeWorld(t, "", sessionOne, sessionTwo)
	writeRecords(t, world.data(), world.record(0), world.record(1))

	first := newSpawns(world.data())
	_, quit := launch(t, world, first)
	sameSpecs(t, first.started(), []pty.Spec{world.resumeSpec(0), world.resumeSpec(1)})

	first.programs[0].exit()
	waitForRecord(t, world.data(), func(records []hosted.Record) bool {
		return len(records) == 1
	})

	quit()

	second := newSpawns(world.data())
	launch(t, world, second)
	sameSpecs(t, second.started(), []pty.Spec{world.resumeSpec(1)})
}

// waitForRecord waits until the record passes done, within lifeWait.
func waitForRecord(t *testing.T, data string, done func([]hosted.Record) bool) {
	t.Helper()

	deadline := time.After(testtime.Scaled(lifeWait))
	poll := time.NewTicker(recordPoll)

	defer poll.Stop()

	for !done(readRecords(t, data)) {
		select {
		case <-deadline:
			t.Fatalf("the record never changed: %+v", readRecords(t, data))
		case <-poll.C:
		}
	}
}

// With resumeOnLaunch off nothing is resumed, and the record is cleared, so turning it on later
// does not bring back sessions from long ago.
func TestResumeOnLaunchOffResumesNothingAndClearsTheRecord(t *testing.T) {
	t.Parallel()

	world := newLifeWorld(t, `,"sessions":{"where":"inside","resumeOnLaunch":false}`,
		sessionOne)
	writeRecords(t, world.data(), world.record(0))

	spawned := newSpawns(world.data())
	launch(t, world, spawned)

	if len(spawned.started()) != 0 {
		t.Errorf("started %+v with resumeOnLaunch off", spawned.started())
	}

	if len(readRecords(t, world.data())) != 0 {
		t.Errorf("the record still holds %+v", readRecords(t, world.data()))
	}
}

// tamperedRecords are record files Conn never writes, each written over a world whose History
// would resume its one session.
func tamperedRecords(t *testing.T, world lifeWorld) map[string]func() {
	t.Helper()

	path := filepath.Join(world.data(), hosted.FileName)
	good := world.record(0)
	text := func(sessions any) string {
		encoded, err := json.Marshal(map[string]any{"version": 1, "sessions": sessions})
		if err != nil {
			t.Fatal(err)
		}

		return string(encoded)
	}
	outside, notUUID := good, good
	outside.Dir = "/etc"
	notUUID.ClaudeSessionID = "--dangerously-skip-permissions"

	many := make([]hosted.Record, 0, recordsMany)
	for index := range recordsMany {
		each := good
		each.ID = hostIDOf(index)
		many = append(many, each)
	}

	withCommand := map[string]any{
		"id": good.ID, "owner": good.Owner, "name": good.Name, "dir": good.Dir,
		"claudeSessionId": good.ClaudeSessionID, "command": "/bin/sh -c 'touch /tmp/owned'",
	}

	return map[string]func(){
		"a command line field": func() { writeLifeFile(t, path, text([]any{withCommand})) },
		"a folder outside":     func() { writeLifeFile(t, path, text([]hosted.Record{outside})) },
		"a session id that is not a UUID": func() {
			writeLifeFile(t, path, text([]hosted.Record{notUUID}))
		},
		"500 entries": func() { writeLifeFile(t, path, text(many)) },
		"a link as the file": func() {
			target := filepath.Join(t.TempDir(), "records.json")
			writeLifeFile(t, target, text([]hosted.Record{good}))
			linkAt(t, target, path)
		},
	}
}

const recordsMany = 500

// hostIDOf is a host-issued id, distinct for each index.
func hostIDOf(index int) string { return fmt.Sprintf("%032x", index+1) }

func linkAt(t *testing.T, target, path string) {
	t.Helper()

	err := os.Symlink(target, path)
	if err != nil {
		t.Fatal(err)
	}
}

// A tampered record starts nothing beyond what History's own check starts, and is replaced.
func TestATamperedRecordStartsNothing(t *testing.T) {
	t.Parallel()

	probe := newLifeWorld(t, "", sessionOne)

	for name := range tamperedRecords(t, probe) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			world := newLifeWorld(t, "", sessionOne)
			tamperedRecords(t, world)[name]()

			spawned := newSpawns(world.data())
			launch(t, world, spawned)

			if len(spawned.started()) != 0 {
				t.Errorf("a tampered record started %+v", spawned.started())
			}

			if len(readRecords(t, world.data())) != 0 {
				t.Errorf("the tampered record was kept: %+v", readRecords(t, world.data()))
			}
		})
	}
}
