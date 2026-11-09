package server_test

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/server"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// maxViewers is the host's limit on one session's viewers, which the route keeps.
const maxViewers = 4

// stoppedClock is a clock that never moves, so a rate test spends only the burst.
func stoppedClock() time.Time { return time.Unix(1_000_000, 0) }

// inputFrame is the page's frame that types data.
func inputFrame(data string) string { return `{"t":"input","d":"` + data + `"}` }

// The scrollback comes first, then the end of the replay, then what the program prints after.
func TestAViewerGetsTheReplayThenLiveOutput(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{})
	sessionID, program := rig.start(t)

	program.feed(t, []byte("before"))
	waitShown(t, rig.host, sessionID, "before")

	conn := rig.open(t, sessionID)

	replay, text := readOutputUntilText(t, conn)
	if string(replay) != "before" {
		t.Fatalf("replay %q, want %q", replay, "before")
	}

	if text != replayEnd {
		t.Fatalf("text %s, want %s", text, replayEnd)
	}

	program.feed(t, []byte("after"))

	kind, live := read(t, conn)
	if kind != websocket.MessageBinary {
		t.Fatalf("live output came as %v, want binary", kind)
	}

	if string(live) != "after" {
		t.Fatalf("live %q, want %q", live, "after")
	}
}

// A session with nothing printed yet still ends its (empty) replay.
func TestAnEmptyReplayStillEnds(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{})
	sessionID, _ := rig.start(t)

	replay, text := readOutputUntilText(t, rig.open(t, sessionID))
	if len(replay) != 0 {
		t.Fatalf("replay of %d bytes, want none", len(replay))
	}

	if text != replayEnd {
		t.Fatalf("text %s, want %s", text, replayEnd)
	}
}

func TestInputReachesTheProgram(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{})
	sessionID, program := rig.start(t)
	conn := rig.open(t, sessionID)
	readOutputUntilText(t, conn)

	sendMessage(t, conn, websocket.MessageText, inputFrame(`ls -la\r\u001b[A`))

	eventually(t, "the input never reached the program", func() bool {
		return string(program.writtenSoFar()) == "ls -la\r\x1b[A"
	})
}

func TestAResizeReachesTheHost(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{})
	sessionID, program := rig.start(t)
	conn := rig.open(t, sessionID)
	readOutputUntilText(t, conn)

	sendMessage(t, conn, websocket.MessageText, `{"t":"resize","cols":132,"rows":43}`)

	eventually(t, "the resize never reached the program", func() bool {
		sizes := program.sizesSoFar()
		if len(sizes) != 1 {
			return false
		}

		return sizes[0] == pty.Size{Columns: 132, Rows: 43}
	})
}

// readAllOf reads conn's output until it has total bytes or the read fails, counting what it has
// in received as it goes, and sends all of it on the channel it returns.
func readAllOf(conn *websocket.Conn, total int, received *atomic.Int64) <-chan []byte {
	done := make(chan []byte, 1)

	go func() {
		var all []byte

		for len(all) < total {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				break
			}

			all = append(all, data...)
			received.Store(int64(len(all)))
		}

		done <- all
	}()

	return done
}

// Each frame that is not one of the page's two, exactly, closes the connection with a policy
// code, before anything of it reaches the program.
func TestABadFrameClosesTheConnection(t *testing.T) {
	t.Parallel()

	for _, tt := range badFrames() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newTerminalRig(t, rigSetup{})
			sessionID, program := rig.start(t)
			conn := rig.open(t, sessionID)
			readOutputUntilText(t, conn)

			sendMessage(t, conn, tt.kind, tt.data)

			if code := closeCode(t, conn); code != tt.want {
				t.Fatalf("closed with %v, want %v", code, tt.want)
			}

			if len(program.writtenSoFar()) != 0 {
				t.Fatal("a bad frame reached the program")
			}

			if len(program.sizesSoFar()) != 0 {
				t.Fatal("a bad size reached the program")
			}
		})
	}
}

// badFrame is a frame that is not one of the page's two, and the close code it gets.
type badFrame struct {
	name string
	kind websocket.MessageType
	data string
	want websocket.StatusCode
}

