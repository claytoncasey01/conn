package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/claytoncasey01/conn/internal/secret"
)

const (
	// apiRoot is the path every API route is under.
	apiRoot = "/api"

	// textUnauthorized is the answer to an /api request without the secret.
	textUnauthorized = "unauthorized"

	// policyFormat is the page's Content-Security-Policy, with %s the Host the page was asked on,
	// for its WebSocket. Scripts come only from this origin (no inline script); styles also from
	// inline (Svelte and the theme set them) and from Google Fonts' stylesheet host; fonts from
	// this origin, from data: (the bundle inlines the rain effect's small font) and Google Fonts'
	// font host; images from this origin and data: (the grain effect draws its noise into one);
	// requests only to this origin; and the page is never framed.
	policyFormat = "default-src 'self'; script-src 'self'; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
		"font-src 'self' data: https://fonts.gstatic.com; img-src 'self' data:; " +
		"connect-src 'self' ws://%s; object-src 'none'; base-uri 'none'; " +
		"form-action 'none'; frame-ancestors 'none'"
)

// route answers one request the gate let through; urlPath is the path as sent.
type route func(w http.ResponseWriter, r *http.Request, urlPath string)

// serve is ServeHTTP with next as the API: it refuses a request from anywhere but this machine,
// puts every request under /api through the gate and then next, and serves the page bundle for
// everything else.
func (s *Server) serve(w http.ResponseWriter, r *http.Request, next route) {
	if !local(r) {
		sendJSON(w, http.StatusForbidden, errorBody{Error: textForbidden})

		return
	}

	w.Header().Set("Content-Security-Policy", fmt.Sprintf(policyFormat, r.Host))
	w.Header().Set("X-Content-Type-Options", "nosniff")

	// Routes match the path as sent, not decoded, as a browser's URL.pathname does.
	urlPath := r.URL.EscapedPath()

	if !underAPI(urlPath) {
		// The decoded path is checked too, so a path that is under /api only once decoded is
		// refused rather than served.
		if !underAPI(r.URL.Path) {
			s.static(w, r.URL.Path)

			return
		}
	}

	if !s.admitted(w, r) {
		return
	}

	next(w, r, urlPath)
}

// underAPI says whether urlPath is /api or a path below it.
func underAPI(urlPath string) bool {
	if urlPath == apiRoot {
		return true
	}

	return strings.HasPrefix(urlPath, apiRoot+"/")
}

// admitted is the gate in front of every /api route, and answers a request it refuses. A request
// must name this machine (ServeHTTP has checked the Host), must come from no page or from a page
// of this very origin, and must carry this launch's secret: a page on another site or port fails
// the second, and another user of the machine, who can reach the port but cannot read the lock
// file, fails the third.
func (s *Server) admitted(w http.ResponseWriter, r *http.Request) bool {
	if !sameOrigin(r) {
		sendJSON(w, http.StatusForbidden, errorBody{Error: textForbidden})

		return false
	}

	if !s.Secret.Matches(bearer(r.Header.Values(secret.Header))) {
		w.Header().Set("WWW-Authenticate", secret.Scheme)
		sendJSON(w, http.StatusUnauthorized, errorBody{Error: textUnauthorized})

		return false
	}

	return true
}

// sameOrigin says whether a request has no Origin (it is not from a browser page, or is a
// same-origin GET, which carries none) or exactly one, which is this server's origin as the
// request names it: http and the Host it was sent to. A browser sets both, so they agree only for
// a page served from that same address; the Host has already been checked to be this machine.
func sameOrigin(r *http.Request) bool {
	origins := r.Header.Values("Origin")

	switch len(origins) {
	case 0:
		return true
	case 1:
		return origins[0] == "http://"+r.Host
	default:
		return false
	}
}

// bearer is the token of the one Authorization value "Bearer <token>", or "" for no value, more
// than one, or another scheme. An empty token matches no secret.
func bearer(values []string) string {
	if len(values) != 1 {
		return ""
	}

	token, found := strings.CutPrefix(values[0], secret.Scheme+" ")
	if !found {
		return ""
	}

	return token
}
