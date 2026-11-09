package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/claytoncasey01/conn/internal/newsession"
)

// newSession answers /api/sessions/new, and is false for any other path: GET lists the
// repositories a session may start in, and POST starts one. A start runs a program, so it takes
// the strictest guard a write has: POST, Conn's header and an Origin (which the gate has already
// checked is this server's own), and a body that is a JSON object of at most bodyBytesMax.
func (s *Server) newSession(w http.ResponseWriter, r *http.Request, urlPath string) bool {
	if urlPath != newsession.Path {
		return false
	}

	if s.NewSession == nil {
		return false
	}

	switch r.Method {
	case http.MethodGet:
		defer s.recovered(w, r)

		reply := s.NewSession.Choices()
		sendJSON(w, reply.Code, reply.Body)
	case http.MethodPost:
		s.startSession(w, r)
	default:
		sendJSON(w, http.StatusMethodNotAllowed, errorBody{Error: textMethod})
	}

	return true
}

// startSession is a POST to /api/sessions/new.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request) {
	if !fromOwnPage(r) {
		sendJSON(w, http.StatusForbidden, errorBody{Error: textForbidden})

		return
	}

	defer s.recovered(w, r)

	body, err := readObject(r)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, errTooLarge) {
			code = http.StatusRequestEntityTooLarge
		}

		sendJSON(w, code, errorBody{Error: err.Error()})

		return
	}

	// A start goes on when the page that asked for it goes away: it is Conn's to finish.
	reply, err := s.NewSession.Start(context.WithoutCancel(r.Context()), body)
	if err != nil {
		if s.Logger != nil {
			s.Logger.Printf("request failed %s %v", r.URL.Path, err)
		}

		sendJSON(w, http.StatusInternalServerError, errorBody{Error: textFailed})

		return
	}

	if reply.Code == http.StatusOK {
		if s.Sessions != nil {
			s.Sessions.Bump()
		}
	}

	sendJSON(w, reply.Code, reply.Body)
}
