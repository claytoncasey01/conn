package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/settingsapi"
)

// bodyBytesMax is the most a request body may hold. The rest of a larger one is not read.
const bodyBytesMax = 64 << 10

var (
	errTooLarge  = errors.New("the request body is too large")
	errNotObject = errors.New("the request body must be a JSON object")
)

// settings answers a route under /api/settings: 404 for a path that names nothing, 405 for the
// wrong method, then for anything but GET the write guard and the body.
func (s *Server) settings(w http.ResponseWriter, r *http.Request, route settingsapi.Route) {
	if route.Kind == settingsapi.KindNone {
		sendJSON(w, http.StatusNotFound, errorBody{Error: textNotFound})

		return
	}

	if r.Method != route.Method() {
		sendJSON(w, http.StatusMethodNotAllowed, errorBody{Error: textMethod})

		return
	}

	defer s.recovered(w, r)

	if route.Kind == settingsapi.KindGet {
		reply := s.Settings.Handle(r.Context(), route, nil)
		sendJSON(w, reply.Code, reply.Body)

		return
	}

	// A PUT changes what Conn may read, and browse and check spend gh calls and metered runs. A
	// custom header forces a CORS preflight, and a browser always sends an Origin on a PUT or a
	// POST (ServeHTTP has refused one that is not local), so no page that is not Conn's can ask.
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

	reply := s.Settings.Handle(r.Context(), route, body)
	sendJSON(w, reply.Code, reply.Body)
}

// fromOwnPage reports whether a write carries Conn's header and an Origin.
func fromOwnPage(r *http.Request) bool {
	if r.Header.Get("X-Conn") != "1" {
		return false
	}

	return r.Header.Get("Origin") != ""
}

// readObject reads a request body of at most bodyBytesMax as a JSON object; an empty body is {}.
func readObject(r *http.Request) (*js.Object, error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, bodyBytesMax+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errNotObject, err)
	}

	if len(data) > bodyBytesMax {
		return nil, errTooLarge
	}

	if len(data) == 0 {
		return js.NewObject(), nil
	}

	parsed, err := js.Parse(data)
	if err != nil {
		return nil, errNotObject
	}

	object, isObject := parsed.(*js.Object)
	if !isObject {
		return nil, errNotObject
	}

	return object, nil
}

// recovered answers a request whose handler panicked with a 500, and logs why. A panic is a
// program error in one request: the server keeps serving the others.
func (s *Server) recovered(w http.ResponseWriter, r *http.Request) {
	failure := recover()
	if failure == nil {
		return
	}

	if s.Logger != nil {
		s.Logger.Printf("request failed %s %v", r.URL.Path, failure)
	}

	sendJSON(w, http.StatusInternalServerError,
		errorBody{Error: textFailed})
}
