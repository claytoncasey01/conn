package server

import (
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"regexp"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/pty"
)

// The page's terminal background. A hosted program asks its terminal for the background colour
// (OSC 11) to pick a light or a dark palette, and the host answers it (pty's answers). Only the
// page knows the theme it wears (the person's choice, the system's light or dark), so it says
// the background its terminal is drawn on here, as it loads and whenever its theme changes, and
// the host answers with it from then on. The last page to say wins. What is taken is a colour
// and nothing else: #rrggbb, three channels, never text a program could be answered with.

const (
	// terminalBackgroundPath is the route.
	terminalBackgroundPath = "/api/term/background"
	// maxBackgroundBodyBytes bounds the body: {"background":"#rrggbb"} with room to spare.
	maxBackgroundBodyBytes = 256
)

// backgroundShape is the only colour the route takes: # and six hex digits.
var backgroundShape = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

var errNotBackground = errors.New(`the body must be {"background":"#rrggbb"}`)

// backgroundTold is the route's answer once the host is told.
type backgroundTold struct {
	OK bool `json:"ok"`
}

// terminalBackground answers PUT /api/term/background, the write guard and the body first. A
// server with no host to tell answers not found.
func (s *Server) terminalBackground(w http.ResponseWriter, r *http.Request) {
	if s.TerminalBackground == nil {
		sendJSON(w, http.StatusNotFound, errorBody{Error: textNotFound})

		return
	}

	if r.Method != http.MethodPut {
		sendJSON(w, http.StatusMethodNotAllowed, errorBody{Error: textMethod})

		return
	}

	if !fromOwnPage(r) {
		sendJSON(w, http.StatusForbidden, errorBody{Error: textForbidden})

		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, maxBackgroundBodyBytes+1))
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorBody{Error: errNotBackground.Error()})

		return
	}

	if len(data) > maxBackgroundBodyBytes {
		sendJSON(w, http.StatusRequestEntityTooLarge, errorBody{Error: errTooLarge.Error()})

		return
	}

	colour, read := backgroundOf(data)
	if !read {
		sendJSON(w, http.StatusBadRequest, errorBody{Error: errNotBackground.Error()})

		return
	}

	s.TerminalBackground(colour)
	sendJSON(w, http.StatusOK, backgroundTold{OK: true})
}

// backgroundOf is the colour a body names, and false when it names none.
func backgroundOf(data []byte) (pty.RGB, bool) {
	parsed, err := js.Parse(data)
	if err != nil {
		return pty.RGB{}, false
	}

	object, isObject := parsed.(*js.Object)
	if !isObject {
		return pty.RGB{}, false
	}

	value, _ := object.Get("background")

	text, isText := value.(string)
	if !isText {
		return pty.RGB{}, false
	}

	if !backgroundShape.MatchString(text) {
		return pty.RGB{}, false
	}

	channels, err := hex.DecodeString(text[1:])
	if err != nil {
		return pty.RGB{}, false
	}

	return pty.RGB{Red: channels[0], Green: channels[1], Blue: channels[2]}, true
}
