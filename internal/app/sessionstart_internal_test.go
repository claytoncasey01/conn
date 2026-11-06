package app

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/drysession"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/settings"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// stopLimit bounds the stop of a test's hosted sessions.
const stopLimit = 5 * time.Second

var errNoRealProgram = errors.New("no real program in this test")

// openerRig is the app's opener over a store and a host that runs only dry sessions.
func openerRig(t *testing.T) (launch.Opener, *settings.Store, *pty.Host) {
	t.Helper()

	store := settings.NewStore(log.New(io.Discard, "", 0))
	store.Reload(t.TempDir())

	host := pty.NewHost(pty.Config{
		Spawner: drysession.Spawner(func(pty.Spec) (pty.Process, error) {
			return nil, errNoRealProgram
		}),
		Limits: pty.DefaultLimits(), Answers: pty.Answers{}, TurnWaited: nil,
	})

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testtime.Scaled(stopLimit))
		defer cancel()

		_ = host.StopAll(ctx)
	})

	return newOpener(config.Config{CmuxBin: "/usr/bin/false"}, store, host), store, host
}

func setWhere(t *testing.T, store *settings.Store, body string) {
	t.Helper()

	parsed, err := js.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}

	result := store.Update(settings.SectionSessions, parsed)
	if !result.OK {
		t.Fatal(result.Error)
	}
}

// TestTheOpenerReadsWhereSessionsOpenAtEachStart: inside Conn a session runs on the host; saved
// to show the command, the next start shows it and starts nothing; saved to my terminal with no
// command of my own, cmux is asked, and its failure leaves the line to paste.
func TestTheOpenerReadsWhereSessionsOpenAtEachStart(t *testing.T) {
	t.Parallel()

	open, store, host := openerRig(t)
	folder := t.TempDir()
	spec := drysession.Spec(folder, "PROJ-1")

	inside := open(t.Context(), spec)
	if !inside.OK {
		t.Fatalf("inside: %+v", inside)
	}

	if len(host.Live()) != 1 {
		t.Fatalf("hosted %d", len(host.Live()))
	}

	if host.Live()[0].ID != inside.HostedID {
		t.Errorf("hosted id %q, live %q", inside.HostedID, host.Live()[0].ID)
	}

	setWhere(t, store, `{"where":"command"}`)

	shown := open(t.Context(), spec)
	if shown.ManualCommand != "cd "+folder+" && "+drysession.Program+" --name PROJ-1" {
		t.Errorf("command: %+v", shown)
	}

	setWhere(t, store, `{"where":"terminal"}`)

	cmuxFailed := open(t.Context(), spec)
	if cmuxFailed.OK {
		t.Fatal("false opened a cmux workspace")
	}

	if cmuxFailed.ManualCommand != shown.ManualCommand {
		t.Errorf("cmux's line to paste %q", cmuxFailed.ManualCommand)
	}

	if len(host.Live()) != 1 {
		t.Errorf("hosted %d after the other two", len(host.Live()))
	}
}

// TestTheTerminalCommandStartsWithNoShellAndIsNotWaitedFor: a program that is there starts, one
// that is not says why.
func TestTheTerminalCommandStartsWithNoShellAndIsNotWaitedFor(t *testing.T) {
	t.Parallel()

	started := time.Now()

	err := openTerminal(t.Context(), []string{"/bin/sleep", "2"})
	if err != nil {
		t.Fatal(err)
	}

	if time.Since(started) > testtime.Scaled(stopLimit) {
		t.Error("the terminal command was waited for")
	}

	err = openTerminal(t.Context(), []string{"/nonexistent/terminal", "{command}"})
	if err == nil {
		t.Fatal("a missing terminal started")
	}

	if !strings.Contains(err.Error(), "starting your terminal command") {
		t.Errorf("error %q", err)
	}
}
