package server

import (
	"io"
	"net/http"

	"github.com/claytoncasey01/conn/internal/historyapi"
	"github.com/claytoncasey01/conn/internal/js"
)

// history answers /api/history, and is false for any other path. While History is off every
// route is a 503 that says why.
func (s *Server) history(w http.ResponseWriter, r *http.Request, urlPath string) bool {
	route, ours := historyapi.RouteOf(urlPath)
	if !ours {
		return false
	}

	if s.History == nil {
		sendJSON(w, http.StatusServiceUnavailable,
			errorBody{Error: "History is unavailable: " + s.HistoryOff})

		return true
	}

	body := js.NewObject()

	if route.Post() {
		if !ownWrite(r) {
			sendJSON(w, http.StatusForbidden, errorBody{Error: textForbidden})

			return true
		}

		body = historyBody(r)
	} else if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorBody{Error: textMethod})

		return true
	}

	defer s.recovered(w, r)

	reply, err := s.History.Handle(r.Context(), route, r.URL.Query(), body)
	if err != nil {
		if s.Logger != nil {
			s.Logger.Printf("request failed %s %v", r.URL.Path, err)
		}

		sendJSON(w, http.StatusInternalServerError,
			errorBody{Error: "the server could not handle that request"})

		return true
	}

	sendRaw(w, reply.Code, js.Marshal(reply.Body))

	return true
}

// historyBody is a POST body read as Bun read it: the first 64 KB or so, as a JSON object, else
// {}.
func historyBody(r *http.Request) *js.Object {
	data, err := io.ReadAll(io.LimitReader(r.Body, bodyBytesMax))
	if err != nil {
		return js.NewObject()
	}

	parsed, err := js.Parse(data)
	if err != nil {
		return js.NewObject()
	}

	object, isObject := parsed.(*js.Object)
	if !isObject {
		return js.NewObject()
	}

	return object
}
