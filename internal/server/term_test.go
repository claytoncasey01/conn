package server_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/server"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// replayEnd is the text frame that ends a viewer's replay.
const replayEnd = `{"t":"replay-end"}`

// terminalRig is a real Conn server on a loopback port, with a real host of fake programs (or
// real ones) behind its terminal route, and the count of calls the route made to the host.
type terminalRig struct {
	key      secret.Secret
	programs *fakePrograms
	host     *pty.Host
	counted  *countedHost
	address  string
	stop     context.CancelFunc
	// terminals is the route's terminals.
	terminals *server.Terminals
}

// rigSetup is how a rig differs from the usual one.
type rigSetup struct {
	times server.TerminalTimes
	// spawner, when set, starts the programs in place of fakes.
	spawner pty.Spawner
	// readTimeout is the HTTP server's ReadTimeout, which Conn's own server sets.
	readTimeout time.Duration
	logger      *lockedBuffer
	// host, when set, is the host behind the route in place of the real one.
	host server.TerminalHost
	// programs, when set, is the fake programs' spawner.
	programs *fakePrograms
}

func newTerminalRig(t *testing.T, setup rigSetup) *terminalRig {
	t.Helper()

	key, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	programs, host := rigHost(setup)
	counted := &countedHost{host: host}
	lifetime, stop := context.WithCancel(context.Background())

	var behind server.TerminalHost = counted
	if setup.host != nil {
		behind = setup.host
	}

	terminals := server.NewTerminalsTimed(lifetime, behind, rigTimes(setup))
	handler := &server.Server{UI: bundle(), Secret: key, Terminals: terminals}

	if setup.logger != nil {
		handler.Logger = newLogger(setup.logger)
	}

	test := httptest.NewUnstartedServer(handler)
	test.Config.ReadTimeout = setup.readTimeout
	test.Start()

	t.Cleanup(func() {
		stop()
		test.Close()

		_ = host.StopAll(context.Background())
	})

	return &terminalRig{
		key: key, programs: programs, host: host, counted: counted,
		address: strings.TrimPrefix(test.URL, "http://"), stop: stop, terminals: terminals,
	}
}

// rigHost is the rig's host and its fake programs: the setup's spawner, else the programs'.
func rigHost(setup rigSetup) (*fakePrograms, *pty.Host) {
	programs := setup.programs
	if programs == nil {
		programs = &fakePrograms{}
	}

	spawner := setup.spawner
	if spawner == nil {
		spawner = programs.spawn
	}

	return programs, pty.NewHost(pty.Config{
		Spawner: spawner, Limits: pty.DefaultLimits(), Answers: pty.Answers{},
	})
}

// rigTimes are the setup's terminal times. A test's viewer answers the server's pings only while
// the test reads it, and most tests stop reading once they have seen what they wait for. The
// server's keepalive rightly drops such a viewer once a ping goes unanswered past its timeout,
// which a slow run outlasts; so a test that does not set the keepalive's times gets one no test
// outlasts.
func rigTimes(setup rigSetup) server.TerminalTimes {
	times := setup.times
	if times.PingInterval == 0 {
		times.PingInterval = rigPingInterval
	}

	return times
}

// start starts a session of a fake program and returns its id and the program.
func (rig *terminalRig) start(t *testing.T) (pty.ID, *fakeProgram) {
	t.Helper()

	sessionID := rig.startSpec(t, pty.Spec{
		Argv: []string{"/bin/true"}, Dir: "/", Env: nil, Name: "terminal test",
	})

	return sessionID, rig.programs.lastStarted()
}

func (rig *terminalRig) startSpec(t *testing.T, spec pty.Spec) pty.ID {
	t.Helper()

	sessionID, err := rig.host.Start(t.Context(), spec)
	if errors.Is(err, pty.ErrUnsupported) {
		t.Skip("terminals are not supported here")
	}

	if err != nil {
		t.Fatal(err)
	}

	return sessionID
}

// options are the page's own: its origin, and the secret in the second subprotocol.
func (rig *terminalRig) options() *websocket.DialOptions {
	header := http.Header{}
	header.Set("Origin", "http://"+rig.address)

	return &websocket.DialOptions{
		HTTPClient: nil, HTTPHeader: header, Host: "",
		Subprotocols:    []string{terminalProtocol, keyProtocol + rig.key.String()},
		CompressionMode: websocket.CompressionDisabled, CompressionThreshold: 0,
		OnPingReceived: nil, OnPongReceived: nil,
	}
}

// handshake is what a dial got: the connection, or the refusal's status, headers and body.
type handshake struct {
	conn   *websocket.Conn
	status int
	header http.Header
	body   string
	err    error
}

// dial opens the terminal at path (an id, or anything after /api/term/) as the page does, with
// change made to the options first; what change returns is added to the path. A connection it
// opens is closed when the test ends.
func (rig *terminalRig) dial(
	t *testing.T, path string, change func(*websocket.DialOptions) string,
) handshake {
	t.Helper()

	options := rig.options()
	if change != nil {
		path += change(options)
	}

	ctx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(waitLimit))
	defer cancel()

	conn, response, err := websocket.Dial(ctx, "ws://"+rig.address+"/api/term/"+path, options)

	got := handshake{conn: conn, status: 0, header: nil, body: "", err: err}

	if conn != nil {
		// The server's output frames are larger than the library's default read limit.
		conn.SetReadLimit(clientReadBytesMax)
		t.Cleanup(func() { _ = conn.CloseNow() })
	}

	if response == nil {
		return got
	}

	// Dial has already read a refusal's body into memory; a connection's answer has none.
	if response.Body != nil {
		defer func() { _ = response.Body.Close() }()
	}

	got.status = response.StatusCode
	got.header = response.Header

	got.body = takeBody(t, response)

	return got
}

