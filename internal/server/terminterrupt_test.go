package server_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/server"
)

// postInterrupt sends POST /api/term/<id>/interrupt as the page does, with change applied.
func (rig *terminalRig) postInterrupt(t *testing.T, sessionText, body string,
	change func(r *http.Request),
) (int, string) {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		"http://"+rig.address+"/api/term/"+sessionText+"/interrupt", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	request.Header.Set(secret.Header, rig.key.Authorization())
	request.Header.Set("Origin", "http://"+rig.address)
	request.Header.Set("X-Conn", "1")

	if change != nil {
		change(request)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = response.Body.Close() }()

	text, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	return response.StatusCode, strings.TrimSpace(string(text))
}

// TestInterruptTypesCtrlCIntoTheProgram: an interrupt types the one byte a keyboard's Ctrl-C
// does, through the host's writer, with no viewer: a program whose viewers have all fallen
// behind still gets it.
func TestInterruptTypesCtrlCIntoTheProgram(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{})
	sessionID, program := rig.start(t)

	code, body := rig.postInterrupt(t, string(sessionID), "", nil)
	if code != http.StatusOK {
		t.Fatalf("code %d: %s", code, body)
	}

	if body != `{"ok":true}` {
		t.Errorf("body %s", body)
	}

	eventually(t, "ETX typed", func() bool {
		return bytes.Equal(program.writtenSoFar(), []byte{0x03})
	})
}

// TestInterruptReachesAProgramWhoseViewerFellBehind: a viewer that stops reading while the
// program floods output is dropped, and the interrupt still types ETX, needing no viewer.
func TestInterruptReachesAProgramWhoseViewerFellBehind(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{})
	sessionID, program := rig.start(t)
	conn := rig.open(t, sessionID)

	defer func() { _ = conn.CloseNow() }()

	chunk := bytes.Repeat([]byte("flood-line\r\n"), 2500)
	for range 800 {
		program.feed(t, chunk)
	}

	// The viewer is dropped, by its queue or by a write that waited too long: its keys have no
	// way in now.
	code, _ := closeReason(t, conn)
	if code == websocket.StatusNormalClosure {
		t.Fatal("the viewer was closed as if detached")
	}

	answered, body := rig.postInterrupt(t, string(sessionID), "", nil)
	if answered != http.StatusOK {
		t.Fatalf("code %d: %s", answered, body)
	}

	eventually(t, "ETX typed", func() bool {
		return bytes.Equal(program.writtenSoFar(), []byte{0x03})
	})
}

// TestInterruptIsRefusedUnlessThePageAsksForALiveSession: the secret, the page's Origin and its
// header are needed, the session must be one the host holds running, nothing but an empty POST
// is taken, and nothing is typed when any of that fails.
func TestInterruptIsRefusedUnlessThePageAsksForALiveSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		id     string
		body   string
		change func(r *http.Request)
		code   int
	}{
		{
			"no secret", "", "", func(r *http.Request) { r.Header.Del(secret.Header) },
			http.StatusUnauthorized,
		},
		{"a wrong secret", "", "", func(r *http.Request) {
			r.Header.Set(secret.Header, "Bearer "+strings.Repeat("B", 43))
		}, http.StatusUnauthorized},
		{"another origin", "", "", func(r *http.Request) {
			r.Header.Set("Origin", "http://evil.example")
		}, http.StatusForbidden},
		{
			"no header", "", "", func(r *http.Request) { r.Header.Del("X-Conn") },
			http.StatusForbidden,
		},
		{
			"a GET", "", "", func(r *http.Request) { r.Method = http.MethodGet },
			http.StatusMethodNotAllowed,
		},
		{"a body", "", "x", nil, http.StatusBadRequest},
		{"an unknown session", "0123456789abcdef0123456789abcdef", "", nil, http.StatusNotFound},
		{"not an id", "not-an-id", "", nil, http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newTerminalRig(t, rigSetup{})
			sessionID, program := rig.start(t)

			id := tt.id
			if id == "" {
				id = string(sessionID)
			}

			code, body := rig.postInterrupt(t, id, tt.body, tt.change)
			if code != tt.code {
				t.Fatalf("code %d, want %d: %s", code, tt.code, body)
			}

			if len(program.writtenSoFar()) != 0 {
				t.Errorf("typed %q", program.writtenSoFar())
			}
		})
	}
}

// TestInterruptOfAnEndedSessionTypesNothing: a session whose program has ended says so.
func TestInterruptOfAnEndedSessionTypesNothing(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{})
	sessionID, program := rig.start(t)

	err := rig.host.Stop(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}

	code, body := rig.postInterrupt(t, string(sessionID), "", nil)
	if code != http.StatusConflict {
		t.Fatalf("code %d: %s", code, body)
	}

	if body != `{"error":"the session has ended"}` {
		t.Errorf("body %s", body)
	}

	if len(program.writtenSoFar()) != 0 {
		t.Errorf("typed %q", program.writtenSoFar())
	}
}

