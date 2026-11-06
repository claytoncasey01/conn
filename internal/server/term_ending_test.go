package server_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"runtime"
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

// Input that backs up past the queue a viewer may have waiting is dropped, as input a program
// does not take is: the terminal stays open, and once the program takes input again, what was
// queued reaches it.
func TestInputBackedUpPastTheQueueIsDropped(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	rig := newTerminalRig(t, rigSetup{programs: &fakePrograms{writeGate: gate}})
	sessionID, program := rig.start(t)
	conn := rig.open(t, sessionID)
	readOutputUntilText(t, conn)

	// One is in the program, the queue holds the next ones, and the rest have nowhere to go.
	sendMessage(t, conn, websocket.MessageText, inputFrame("x"))
	eventually(t, "the first frame never reached the program", func() bool {
		return program.attemptsSoFar() == 1
	})

	for range server.InputQueueFramesMax + 4 {
		sendMessage(t, conn, websocket.MessageText, inputFrame("x"))
	}

	// The server reads frames in order, so once a ping sent after them is answered, it has
	// queued or dropped every one; and the terminal is still open.
	output := make(chan []byte, 1)

	go func() {
		defer close(output)

		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}

			output <- data
		}
	}()

	pingCtx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(waitLimit))
	defer cancel()

	err := conn.Ping(pingCtx)
	if err != nil {
		t.Fatalf("the terminal was closed: %v", err)
	}

	close(gate)

	eventually(t, "the queued input never reached the program", func() bool {
		return len(program.writtenSoFar()) == server.InputQueueFramesMax+1
	})

	program.feed(t, []byte("still open"))

	if got := <-output; string(got) != "still open" {
		t.Fatalf("output %q, want %q", got, "still open")
	}

	sendMessage(t, conn, websocket.MessageText, inputFrame("y"))
	eventually(t, "input after the backlog never reached the program", func() bool {
		return strings.HasSuffix(string(program.writtenSoFar()), "y")
	})
}