// takeBody reads a handshake's answer's body, if it has one.
func takeBody(t *testing.T, response *http.Response) string {
	t.Helper()

	if response.Body == nil {
		return ""
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	return string(body)
}

// open dials the session's terminal as the page does and fails the test if it is refused.
func (rig *terminalRig) open(t *testing.T, sessionID pty.ID) *websocket.Conn {
	t.Helper()

	got := rig.dial(t, string(sessionID), nil)
	if got.err != nil {
		t.Fatalf("the page's handshake was refused: status %d: %v", got.status, got.err)
	}

	return got.conn
}

// read is the next message from conn, failing the test if none comes in time.
func read(t *testing.T, conn *websocket.Conn) (websocket.MessageType, []byte) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(waitLimit))
	defer cancel()

	kind, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("reading the terminal: %v", err)
	}

	return kind, data
}

// readOutputUntilText reads binary output until the next text message, and returns both.
func readOutputUntilText(t *testing.T, conn *websocket.Conn) ([]byte, string) {
	t.Helper()

	var output []byte

	for range messagesMax {
		kind, data := read(t, conn)
		if kind == websocket.MessageText {
			return output, string(data)
		}

		output = append(output, data...)
	}

	t.Fatalf("no text message in %d messages", messagesMax)

	return nil, ""
}

// messagesMax bounds how many messages a test reads looking for one.
const messagesMax = 100_000

// clientReadBytesMax is the test client's read limit: more than an output frame holds.
const clientReadBytesMax = 1 << 20

// screenLinesRead is how many of a screen's lines a test reads: more than a new terminal has.
const screenLinesRead = 100

// readOutputUntil reads binary output until it holds want, failing the test on a text message.
func readOutputUntil(t *testing.T, conn *websocket.Conn, want string) {
	t.Helper()

	var output []byte

	for range messagesMax {
		kind, data := read(t, conn)
		if kind == websocket.MessageText {
			t.Fatalf("a text message before the output: %s", data)
		}

		output = append(output, data...)
		if bytes.Contains(output, []byte(want)) {
			return
		}
	}

	t.Fatalf("the output never held %q", want)
}

// closeCode reads conn until it ends and returns the close code it ended with.
func closeCode(t *testing.T, conn *websocket.Conn) websocket.StatusCode {
	t.Helper()

	code, _ := closeReason(t, conn)

	return code
}

// closeReason reads conn until it ends and returns the close code and reason it ended with.
func closeReason(t *testing.T, conn *websocket.Conn) (websocket.StatusCode, string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(waitLimit))
	defer cancel()

	for range messagesMax {
		_, _, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				t.Fatal("the connection was not closed in time")
			}

			closed, isClose := errors.AsType[websocket.CloseError](err)
			if isClose {
				return closed.Code, closed.Reason
			}

			return websocket.CloseStatus(err), ""
		}
	}

	t.Fatalf("the connection was still open after %d messages", messagesMax)

	return 0, ""
}

// reasonInputRate is the close reason for input past the session's rate.
const reasonInputRate = "input over the session's rate"

// sendTyped sends an input frame and waits until the program has been given typed bytes in
// all, so frames never wait in the server's queue: a rate test then meets the rate, never the
// queue's bound.
func sendTyped(t *testing.T, conn *websocket.Conn, program *fakeProgram, frame string, typed int) {
	t.Helper()

	sendMessage(t, conn, websocket.MessageText, frame)

	deadline := time.Now().Add(testtime.Scaled(waitLimit))

	for len(program.writtenSoFar()) < typed {
		if time.Now().After(deadline) {
			t.Fatalf("the program was given %d bytes, want %d", len(program.writtenSoFar()), typed)
		}

		time.Sleep(typedPoll)
	}
}

// rigPingInterval is how often the rig's server pings a viewer, unless a test sets it: longer
// than any test runs.
const rigPingInterval = 24 * time.Hour

// typedPoll is how often sendTyped looks.
const typedPoll = 100 * time.Microsecond

// sendMessage writes one message, failing the test if it cannot.
func sendMessage(t *testing.T, conn *websocket.Conn, kind websocket.MessageType, data string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(waitLimit))
	defer cancel()

	err := conn.Write(ctx, kind, []byte(data))
	if err != nil {
		t.Fatalf("writing to the terminal: %v", err)
	}
}

// eventually polls until check is true, failing the test with what when it never is.
func eventually(t *testing.T, what string, check func() bool) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(waitLimit))

	for time.Now().Before(deadline) {
		if check() {
			return
		}

		time.Sleep(waitPoll)
	}

	t.Fatal(what)
}

// waitShown waits until the session's screen shows text, so the host has taken the output
// before it into its scrollback.
func waitShown(t *testing.T, host *pty.Host, sessionID pty.ID, text string) {
	t.Helper()

	eventually(t, "the screen never showed the output", func() bool {
		lines, _, err := host.ScreenLines(sessionID, screenLinesRead)
		if err != nil {
			t.Fatal(err)
		}

		return strings.Contains(strings.Join(lines, "\n"), text)
	})
}
