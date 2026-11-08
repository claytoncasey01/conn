package app_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/claytoncasey01/conn/internal/app"
	"github.com/claytoncasey01/conn/internal/instance"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

var errProgramClosed = errors.New("the fake terminal is closed")

// quitWait bounds each wait of the quit test.
const quitWait = 10 * time.Second

// stoppedProgram is a hosted fake that records what Conn looked like when it was told to stop.
type stoppedProgram struct {
	dataDir string
	output  chan []byte
	closed  chan struct{}
	exited  chan struct{}
	once    sync.Once
	close   sync.Once

	mutex sync.Mutex
	// stopped is set by the first stop; lockHeld says whether Conn still held its data folder
	// then.
	stopped  bool
	lockHeld bool
}

func (p *stoppedProgram) Read(buffer []byte) (int, error) {
	select {
	case chunk := <-p.output:
		return copy(buffer, chunk), nil
	case <-p.closed:
		return 0, errProgramClosed
	}
}

func (p *stoppedProgram) Write(data []byte) (int, error) { return len(data), nil }
func (p *stoppedProgram) Resize(pty.Size) error          { return nil }
func (p *stoppedProgram) PID() int                       { return 4242 }
func (p *stoppedProgram) Keyboard() (pty.Keyboard, error) {
	return pty.Keyboard{Foreground: true, LineMode: false}, nil
}
func (p *stoppedProgram) Terminate() error { return p.stop() }
func (p *stoppedProgram) Kill() error      { return p.stop() }

func (p *stoppedProgram) Group() pty.GroupState {
	select {
	case <-p.exited:
		return pty.GroupGone
	default:
		return pty.GroupRunning
	}
}

func (p *stoppedProgram) Wait() int {
	<-p.exited

	return 0
}

func (p *stoppedProgram) Close() error {
	p.close.Do(func() { close(p.closed) })

	return nil
}

func (p *stoppedProgram) stop() error {
	p.once.Do(func() {
		_, held, err := instance.Running(p.dataDir)

		p.mutex.Lock()
		p.stopped = true
		p.lockHeld = held

		if err != nil {
			p.lockHeld = false
		}
		p.mutex.Unlock()

		close(p.exited)
	})

	return nil
}

// Quitting closes every terminal as going away, then stops every hosted session, then lets the
// data folder go: the program is stopped while the lock is still held, and its viewer is told
// Conn is going away, not that the program ended.
func TestQuitClosesTerminalsThenStopsSessionsThenLetsTheFolderGo(t *testing.T) {
	t.Parallel()

	env := environment(t)
	program := &stoppedProgram{
		dataDir: env["CONN_DATA"], output: make(chan []byte), closed: make(chan struct{}),
		exited: make(chan struct{}), once: sync.Once{}, close: sync.Once{},
		mutex: sync.Mutex{}, stopped: false, lockHeld: false,
	}
	host := pty.NewHost(pty.Config{
		Spawner: func(pty.Spec) (pty.Process, error) { return program, nil },
		Limits:  pty.DefaultLimits(), Answers: pty.Answers{}, TurnWaited: nil,
	})

	sessionID, err := host.Start(t.Context(), pty.Spec{
		Argv: []string{"/bin/claude"}, Dir: t.TempDir(), Env: nil, Name: "hosted",
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	server, err := app.StartWith(ctx, lookupFrom(env), io.Discard, host)
	if err != nil {
		t.Fatal(err)
	}

	closeStatus := viewTerminal(t, server, sessionID)

	cancel()

	select {
	case <-server.Done():
	case <-time.After(testtime.Scaled(quitWait)):
		t.Fatal("the server did not stop")
	}

	checkStopped(t, program)

	select {
	case status := <-closeStatus:
		if status != websocket.StatusGoingAway {
			t.Errorf("the viewer was closed with %v, want going away", status)
		}
	case <-time.After(testtime.Scaled(quitWait)):
		t.Fatal("the viewer was never closed")
	}
}

func checkStopped(t *testing.T, program *stoppedProgram) {
	t.Helper()

	program.mutex.Lock()
	defer program.mutex.Unlock()

	if !program.stopped {
		t.Fatal("the hosted program was not stopped at quit")
	}

	if !program.lockHeld {
		t.Error("the data folder was let go before the hosted program was stopped")
	}
}

// viewTerminal opens the session's terminal and reads it until it closes; the close's status
// comes on the channel.
func viewTerminal(t *testing.T, server *app.Server, sessionID pty.ID) <-chan websocket.StatusCode {
	t.Helper()

	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/term/" + string(sessionID)

	conn, response, err := websocket.Dial(t.Context(), url, &websocket.DialOptions{
		Subprotocols: []string{"conn.term.v1", "conn.key." + server.Secret.String()},
		HTTPClient:   http.DefaultClient,
		// A terminal is opened by the page, from Conn's own origin.
		HTTPHeader: http.Header{"Origin": []string{server.URL}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if response.Body != nil {
		_ = response.Body.Close()
	}

	readReplayEnd(t, conn)

	closed := make(chan websocket.StatusCode, 1)

	go func() {
		for {
			_, _, err := conn.Read(context.WithoutCancel(t.Context()))
			if err != nil {
				closed <- websocket.CloseStatus(err)

				return
			}
		}
	}()

	return closed
}

// readReplayEnd reads the terminal until the end of its replay.
func readReplayEnd(t *testing.T, conn *websocket.Conn) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(quitWait))
	defer cancel()

	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if kind == websocket.MessageText {
			if strings.Contains(string(data), "replay-end") {
				return
			}
		}
	}
}
