package server

import (
	"errors"
	"io"
	"net/http"
	"regexp"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/sessions"
)

var (
	// stopRoute stops a live session: POST /api/sessions/<pid>/stop.
	stopRoute = regexp.MustCompile(`^/api/sessions/([0-9]+)/stop$`)
	// actionRoute is the other actions on a live session.
	actionRoute = regexp.MustCompile(`^/api/sessions/([0-9]+)/(reply|answer|terminal)$`)
)

// actions answers the routes that act on a live session, and is false for a path that is none of
// them. Each changes something outside this process, so each takes POST with Conn's header only
// (a custom header forces a CORS preflight, so no other site can fire one).
func (s *Server) actions(w http.ResponseWriter, r *http.Request, urlPath string) bool {
	if s.Sessions == nil {
		return false
	}

	if match := stopRoute.FindStringSubmatch(urlPath); match != nil {
		if !ownWrite(r) {
			sendJSON(w, http.StatusForbidden, errorBody{Error: textForbidden})

			return true
		}

		s.answer(w, s.Sessions.Stop(r.Context(), match[1], forced(r)))

		return true
	}

	match := actionRoute.FindStringSubmatch(urlPath)
	if match == nil {
		return false
	}

	if !ownWrite(r) {
		sendJSON(w, http.StatusForbidden, errorBody{Error: textForbidden})

		return true
	}

	defer s.recovered(w, r)

	body, tooLarge := lenientObject(r)
	if tooLarge {
		sendJSON(w, http.StatusRequestEntityTooLarge, errorBody{Error: errTooLarge.Error()})

		return true
	}

	s.answer(w, s.act(r, match[1], match[2], body))

	return true
}

func (s *Server) act(r *http.Request, pid, action string, body *js.Object) sessions.Reply {
	switch action {
	case "reply":
		text, _ := body.Get("text")

		return s.Sessions.Reply(r.Context(), pid, text)
	case "answer":
		askID, _ := body.Get("askId")
		choice, hasChoice := body.Get("choice")
		text, hasText := body.Get("text")

		return s.Sessions.Answer(r.Context(), pid, sessions.AnswerBody{
			AskID: askID, Choice: choice, Text: text, HasChoice: hasChoice, HasText: hasText,
		})
	}

	return s.Sessions.OpenTerminal(r.Context(), pid)
}

// answer sends an action's reply; one that went through moves the snapshot's version.
func (s *Server) answer(w http.ResponseWriter, reply sessions.Reply) {
	if reply.Code == http.StatusOK {
		s.Sessions.Bump()
	}

	sendRaw(w, reply.Code, js.Marshal(reply.Body))
}

func ownWrite(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}

	return r.Header.Get("X-Conn") == "1"
}

// forced is a stop body's force: true only when it is exactly true. A body that is not JSON, or
// not an object, forces nothing.
func forced(r *http.Request) bool {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return false
	}

	if len(data) == 0 {
		return false
	}

	parsed, err := js.Parse(data)
	if err != nil {
		return false
	}

	object, _ := parsed.(*js.Object)
	force, _ := object.Get("force")

	return force == true
}

// lenientObject reads a body of at most 64 KB as a JSON object; one that is empty, not JSON or
// not an object is {}. tooLarge is set past the limit.
func lenientObject(r *http.Request) (*js.Object, bool) {
	body, err := readObject(r)
	if errors.Is(err, errTooLarge) {
		return nil, true
	}

	if err != nil {
		return js.NewObject(), false
	}

	return body, false
}
