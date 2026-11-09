//go:build unix

// Package termleaktest_test checks that opening and closing terminals, cleanly or not, leaves no
// goroutine, no descriptor and no viewer's place behind. It is its own package so that its one
// test has the test binary to itself: any other test running beside it would open descriptors
// and start goroutines of its own.
package termleaktest_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/sys/unix"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/server"
	"github.com/claytoncasey01/conn/internal/testtime"
)

const (
	// rounds is how many times every way of leaving is tried after the first round.
	rounds = 5
	// maxViewers is the host's limit on one session's viewers.
	maxViewers = 4
	// maxDescriptorsScan is how many descriptors the count looks at.
	maxDescriptorsScan = 4096
	// waitLimit bounds every wait, and settlePoll is how often the counts are looked at.
	waitLimit  = 5 * time.Second
	settlePoll = 10 * time.Millisecond
	// handshakeKey is a valid Sec-WebSocket-Key.
	handshakeKey = "dGhlIHNhbXBsZSBub25jZQ=="
)

// rig is a real server with a real program behind its terminal route.
type rig struct {
	key       secret.Secret
	address   string
	sessionID pty.ID
}

func TestOpeningAndClosingTerminalsLeavesNothingBehind(t *testing.T) {
	t.Parallel()

	// An *os.File or socket left open is closed by its finalizer at some garbage collection,
	// which would hide the leak; with collection off, a leaked descriptor stays open.
	previous := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previous)

	served := newRig(t)

	// A first round opens what the runtime and the HTTP client open once.
	leaveEveryWay(t, served)
	waitPlacesFree(t, served)

	descriptors := descriptorsOpen()
	goroutines := runtime.NumGoroutine()

	for range rounds {
		leaveEveryWay(t, served)
	}

	waitPlacesFree(t, served)
	waitSettled(t, "goroutines", goroutines, runtime.NumGoroutine)
	waitSettled(t, "descriptors", descriptors, descriptorsOpen)
}

func newRig(t *testing.T) rig {
	t.Helper()

	key, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	host := pty.NewHost(pty.Config{
		Spawner: pty.Spawn, Limits: pty.DefaultLimits(), Answers: pty.Answers{},
	})

	sessionID, err := host.Start(t.Context(), pty.Spec{
		Argv: []string{"/bin/sh", "-c", "exec cat"}, Dir: t.TempDir(), Env: nil, Name: "leak",
	})
	if errors.Is(err, pty.ErrUnsupported) {
		t.Skip("terminals are not supported here")
	}

	if err != nil {
		t.Fatal(err)
	}

	lifetime, stop := context.WithCancel(context.Background())
	test := httptest.NewServer(&server.Server{
		UI:        fstest.MapFS{"index.html": {Data: []byte("<html></html>")}},
		Secret:    key,
		Terminals: server.NewTerminals(lifetime, host),
	})

	t.Cleanup(func() {
		stop()
		test.Close()

		_ = host.StopAll(context.Background())
	})

	return rig{key: key, address: strings.TrimPrefix(test.URL, "http://"), sessionID: sessionID}
}

// leaveEveryWay opens the terminal and leaves it each way a client can: a clean close, a hang-up,
// a frame that gets it closed, a handshake cut off halfway, and a handshake hung up on at once.
func leaveEveryWay(t *testing.T, served rig) {
	t.Helper()

	conn := dial(t, served)
	_ = conn.Close(websocket.StatusNormalClosure, "")

	_ = dial(t, served).CloseNow()

	// The replay's end is read first, so the bad frame cannot race it; the read after the bad
	// frame then ends with the server's close.
	conn = dial(t, served)

	_, replayed, err := conn.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if string(replayed) != `{"t":"replay-end"}` {
		t.Fatal("the replay of a program that printed nothing was not just its end")
	}

	_ = conn.Write(t.Context(), websocket.MessageText, []byte(`{"t":"exec"}`))

	_, _, err = conn.Read(t.Context())
	if err == nil {
		t.Fatal("a frame of an unknown type was taken")
	}

	_ = conn.CloseNow()

	request := handshake(served)
	rawSend(t, served, request[:len(request)/2])
	rawSend(t, served, request)
}

