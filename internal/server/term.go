package server

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/claytoncasey01/conn/internal/pty"
)

// The terminal route: GET /api/term/<session id>, upgraded to a WebSocket over which a page
// views a hosted session and types into it. The gate (gate.go) admits the handshake only from
// the server's own page, with the secret in its subprotocols; this file then refuses everything
// else that is not a well-formed handshake for a session the host holds, before it asks the host
// anything, and bounds everything after.
const (
	// terminalPrefix is the route's path before the session's id.
	terminalPrefix = "/api/term/"

	// inputFrameBytesMax is the most input one client frame carries; it is also the most the
	// host types in one Write.
	inputFrameBytesMax = 64 << 10
	// frameEnvelopeBytesMax is the most a client frame's JSON adds around its input, so
	// frameBytesMax is the most one client frame may be: the WebSocket's read limit. A page
	// splits a paste so that each frame, escaped as JSON, stays within it.
	frameEnvelopeBytesMax = 256
	frameBytesMax         = inputFrameBytesMax + frameEnvelopeBytesMax
	// outputFrameBytesMax is the most one binary frame of output holds; the replay and a large
	// batch go in several.
	outputFrameBytesMax = 64 << 10
	// connectionsMax is the most terminals open at once over all sessions: more than the host's
	// sessions times their viewers, so it only refuses a flood of handshakes for ended sessions.
	connectionsMax = 64

	// handshakeTimeout bounds the writing of the handshake's answer.
	handshakeTimeout = 5 * time.Second
	// frameWriteTimeout bounds the writing of each frame: a viewer that takes longer is closed.
	frameWriteTimeout = 5 * time.Second
	// pingInterval is how often a viewer is pinged, and pingTimeout how long its answer may
	// take: a dead peer is dropped within both. The answer waits for the input before it to be
	// typed, which takes at most the host's turn wait and write timeout, 10 s in all.
	pingInterval = 15 * time.Second
	pingTimeout  = 20 * time.Second

	// websocketVersion is the one WebSocket version there is; keyBytes is the size of a
	// handshake's nonce.
	websocketVersion = "13"
	keyBytes         = 16
)

// The route's refusals, in its own words.
const (
	textUpgradeRequired = "this route takes a WebSocket handshake only"
	textTooManyViewers  = "the session has too many viewers"
	textTooManyOpen     = "too many terminals are open"
	textNoSession       = "no such session"
)

// TerminalHost is what the terminal route needs of the host of sessions; *pty.Host is one.
type TerminalHost interface {
	Subscribe(sessionID pty.ID) (pty.Viewer, error)
	Write(sessionID pty.ID, data []byte) error
	Resize(sessionID pty.ID, columns, rows uint16) error
	Live() []pty.Info
}

// terminalTimes are the route's waits.
type terminalTimes struct {
	handshakeTimeout  time.Duration
	frameWriteTimeout time.Duration
	pingInterval      time.Duration
	pingTimeout       time.Duration
}

// Terminals answers the terminal route for a host's sessions.
type Terminals struct {
	host    TerminalHost
	times   terminalTimes
	budgets *budgets
	// open holds a value for each terminal open, at most connectionsMax.
	open chan struct{}
	// stopping is closed when the terminals' context ends: every terminal open is closed. A
	// connection the HTTP server has handed over outlives the server's own shutdown otherwise.
	stopping <-chan struct{}
}

// NewTerminals answers the terminal route for host's sessions until ctx ends, when it closes
// every terminal open.
func NewTerminals(ctx context.Context, host TerminalHost) *Terminals {
	if host == nil {
		panic("server: NewTerminals needs a host")
	}

	return &Terminals{
		host: host,
		times: terminalTimes{
			handshakeTimeout:  handshakeTimeout,
			frameWriteTimeout: frameWriteTimeout,
			pingInterval:      pingInterval,
			pingTimeout:       pingTimeout,
		},
		budgets:  newBudgets(time.Now),
		open:     make(chan struct{}, connectionsMax),
		stopping: ctx.Done(),
	}
}

// terminalID is the session id a terminal route's path names, and whether it is one: exactly
// /api/term/ and an id of the host's shape, as sent.
func terminalID(urlPath string) (pty.ID, bool) {
	text, found := strings.CutPrefix(urlPath, terminalPrefix)
	if !found {
		return "", false
	}

	sessionID, err := pty.ParseID(text)
	if err != nil {
		return "", false
	}

	return sessionID, true
}

