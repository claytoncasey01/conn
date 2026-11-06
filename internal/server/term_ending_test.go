package server_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/server"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// sessionShape is an id of the host's shape, for a scripted host that holds any.
const sessionShape = "0123456789abcdef0123456789abcdef"

// Typing while the program exits (the person types /exit, Enter, then moves the mouse) still
// ends the terminal with the exit and a normal close, never as a failure.
func TestTypingWhileTheProgramExitsEndsWithTheExit(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{})
	sessionID, program := rig.start(t)
	conn := rig.open(t, sessionID)
	readOutputUntilText(t, conn)

	// The program exits; its output stays open while the host drains it.
	program.exit(3)

	for range 5 {
		sendMessage(t, conn, websocket.MessageText, inputFrame("x"))
		time.Sleep(10 * time.Millisecond)
	}

	if _, text := readOutputUntilText(t, conn); text != `{"t":"exit","code":3}` {
		t.Fatalf("text %s, want the exit", text)
	}

	if code := closeCode(t, conn); code != websocket.StatusNormalClosure {
		t.Fatalf("closed with %v, want %v", code, websocket.StatusNormalClosure)
	}
}

// A program that does not take its input (busy, or stopped with Ctrl-Z) loses that input, as on
// any terminal, and the terminal stays open.
func TestAProgramNotTakingInputKeepsTheTerminal(t *testing.T) {
	t.Parallel()

	programs := &fakePrograms{writeErr: fmt.Errorf("write: %w", os.ErrDeadlineExceeded)}
	rig := newTerminalRig(t, rigSetup{programs: programs})
	sessionID, program := rig.start(t)
	conn := rig.open(t, sessionID)
	readOutputUntilText(t, conn)

	sendMessage(t, conn, websocket.MessageText, inputFrame("lost"))
	eventually(t, "the input never reached the program", func() bool {
		return program.attemptsSoFar() == 1
	})

	sendMessage(t, conn, websocket.MessageText, `{"t":"resize","cols":100,"rows":30}`)
	eventually(t, "the resize after it never arrived", func() bool {
		return len(program.sizesSoFar()) == 1
	})

	program.feed(t, []byte("still open"))
	readOutputUntil(t, conn, "still open")
}

// held is a write gate that the test opens once it is done, so a program's held writes end.
func held(t *testing.T) chan struct{} {
	t.Helper()

	gate := make(chan struct{})

	t.Cleanup(func() { close(gate) })

	return gate
}

// Input waiting for a program never holds up the reading of the viewer's frames: pings are
// answered, and the terminal stays open, however long the input waits.
func TestPingsAreAnsweredWhileInputWaits(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{
		programs: &fakePrograms{writeGate: held(t)},
		times: server.TerminalTimes{
			PingInterval: 50 * time.Millisecond, PingTimeout: 100 * time.Millisecond,
		},
	})
	sessionID, program := rig.start(t)
	conn := rig.open(t, sessionID)
	readOutputUntilText(t, conn)

	// The reader keeps reading, so the client's pongs are read too.
	output := make(chan []byte, 1)

	go func() {
		_, data, err := conn.Read(context.Background())
		if err == nil {
			output <- data
		}

		close(output)
	}()

	for range 3 {
		sendMessage(t, conn, websocket.MessageText, inputFrame("x"))
	}

	time.Sleep(testtime.Scaled(time.Second))

	program.feed(t, []byte("alive"))

	select {
	case data, open := <-output:
		if !open {
			t.Fatal("the terminal was dropped while its input waited")
		}

		if string(data) != "alive" {
			t.Fatalf("output %q", data)
		}
	case <-time.After(testtime.Scaled(waitLimit)):
		t.Fatal("no output came")
	}
}

// Input that backs up past the queue a viewer may have waiting closes that viewer.
func TestInputBackedUpPastTheQueueCloses(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{programs: &fakePrograms{writeGate: held(t)}})
	sessionID, _ := rig.start(t)
	conn := rig.open(t, sessionID)
	readOutputUntilText(t, conn)

	// One is in the program, the queue holds the next, and one more has nowhere to go.
	for range server.InputQueueFramesMax + 2 {
		sendMessage(t, conn, websocket.MessageText, inputFrame("x"))
	}

	if _, reason := closeReason(t, conn); reason != "input backed up behind the program" {
		t.Fatalf("closed because %q, want the input backed up", reason)
	}
}

// The terminal ends as the host says its viewer's output ended: a viewer that fell behind is
// told to come back (1013) while every write to it still succeeds, and one whose program ended
// gets the exit and a normal close, whatever the host lists by then.
func TestATerminalEndsAsTheHostSays(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		end  pty.ViewerEnd
		exit string
		want websocket.StatusCode
	}{
		{
			"dropped",
			pty.ViewerEnd{Dropped: true, Ended: false, ExitCode: 0},
			"",
			websocket.StatusTryAgainLater,
		},
		{
			"ended",
			pty.ViewerEnd{Dropped: false, Ended: true, ExitCode: 7},
			`{"t":"exit","code":7}`, websocket.StatusNormalClosure,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host := &scriptedHost{}
			rig := newTerminalRig(t, rigSetup{host: host})
			conn := rig.open(t, sessionShape)
			readOutputUntilText(t, conn)

			host.closeAll(tt.end)

			if tt.exit != "" {
				if _, text := readOutputUntilText(t, conn); text != tt.exit {
					t.Fatalf("text %s, want %s", text, tt.exit)
				}
			}

			if code := closeCode(t, conn); code != tt.want {
				t.Fatalf("closed with %v, want %v", code, tt.want)
			}
		})
	}
}

