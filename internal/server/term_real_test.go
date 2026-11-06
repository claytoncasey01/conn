package server_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// What a viewer types reaches a real program on a real terminal, and what the program prints
// back reaches the viewer, through a real server. The program stays alive after its answer,
// since macOS drops a terminal's unread output when its program exits.
func TestARealProgramRoundTripsThroughTheTerminal(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{spawner: pty.Spawn})

	sessionID := rig.startSpec(t, pty.Spec{
		Argv: []string{
			"/bin/sh", "-c",
			`printf 'ready\n'; read line; printf 'got:%s:end\n' "$line"; read rest`,
		},
		Dir: t.TempDir(), Env: nil, Name: "round trip",
	})

	conn := rig.open(t, sessionID)

	// The program may have printed before the viewer came, or after: the replay and the live
	// output together hold it either way.
	replay, text := readOutputUntilText(t, conn)
	if text != replayEnd {
		t.Fatalf("text %s, want %s", text, replayEnd)
	}

	if !bytes.Contains(replay, []byte("ready")) {
		readOutputUntil(t, conn, "ready")
	}

	sendMessage(t, conn, websocket.MessageText, inputFrame(`hello\r`))
	readOutputUntil(t, conn, "got:hello:end")
}

// mouseMotion is one mouse-motion report, as a page sends many a second once a program asks for
// every motion.
const mouseMotion = `\u001b[<35;10;10M`

// A real program that stops reading its input (it sleeps with the terminal raw) fills the
// terminal's input queue and then the server's; the input past them is dropped and the terminal
// stays open, and once the program reads again what is typed reaches it.
func TestARealProgramThatStopsReadingKeepsItsTerminal(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{spawner: pty.Spawn})

	sessionID := rig.startSpec(t, pty.Spec{
		Argv: []string{
			"/bin/sh", "-c",
			`stty raw -echo; printf 'ready\n'; sleep 4; printf 'reading\n'; ` +
				`stty -raw; while read line; do printf 'got:%s:end\n' "$line"; done`,
		},
		Dir: t.TempDir(), Env: nil, Name: "not reading",
	})

	conn := rig.open(t, sessionID)

	replay, _ := readOutputUntilText(t, conn)
	if !bytes.Contains(replay, []byte("ready")) {
		readOutputUntil(t, conn, "ready")
	}

	// Mouse motion at 60 a second, for longer than it takes to fill both queues.
	for range 150 {
		sendMessage(t, conn, websocket.MessageText, inputFrame(mouseMotion))
		time.Sleep(time.Second / 60)
	}

	// One reader takes the output from here on, so the test can type while it waits.
	output := &lockedBuffer{}
	readErr := make(chan error, 1)

	go func() {
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				readErr <- err

				return
			}

			_, _ = output.Write(data)
		}
	}()

	// The program reads lines again once it has slept; a line typed then reaches it, after
	// whatever motion it still had to read. Each line is typed a second apart until one does.
	deadline := time.Now().Add(testtime.Scaled(4 * waitLimit))

	for !strings.Contains(output.String(), "got:after:end") {
		select {
		case err := <-readErr:
			t.Fatalf("the terminal was closed: %v", err)
		case <-time.After(time.Second):
		}

		if time.Now().After(deadline) {
			t.Fatal("the terminal never took input again")
		}

		sendMessage(t, conn, websocket.MessageText, inputFrame(`\rafter\r`))
	}
}