// terminal answers a path under the terminal route, and is false for any other path. A route
// that names no session, or a server with no terminals, is not found.
func (s *Server) terminal(w http.ResponseWriter, r *http.Request, urlPath string) bool {
	if !strings.HasPrefix(urlPath, terminalPrefix) {
		return false
	}

	sessionID, found := terminalID(urlPath)
	if !found {
		sendJSON(w, http.StatusNotFound, errorBody{Error: textNotFound})

		return true
	}

	if s.Terminals == nil {
		sendJSON(w, http.StatusNotFound, errorBody{Error: textNotFound})

		return true
	}

	s.Terminals.serve(w, r, sessionID)

	return true
}

// serve takes a handshake for the session's terminal and runs the terminal until either side
// ends it. Everything is refused before the host is asked anything, but for the session itself:
// the host's Subscribe both finds it and takes one of its viewers' places, which is given back
// on every way out.
func (t *Terminals) serve(w http.ResponseWriter, r *http.Request, sessionID pty.ID) {
	if !wellFormedHandshake(r) {
		w.Header().Set("Upgrade", "websocket")
		sendJSON(w, http.StatusUpgradeRequired, errorBody{Error: textUpgradeRequired})

		return
	}

	select {
	case t.open <- struct{}{}:
	default:
		sendJSON(w, http.StatusServiceUnavailable, errorBody{Error: textTooManyOpen})

		return
	}

	defer func() { <-t.open }()

	viewer, err := t.host.Subscribe(sessionID)
	if err != nil {
		refuseViewer(w, err)

		return
	}

	defer viewer.Close()

	t.budgets.join(sessionID)
	defer t.budgets.leave(sessionID)

	conn := t.accept(w, r)
	if conn == nil {
		return
	}

	// Every way out ends here, so the connection is always closed; a close already made does
	// nothing.
	defer func() { _ = conn.CloseNow() }()

	t.run(conn, viewer, sessionID)
}

// refuseViewer answers a handshake whose session the host would not give a viewer.
func refuseViewer(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pty.ErrNoSession):
		sendJSON(w, http.StatusNotFound, errorBody{Error: textNoSession})
	case errors.Is(err, pty.ErrViewersFull):
		sendJSON(w, http.StatusServiceUnavailable, errorBody{Error: textTooManyViewers})
	default:
		sendJSON(w, http.StatusInternalServerError, errorBody{Error: textFailed})
	}
}

// wellFormedHandshake says whether a request is a WebSocket handshake the library will take: a
// GET asking to upgrade, of the one version, with one nonce of the right size. It is checked
// here so that a malformed one is refused in the server's own words: the library's refusals
// quote the request's headers.
func wellFormedHandshake(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}

	if !asksForWebSocket(r.Header) {
		return false
	}

	versions := r.Header.Values("Sec-WebSocket-Version")
	if len(versions) != 1 {
		return false
	}

	if versions[0] != websocketVersion {
		return false
	}

	keys := r.Header.Values("Sec-WebSocket-Key")
	if len(keys) != 1 {
		return false
	}

	nonce, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keys[0]))
	if err != nil {
		return false
	}

	return len(nonce) == keyBytes
}

// accept completes the handshake, or answers it with a refusal and returns nil. The answer must
// be written within handshakeTimeout; taking the connection over clears that deadline, and any
// the HTTP server had set, so the terminal lives as long as its viewer.
func (t *Terminals) accept(w http.ResponseWriter, r *http.Request) *websocket.Conn {
	err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(t.times.handshakeTimeout))
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, errorBody{Error: textFailed})

		return nil
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Only the protocol's name is offered, so it is the only one echoed: the secret's
		// protocol is never chosen.
		Subprotocols: []string{terminalProtocol},
		// The gate has checked the Origin exactly; the library checks it again.
		InsecureSkipVerify:   false,
		OriginPatterns:       nil,
		CompressionMode:      websocket.CompressionDisabled,
		CompressionThreshold: 0,
		OnPingReceived:       nil,
		OnPongReceived:       nil,
	})
	if err != nil {
		// Accept has answered the request; its error quotes the request, so it is not logged.
		return nil
	}

	if conn.Subprotocol() != terminalProtocol {
		_ = conn.Close(websocket.StatusPolicyViolation, reasonMalformed)

		return nil
	}

	conn.SetReadLimit(frameBytesMax)

	return conn
}
