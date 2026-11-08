package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/server"
)

// The terminal route's contract with the page: the subprotocol it asks for, which the server
// echoes, and the prefix of the second one, which carries the secret and is never echoed.
const (
	terminalProtocol = "conn.term.v1"
	keyProtocol      = "conn.key."
	protocolHeader   = "Sec-WebSocket-Protocol"
	// sessionText is a well-formed session id the gate tests ask for.
	sessionText = "0123456789abcdef0123456789abcdef"
)

// upgradeRequest is a browser's WebSocket handshake for the terminal of sessionText, from the
// server's own page, with the secret where the page puts it.
func upgradeRequest(t *testing.T, made secret.Secret) *http.Request {
	t.Helper()

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/term/"+sessionText, nil)
	request.Host = ownHost
	request.Header.Set("Origin", pageOrigin)
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	request.Header.Set(protocolHeader, terminalProtocol+", "+keyProtocol+made.String())

	return request
}

// terminalKnock is one change to the page's handshake, and what the gate must answer.
type terminalKnock struct {
	name   string
	change func(request *http.Request, made, other secret.Secret)
	want   int
}

func terminalKnocks() []terminalKnock {
	return joinKnocks(terminalKnocksAdmitted(), terminalKnocksOrigin(), terminalKnocksKey(),
		terminalKnocksElsewhere())
}

func joinKnocks(groups ...[]terminalKnock) []terminalKnock {
	var all []terminalKnock
	for _, group := range groups {
		all = append(all, group...)
	}

	return all
}

func terminalKnocksAdmitted() []terminalKnock {
	return []terminalKnock{
		{"the page's handshake", func(*http.Request, secret.Secret, secret.Secret) {}, 200},
		{"the protocols separated by a tab", func(r *http.Request, made, _ secret.Secret) {
			r.Header.Set(protocolHeader, terminalProtocol+",\t"+keyProtocol+made.String())
		}, 200},
		{"connection names keep-alive too", func(r *http.Request, _, _ secret.Secret) {
			r.Header.Set("Connection", "keep-alive, Upgrade")
		}, 200},
		{"through the dev server", func(r *http.Request, _, _ secret.Secret) {
			r.Host = "127.0.0.1:5173"
			r.Header.Set("Origin", "http://127.0.0.1:5173")
		}, 200},
	}
}

func terminalKnocksOrigin() []terminalKnock {
	return []terminalKnock{
		{"no origin", func(r *http.Request, _, _ secret.Secret) {
			r.Header.Del("Origin")
		}, 403},
		{"a null origin", func(r *http.Request, _, _ secret.Secret) {
			r.Header.Set("Origin", "null")
		}, 403},
		{"another site, with the secret", func(r *http.Request, _, _ secret.Secret) {
			r.Header.Set("Origin", "https://evil.example")
		}, 403},
		{"another port, with the secret", func(r *http.Request, _, _ secret.Secret) {
			r.Header.Set("Origin", "http://127.0.0.1:7778")
		}, 403},
		{"the other loopback name", func(r *http.Request, _, _ secret.Secret) {
			r.Header.Set("Origin", "http://localhost:7777")
		}, 403},
		{"two origins", func(r *http.Request, _, _ secret.Secret) {
			r.Header.Add("Origin", pageOrigin)
		}, 403},
		{"a rebound host", func(r *http.Request, _, _ secret.Secret) {
			r.Host = "evil.example:7777"
			r.Header.Set("Origin", "http://evil.example:7777")
		}, 403},
	}
}

