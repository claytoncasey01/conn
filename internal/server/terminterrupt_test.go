package server_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
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
// frames, which its viewers' input shares: once the burst is spent, with the clock stopped, the
// next is refused and types nothing.
func TestInterruptsAreUnderTheSessionsInputCap(t *testing.T) {
	t.Parallel()

	stopped := time.Unix(1_800_000_000, 0)
	rig := newTerminalRig(t, rigSetup{times: server.TerminalTimes{Now: func() time.Time {
		return stopped
	}}})
	sessionID, program := rig.start(t)

	for range server.InputFramesBurst {
		code, body := rig.postInterrupt(t, string(sessionID), "", nil)
		if code != http.StatusOK {
			t.Fatalf("code %d: %s", code, body)
		}
	}

	eventually(t, "every ETX typed", func() bool {
		return len(program.writtenSoFar()) == server.InputFramesBurst
	})

	code, body := rig.postInterrupt(t, string(sessionID), "", nil)
	if code != http.StatusTooManyRequests {
		t.Fatalf("code %d: %s", code, body)
	}

	if len(program.writtenSoFar()) != server.InputFramesBurst {
		t.Errorf("typed %d", len(program.writtenSoFar()))
	}
}
