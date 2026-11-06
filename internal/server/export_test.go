package server

import "net/http"

// MarshalJSON lets the tests check the JSON text without a route that returns user text.
func MarshalJSON(v any) []byte { return marshalJSON(v) }

// ServeWith is ServeHTTP with next in place of the API's routes.
func ServeWith(s *Server, next func(http.ResponseWriter, *http.Request, string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serve(w, r, next) })
}
