package server

import (
	"context"
	"net/http"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// tabHandler answers one route of a tab with a POST body.
type tabHandler func(ctx context.Context, route string, body *js.Object) (reviews.Reply, error)

// tabs answers /api/reviews and /api/myprs, and is false for any other path. Every route is a
// POST with Conn's own header: REVIEW and SELF-REVIEW launch a process, refresh spends a gh
// call, legacy rewrites reviews.json and thread writes to GitHub (a custom header forces a CORS
// preflight).
func (s *Server) tabs(w http.ResponseWriter, r *http.Request, urlPath string) bool {
	if route, ours := reviews.RouteOf(urlPath); ours {
		if s.Reviews != nil {
			s.tabPost(w, r, route == reviews.RouteNone, route, s.Reviews.Handle)

			return true
		}
	}

	if route, ours := myprs.RouteOf(urlPath); ours {
		if s.MyPrs != nil {
			s.tabPost(w, r, route == myprs.RouteNone, route, s.MyPrs.Handle)

			return true
		}
	}

	return false
}

func (s *Server) tabPost(w http.ResponseWriter, r *http.Request, none bool, route string,
	handle tabHandler,
) {
	if none {
		sendJSON(w, http.StatusNotFound, errorBody{Error: textNotFound})

		return
	}

	if !ownWrite(r) {
		sendJSON(w, http.StatusForbidden, errorBody{Error: textForbidden})

		return
	}

	body := historyBody(r)

	defer s.recovered(w, r)

	// A launch or a write goes on when the page that asked for it goes away: it is Conn's to
	// finish.
	reply, err := handle(context.WithoutCancel(r.Context()), route, body)
	if err != nil {
		if s.Logger != nil {
			s.Logger.Printf("request failed %s %v", r.URL.Path, err)
		}

		sendJSON(w, http.StatusInternalServerError, errorBody{Error: textFailed})

		return
	}

	sendRaw(w, reply.Code, js.Marshal(reply.Body))
}
