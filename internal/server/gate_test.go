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

// apiRoutes is one request for every route family the server answers under /api, plus paths that
// name no route: the gate stands in front of all of them, so a route added later is behind it too.
func apiRoutes() []apiRoute {
	return []apiRoute{
		{http.MethodGet, "/api/health"},
		{http.MethodGet, "/api/snapshot"},
		{http.MethodGet, "/api/events"},
		{http.MethodGet, "/api/sessions/123/agents/abc"},
		{http.MethodPost, "/api/sessions/123/stop"},
		{http.MethodPost, "/api/sessions/123/reply"},
		{http.MethodPost, "/api/sessions/123/answer"},
		{http.MethodPost, "/api/sessions/123/terminal"},
		{http.MethodGet, "/api/work"},
		{http.MethodPost, "/api/work/refresh"},
		{http.MethodPost, "/api/work/ABC-1/start"},
		{http.MethodPost, "/api/runs/ABC-1/cancel"},
		{http.MethodGet, "/api/history"},
		{http.MethodGet, "/api/history/00000000-0000-0000-0000-000000000000"},
		{http.MethodPost, "/api/history/00000000-0000-0000-0000-000000000000/resume"},
		{http.MethodPost, "/api/reviews/start"},
		{http.MethodPost, "/api/myprs/refresh"},
		{http.MethodGet, "/api/settings"},
		{http.MethodPut, "/api/settings/github"},
		{http.MethodPost, "/api/settings/browse"},
		{http.MethodGet, "/api/gallery"},
		{http.MethodPost, "/api/gallery/install"},
		{http.MethodGet, "/api/provided"},
		{http.MethodPost, "/api/provided/install"},
		{http.MethodGet, "/api/themes"},
		{http.MethodGet, "/api/themes/neon/theme.css?v=" + strings.Repeat("a", 64)},
		{http.MethodGet, "/api"},
		{http.MethodGet, "/api/no-such-route"},
		// Routes match the path as sent; one spelled with an escape is still under /api.
		{http.MethodGet, "/%61pi/snapshot"},
		{http.MethodGet, "/api%2Fsnapshot"},
	}
}

type apiRoute struct {
	method, path string
}

const (
	ownHost    = "127.0.0.1:7777"
	pageOrigin = "http://127.0.0.1:7777"
)

// knock is one way a request can come to an /api route.
type knock struct {
	name   string
	host   string
	origin string
	// authorization is the Authorization header's values: none, one, or several.
	authorization []string
	want          int
}

func knocks(t *testing.T, made secret.Secret) []knock {
	t.Helper()

	other, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	right := []string{made.Authorization()}

	return []knock{
		{"no secret", ownHost, "", nil, http.StatusUnauthorized},
		{"no secret, own origin", ownHost, pageOrigin, nil, http.StatusUnauthorized},
		{"wrong secret", ownHost, "", []string{other.Authorization()}, http.StatusUnauthorized},
		{"empty bearer", ownHost, "", []string{"Bearer "}, http.StatusUnauthorized},
		{"secret without scheme", ownHost, "", []string{made.String()}, http.StatusUnauthorized},
		{"basic scheme", ownHost, "", []string{"Basic " + made.String()}, http.StatusUnauthorized},
		{
			"two headers", ownHost, "",
			[]string{made.Authorization(), made.Authorization()},
			http.StatusUnauthorized,
		},
		{"wrong host", "evil.example:7777", "", right, http.StatusForbidden},
		{"rebound host", "127.0.0.1.evil.example:7777", "", right, http.StatusForbidden},
		{"other origin", ownHost, "https://evil.example", right, http.StatusForbidden},
		{"other port", ownHost, "http://127.0.0.1:7778", right, http.StatusForbidden},
		{"other loopback name", ownHost, "http://localhost:7777", right, http.StatusForbidden},
		{"dev server origin", ownHost, "http://localhost:5173", right, http.StatusForbidden},
		{"https origin", ownHost, "https://127.0.0.1:7777", right, http.StatusForbidden},
		{"null origin", ownHost, "null", right, http.StatusForbidden},
		{"empty origin", ownHost, " ", right, http.StatusForbidden},
		{"no origin, the secret", ownHost, "", right, http.StatusOK},
		{"own origin, the secret", ownHost, pageOrigin, right, http.StatusOK},
		{"localhost, own origin", "localhost:7777", "http://localhost:7777", right, http.StatusOK},
		// The dev server forwards its own Host and Origin: the same page, one origin.
		{"through the dev server", "127.0.0.1:5173", "http://127.0.0.1:5173", right, http.StatusOK},
	}
}

