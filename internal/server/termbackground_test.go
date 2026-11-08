package server_test

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/server"
)

// putBackground sends PUT /api/term/background with body from Conn's own page, or with headers
// changed by change.
func putBackground(t *testing.T, handler http.Handler, method, body string,
	change func(r *http.Request),
) (int, string) {
	t.Helper()

	request := httptest.NewRequestWithContext(t.Context(), method, "/api/term/background",
		strings.NewReader(body))
	request.Host = "127.0.0.1:7777"
	request.Header.Set(secret.Header, testKey(t).Authorization())
	request.Header.Set("Origin", ownOrigin)
	request.Header.Set("X-Conn", "1")
	request.Header.Set("Content-Type", "application/json")

	if change != nil {
		change(request)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	return recorder.Code, strings.TrimSpace(recorder.Body.String())
}

// TestThePageTellsTheHostItsTerminalBackground: the page's terminal background, a light theme's
// included, reaches the host as three channels, and nothing but a #rrggbb colour does.
func TestThePageTellsTheHostItsTerminalBackground(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		body   string
		change func(r *http.Request)
		code   int
		// set is the colour the host is told, nil for none.
		set *pty.RGB
	}{
		{
			"a light theme", http.MethodPut, `{"background":"#f3f5f9"}`, nil, http.StatusOK,
			&pty.RGB{Red: 0xf3, Green: 0xf5, Blue: 0xf9},
		},
		{
			"a dark theme", http.MethodPut, `{"background":"#0E1117"}`, nil, http.StatusOK,
			&pty.RGB{Red: 0x0e, Green: 0x11, Blue: 0x17},
		},
		{
			"not a colour", http.MethodPut, `{"background":"rgb(1,2,3)"}`, nil,
			http.StatusBadRequest, nil,
		},
		{
			"text after it", http.MethodPut, `{"background":"#000000;"}`, nil,
			http.StatusBadRequest, nil,
		},
		{
			"a short colour", http.MethodPut, `{"background":"#fff"}`, nil,
			http.StatusBadRequest, nil,
		},
		{
			"a colour with alpha", http.MethodPut, `{"background":"#f3f5f9ff"}`, nil,
			http.StatusBadRequest, nil,
		},
		{"no colour", http.MethodPut, `{}`, nil, http.StatusBadRequest, nil},
		{"a number", http.MethodPut, `{"background":16777215}`, nil, http.StatusBadRequest, nil},
		{"a body too large", http.MethodPut, `{"background":"#000000","pad":"` +
			strings.Repeat("x", 2048) + `"}`, nil, http.StatusRequestEntityTooLarge, nil},
		{"a GET", http.MethodGet, ``, nil, http.StatusMethodNotAllowed, nil},
		{
			"without Conn's header", http.MethodPut, `{"background":"#000000"}`,
			func(r *http.Request) { r.Header.Del("X-Conn") }, http.StatusForbidden, nil,
		},
		{
			"without the secret", http.MethodPut, `{"background":"#000000"}`,
			func(r *http.Request) { r.Header.Del(secret.Header) }, http.StatusUnauthorized, nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var told []pty.RGB

			handler := &server.Server{
				UI: bundle(), Secret: testKey(t), Logger: log.New(io.Discard, "", 0),
				TerminalBackground: func(colour pty.RGB) { told = append(told, colour) },
			}

			code, body := putBackground(t, handler, tt.method, tt.body, tt.change)
			if code != tt.code {
				t.Fatalf("code %d, want %d: %s", code, tt.code, body)
			}

			if tt.set == nil {
				if len(told) != 0 {
					t.Errorf("the host was told %v", told)
				}

				return
			}

			if len(told) != 1 {
				t.Fatalf("told %v", told)
			}

			if told[0] != *tt.set {
				t.Errorf("told %v, want %v", told[0], *tt.set)
			}
		})
	}
}

// TestNoHostMeansNoBackgroundRoute: a server with no host to tell answers not found.
func TestNoHostMeansNoBackgroundRoute(t *testing.T) {
	t.Parallel()

	handler := &server.Server{UI: bundle(), Secret: testKey(t), Logger: nil}

	code, _ := putBackground(t, handler, http.MethodPut, `{"background":"#000000"}`, nil)
	if code != http.StatusNotFound {
		t.Errorf("code %d", code)
	}
}