func terminalKnocksKey() []terminalKnock {
	set := func(value string) func(*http.Request, secret.Secret, secret.Secret) {
		return func(r *http.Request, _, _ secret.Secret) { r.Header.Set(protocolHeader, value) }
	}

	return []terminalKnock{
		{"no protocols", func(r *http.Request, _, _ secret.Secret) {
			r.Header.Del(protocolHeader)
		}, 401},
		{"only the protocol name", set(terminalProtocol), 401},
		{"a wrong secret", func(r *http.Request, _, other secret.Secret) {
			r.Header.Set(protocolHeader, terminalProtocol+", "+keyProtocol+other.String())
		}, 401},
		{"an empty secret", set(terminalProtocol + ", " + keyProtocol), 401},
		{"the secret without the protocol name", func(r *http.Request, made, _ secret.Secret) {
			r.Header.Set(protocolHeader, keyProtocol+made.String())
		}, 401},
		{"the secret first", func(r *http.Request, made, _ secret.Secret) {
			r.Header.Set(protocolHeader, keyProtocol+made.String()+", "+terminalProtocol)
		}, 401},
		{"the secret twice", func(r *http.Request, made, _ secret.Secret) {
			key := keyProtocol + made.String()
			r.Header.Set(protocolHeader, terminalProtocol+", "+key+", "+key)
		}, 401},
		{"a third protocol", func(r *http.Request, made, _ secret.Secret) {
			r.Header.Set(protocolHeader,
				terminalProtocol+", "+keyProtocol+made.String()+", chat")
		}, 401},
		{"the protocols on two header lines", func(r *http.Request, made, _ secret.Secret) {
			r.Header.Set(protocolHeader, terminalProtocol)
			r.Header.Add(protocolHeader, keyProtocol+made.String())
		}, 401},
		{"the whole header twice", func(r *http.Request, made, _ secret.Secret) {
			r.Header.Add(protocolHeader, terminalProtocol+", "+keyProtocol+made.String())
		}, 401},
		{"an oversized header", func(r *http.Request, made, _ secret.Secret) {
			r.Header.Set(protocolHeader, terminalProtocol+","+strings.Repeat(" ", 4096)+
				keyProtocol+made.String())
		}, 401},
		{"the protocol name in capitals", func(r *http.Request, made, _ secret.Secret) {
			r.Header.Set(protocolHeader, "CONN.TERM.V1, "+keyProtocol+made.String())
		}, 401},
		{"the secret without its prefix", func(r *http.Request, made, _ secret.Secret) {
			r.Header.Set(protocolHeader, terminalProtocol+", "+made.String())
		}, 401},
		{"the key prefix in capitals", func(r *http.Request, made, _ secret.Secret) {
			r.Header.Set(protocolHeader, terminalProtocol+", CONN.KEY."+made.String())
		}, 401},
		{"the secret in the protocol and the right bearer", func(r *http.Request, made,
			_ secret.Secret,
		) {
			r.Header.Set(secret.Header, made.Authorization())
		}, 401},
		{"only the bearer, on the handshake", func(r *http.Request, made, _ secret.Secret) {
			r.Header.Del(protocolHeader)
			r.Header.Set(secret.Header, made.Authorization())
		}, 401},
		{"the secret in a query string", func(r *http.Request, made, _ secret.Secret) {
			r.Header.Set(protocolHeader, terminalProtocol)
			r.URL.RawQuery = "k=" + made.String()
		}, 401},
		{"the secret in a cookie", func(r *http.Request, made, _ secret.Secret) {
			r.Header.Set(protocolHeader, terminalProtocol)
			r.Header.Set("Cookie", "k="+made.String())
		}, 401},
	}
}