// Once the terminals are stopping, a new handshake is refused before the host is asked.
func TestAStoppingServerRefusesNewTerminals(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{})
	sessionID, _ := rig.start(t)
	before := rig.counted.calls.Load()

	rig.stop()

	refused := rig.dial(t, string(sessionID), nil)
	if refused.status != 503 {
		t.Fatalf("status %d, want 503", refused.status)
	}

	if rig.counted.calls.Load() != before {
		t.Fatal("the route asked the host while stopping")
	}
}

// bigReplay is a replay larger than any socket's buffers, so a viewer that does not read holds
// the server in the middle of it.
const bigReplayBytes = 16 << 20

// Stopping closes a terminal in the middle of its replay, without writing the rest.
func TestStoppingInterruptsAReplay(t *testing.T) {
	t.Parallel()

	host := &scriptedHost{replay: bytes.Repeat([]byte{'r'}, bigReplayBytes)}
	rig := newTerminalRig(t, rigSetup{
		host:  host,
		times: server.TerminalTimes{FrameWriteTimeout: testtime.Scaled(time.Minute)},
	})
	conn := rig.open(t, sessionShape)

	// The server is now stuck writing the replay to a viewer that has read nothing.
	time.Sleep(testtime.Scaled(200 * time.Millisecond))
	rig.stop()

	received := 0

	ctx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(waitLimit))
	defer cancel()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			break
		}

		received += len(data)
	}

	if ctx.Err() != nil {
		t.Fatal("the terminal was not closed in time")
	}

	if received >= bigReplayBytes {
		t.Fatalf("the whole replay of %d bytes was written after the stop", received)
	}
}

// The replays being written at once are bounded in bytes, so viewers that do not read cannot
// hold more than that much of them in memory.
func TestReplaysInFlightAreBounded(t *testing.T) {
	t.Parallel()

	host := &scriptedHost{replay: bytes.Repeat([]byte{'r'}, bigReplayBytes)}
	rig := newTerminalRig(t, rigSetup{
		host:  host,
		times: server.TerminalTimes{FrameWriteTimeout: testtime.Scaled(time.Minute)},
	})

	for range server.ReplayBytesInFlightMax / bigReplayBytes {
		rig.open(t, sessionShape)
	}

	refused := rig.dial(t, sessionShape, nil)
	if refused.err == nil {
		t.Fatal("a replay past the bound was started")
	}

	if refused.status != 503 {
		t.Fatalf("status %d, want 503", refused.status)
	}
}

// Resizes have their own small rate, so a flood of them cannot make the program redraw (and the
// host reallocate its screen) hundreds of times a second.
func TestResizesHaveTheirOwnRate(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{times: server.TerminalTimes{Now: stoppedClock}})
	sessionID, program := rig.start(t)
	conn := rig.open(t, sessionID)
	readOutputUntilText(t, conn)

	// Each resize is taken before the next is sent, so the rate is met, never the queue's bound.
	for index := range server.InputResizesBurst + 1 {
		sendMessage(t, conn, websocket.MessageText,
			fmt.Sprintf(`{"t":"resize","cols":%d,"rows":24}`, 80+index))

		if index == server.InputResizesBurst {
			break
		}

		eventually(t, "a resize within the rate never arrived", func() bool {
			return len(program.sizesSoFar()) == index+1
		})
	}

	if _, reason := closeReason(t, conn); reason != reasonInputRate {
		t.Fatalf("closed because %q, want %q", reason, reasonInputRate)
	}

	if got := len(program.sizesSoFar()); got != server.InputResizesBurst {
		t.Fatalf("%d resizes reached the program, want exactly %d", got, server.InputResizesBurst)
	}
}

// Coming back does not refill a session's input budget: a viewer closed for overspending finds
// it as it left it.
func TestReconnectingDoesNotRefillTheBudget(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{times: server.TerminalTimes{Now: stoppedClock}})
	sessionID, program := rig.start(t)

	first := rig.open(t, sessionID)
	readOutputUntilText(t, first)

	for index := range server.InputFramesBurst {
		sendTyped(t, first, program, inputFrame("a"), index+1)
	}

	sendMessage(t, first, websocket.MessageText, inputFrame("a"))

	if _, reason := closeReason(t, first); reason != reasonInputRate {
		t.Fatalf("the first viewer closed because %q, want %q", reason, reasonInputRate)
	}

	second := rig.open(t, sessionID)
	readOutputUntilText(t, second)
	sendMessage(t, second, websocket.MessageText, inputFrame("b"))

	if _, reason := closeReason(t, second); reason != reasonInputRate {
		t.Fatalf("closed because %q, want %q", reason, reasonInputRate)
	}

	if strings.Contains(string(program.writtenSoFar()), "b") {
		t.Fatal("input past the budget reached the program after a reconnect")
	}
}

// A replay counts against the bound only while it is being written: viewers that have read
// theirs leave room for more, though they stay open.
func TestAWrittenReplayNoLongerCounts(t *testing.T) {
	t.Parallel()

	host := &scriptedHost{replay: bytes.Repeat([]byte{'r'}, bigReplayBytes)}
	rig := newTerminalRig(t, rigSetup{host: host})

	for range 2 * server.ReplayBytesInFlightMax / bigReplayBytes {
		conn := rig.open(t, sessionShape)

		replay, text := readOutputUntilText(t, conn)
		if len(replay) != bigReplayBytes {
			t.Fatalf("replay of %d bytes, want %d", len(replay), bigReplayBytes)
		}

		if text != replayEnd {
			t.Fatalf("text %s, want %s", text, replayEnd)
		}
	}
}
