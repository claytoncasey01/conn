package server_test

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/server"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// lockedBuffer is a log's destination that a test can read while the server may still write.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	count, err := b.buffer.Write(data)
	if err != nil {
		return count, fmt.Errorf("writing the log: %w", err)
	}

	return count, nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buffer.String()
}

func newLogger(destination *lockedBuffer) *log.Logger { return log.New(destination, "", 0) }

// refusal is a change to the page's handshake that must be refused, and with what.
type refusal struct {
	name   string
	change func(rig *terminalRig, options *websocket.DialOptions) string
	want   int
}

func refusals() []refusal {
	withProtocols := func(protocols ...string) func(*terminalRig, *websocket.DialOptions) string {
		return func(_ *terminalRig, options *websocket.DialOptions) string {
			options.Subprotocols = protocols

			return ""
		}
	}

	return []refusal{
		{"a rebound host", func(_ *terminalRig, options *websocket.DialOptions) string {
			options.Host = "evil.example"

			return ""
		}, http.StatusForbidden},
		{
			"another site, with the right secret (CSRF)",
			func(_ *terminalRig, options *websocket.DialOptions) string {
				options.HTTPHeader.Set("Origin", "https://evil.example")

				return ""
			}, http.StatusForbidden,
		},
		{"another port", func(rig *terminalRig, options *websocket.DialOptions) string {
			options.HTTPHeader.Set("Origin", "http://"+strings.Replace(rig.address, ":", ":1", 1))

			return ""
		}, http.StatusForbidden},
		{"no origin", func(_ *terminalRig, options *websocket.DialOptions) string {
			options.HTTPHeader.Del("Origin")

			return ""
		}, http.StatusForbidden},
		{"a null origin", func(_ *terminalRig, options *websocket.DialOptions) string {
			options.HTTPHeader.Set("Origin", "null")

			return ""
		}, http.StatusForbidden},
		{"no secret", withProtocols(terminalProtocol), http.StatusUnauthorized},
		{"a wrong secret", func(_ *terminalRig, options *websocket.DialOptions) string {
			other, err := secret.New()
			if err != nil {
				panic(err)
			}

			options.Subprotocols = []string{terminalProtocol, keyProtocol + other.String()}

			return ""
		}, http.StatusUnauthorized},
		{
			"the secret only in a query string",
			func(rig *terminalRig, options *websocket.DialOptions) string {
				options.Subprotocols = []string{terminalProtocol}

				return "?k=" + rig.key.String()
			}, http.StatusUnauthorized,
		},
		{"the secret only in a cookie", func(
			rig *terminalRig, options *websocket.DialOptions,
		) string {
			options.Subprotocols = []string{terminalProtocol}
			options.HTTPHeader.Set("Cookie", "k="+rig.key.String())

			return ""
		}, http.StatusUnauthorized},
		{"the secret only as a bearer", func(
			rig *terminalRig, options *websocket.DialOptions,
		) string {
			options.Subprotocols = []string{terminalProtocol}
			options.HTTPHeader.Set(secret.Header, rig.key.Authorization())

			return ""
		}, http.StatusUnauthorized},
	}
}

// TestABadHandshakeNeverReachesTheHost dials the terminal of a running session every way that
// must be refused: each is refused before the route asks the host anything, and no refusal holds
// the secret.
func TestABadHandshakeNeverReachesTheHost(t *testing.T) {
	t.Parallel()

	for _, way := range refusals() {
		t.Run(way.name, func(t *testing.T) {
			t.Parallel()

			rig := newTerminalRig(t, rigSetup{})
			sessionID, _ := rig.start(t)
			before := rig.counted.calls.Load()

			refused := rig.dial(t, string(sessionID), func(options *websocket.DialOptions) string {
				return way.change(rig, options)
			})
			if refused.err == nil {
				t.Fatal("the handshake was taken")
			}

			if refused.status != way.want {
				t.Fatalf("status %d, want %d", refused.status, way.want)
			}

			if rig.counted.calls.Load() != before {
				t.Fatal("the route asked the host before refusing the handshake")
			}

			if strings.Contains(refused.body, rig.key.String()) {
				t.Fatal("the refusal holds the secret")
			}
		})
	}
}

