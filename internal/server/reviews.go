package server

import (
	"context"
	"net/http"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// reviews answers /api/reviews, and is false for any other path. Every route is a POST with
// Conn's own header: REVIEW launches a process, refresh spends a gh call and legacy rewrites
// reviews.json (a custom header forces a CORS preflight).
func (s *Server) reviews(w http.ResponseWriter, r *http.Request, urlPath string) bool {
	route, ours := reviews.RouteOf(urlPath)
	if !ours {
		return false
	}

	if s.Reviews == nil {
		return false
	}

	if route == reviews.RouteNone {
		sendJSON(w, http.StatusNotFound, errorBody{Error: textNotFound})

		return true
	}

	if !ownWrite(r) {
		sendJSON(w, http.StatusForbidden, errorBody{Error: textForbidden})

		return true
	}

	body := historyBody(r)

	defer s.recovered(w, r)

	// A launch goes on when the page that asked for it goes away: its worktree and session are
	// Conn's to finish.
	reply, err := s.Reviews.Handle(context.WithoutCancel(r.Context()), route, body)
	if err != nil {
		if s.Logger != nil {
			s.Logger.Printf("request failed %s %v", r.URL.Path, err)
		}

		sendJSON(w, http.StatusInternalServerError,
			errorBody{Error: textFailed})

		return true
	}

	sendRaw(w, reply.Code, js.Marshal(reply.Body))

	return true
}
