package server

import (
	"net/http"

	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/openwork"
)

// work answers Open work's routes, and is false for any other path: GET /api/work, and the
// POSTs that change something outside this process, with the same guard as a session's stop.
// While Open work is off none of them is there: after the guard and the size check, they answer
// 404 with the reason.
func (s *Server) work(w http.ResponseWriter, r *http.Request, urlPath string) bool {
	if s.Work == nil {
		return false
	}

	if urlPath == openwork.ListPath {
		if r.Method != http.MethodGet {
			return false
		}

		if !s.Work.On() {
			sendJSON(w, http.StatusNotFound, errorBody{Error: jira.Off})

			return true
		}

		sendRaw(w, http.StatusOK, js.Marshal(s.Work.Tickets()))

		return true
	}

	route, ours := openwork.RouteOf(urlPath)
	if !ours {
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

	if !s.Work.On() {
		sendJSON(w, http.StatusNotFound, errorBody{Error: jira.Off})

		return true
	}

	s.workReply(w, r, route, body)

	return true
}

func (s *Server) workReply(w http.ResponseWriter, r *http.Request, route openwork.Route,
	body *js.Object,
) {
	reply, err := s.Work.Handle(route, body)
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

	sendRaw(w, reply.Code, js.Marshal(reply.Body))
}