// TestAnIDThatIsNotASessionsIsRefused refuses a malformed id before the host is asked, and an id
// of the right shape that names no session once the host says so.
func TestAnIDThatIsNotASessionsIsRefused(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		path      string
		want      int
		hostCalls int64
	}{
		{"capitals", strings.Repeat("A", 32), http.StatusUnauthorized, 0},
		{"too short", strings.Repeat("a", 31), http.StatusUnauthorized, 0},
		{"another segment", strings.Repeat("a", 32) + "/x", http.StatusUnauthorized, 0},
		{"none", "", http.StatusUnauthorized, 0},
		{"unknown", strings.Repeat("a", 32), http.StatusNotFound, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newTerminalRig(t, rigSetup{})

			got := rig.dial(t, tt.path, nil)
			if got.err == nil {
				t.Fatal("the handshake was taken")
			}

			if got.status != tt.want {
				t.Fatalf("status %d, want %d", got.status, tt.want)
			}

			if rig.counted.calls.Load() != tt.hostCalls {
				t.Fatalf("%d calls to the host, want %d", rig.counted.calls.Load(), tt.hostCalls)
			}
		})
	}
}

// A request to the terminal route that is not a WebSocket handshake gets a clean refusal, even
// with the secret, and never reaches the host.
func TestARequestThatIsNotAHandshakeIsRefused(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			rig := newTerminalRig(t, rigSetup{})
			sessionID, _ := rig.start(t)
			before := rig.counted.calls.Load()

			request, err := http.NewRequestWithContext(t.Context(), method,
				"http://"+rig.address+"/api/term/"+string(sessionID), nil)
			if err != nil {
				t.Fatal(err)
			}

			request.Header.Set("Origin", "http://"+rig.address)
			request.Header.Set(secret.Header, rig.key.Authorization())

			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}

			_ = response.Body.Close()

			if response.StatusCode != http.StatusUpgradeRequired {
				t.Fatalf("status %d, want %d", response.StatusCode, http.StatusUpgradeRequired)
			}

			if rig.counted.calls.Load() != before {
				t.Fatal("the route asked the host")
			}
		})
	}
}

// The server echoes only the protocol's name: the subprotocol that carries the secret never
// comes back, in the handshake's answer or anywhere in its headers.
func TestTheHandshakeEchoesOnlyTheProtocolName(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{})
	sessionID, _ := rig.start(t)

	got := rig.dial(t, string(sessionID), nil)
	if got.err != nil {
		t.Fatalf("the page's handshake was refused: %v", got.err)
	}

	if got.status != http.StatusSwitchingProtocols {
		t.Fatalf("status %d", got.status)
	}

	if got.header.Values(protocolHeader)[0] != terminalProtocol {
		t.Fatalf("echoed protocol %q, want %q", got.header.Get(protocolHeader), terminalProtocol)
	}

	if len(got.header.Values(protocolHeader)) != 1 {
		t.Fatalf("%d protocol headers echoed, want 1", len(got.header.Values(protocolHeader)))
	}

	if got.conn.Subprotocol() != terminalProtocol {
		t.Fatalf("negotiated %q", got.conn.Subprotocol())
	}

	for name, values := range got.header {
		for _, value := range values {
			if strings.Contains(value, rig.key.String()) {
				t.Fatalf("the answer's %s header holds the secret", name)
			}
		}
	}
}

// deadlineWriter is a ResponseWriter whose connection is a pipe, recording the write deadline
// the route sets before it takes the connection over.
type deadlineWriter struct {
	header http.Header
	code   int
	conn   net.Conn

	mu       sync.Mutex
	deadline time.Time
	hijacked bool
	// deadlineBeforeHijack is the deadline as it was when the connection was taken over.
	deadlineBeforeHijack time.Time
}

func (d *deadlineWriter) Header() http.Header { return d.header }

func (d *deadlineWriter) Write(data []byte) (int, error) { return len(data), nil }