// badFrames are frames that close the connection, each for its own reason.
func badFrames() []badFrame {
	return []badFrame{
		{
			"an unknown type", websocket.MessageText, `{"t":"exec","d":"x"}`,
			websocket.StatusPolicyViolation,
		},
		{"not JSON", websocket.MessageText, `ls`, websocket.StatusPolicyViolation},
		{
			"an unknown member", websocket.MessageText, `{"t":"input","d":"x","run":1}`,
			websocket.StatusPolicyViolation,
		},
		{
			"a binary frame", websocket.MessageBinary, `{"t":"input","d":"x"}`,
			websocket.StatusUnsupportedData,
		},
		{
			"no columns", websocket.MessageText, `{"t":"resize","cols":0,"rows":24}`,
			websocket.StatusPolicyViolation,
		},
		{
			"no rows", websocket.MessageText, `{"t":"resize","cols":80,"rows":0}`,
			websocket.StatusPolicyViolation,
		},
		{
			"too many columns", websocket.MessageText, `{"t":"resize","cols":1001,"rows":24}`,
			websocket.StatusPolicyViolation,
		},
		{
			"too many cells", websocket.MessageText, `{"t":"resize","cols":1000,"rows":1000}`,
			websocket.StatusPolicyViolation,
		},
		{
			"input of one byte too many", websocket.MessageText,
			inputFrame(strings.Repeat("a", server.MaxInputFrameBytes+1)),
			websocket.StatusPolicyViolation,
		},
		{
			"a frame longer than the read limit", websocket.MessageText,
			strings.Repeat(" ", server.MaxFrameBytes+1), websocket.StatusMessageTooBig,
		},
	}
}

// The most input one frame may carry is typed whole.
func TestTheLargestInputFrameIsTyped(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{})
	sessionID, program := rig.start(t)
	conn := rig.open(t, sessionID)
	readOutputUntilText(t, conn)

	data := strings.Repeat("a", server.MaxInputFrameBytes)
	sendMessage(t, conn, websocket.MessageText, inputFrame(data))

	eventually(t, "the input never reached the program", func() bool {
		return len(program.writtenSoFar()) == server.MaxInputFrameBytes
	})
}

// A flood of input, in bytes or in frames, closes the connection once it has spent the session's
// burst: with the clock stopped, exactly the burst reaches the program and nothing more.
func TestAnInputFloodIsCutOff(t *testing.T) {
	t.Parallel()

	frameData := strings.Repeat("a", server.MaxInputFrameBytes)

	tests := []struct {
		name      string
		frameData string
		frames    int
	}{
		{"in bytes", frameData, server.InputBytesBurst / server.MaxInputFrameBytes},
		{"in frames", "a", server.InputFramesBurst},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newTerminalRig(t, rigSetup{times: server.TerminalTimes{Now: stoppedClock}})
			sessionID, program := rig.start(t)
			conn := rig.open(t, sessionID)
			readOutputUntilText(t, conn)

			for index := range tt.frames {
				sendTyped(t, conn, program, inputFrame(tt.frameData), (index+1)*len(tt.frameData))
			}

			sendMessage(t, conn, websocket.MessageText, inputFrame(tt.frameData))

			code, reason := closeReason(t, conn)
			if code != websocket.StatusPolicyViolation {
				t.Fatalf("closed with %v, want %v", code, websocket.StatusPolicyViolation)
			}

			if reason != reasonInputRate {
				t.Fatalf("closed because %q, want %q", reason, reasonInputRate)
			}

			if typed := len(program.writtenSoFar()); typed != tt.frames*len(tt.frameData) {
				t.Fatalf("%d bytes typed, want exactly %d", typed, tt.frames*len(tt.frameData))
			}
		})
	}
}

// The rate cap is the session's: a second viewer does not double it. The viewer that spends past
// it is closed, and the other keeps its output.
func TestTheInputCapIsTheSessions(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{times: server.TerminalTimes{Now: stoppedClock}})
	sessionID, program := rig.start(t)

	first := rig.open(t, sessionID)
	readOutputUntilText(t, first)

	second := rig.open(t, sessionID)
	readOutputUntilText(t, second)

	half := server.InputFramesBurst / 2
	for index := range half {
		sendTyped(t, first, program, inputFrame("a"), index+1)
	}

	for index := range server.InputFramesBurst - half {
		sendTyped(t, second, program, inputFrame("b"), half+index+1)
	}

	sendMessage(t, second, websocket.MessageText, inputFrame("b"))

	code, reason := closeReason(t, second)
	if code != websocket.StatusPolicyViolation {
		t.Fatalf("closed with %v, want %v", code, websocket.StatusPolicyViolation)
	}

	if reason != reasonInputRate {
		t.Fatalf("closed because %q, want %q", reason, reasonInputRate)
	}

	if typed := len(program.writtenSoFar()); typed != server.InputFramesBurst {
		t.Fatalf("%d bytes typed, want exactly %d", typed, server.InputFramesBurst)
	}

	program.feed(t, []byte("still here"))
	readOutputUntil(t, first, "still here")
}

