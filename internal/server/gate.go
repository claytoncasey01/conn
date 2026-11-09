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

	// protocolHeader is the WebSocket handshake's header of subprotocols. The page's terminal
	// asks for two: terminalProtocol, which the server echoes, and keyProtocolPrefix followed by
	// the secret, which it never echoes. A browser's WebSocket cannot send any other header the
	// page chooses, and a subprotocol never appears in a URL, a history or a log.
	protocolHeader    = "Sec-WebSocket-Protocol"
	terminalProtocol  = "conn.term.v1"
	keyProtocolPrefix = "conn.key."
	// terminalProtocolsCount is how many subprotocols the page's handshake names.
	terminalProtocolsCount = 2
	// maxProtocolHeaderBytes bounds the header the secret is read from: the two protocols take
	// 66 bytes.
	maxProtocolHeaderBytes = 128
	// headerSpace is the space HTTP allows around a header's list items.
	headerSpace = " \t"

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
	// A link the page follows tells the next site nothing of Conn's address.
	w.Header().Set("Referrer-Policy", "no-referrer")

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

	if !s.admitted(w, r, urlPath) {
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
//
// The secret travels in the Authorization header, except on the one request a browser cannot
// give that header: the WebSocket handshake of the terminal route. There, and only there, the
// page must also send its Origin, and the secret travels in the handshake's subprotocols instead
// (terminalKey), never in the URL or a cookie.
func (s *Server) admitted(w http.ResponseWriter, r *http.Request, urlPath string) bool {
	if !sameOrigin(r) {
		sendJSON(w, http.StatusForbidden, errorBody{Error: textForbidden})

		return false
	}

	presented := bearer(r.Header.Values(secret.Header))

	if terminalHandshake(r, urlPath) {
		if !ownOrigin(r) {
			sendJSON(w, http.StatusForbidden, errorBody{Error: textForbidden})

			return false
		}

		presented = terminalKey(r.Header)
	}

	if !s.Secret.Matches(presented) {
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

// ownOrigin says whether a request carries exactly one Origin, this server's own. A browser
// always sends one with a WebSocket handshake, so a handshake without one is not the page's.
func ownOrigin(r *http.Request) bool {
	origins := r.Header.Values("Origin")
	if len(origins) != 1 {
		return false
	}

	return origins[0] == "http://"+r.Host
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

// terminalHandshake says whether a request is a WebSocket handshake for the terminal route: a GET
// of /api/term/<a session id's shape>, asking to upgrade to a WebSocket. Only such a request may
// carry the secret in its subprotocols.
func terminalHandshake(r *http.Request, urlPath string) bool {
	if r.Method != http.MethodGet {
		return false
	}

	_, found := terminalID(urlPath)
	if !found {
		return false
	}

	return asksForWebSocket(r.Header)
}

// asksForWebSocket says whether the headers ask to upgrade the connection to a WebSocket: an
// Upgrade naming websocket and a Connection naming upgrade, each matched as a token in any case,
// as a browser and the WebSocket library read them.
func asksForWebSocket(header http.Header) bool {
	if !hasToken(header, "Upgrade", "websocket") {
		return false
	}

	return hasToken(header, "Connection", "upgrade")
}

// hasToken says whether any of the header's comma-separated tokens is token, in any case.
func hasToken(header http.Header, name, token string) bool {
	for _, value := range header.Values(name) {
		for part := range strings.SplitSeq(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}

	return false
}

// terminalKey is the secret a terminal handshake carries, or "" for anything but the one shape
// the page sends: no Authorization header (one credential, in one place), and exactly one
// Sec-WebSocket-Protocol header of at most maxProtocolHeaderBytes bytes, holding exactly two
// protocols, terminalProtocol and then keyProtocolPrefix followed by the secret. The server
// echoes only the first, so the second never comes back. An empty secret matches nothing.
func terminalKey(header http.Header) string {
	if len(header.Values(secret.Header)) != 0 {
		return ""
	}

	values := header.Values(protocolHeader)
	if len(values) != 1 {
		return ""
	}

	if len(values[0]) > maxProtocolHeaderBytes {
		return ""
	}

	protocols := strings.Split(values[0], ",")
	if len(protocols) != terminalProtocolsCount {
		return ""
	}

	if strings.Trim(protocols[0], headerSpace) != terminalProtocol {
		return ""
	}

	key, found := strings.CutPrefix(strings.Trim(protocols[1], headerSpace), keyProtocolPrefix)
	if !found {
		return ""
	}

	return key
}