func (d *deadlineWriter) WriteHeader(code int) { d.code = code }

func (d *deadlineWriter) SetWriteDeadline(deadline time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.deadline = deadline

	return nil
}

func (d *deadlineWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.hijacked = true
	d.deadlineBeforeHijack = d.deadline

	return d.conn, bufio.NewReadWriter(bufio.NewReader(d.conn), bufio.NewWriter(d.conn)), nil
}

// The handshake is bounded: the route gives the answer that switches protocols HandshakeTimeout
// to be written, before it takes the connection over (taking it over clears the deadline).
func TestTheHandshakeHasADeadline(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{})
	sessionID, _ := rig.start(t)

	serverEnd, clientEnd := net.Pipe()
	defer func() { _ = clientEnd.Close() }()

	writer := &deadlineWriter{header: http.Header{}, code: 0, conn: serverEnd}

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/term/"+string(sessionID), nil)
	request.Host = ownHost
	request.Header.Set("Origin", pageOrigin)
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	request.Header.Set(protocolHeader, terminalProtocol+", "+keyProtocol+rig.key.String())

	handler := &server.Server{
		UI: bundle(), Secret: rig.key,
		Terminals: server.NewTerminalsTimed(t.Context(), rig.counted, server.TerminalTimes{}),
	}

	started := time.Now()
	served := make(chan struct{})

	go func() {
		defer close(served)

		handler.ServeHTTP(writer, request)
	}()

	// The route writes the replay's end once it has the connection; reading it, then hanging
	// up, ends the route.
	_ = clientEnd.SetReadDeadline(time.Now().Add(testtime.Scaled(waitLimit)))
	_, _ = clientEnd.Read(make([]byte, 64))
	_ = clientEnd.Close()

	select {
	case <-served:
	case <-time.After(testtime.Scaled(waitLimit)):
		t.Fatal("the route did not end when its peer hung up")
	}

	writer.mu.Lock()
	defer writer.mu.Unlock()

	if !writer.hijacked {
		t.Fatalf("the route never took the connection over: status %d", writer.code)
	}

	bound := writer.deadlineBeforeHijack.Sub(started)
	if bound <= 0 {
		t.Fatal("no write deadline was set for the handshake")
	}

	if bound > server.HandshakeTimeout+testtime.Scaled(waitLimit) {
		t.Fatalf("the handshake's deadline is %v away, want about %v", bound,
			server.HandshakeTimeout)
	}

	if bound < server.HandshakeTimeout-time.Second {
		t.Fatalf("the handshake's deadline is %v away, want about %v", bound,
			server.HandshakeTimeout)
	}
}

// Nothing a viewer types, nothing the program prints and not the secret reaches the server's
// log, whatever happens on the connection.
func TestTheTerminalLogsNothingOfWhatPassesThrough(t *testing.T) {
	t.Parallel()

	const (
		typed   = "typed-hunter2"
		printed = "printed-hunter3"
	)

	logged := &lockedBuffer{}
	rig := newTerminalRig(t, rigSetup{logger: logged})
	sessionID, program := rig.start(t)

	conn := rig.open(t, sessionID)
	readOutputUntilText(t, conn)

	sendMessage(t, conn, websocket.MessageText, `{"t":"input","d":"`+typed+`"}`)
	eventually(t, "the input never reached the program", func() bool {
		return bytes.Contains(program.writtenSoFar(), []byte(typed))
	})

	program.feed(t, []byte(printed))
	readOutputUntil(t, conn, printed)

	sendMessage(t, conn, websocket.MessageText, `{"t":"`+typed+`"}`)
	closeCode(t, conn)

	rig.dial(t, string(sessionID), func(options *websocket.DialOptions) string {
		options.HTTPHeader.Set("Origin", "https://"+typed+".example")

		return ""
	})

	program.finish(0)
	rig.stop()

	for _, private := range []string{typed, printed, rig.key.String(), typed + ".example"} {
		if strings.Contains(logged.String(), private) {
			t.Fatal("the log holds what passed through the terminal")
		}
	}
}
