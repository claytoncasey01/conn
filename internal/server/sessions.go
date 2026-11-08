package server

import (
	"net/http"
	"regexp"
)

// agentRoute is one agent page of a live session: /api/sessions/<id>/agents/<agent>, the
// session's id a pid or a hosted session's (see sessionID).
var agentRoute = regexp.MustCompile(`^/api/sessions/([0-9a-f]+)/agents/([A-Za-z0-9_-]+)$`)

const (
	pathSnapshot = "/api/snapshot"
	pathEvents   = "/api/events"
	// streamRetry tells a page how long to wait before it reconnects a dropped stream.
	streamRetry = "retry: 1500\n"
)

// sessions answers the session routes, and is false for a path that is none of them. They
// answer whatever the method.
func (s *Server) sessions(w http.ResponseWriter, r *http.Request, urlPath string) bool {
	if s.Sessions == nil {
		return false
	}

	switch urlPath {
	case pathSnapshot:
		s.snapshot(w, r)

		return true
	case pathEvents:
		s.events(w, r)

		return true
	}

	match := agentRoute.FindStringSubmatch(urlPath)
	if match == nil {
		return false
	}

	if !sessionID(match[1]) {
		return false
	}

	page, found, err := s.Sessions.AgentDetail(r.Context(), match[1], match[2])
	if err != nil {
		return true
	}

	if !found {
		sendJSON(w, http.StatusNotFound, errorBody{Error: textNotFound})

		return true
	}

	sendRaw(w, http.StatusOK, page)

	return true
}

func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	data, err := s.Sessions.Snapshot(r.Context())
	if err != nil {
		return
	}

	sendRaw(w, http.StatusOK, data)
}

// events is the live-update stream: the snapshot now, then again whenever it changes (and every
// 5 s), until the page goes or the server stops.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	// The page joins before its first snapshot is taken: a change that lands between the two is
	// pushed to it (at worst as one snapshot more, which is whole and harmless), and the page is
	// counted before it has read a byte.
	hub := s.Sessions.Hub()
	client := hub.Join()

	defer hub.Leave(client)

	data, err := s.Sessions.Snapshot(r.Context())
	if err != nil {
		return
	}

	controller := http.NewResponseController(w)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", cacheAPI)
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	_, _ = w.Write(append([]byte(streamRetry+"data: "), append(data, '\n', '\n')...))
	_ = controller.Flush()

	// A page that opens gets the waiting screens read now, not at the next tick; the next frame
	// carries what they ask.
	s.Sessions.ReadAsks(r.Context())

	for {
		select {
		case frame := <-client.Frames():
			_, err := w.Write(frame)
			if err != nil {
				return
			}

			_ = controller.Flush()
		case <-r.Context().Done():
			return
		case <-hub.Closed():
			return
		}
	}
}

// sendRaw writes JSON already written, never cached.
func sendRaw(w http.ResponseWriter, code int, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", cacheAPI)
	w.WriteHeader(code)

	_, _ = w.Write(data)
}
