package server_test

import (
	"bytes"
	"testing"

	"github.com/coder/websocket"

	"github.com/claytoncasey01/conn/internal/pty"
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