// dial opens the terminal as the page does.
func dial(t *testing.T, served rig) *websocket.Conn {
	t.Helper()

	header := http.Header{}
	header.Set("Origin", "http://"+served.address)

	conn, err := tryDial(t, served)
	if err != nil {
		t.Fatalf("the page's handshake was refused: %v", err)
	}

	return conn
}

// tryDial opens the terminal as the page does, or says why it could not.
func tryDial(t *testing.T, served rig) (*websocket.Conn, error) {
	t.Helper()

	header := http.Header{}
	header.Set("Origin", "http://"+served.address)

	ctx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(waitLimit))
	defer cancel()

	conn, response, err := websocket.Dial(ctx,
		"ws://"+served.address+"/api/term/"+string(served.sessionID),
		&websocket.DialOptions{
			HTTPClient: nil, HTTPHeader: header, Host: "",
			Subprotocols:    []string{"conn.term.v1", "conn.key." + served.key.String()},
			CompressionMode: websocket.CompressionDisabled, CompressionThreshold: 0,
			OnPingReceived: nil, OnPongReceived: nil,
		})
	// Dial has already read a refusal's body into memory; a connection's answer has none.
	if response != nil {
		if response.Body != nil {
			_ = response.Body.Close()
		}
	}

	if err != nil {
		return nil, fmt.Errorf("dialing the terminal: %w", err)
	}

	return conn, nil
}

// handshake is the page's handshake as raw HTTP.
func handshake(served rig) string {
	return "GET /api/term/" + string(served.sessionID) + " HTTP/1.1\r\n" +
		"Host: " + served.address + "\r\n" +
		"Origin: http://" + served.address + "\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Key: " + handshakeKey + "\r\n" +
		"Sec-WebSocket-Protocol: conn.term.v1, conn.key." + served.key.String() + "\r\n\r\n"
}

// rawSend connects, sends text and hangs up at once.
func rawSend(t *testing.T, served rig, text string) {
	t.Helper()

	var dialer net.Dialer

	conn, err := dialer.DialContext(t.Context(), "tcp", served.address)
	if err != nil {
		t.Fatal(err)
	}

	_, err = conn.Write([]byte(text))
	if err != nil {
		t.Fatal(err)
	}

	_ = conn.Close()
}

// waitPlacesFree waits until maxViewers viewers can open the terminal at once: every place a
// client that left held is free again.
func waitPlacesFree(t *testing.T, served rig) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(waitLimit))

	for time.Now().Before(deadline) {
		if openAtOnce(t, served) {
			return
		}

		time.Sleep(settlePoll)
	}

	t.Fatal("the places of viewers that left were never freed")
}

// openAtOnce says whether maxViewers viewers can open the terminal together; it closes them and
// waits until each is closed.
func openAtOnce(t *testing.T, served rig) bool {
	t.Helper()

	opened := make([]*websocket.Conn, 0, maxViewers)

	defer func() {
		for _, conn := range opened {
			_ = conn.Close(websocket.StatusNormalClosure, "")
		}
	}()

	for range maxViewers {
		conn, err := tryDial(t, served)
		if err != nil {
			return false
		}

		opened = append(opened, conn)
	}

	return true
}

// descriptorsOpen is how many of this process's first maxDescriptorsScan descriptors are open.
func descriptorsOpen() int {
	count := 0

	for descriptor := range maxDescriptorsScan {
		_, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0)
		if err == nil {
			count++
		}
	}

	return count
}

// waitSettled waits until count() is back to baseline or below.
func waitSettled(t *testing.T, what string, baseline int, count func() int) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(waitLimit))

	for time.Now().Before(deadline) {
		if count() <= baseline {
			return
		}

		time.Sleep(settlePoll)
	}

	t.Fatalf("%s: %d after the terminals were left, %d before", what, count(), baseline)
}
