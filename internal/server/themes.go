package server

import (
	"errors"
	"net/http"

	"github.com/claytoncasey01/conn/internal/gallery"
	"github.com/claytoncasey01/conn/internal/themes"
)

// themes answers /api/themes (the list) and /api/themes/<id>/theme.css. Every route takes GET
// only, a path that names nothing included.
func (s *Server) themes(w http.ResponseWriter, r *http.Request, route themes.Route) {
	if r.Method != http.MethodGet {
		sendJSON(w, http.StatusMethodNotAllowed, errorBody{Error: textMethod})

		return
	}

	defer s.recovered(w, r)

	switch route.Kind {
	case themes.RouteList:
		sendJSON(w, http.StatusOK, s.Themes.List())

		return
	case themes.RouteCSS:
		css, served := s.Themes.CSS(route.ID, r.URL.Query().Get("v"))
		if !served {
			break
		}

		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", cacheAPI)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)

		_, _ = w.Write([]byte(css))

		return
	case themes.RouteNone:
	}

	sendJSON(w, http.StatusNotFound, errorBody{Error: textNotFound})
}

// gallery answers /api/gallery: 404 for a path that names nothing, 405 for the wrong method,
// then for an install or an uninstall the header guard and the body.
func (s *Server) gallery(w http.ResponseWriter, r *http.Request, route gallery.RouteKind) {
	if route == gallery.RouteNone {
		sendJSON(w, http.StatusNotFound, errorBody{Error: textNotFound})

		return
	}

	if r.Method != route.Method() {
		sendJSON(w, http.StatusMethodNotAllowed, errorBody{Error: textMethod})

		return
	}

	defer s.recovered(w, r)

	if route == gallery.RouteIndex {
		reply := s.Gallery.Handle(r.Context(), route, nil)
		sendJSON(w, reply.Code, reply.Body)

		return
	}

	// Install writes files and fetches from GitHub, uninstall deletes a folder. A custom header
	// forces a CORS preflight, so no page that is not Conn's can ask.
	if r.Header.Get("X-Conn") != "1" {
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

	reply := s.Gallery.Handle(r.Context(), route, body)
	sendJSON(w, reply.Code, reply.Body)
}