// A session takes maxViewers viewers; the next is refused before the handshake completes, and a
// place freed by a viewer that leaves can be taken again.
func TestTheViewerPastTheLimitIsRefused(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{})
	sessionID, _ := rig.start(t)

	conns := make([]*websocket.Conn, 0, maxViewers)
	for range maxViewers {
		conn := rig.open(t, sessionID)
		readOutputUntilText(t, conn)
		conns = append(conns, conn)
	}

	refused := rig.dial(t, string(sessionID), nil)
	if refused.err == nil {
		t.Fatal("a viewer past the limit was taken")
	}

	if refused.status != 503 {
		t.Fatalf("status %d, want 503", refused.status)
	}

	err := conns[0].Close(websocket.StatusNormalClosure, "")
	if err != nil {
		t.Fatal(err)
	}

	eventually(t, "the place of a viewer that left was never freed", func() bool {
		got := rig.dial(t, string(sessionID), nil)

		return got.err == nil
	})
}

// A viewer that stops reading is closed once it falls behind, and the program's output goes on
// reaching a viewer that keeps up, whole and in order; the closed viewer's place is freed.
func TestASlowViewerIsClosedAndAFastOneKeepsItsOutput(t *testing.T) {
	t.Parallel()

	const (
		chunkBytes = 32 << 10
		chunks     = 768
	)

	rig := newTerminalRig(t, rigSetup{
		times: server.TerminalTimes{FrameWriteTimeout: testtime.Scaled(500 * time.Millisecond)},
	})
	sessionID, program := rig.start(t)

	slow := rig.open(t, sessionID)
	readOutputUntilText(t, slow)

	fast := rig.open(t, sessionID)
	readOutputUntilText(t, fast)

	var received atomic.Int64

	fastDone := readAllOf(fast, chunks*chunkBytes, &received)

	want := make([]byte, 0, chunks*chunkBytes)

	for index := range chunks {
		chunk := bytes.Repeat([]byte{byte('a' + index%26)}, chunkBytes)
		want = append(want, chunk...)
		program.feed(t, chunk)

		eventually(t, "the fast viewer fell behind", func() bool {
			return received.Load() >= int64(len(want))
		})
	}

	if got := <-fastDone; !bytes.Equal(got, want) {
		t.Fatalf("the fast viewer got %d bytes, not the %d printed", len(got), len(want))
	}

	closeCode(t, slow)

	eventually(t, "the slow viewer's place was never freed", func() bool {
		return rig.canOpen(t, sessionID, maxViewers-1)
	})
}

// canOpen says whether count viewers can open the session's terminal at once; it closes them.
func (rig *terminalRig) canOpen(t *testing.T, sessionID pty.ID, count int) bool {
	t.Helper()

	opened := make([]*websocket.Conn, 0, count)

	defer func() {
		for _, conn := range opened {
			_ = conn.CloseNow()
		}
	}()

	for range count {
		got := rig.dial(t, string(sessionID), nil)
		if got.err != nil {
			return false
		}

		opened = append(opened, got.conn)
	}

	return true
}

// A program that exits is reported with its code after its last output, and the connection then
// closes normally; a viewer who comes after gets the replay, its end and the exit.
func TestTheExitIsReported(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{})
	sessionID, program := rig.start(t)

	conn := rig.open(t, sessionID)
	readOutputUntilText(t, conn)

	program.feed(t, []byte("bye"))
	program.finish(3)

	output, text := readOutputUntilText(t, conn)
	if string(output) != "bye" {
		t.Fatalf("output %q, want %q", output, "bye")
	}

	if text != `{"t":"exit","code":3}` {
		t.Fatalf("text %s", text)
	}

	if code := closeCode(t, conn); code != websocket.StatusNormalClosure {
		t.Fatalf("closed with %v, want %v", code, websocket.StatusNormalClosure)
	}

	late := rig.open(t, sessionID)

	replay, text := readOutputUntilText(t, late)
	if string(replay) != "bye" {
		t.Fatalf("replay %q, want %q", replay, "bye")
	}

	if text != replayEnd {
		t.Fatalf("text %s, want %s", text, replayEnd)
	}

	if _, text := readOutputUntilText(t, late); text != `{"t":"exit","code":3}` {
		t.Fatalf("text %s", text)
	}

	if code := closeCode(t, late); code != websocket.StatusNormalClosure {
		t.Fatalf("closed with %v, want %v", code, websocket.StatusNormalClosure)
	}
}

