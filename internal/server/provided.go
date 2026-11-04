package server

import (
	"errors"
	"net/http"

	"github.com/claytoncasey01/conn/internal/provided"
)

// provided answers a route under /api/provided: 404 for a path that names nothing, 405 for the
// wrong method, then for an install or a remove the write guard and the body.
func (s *Server) provided(w http.ResponseWriter, r *http.Request, route provided.RouteKind) {
	if route == provided.RouteNone {
		sendJSON(w, http.StatusNotFound, errorBody{Error: textNotFound})

		return
	}

	if r.Method != route.Method() {
		sendJSON(w, http.StatusMethodNotAllowed, errorBody{Error: textMethod})

		return
	}

	defer s.recovered(w, r)

	if route == provided.RouteList {
		reply := s.Provided.Handle(route, nil)
		sendJSON(w, reply.Code, reply.Body)

		return
	}

	// An install or a remove changes Claude Code's folder. A custom header forces a CORS
	// preflight, and a browser always sends an Origin on a POST (ServeHTTP has refused one that is
	// not local), so no page that is not Conn's can ask.
	if !fromOwnPage(r) {
		sendJSON(w, http.StatusForbidden, errorBody{Error: textForbidden})

		return
	}

	body, err := readObject(r)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, errTooLarge) {
			code = http.StatusRequestEntityTooLarge
		}

		sendJSON(w, code, errorBody{Error: err.Error()})

		return
	}

	reply := s.Provided.Handle(route, body)
	sendJSON(w, reply.Code, reply.Body)
}
