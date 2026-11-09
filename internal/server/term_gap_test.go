package server_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/server"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// drainInBackground reads and discards conn's messages from now on, until conn closes; a reader
// must be running for a ping to be answered.
func drainInBackground(conn *websocket.Conn) {
	go func() {
		for {
			_, _, err := conn.Read(context.Background())
			if err != nil {
				return
			}
		}
	}()
}

// settled waits until the server has read every frame sent on conn so far: it reads frames in
// order, and answers a ping only once it has read what came before it.
func settled(t *testing.T, conn *websocket.Conn) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(waitLimit))
	defer cancel()

	err := conn.Ping(ctx)
	if err != nil {
		t.Fatalf("the terminal was closed: %v", err)
	}
}

// takeNext lets the program take its next Write, failing the test if none comes.
func takeNext(t *testing.T, replies chan<- error) {
	t.Helper()

	select {
	case replies <- nil:
	case <-time.After(testtime.Scaled(waitLimit)):
		t.Fatal("the program was never given the next frame")
	}
}

// Once a frame is dropped because the queue is full, every input frame after it is dropped too
// until the queue has emptied: a paste that outruns a slow program loses its tail, never a piece
// from its middle.
func TestInputAfterADroppedFrameWaitsForTheQueueToEmpty(t *testing.T) {
	t.Parallel()

	replies := make(chan error)
	rig := newTerminalRig(t, rigSetup{programs: &fakePrograms{writeReplies: replies}})
	sessionID, program := rig.start(t)
	conn := rig.open(t, sessionID)
	readOutputUntilText(t, conn)
	drainInBackground(conn)

	letters := "abcdefghijklmnopqrst"
	queueFull := server.MaxInputQueueFrames + 1

	// a is in the program; b to q fill the queue; r is dropped.
	sendMessage(t, conn, websocket.MessageText, inputFrame(letters[:1]))
	eventually(t, "the first frame never reached the program", func() bool {
		return program.attemptsSoFar() == 1
	})

	for _, letter := range letters[1 : queueFull+1] {
		sendMessage(t, conn, websocket.MessageText, inputFrame(string(letter)))
	}

	settled(t, conn)

	// The program takes a and then is given b: the queue is no longer full, but not empty.
	takeNext(t, replies)
	eventually(t, "the second frame never reached the program", func() bool {
		return program.attemptsSoFar() == 2
	})

	// s comes while what was queued before the drop is still being typed: it goes too.
	sendMessage(t, conn, websocket.MessageText, inputFrame(letters[queueFull+1:queueFull+2]))
	settled(t, conn)

	for range queueFull - 1 {
		takeNext(t, replies)
	}

	// The queue is empty now: t is typed.
	sendMessage(t, conn, websocket.MessageText, inputFrame(letters[queueFull+2:queueFull+3]))
	takeNext(t, replies)

	want := letters[:queueFull] + letters[queueFull+2:queueFull+3]

	eventually(t, "the input after the gap never reached the program", func() bool {
		return len(program.writtenSoFar()) == len(want)
	})

	if got := string(program.writtenSoFar()); got != want {
		t.Fatalf("typed %q, want %q", got, want)
	}
}

// A resize is never lost to a full queue or a program that is not taking input: the terminal
// ends at the last size the viewer sent, while the program's input is still held up.
func TestAResizeIsNeverLost(t *testing.T) {
	t.Parallel()

	gate := held(t)
	rig := newTerminalRig(t, rigSetup{programs: &fakePrograms{writeGate: gate}})
	sessionID, program := rig.start(t)
	conn := rig.open(t, sessionID)
	readOutputUntilText(t, conn)
	drainInBackground(conn)

	sendMessage(t, conn, websocket.MessageText, inputFrame("x"))
	eventually(t, "the first frame never reached the program", func() bool {
		return program.attemptsSoFar() == 1
	})

	for range server.MaxInputQueueFrames + 2 {
		sendMessage(t, conn, websocket.MessageText, inputFrame("x"))
	}

	sendMessage(t, conn, websocket.MessageText, `{"t":"resize","cols":100,"rows":30}`)
	sendMessage(t, conn, websocket.MessageText, inputFrame("x"))
	sendMessage(t, conn, websocket.MessageText, `{"t":"resize","cols":120,"rows":40}`)
	settled(t, conn)

	want := pty.Size{Columns: 120, Rows: 40}

	eventually(t, "the last resize never reached the program", func() bool {
		sizes := program.sizesSoFar()
		if len(sizes) == 0 {
			return false
		}

		return sizes[len(sizes)-1] == want
	})
}