// terminalKnocksElsewhere carry the secret in the protocol to where it proves nothing: a request
// that is not a WebSocket handshake, or a path that is not the terminal route.
func terminalKnocksElsewhere() []terminalKnock {
	return []terminalKnock{
		{"no upgrade header", func(r *http.Request, _, _ secret.Secret) {
			r.Header.Del("Upgrade")
		}, 401},
		{"no connection upgrade", func(r *http.Request, _, _ secret.Secret) {
			r.Header.Set("Connection", "keep-alive")
		}, 401},
		{"another upgrade", func(r *http.Request, _, _ secret.Secret) {
			r.Header.Set("Upgrade", "h2c")
		}, 401},
		{"a POST", func(r *http.Request, _, _ secret.Secret) {
			r.Method = http.MethodPost
		}, 401},
		{"another route", func(r *http.Request, _, _ secret.Secret) {
			r.URL.Path = "/api/snapshot"
		}, 401},
		{"the event stream", func(r *http.Request, _, _ secret.Secret) {
			r.URL.Path = "/api/events"
		}, 401},
		{"a malformed id", func(r *http.Request, _, _ secret.Secret) {
			r.URL.Path = "/api/term/" + strings.ToUpper(sessionText)
		}, 401},
		{"a trailing slash", func(r *http.Request, _, _ secret.Secret) {
			r.URL.Path = "/api/term/" + sessionText + "/"
		}, 401},
		{"another segment", func(r *http.Request, _, _ secret.Secret) {
			r.URL.Path = "/api/term/" + sessionText + "/x"
		}, 401},
		{"the route's slash escaped", func(r *http.Request, _, _ secret.Secret) {
			setEscapedPath(r, "/api/term%2F"+sessionText)
		}, 401},
		{"the route's name escaped", func(r *http.Request, _, _ secret.Secret) {
			setEscapedPath(r, "/api/te%72m/"+sessionText)
		}, 401},
	}
}

// setEscapedPath makes the request's path the one escaped, as a client could send it.
func setEscapedPath(request *http.Request, escaped string) {
	request.URL.RawPath = escaped
	request.URL.Path = strings.ReplaceAll(strings.ReplaceAll(escaped, "%2F", "/"), "%72", "r")
}

// TestTheTerminalHandshakeNeedsTheSecretInItsProtocol runs every way a handshake can come to the
// terminal route through the gate ServeHTTP uses: only the page's own, with the secret in the
// subprotocol, reaches the handler, and the secret in the protocol proves nothing anywhere else.
func TestTheTerminalHandshakeNeedsTheSecretInItsProtocol(t *testing.T) {
	t.Parallel()

	made, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	other, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	for _, way := range terminalKnocks() {
		t.Run(way.name, func(t *testing.T) {
			t.Parallel()

			var ran atomic.Bool

			handler := server.ServeWith(&server.Server{UI: bundle(), Secret: made},
				func(w http.ResponseWriter, _ *http.Request, _ string) {
					ran.Store(true)
					w.WriteHeader(http.StatusOK)
				})

			request := upgradeRequest(t, made)
			way.change(request, made, other)

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)

			if recorder.Code != way.want {
				t.Fatalf("status %d, want %d", recorder.Code, way.want)
			}

			if ran.Load() != (way.want == http.StatusOK) {
				t.Fatalf("the handler ran: %v", ran.Load())
			}

			if strings.Contains(recorder.Body.String(), made.String()) {
				t.Fatal("the answer holds the secret")
			}

			if strings.Contains(recorder.Header().Get(protocolHeader), keyProtocol) {
				t.Fatal("the answer echoes the key protocol")
			}
		})
	}
}

// A plain request to the terminal route, not a handshake, still goes by the bearer rule.
func TestTheTerminalRouteTakesTheBearerOutsideAHandshake(t *testing.T) {
	t.Parallel()

	made, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	route := apiRoute{http.MethodGet, "/api/term/" + sessionText}

	for _, way := range knocks(t, made) {
		t.Run(way.name, func(t *testing.T) {
			t.Parallel()

			var ran atomic.Bool

			handler := server.ServeWith(&server.Server{UI: bundle(), Secret: made},
				func(w http.ResponseWriter, _ *http.Request, _ string) {
					ran.Store(true)
					w.WriteHeader(http.StatusOK)
				})

			got := send(t, handler, route.method, route.path, way)
			if got.status != way.want {
				t.Fatalf("status %d, want %d", got.status, way.want)
			}

			if ran.Load() != (way.want == http.StatusOK) {
				t.Fatalf("the handler ran: %v", ran.Load())
			}
		})
	}
}
