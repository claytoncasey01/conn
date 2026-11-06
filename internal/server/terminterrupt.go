package server

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/claytoncasey01/conn/internal/pty"
)

// The interrupt of a hosted session: POST /api/term/<id>/interrupt types the one byte a
// keyboard's Ctrl-C does (ETX) into the session's terminal, through the host's one writer, as a
// key a viewer typed would be. It is what a person needs when a program floods output faster than
// the page can draw it: the viewer is dropped then (the reader never waits for it), so its keys
// have no way in, and this needs none. The terminal's line discipline makes ETX a SIGINT for
// the program in the foreground when it reads in cooked mode; a program reading raw keys (claude)
// gets it as Ctrl-C and handles it as one. It is input, the action the spec's Security section
// lets a client take, never a signal; it names nothing but a session the host issued, takes no
// body, needs the page's write guard on top of the gate every /api route has, and spends the
// session's input budget, which its viewers' keys share.

const (
	// interruptSuffix ends the route's path, after the session's id.
	interruptSuffix = "/interrupt"
	// interruptKey is ETX: Ctrl-C on a keyboard.
	interruptKey = 0x03

	textEnded     = "the session has ended"
	textInputCap  = "too much input to this session at once: try again in a moment"
	textNotTaking = "the program is not taking input"
	textPasteShut = "a paste into the terminal was cut short and has just been closed: " +
		"interrupt again"
	textNotEmpty = "an interrupt takes no body"
)

// interruptID is the session an interrupt route names, and whether the path is one.
func interruptID(urlPath string) (pty.ID, bool) {
	rest, found := strings.CutSuffix(urlPath, interruptSuffix)
	if !found {
		return "", false
	}

	return terminalID(rest)
}

// interrupt answers the interrupt route: the write guard, an empty body, the input budget, then
// ETX typed through the host.
func (t *Terminals) interrupt(w http.ResponseWriter, r *http.Request, sessionID pty.ID) {
	if r.Method != http.MethodPost {
		sendJSON(w, http.StatusMethodNotAllowed, errorBody{Error: textMethod})

		return
	}

	if !fromOwnPage(r) {
		sendJSON(w, http.StatusForbidden, errorBody{Error: textForbidden})

		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if err != nil {
		sendJSON(w, http.StatusBadRequest, errorBody{Error: textNotEmpty})

		return
	}

	if len(data) > 0 {
		sendJSON(w, http.StatusBadRequest, errorBody{Error: textNotEmpty})

		return
	}

	t.budgets.join(sessionID)
	defer t.budgets.leave(sessionID)

	if !t.budgets.spend(sessionID, inputCost{bytes: 1, resize: false}) {
		sendJSON(w, http.StatusTooManyRequests, errorBody{Error: textInputCap})

		return
	}

	err = t.host.Write(sessionID, []byte{interruptKey})
	if err != nil {
		code, text := interruptFailure(err)
		sendJSON(w, code, errorBody{Error: text})

		return
	}

	sendJSON(w, http.StatusOK, interrupted{OK: true})
}

// interrupted is the route's answer once ETX is typed.
type interrupted struct {
	OK bool `json:"ok"`
}

// interruptFailure is the code and the words for an interrupt the host could not type.
func interruptFailure(err error) (int, string) {
	switch {
	case errors.Is(err, pty.ErrNoSession):
		return http.StatusNotFound, textNotFound
	case errors.Is(err, pty.ErrEnded):
		return http.StatusConflict, textEnded
	case errors.Is(err, pty.ErrPasteClosed):
		return http.StatusConflict, textPasteShut
	case errors.Is(err, pty.ErrInputBlocked):
		return http.StatusBadGateway, textNotTaking
	}

	return http.StatusInternalServerError, textFailed
}