// TestInterruptsAreUnderTheSessionsInputCap: an interrupt spends one of the session's input
// frames, which its viewers' input shares: once a viewer has spent the burst, with the clock
// stopped, the interrupt is refused and types nothing; and since it typed nothing, it does not
// count as sent, so one once the budget has refilled is typed, well inside the repeat window.
func TestInterruptsAreUnderTheSessionsInputCap(t *testing.T) {
	t.Parallel()

	clock := &movableClock{}
	clock.add(time.Hour)

	rig := newTerminalRig(t, rigSetup{times: server.TerminalTimes{Now: clock.now}})
	sessionID, program := rig.start(t)
	conn := rig.open(t, sessionID)

	defer func() { _ = conn.CloseNow() }()

	readOutputUntilText(t, conn)

	for index := range server.InputFramesBurst {
		sendTyped(t, conn, program, inputFrame("a"), index+1)
	}

	code, body := rig.postInterrupt(t, string(sessionID), "", nil)
	if code != http.StatusTooManyRequests {
		t.Fatalf("code %d: %s", code, body)
	}

	if len(program.writtenSoFar()) != server.InputFramesBurst {
		t.Errorf("typed %d", len(program.writtenSoFar()))
	}

	clock.add(interruptRefill)

	code, body = rig.postInterrupt(t, string(sessionID), "", nil)
	if code != http.StatusOK {
		t.Fatalf("once refilled: code %d: %s", code, body)
	}

	eventually(t, "the ETX typed", func() bool {
		return bytes.HasSuffix(program.writtenSoFar(), []byte{0x03})
	})
}

// interruptRefill is long enough for the input budget to refill a frame, and far shorter than
// the repeat window.
const interruptRefill = 10 * time.Millisecond

// movableClock is a clock a test moves on.
type movableClock struct{ at atomic.Int64 }

func (c *movableClock) now() time.Time { return time.Unix(0, c.at.Load()) }

func (c *movableClock) add(by time.Duration) { c.at.Add(int64(by)) }

// TestASecondInterruptSoonAfterIsNotSent: two presses close together send one Ctrl-C, since a
// second one would make claude quit; the second is answered that an interrupt was just sent or
// is being sent, and one after the window is sent.
func TestASecondInterruptSoonAfterIsNotSent(t *testing.T) {
	t.Parallel()

	clock := &movableClock{}
	clock.add(time.Hour)

	rig := newTerminalRig(t, rigSetup{times: server.TerminalTimes{Now: clock.now}})
	sessionID, program := rig.start(t)

	code, _ := rig.postInterrupt(t, string(sessionID), "", nil)
	if code != http.StatusOK {
		t.Fatalf("first: %d", code)
	}

	clock.add(server.InterruptRepeatWindow - time.Millisecond)

	code, body := rig.postInterrupt(t, string(sessionID), "", nil)
	if code != http.StatusConflict {
		t.Fatalf("second: %d %s", code, body)
	}

	if !strings.Contains(body, "just sent or is being sent") {
		t.Errorf("second: %s", body)
	}

	clock.add(time.Millisecond)

	code, _ = rig.postInterrupt(t, string(sessionID), "", nil)
	if code != http.StatusOK {
		t.Fatalf("after the window: %d", code)
	}

	eventually(t, "two ETX typed", func() bool {
		return bytes.Equal(program.writtenSoFar(), []byte{0x03, 0x03})
	})
}

// TestAnInterruptForAnUnknownSessionKeepsNothing: an id the host does not hold is refused before
// any budget or record is made for it.
func TestAnInterruptForAnUnknownSessionKeepsNothing(t *testing.T) {
	t.Parallel()

	rig := newTerminalRig(t, rigSetup{})

	for _, id := range []string{
		"0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210",
	} {
		code, _ := rig.postInterrupt(t, id, "", nil)
		if code != http.StatusNotFound {
			t.Fatalf("code %d", code)
		}
	}

	if kept := server.BudgetsKept(rig.terminals); kept != 0 {
		t.Errorf("%d budgets kept for sessions that do not exist", kept)
	}
}

// TestAnInterruptThatFailedIsNotCountedAsSent: an interrupt the program did not take (its write
// failed) typed nothing, so a press straight after it, inside the repeat window, is typed.
func TestAnInterruptThatFailedIsNotCountedAsSent(t *testing.T) {
	t.Parallel()

	// The program refuses the first write and takes the second.
	replies := make(chan error, 2)
	for _, reply := range []error{fmt.Errorf("write: %w", os.ErrDeadlineExceeded), nil} {
		replies <- reply
	}

	rig := newTerminalRig(t, rigSetup{programs: &fakePrograms{writeReplies: replies}})
	sessionID, program := rig.start(t)

	code, body := rig.postInterrupt(t, string(sessionID), "", nil)
	if code != http.StatusBadGateway {
		t.Fatalf("the failed press: %d %s", code, body)
	}

	code, body = rig.postInterrupt(t, string(sessionID), "", nil)
	if code != http.StatusOK {
		t.Fatalf("the press after it: %d %s", code, body)
	}

	eventually(t, "the second press typed one ETX", func() bool {
		return bytes.Equal(program.writtenSoFar(), []byte{0x03})
	})
}