// TestEveryAPIRouteNeedsTheSecret runs every way in against every /api route through the gate
// ServeHTTP uses, with a handler that says whether it ran: a refused request never reaches it.
func TestEveryAPIRouteNeedsTheSecret(t *testing.T) {
	t.Parallel()

	made, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	for _, route := range apiRoutes() {
		for _, way := range knocks(t, made) {
			t.Run(route.method+" "+route.path+" "+way.name, func(t *testing.T) {
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
}

// TestTheRealServerRefusesWithoutTheSecret is the same through ServeHTTP itself.
func TestTheRealServerRefusesWithoutTheSecret(t *testing.T) {
	t.Parallel()

	made, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	for _, route := range apiRoutes() {
		for _, way := range knocks(t, made) {
			if way.want == http.StatusOK {
				continue
			}

			t.Run(route.method+" "+route.path+" "+way.name, func(t *testing.T) {
				t.Parallel()

				handler := &server.Server{UI: bundle(), Secret: made}

				got := send(t, handler, route.method, route.path, way)
				if got.status != way.want {
					t.Fatalf("status %d, want %d", got.status, way.want)
				}

				if strings.Contains(got.body, made.String()) {
					t.Fatal("the answer holds the secret")
				}
			})
		}
	}
}

// A server with no secret set is a program error, never an open door: it refuses every /api
// request, even one that sends an empty bearer.
func TestAServerWithoutASecretRefusesEverything(t *testing.T) {
	t.Parallel()

	for _, route := range apiRoutes() {
		t.Run(route.path, func(t *testing.T) {
			t.Parallel()

			handler := &server.Server{UI: bundle()}

			for _, presented := range [][]string{nil, {"Bearer "}, {"Bearer"}} {
				got := send(t, handler, route.method, route.path,
					knock{name: "", host: ownHost, origin: "", authorization: presented, want: 0})
				if got.status != http.StatusUnauthorized {
					t.Fatalf("status %d with %q", got.status, presented)
				}
			}
		})
	}
}

func TestTheRefusalSaysHowToAuthenticate(t *testing.T) {
	t.Parallel()

	made, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	got := send(t, &server.Server{UI: bundle(), Secret: made}, http.MethodGet, "/api/snapshot",
		knock{name: "", host: ownHost, origin: "", authorization: nil, want: 0})

	if got.header.Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("WWW-Authenticate %q", got.header.Get("WWW-Authenticate"))
	}

	if got.body != `{"error":"unauthorized"}` {
		t.Fatalf("body %s", got.body)
	}
}

// The page shell and the bundle hold nothing private, so they are served without the secret,
// and a path that only looks like /api (doubled or dotted slashes, capitals) gets the shell: the
// API's handler never runs for any of them.
func TestThePageNeedsNoSecret(t *testing.T) {
	t.Parallel()

	made, err := secret.New()
	if err != nil {
		t.Fatal(err)
	}

	paths := []string{
		"/", "/index.html", "/favicon.svg", "/assets/index-1.js", "/apix",
		"//api/x", "/./api/x", "/API/x", "/Api/snapshot",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			var ran atomic.Bool

			handler := server.ServeWith(&server.Server{UI: bundle(), Secret: made},
				func(w http.ResponseWriter, _ *http.Request, _ string) {
					ran.Store(true)
					w.WriteHeader(http.StatusTeapot)
				})

			got := send(t, handler, http.MethodGet, path,
				knock{name: "", host: ownHost, origin: "", authorization: nil, want: 0})
			if got.status != http.StatusOK {
				t.Fatalf("status %d", got.status)
			}

			if ran.Load() {
				t.Fatal("the API's handler ran")
			}
		})
	}
}

func TestTheContentSecurityPolicy(t *testing.T) {
	t.Parallel()

	got := send(t, &server.Server{UI: bundle()}, http.MethodGet, "/",
		knock{name: "", host: "localhost:7777", origin: "", authorization: nil, want: 0})

	want := "default-src 'self'; script-src 'self'; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
		"font-src 'self' data: https://fonts.gstatic.com; img-src 'self' data:; " +
		"connect-src 'self' ws://localhost:7777; object-src 'none'; base-uri 'none'; " +
		"form-action 'none'; frame-ancestors 'none'"

	if policy := got.header.Get("Content-Security-Policy"); policy != want {
		t.Fatalf("policy\n%s\nwant\n%s", policy, want)
	}

	if got.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("X-Content-Type-Options %q", got.header.Get("X-Content-Type-Options"))
	}

	if got.header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("Referrer-Policy %q", got.header.Get("Referrer-Policy"))
	}
}

// send sends method path to handler the way a knock says.
func send(t *testing.T, handler http.Handler, method, path string, way knock) answer {
	t.Helper()

	request := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	request.Host = way.host

	if way.origin != "" {
		request.Header.Set("Origin", strings.TrimSpace(way.origin))
	}

	for _, value := range way.authorization {
		request.Header.Add(secret.Header, value)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	return answer{
		status: recorder.Code, header: recorder.Header(), body: recorder.Body.String(),
	}
}