// typistRounds is how many terminals the stopped-typist test ends: a typist that picks between
// an ended terminal and a queued frame at random types in about half of them.
const typistRounds = 8

// Once a terminal has ended, its typist types nothing more of what was queued.
func TestATypistStopsWhenItsTerminalEnds(t *testing.T) {
	t.Parallel()

	replies := make(chan error)

	t.Cleanup(func() { close(replies) })

	rig := newTerminalRig(t, rigSetup{programs: &fakePrograms{writeReplies: replies}})
	ended := make(chan pty.ID, typistRounds)

	server.SetTerminalEnded(rig.terminals, func(sessionID pty.ID) { ended <- sessionID })

	for round := range typistRounds {
		sessionID, program := rig.start(t)
		conn := rig.open(t, sessionID)
		readOutputUntilText(t, conn)

		sendMessage(t, conn, websocket.MessageText, inputFrame("a"))
		eventually(t, "the first frame never reached the program", func() bool {
			return program.attemptsSoFar() == 1
		})

		for range server.MaxInputQueueFrames {
			sendMessage(t, conn, websocket.MessageText, inputFrame("b"))
		}

		// The viewer goes while its first frame is in the program and the rest are queued. The
		// program takes the frame only once the terminal has been told to end, and what the
		// typist did is looked at once the terminal is done.
		_ = conn.CloseNow()

		waitEnded(t, ended, sessionID)
		takeNext(t, replies)
		eventually(t, "the terminal was never done", func() bool {
			return server.ActiveTerminals(rig.terminals) == 0
		})

		if attempts := program.attemptsSoFar(); attempts != 1 {
			t.Fatalf("round %d: %d frames given to the program after its terminal ended, want 0",
				round, attempts-1)
		}
	}
}

// waitEnded waits until the server has told sessionID's terminal to end.
func waitEnded(t *testing.T, ended <-chan pty.ID, sessionID pty.ID) {
	t.Helper()

	select {
	case got := <-ended:
		if got != sessionID {
			t.Fatalf("the terminal of %s ended, want %s", got, sessionID)
		}
	case <-time.After(testtime.Scaled(waitLimit)):
		t.Fatal("the terminal was never told to end")
	}
}

// A replay that runs out of time gives its bytes back to the bound before the close is sent, not
// after the close's own wait for a peer that does not answer.
func TestAReplayOutOfTimeGivesItsBytesBackAtOnce(t *testing.T) {
	t.Parallel()

	host := &scriptedHost{replay: []byte(strings.Repeat("r", server.MaxReplayBytesInFlight))}
	rig := newTerminalRig(t, rigSetup{
		host: host,
		// The replay is out of time before its first frame, so it is closed with 1013 rather
		// than hung up on in the middle of a frame.
		times: server.TerminalTimes{ReplayWriteTimeout: time.Nanosecond},
	})

	// This viewer never reads, so it never answers the close.
	rig.open(t, sessionShape)

	deadline := time.Now().Add(testtime.Scaled(2 * time.Second))

	for rig.dial(t, sessionShape, nil).err != nil {
		if time.Now().After(deadline) {
			t.Fatal("the replay's bytes were held while its close waited for the peer")
		}

		time.Sleep(waitPoll)
	}
}
