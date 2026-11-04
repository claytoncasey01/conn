package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/claytoncasey01/conn/internal/server"
)

const shell = "<!doctype html><title>Conn</title>"

func bundle() fstest.MapFS {
	return fstest.MapFS{
		"index.html":         {Data: []byte(shell)},
		"favicon.svg":        {Data: []byte("<svg/>")},
		"assets/index-1.js":  {Data: []byte("console.log(1)")},
		"assets/index-1.css": {Data: []byte("body{}")},
		"assets/font.woff2":  {Data: []byte("wOF2")},
		"assets/blob.bin":    {Data: []byte{0, 1}},
	}
}

// answer is what a request got back.
type answer struct {
	status int
	header http.Header
	body   string
}

// get sends a GET for target to handler, as a page on this machine would unless host or origin
// say otherwise.
func get(t *testing.T, handler http.Handler, target, host, origin string) answer {
	t.Helper()

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)

	request.Host = "127.0.0.1:7777"
	if host != "" {
		request.Host = host
	}

	if origin != "" {
		request.Header.Set("Origin", origin)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	response := recorder.Result()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	err = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	return answer{status: response.StatusCode, header: response.Header, body: string(body)}
}

func TestOnlyThisMachineAndItsOwnPages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		host   string
		origin string
		want   int
	}{
		{"no origin", "", "", http.StatusOK},
		{"localhost host", "localhost:7777", "", http.StatusOK},
		{"host without port", "127.0.0.1", "", http.StatusOK},
		{"own origin", "", "http://127.0.0.1:7777", http.StatusOK},
		{"dev origin", "", "http://localhost:5173", http.StatusOK},
		{"origin in capitals", "", "http://LOCALHOST:5173", http.StatusOK},
		{"other host", "evil.example", "", http.StatusForbidden},
		{"lookalike host", "127.0.0.1.evil.example:7777", "", http.StatusForbidden},
		{"empty port", "localhost:", "", http.StatusForbidden},
		{"word port", "localhost:abc", "", http.StatusForbidden},
		{"ipv6 host", "[::1]:7777", "", http.StatusForbidden},
		{"other origin", "", "https://evil.example", http.StatusForbidden},
		{"null origin", "", "null", http.StatusForbidden},
		{"lookalike origin", "", "http://127.0.0.1.evil.example", http.StatusForbidden},
		{"file origin", "", "file://", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := &server.Server{UI: bundle(), Clients: nil}

			got := get(t, handler, "/api/health", tt.host, tt.origin)
			if got.status != tt.want {
				t.Fatalf("status %d, want %d", got.status, tt.want)
			}

			if tt.want != http.StatusForbidden {
				return
			}

			if got.body != `{"error":"forbidden"}` {
				t.Fatalf("body %s", got.body)
			}
		})
	}
}

func TestHealthSaysHowManyPagesListen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		clients func() int
		want    string
	}{
		{"two pages", func() int { return 2 }, `{"ok":true,"clients":2}`},
		{"none wired", nil, `{"ok":true,"clients":0}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := &server.Server{UI: bundle(), Clients: tt.clients}

			got := get(t, handler, "/api/health", "", "")
			if got.body != tt.want {
				t.Fatalf("body %s", got.body)
			}

			if cache := got.header.Get("Cache-Control"); cache != "no-store" {
				t.Fatalf("cache-control %q", cache)
			}

			if kind := got.header.Get("Content-Type"); kind != "application/json" {
				t.Fatalf("content-type %q", kind)
			}
		})
	}
}

func TestStaticFilesAndThePageShell(t *testing.T) {
	t.Parallel()

	const (
		html      = "text/html; charset=utf-8"
		immutable = "public, max-age=31536000, immutable"
	)

	tests := []struct {
		path, want, contentType, cache string
	}{
		{"/", shell, html, "no-cache"},
		{"/favicon.svg", "<svg/>", "image/svg+xml", "no-cache"},
		{"/assets/index-1.js", "console.log(1)", "text/javascript; charset=utf-8", immutable},
		{"/assets/index-1.css", "body{}", "text/css; charset=utf-8", immutable},
		{"/assets/font.woff2", "wOF2", "font/woff2", immutable},
		{"/assets/blob.bin", "\x00\x01", "application/octet-stream", immutable},
		// A page route, a missing file and a folder all get the shell.
		{"/history", shell, html, "no-cache"},
		{"/assets/missing.js", shell, html, "no-cache"},
		{"/assets", shell, html, "no-cache"},
		// A climb out of the bundle stays inside it.
		{"/../../etc/passwd", shell, html, "no-cache"},
		{"/assets/../favicon.svg", "<svg/>", "image/svg+xml", "no-cache"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()

			got := get(t, &server.Server{UI: bundle(), Clients: nil}, tt.path, "", "")
			if got.status != http.StatusOK {
				t.Fatalf("status %d", got.status)
			}

			if got.body != tt.want {
				t.Fatalf("body %q, want %q", got.body, tt.want)
			}

			if kind := got.header.Get("Content-Type"); kind != tt.contentType {
				t.Fatalf("content-type %q", kind)
			}

			if cache := got.header.Get("Cache-Control"); cache != tt.cache {
				t.Fatalf("cache-control %q", cache)
			}
		})
	}
}

func TestMissingDistSaysHowToBuildIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		bundle fstest.MapFS
	}{
		{"empty bundle", fstest.MapFS{}},
		{"no bundle", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := &server.Server{UI: nil, Clients: nil}
			if tt.bundle != nil {
				handler.UI = tt.bundle
			}

			got := get(t, handler, "/", "", "")
			if got.status != http.StatusServiceUnavailable {
				t.Fatalf("status %d", got.status)
			}

			if got.body != `{"error":"`+server.DistMissing+`"}` {
				t.Fatalf("body %s", got.body)
			}
		})
	}
}

func TestJSONIsWhatABrowserWouldWrite(t *testing.T) {
	t.Parallel()

	got := string(server.MarshalJSON(map[string]string{"a": "<b>&"}))
	if got != `{"a":"<b>&"}` {
		t.Fatalf("got %s", got)
	}
}
