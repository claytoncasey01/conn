package sessions_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// TestLiveSessionsCarryTheStatusThePageShows: an Open work run reads its build session's status
// as every page shows it: a
// busy session gone quiet for an hour is idle, one that wrote a second ago is busy; the
// registry's own status stays as it was.
func TestLiveSessionsCarryTheStatusThePageShows(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	now := float64(time.Now().UnixMilli())

	for _, entry := range []struct {
		pid  int
		ago  time.Duration
		name string
	}{{30, time.Hour, "stale"}, {31, time.Second, "recent"}} {
		folder := fake.folder(entry.name, "")
		pid := strconv.Itoa(entry.pid)
		fake.procs = append(fake.procs, pid+" 1 claude")
		fake.register(pid+".json", map[string]any{
			"pid": entry.pid, "sessionId": "s-" + pid, "cwd": folder,
			"name": entry.name, "status": "busy", "updatedAt": now,
		})
		fake.transcript(strings.ReplaceAll(folder, "/", "-"), "s-"+pid,
			line("user", stamp(entry.ago), `,"message":{"content":"go"}`),
			line("assistant", stamp(entry.ago), `,"message":{"content":[{"type":"text",`+
				`"text":"working"}]}`))
	}

	loop := start(t, fake)
	live := waitLive(t, loop, 2)

	shown := map[string]string{}
	for _, session := range live {
		shown[session.Name] = session.Status + "/" + session.Shown
	}

	if shown["stale"] != "busy/idle" {
		t.Errorf("stale %q", shown["stale"])
	}

	if shown["recent"] != "busy/busy" {
		t.Errorf("recent %q", shown["recent"])
	}
}

func waitLive(t *testing.T, loop *sessions.Loop, count int) []sessions.LiveSession {
	t.Helper()

	end := time.Now().Add(testtime.Scaled(5 * time.Second))
	for time.Now().Before(end) {
		if live := loop.Live(); len(live) == count {
			return live
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("the live sessions never reached %d", count)

	return nil
}

// TestTickedClosesOnceTheFirstTickIsPublished: a reader that must not miss a live session (a
// launch's resume) waits on Ticked, after which Live holds what the first tick read.
func TestTickedClosesOnceTheFirstTickIsPublished(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	folder := fake.folder("running", "")
	fake.procs = append(fake.procs, "40 1 claude")
	fake.register("40.json", map[string]any{
		"pid": 40, "sessionId": "s-40", "cwd": folder, "name": "running", "status": "busy",
		"updatedAt": float64(time.Now().UnixMilli()),
	})

	loop := sessions.NewLoop(fake.deps())

	select {
	case <-loop.Ticked():
		t.Fatal("ticked before the loop ran")
	default:
	}

	go loop.Run(t.Context())

	select {
	case <-loop.Ticked():
	case <-time.After(testtime.Scaled(5 * time.Second)):
		t.Fatal("the first tick never came")
	}

	if len(loop.Live()) != 1 {
		t.Errorf("live %+v after the first tick, want the running session", loop.Live())
	}
}