// Stopping the server's terminals (Conn quitting) closes every open terminal: a taken-over
// connection outlives the HTTP server's own shutdown otherwise.
func TestStoppingTheTerminalsClosesThem(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{})
	sessionID, _ := rig.start(t)
	conn := rig.open(t, sessionID)
	readOutputUntilText(t, conn)

	rig.stop()

	if code := closeCode(t, conn); code != websocket.StatusGoingAway {
		t.Fatalf("closed with %v, want %v", code, websocket.StatusGoingAway)
	}
}

// Conn's HTTP server sets a ReadTimeout; a terminal stays open past it.
func TestATerminalOutlivesTheServersReadTimeout(t *testing.T) {
	t.Parallel()

	readTimeout := testtime.Scaled(200 * time.Millisecond)

	rig := newTerminalRig(t, rigSetup{readTimeout: readTimeout})
	sessionID, program := rig.start(t)
	conn := openWithinReadTimeout(t, rig, sessionID)
	readOutputUntilText(t, conn)

	// The test is that time passes beyond the read timeout with the terminal open.
	time.Sleep(3 * readTimeout)

	sendMessage(t, conn, websocket.MessageText, inputFrame("late"))
	eventually(t, "input after the read timeout never arrived", func() bool {
		return string(program.writtenSoFar()) == "late"
	})

	program.feed(t, []byte("still open"))
	readOutputUntil(t, conn, "still open")
}

// openWithinReadTimeout opens a terminal whose handshake the server read within its read
// timeout. The timeout bounds the handshake as well, so on a slow machine the server rightly
// hangs up on one that took longer, before any terminal is open; the next is tried.
func openWithinReadTimeout(t *testing.T, rig *terminalRig, sessionID pty.ID) *websocket.Conn {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(waitLimit))

	for {
		got := rig.dial(t, string(sessionID), nil)
		if got.err == nil {
			return got.conn
		}

		if time.Now().After(deadline) {
			t.Fatalf("no handshake was read within the read timeout: status %d: %v", got.status,
				got.err)
		}

		// The server is the slow side: a pause before the next handshake leaves it the machine.
		time.Sleep(waitPoll)
	}
}

// A peer that stops answering pings is dropped, and its place is freed.
func TestAPeerThatStopsAnsweringIsDropped(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{times: server.TerminalTimes{
		PingInterval: 50 * time.Millisecond, PingTimeout: 100 * time.Millisecond,
	}})
	sessionID, _ := rig.start(t)

	// A connection that is never read never answers a ping.
	for range maxViewers {
		rig.open(t, sessionID)
	}

	eventually(t, "a silent peer's place was never freed", func() bool {
		got := rig.dial(t, string(sessionID), nil)
		if got.err != nil {
			return false
		}

		// This one answers pings while it is read; reading it once is enough to see it open.
		readOutputUntilText(t, got.conn)

		return true
	})
}

// The terminals open at once are bounded over all sessions, not only per session: an ended
// session takes any number of viewers (each holds no place of the host's), so a flood of
// handshakes is refused past maxConnections. A scripted host keeps every viewer's output open,
// so the terminals stay open for as long as the test needs.
func TestTheTerminalsOpenAtOnceAreBounded(t *testing.T) {
	t.Parallel()

	host := &scriptedHost{}
	rig := newTerminalRig(t, rigSetup{host: host})

	for range server.MaxConnections {
		rig.open(t, sessionShape)
	}

	refused := rig.dial(t, sessionShape, nil)
	if refused.err == nil {
		t.Fatal("a terminal past the limit was opened")
	}

	if refused.status != 503 {
		t.Fatalf("status %d, want 503", refused.status)
	}

	if host.subscribed() != server.MaxConnections {
		t.Fatalf("%d viewers subscribed, want %d", host.subscribed(), server.MaxConnections)
	}
}