// When the program does not take a frame in time, the frames queued behind it are dropped with
// it, so text is never typed after a gap; input sent after that is typed.
func TestABlockedWriteDropsWhatIsQueuedBehindIt(t *testing.T) {
	t.Parallel()

	replies := make(chan error)
	rig := newTerminalRig(t, rigSetup{programs: &fakePrograms{writeReplies: replies}})
	sessionID, program := rig.start(t)
	conn := rig.open(t, sessionID)
	readOutputUntilText(t, conn)

	sendMessage(t, conn, websocket.MessageText, inputFrame("a"))
	eventually(t, "the first frame never reached the program", func() bool {
		return program.attemptsSoFar() == 1
	})

	for _, queued := range []string{"b", "c", "d"} {
		sendMessage(t, conn, websocket.MessageText, inputFrame(queued))
	}

	// Let the reader queue them before the program refuses the first.
	time.Sleep(testtime.Scaled(100 * time.Millisecond))

	replies <- fmt.Errorf("write: %w", os.ErrDeadlineExceeded)

	time.Sleep(testtime.Scaled(100 * time.Millisecond))
	sendMessage(t, conn, websocket.MessageText, inputFrame("e"))

	eventually(t, "input after the dropped ones never reached the program", func() bool {
		return program.attemptsSoFar() == 2
	})

	replies <- nil

	eventually(t, "the input after the gap was not typed", func() bool {
		return len(program.writtenSoFar()) > 0
	})

	if got := string(program.writtenSoFar()); got != "e" {
		t.Fatalf("typed %q, want only %q: input queued behind a refused frame was typed", got, "e")
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
// hold more than that much of them in memory. One replay the size of the bound, larger than a
// socket's buffers can take, holds it while its viewer reads nothing.
func TestReplaysInFlightAreBounded(t *testing.T) {
	t.Parallel()

	host := &scriptedHost{replay: bytes.Repeat([]byte{'r'}, server.ReplayBytesInFlightMax)}
	rig := newTerminalRig(t, rigSetup{
		host:  host,
		times: server.TerminalTimes{FrameWriteTimeout: testtime.Scaled(time.Minute)},
	})

	rig.open(t, sessionShape)

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

// A replay is not kept once it has been written: its memory can be collected while the terminal
// is still open.
func TestAReplayIsCollectedOnceWritten(t *testing.T) {
	t.Parallel()

	host := &scriptedHost{freshReplayBytes: 4 << 20}
	rig := newTerminalRig(t, rigSetup{host: host})
	conn := rig.open(t, sessionShape)

	replay, text := readOutputUntilText(t, conn)
	if len(replay) != 4<<20 {
		t.Fatalf("replay of %d bytes", len(replay))
	}

	if text != replayEnd {
		t.Fatalf("text %s", text)
	}

	eventually(t, "the written replay was still held while the terminal was open", func() bool {
		runtime.GC()

		return host.replayCollected(0)
	})

	// The terminal is still open: a ping goes both ways.
	pingCtx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(waitLimit))
	defer cancel()

	go func() { _, _, _ = conn.Read(pingCtx) }()

	err := conn.Ping(pingCtx)
	if err != nil {
		t.Fatalf("the terminal was closed: %v", err)
	}
}

// slowReadPause is how long a slow viewer waits between the frames it reads: far under the
// write timeout of one frame, so only the bound on the whole replay can end it.
const slowReadPause = 900 * time.Millisecond

// A viewer that reads its replay just fast enough to meet each frame's write timeout still has
// it ended within replayWriteTimeout, and the replay's bytes go back to the bound, so viewers
// like it cannot keep new terminals out.
func TestASlowReplayIsBounded(t *testing.T) {
	t.Parallel()

	// One replay fills the bound, and is larger than a socket's buffers can take.
	host := &scriptedHost{replay: bytes.Repeat([]byte{'r'}, server.ReplayBytesInFlightMax)}
	rig := newTerminalRig(t, rigSetup{
		host: host,
		times: server.TerminalTimes{
			// A frame may wait for TCP to reopen its window long after the reader has read, so
			// the frame's own timeout is far above the reader's pause.
			FrameWriteTimeout:  testtime.Scaled(30 * time.Second),
			ReplayWriteTimeout: testtime.Scaled(3 * time.Second),
		},
	})

	{
		conn := rig.open(t, sessionShape)

		go func() {
			for {
				_, _, err := conn.Read(context.Background())
				if err != nil {
					return
				}

				time.Sleep(testtime.Scaled(slowReadPause))
			}
		}()
	}

	if refused := rig.dial(t, sessionShape, nil); refused.status != 503 {
		t.Fatalf("status %d while the slow replays hold the bound, want 503", refused.status)
	}

	eventually(t, "slow replays kept new terminals out", func() bool {
		return rig.dial(t, sessionShape, nil).err == nil
	})
}

// Close closes every terminal open as going away and returns once each has closed, and the
// terminals refuse every handshake from then on, before the host is asked.
func TestCloseReturnsOnceEveryTerminalHasClosed(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{})
	sessionID, _ := rig.start(t)
	conns := []*websocket.Conn{rig.open(t, sessionID), rig.open(t, sessionID)}
	closed := watchClosed(t, conns)

	rig.terminals.Close()

	// Each terminal has given back its viewer's place by then: the session's every place is
	// free.
	for range pty.DefaultLimits().ViewersMax {
		viewer, err := rig.host.Subscribe(sessionID)
		if err != nil {
			t.Fatalf("Close returned before a terminal had closed: %v", err)
		}

		t.Cleanup(viewer.Close)
	}

	for range conns {
		select {
		case status := <-closed:
			if status != websocket.StatusGoingAway {
				t.Errorf("a terminal was closed with %v, want going away", status)
			}
		case <-time.After(testtime.Scaled(5 * time.Second)):
			t.Fatal("a viewer was never told of the close")
		}
	}

	before := rig.counted.calls.Load()

	refused := rig.dial(t, string(sessionID), nil)
	if refused.status != 503 {
		t.Errorf("status %d after Close, want 503", refused.status)
	}

	if rig.counted.calls.Load() != before {
		t.Error("the route asked the host after Close")
	}
}

// watchClosed reads each terminal past its replay's end until it closes, and sends the close's
// status.
func watchClosed(t *testing.T, conns []*websocket.Conn) <-chan websocket.StatusCode {
	t.Helper()

	closed := make(chan websocket.StatusCode, len(conns))

	for _, conn := range conns {
		if _, data := read(t, conn); string(data) != replayEnd {
			t.Fatalf("the replay of a session that printed nothing ended with %q", data)
		}

		go func() {
			for {
				_, _, err := conn.Read(context.WithoutCancel(t.Context()))
				if err != nil {
					closed <- websocket.CloseStatus(err)

					return
				}
			}
		}()
	}

	return closed
}

// Close with no terminal open returns at once.
func TestCloseWithNoTerminalReturnsAtOnce(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{})
	started := time.Now()

	rig.terminals.Close()

	if waited := time.Since(started); waited > server.TerminalsCloseTimeout/2 {
		t.Errorf("Close waited %v with no terminal open", waited)
	}
}

// The replay bound holds eight full replays, each a mode preamble and a whole scrollback, so the
// eighth viewer to join at once is never refused for the preamble's few bytes.
func TestReplayBoundHoldsEightFullReplays(t *testing.T) {
	t.Parallel()

	full := pty.DefaultLimits().ScrollbackBytesMax + pty.PreambleBytesMax
	if pty.ReplayBytesMax != full {
		t.Fatalf("ReplayBytesMax = %d, want the scrollback and the preamble, %d",
			pty.ReplayBytesMax, full)
	}

	if server.ReplayBytesInFlightMax != 8*full {
		t.Fatalf("ReplayBytesInFlightMax = %d, want eight full replays, %d",
			server.ReplayBytesInFlightMax, 8*full)
	}
}
