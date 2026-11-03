package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

var bundle = fstest.MapFS{
	"index.html":         {Data: []byte("<!doctype html><title>Conn</title>")},
	"favicon.svg":        {Data: []byte("<svg/>")},
	"assets/index-1.js":  {Data: []byte("console.log(1)")},
	"assets/index-1.css": {Data: []byte("body{}")},
	"assets/font.woff2":  {Data: []byte("wOF2")},
	"assets/blob.bin":    {Data: []byte{0, 1}},
}

func get(t *testing.T, s *Server, target string, header map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = "127.0.0.1:7777"
	for k, v := range header {
		if k == "host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec.Result()
}

func body(t *testing.T, r *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestOnlyThisMachineAndItsOwnPages(t *testing.T) {
	s := &Server{UI: bundle}
	ok := []map[string]string{
		{},
		{"host": "localhost:7777"},
		{"host": "127.0.0.1"},
		{"origin": "http://127.0.0.1:7777"},
		{"origin": "http://localhost:5173"},
		{"origin": "http://LOCALHOST:5173"},
	}
	for _, h := range ok {
		if r := get(t, s, "/api/health", h); r.StatusCode != 200 {
			t.Errorf("%v: got %d, want 200", h, r.StatusCode)
		}
	}
	refused := []map[string]string{
		{"host": "evil.example"},
		{"host": "127.0.0.1.evil.example:7777"},
		{"host": "localhost:"},
		{"host": "localhost:abc"},
		{"host": "[::1]:7777"},
		{"origin": "https://evil.example"},
		{"origin": "null"},
		{"origin": "http://127.0.0.1.evil.example"},
		{"origin": "file://"},
	}
	for _, h := range refused {
		r := get(t, s, "/api/health", h)
		if r.StatusCode != 403 || body(t, r) != `{"error":"forbidden"}` {
			t.Errorf("%v: got %d, want 403 forbidden", h, r.StatusCode)
		}
	}
}

func TestHealthSaysHowManyPagesListen(t *testing.T) {
	s := &Server{UI: bundle, Clients: func() int { return 2 }}
	r := get(t, s, "/api/health", nil)
	if got := body(t, r); got != `{"ok":true,"clients":2}` {
		t.Fatalf("health: %s", got)
	}
	if r.Header.Get("cache-control") != "no-store" || r.Header.Get("content-type") != "application/json" {
		t.Fatalf("headers: %v", r.Header)
	}
	if got := body(t, get(t, &Server{UI: bundle}, "/api/health", nil)); got != `{"ok":true,"clients":0}` {
		t.Fatalf("no listeners: %s", got)
	}
}

func TestStaticFilesAndThePageShell(t *testing.T) {
	s := &Server{UI: bundle}
	cases := []struct{ path, want, ctype, cache string }{
		{"/", "<!doctype html><title>Conn</title>", "text/html; charset=utf-8", "no-cache"},
		{"/favicon.svg", "<svg/>", "image/svg+xml", "no-cache"},
		{"/assets/index-1.js", "console.log(1)", "text/javascript; charset=utf-8", "public, max-age=31536000, immutable"},
		{"/assets/index-1.css", "body{}", "text/css; charset=utf-8", "public, max-age=31536000, immutable"},
		{"/assets/font.woff2", "wOF2", "font/woff2", "public, max-age=31536000, immutable"},
		{"/assets/blob.bin", "\x00\x01", "application/octet-stream", "public, max-age=31536000, immutable"},
		// a page route, a missing file and a folder all get the shell
		{"/history", "<!doctype html><title>Conn</title>", "text/html; charset=utf-8", "no-cache"},
		{"/assets/missing.js", "<!doctype html><title>Conn</title>", "text/html; charset=utf-8", "no-cache"},
		{"/assets", "<!doctype html><title>Conn</title>", "text/html; charset=utf-8", "no-cache"},
		// a climb out of the bundle stays inside it
		{"/../../etc/passwd", "<!doctype html><title>Conn</title>", "text/html; charset=utf-8", "no-cache"},
		{"/assets/../favicon.svg", "<svg/>", "image/svg+xml", "no-cache"},
	}
	for _, c := range cases {
		r := get(t, s, c.path, nil)
		if r.StatusCode != 200 {
			t.Errorf("%s: status %d", c.path, r.StatusCode)
		}
		if got := body(t, r); got != c.want {
			t.Errorf("%s: body %q, want %q", c.path, got, c.want)
		}
		if r.Header.Get("content-type") != c.ctype || r.Header.Get("cache-control") != c.cache {
			t.Errorf("%s: headers %q %q", c.path, r.Header.Get("content-type"), r.Header.Get("cache-control"))
		}
	}
}

func TestNoBundleSaysHowToBuildIt(t *testing.T) {
	for _, s := range []*Server{{UI: fstest.MapFS{}}, {}} {
		r := get(t, s, "/", nil)
		if r.StatusCode != 503 || body(t, r) != `{"error":"`+DistMissing+`"}` {
			t.Fatalf("got %d", r.StatusCode)
		}
	}
}

func TestJSONIsWhatABrowserWouldWrite(t *testing.T) {
	if got := string(MarshalJSON(map[string]string{"a": "<b>&"})); got != `{"a":"<b>&"}` {
		t.Fatalf("got %s", got)
	}
}
