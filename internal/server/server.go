// Package server is Conn's HTTP server: the API under /api and the page bundle for everything else. It listens on 127.0.0.1 only,
// and answers only requests addressed to this machine from pages served here, because the transcripts it reads hold secrets.
package server

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// A Host header's port, as in host:7777
var portSuffix = regexp.MustCompile(`:\d+$`)

// Host is the only address Conn listens on.
const Host = "127.0.0.1"

// DistMissing is the answer when the page bundle has no index.html.
const DistMissing = "dist missing: run bun run build in the conn repository folder"

var mimeTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".svg":   "image/svg+xml",
	".json":  "application/json",
	".woff2": "font/woff2",
}

// Server answers Conn's HTTP requests.
type Server struct {
	// UI is the built page bundle (index.html at its root)
	UI fs.FS
	// Clients reports how many pages are listening for live updates
	Clients func() int
}

// SendJSON writes body as JSON with code, never cached.
func SendJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("content-type", "application/json")
	w.Header().Set("cache-control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(MarshalJSON(body))
}

// MarshalJSON encodes v as compact JSON without escaping <, > and &, the same text a browser's JSON.stringify gives.
func MarshalJSON(v any) []byte {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return []byte("null")
	}
	return []byte(strings.TrimSuffix(b.String(), "\n"))
}

// Local reports whether a request is addressed to this machine and, when it carries an Origin, comes from a page served here.
func Local(r *http.Request) bool {
	host := portSuffix.ReplaceAllString(r.Host, "")
	if host != "127.0.0.1" && host != "localhost" {
		return false
	}
	origin := r.Header.Get("origin")
	if origin == "" {
		return true
	}
	o, err := url.Parse(origin)
	if err != nil || o.Scheme == "" || o.Host == "" {
		return false
	}
	h := strings.ToLower(o.Hostname())
	return h == "127.0.0.1" || h == "localhost"
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !Local(r) {
		SendJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	p := r.URL.Path
	if p == "/api/health" {
		clients := 0
		if s.Clients != nil {
			clients = s.Clients()
		}
		SendJSON(w, http.StatusOK, struct {
			OK      bool `json:"ok"`
			Clients int  `json:"clients"`
		}{true, clients})
		return
	}
	s.static(w, p)
}

// static serves a file of the page bundle, falling back to the page shell (index.html) for any path that is not a file in it.
func (s *Server) static(w http.ResponseWriter, p string) {
	name := "index.html"
	if p != "/" {
		// path.Clean of a rooted path never climbs above the root, so the name stays inside the bundle
		clean := strings.TrimPrefix(path.Clean("/"+p), "/")
		if clean != "" && isFile(s.UI, clean) {
			name = clean
		}
	}
	if !isFile(s.UI, name) {
		SendJSON(w, http.StatusServiceUnavailable, map[string]string{"error": DistMissing})
		return
	}
	f, err := s.UI.Open(name)
	if err != nil {
		SendJSON(w, http.StatusServiceUnavailable, map[string]string{"error": DistMissing})
		return
	}
	defer f.Close()
	ct, ok := mimeTypes[path.Ext(name)]
	if !ok {
		ct = "application/octet-stream"
	}
	w.Header().Set("content-type", ct)
	if strings.Contains("/"+name, "/assets/") {
		w.Header().Set("cache-control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("cache-control", "no-cache")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

func isFile(fsys fs.FS, name string) bool {
	if fsys == nil || !fs.ValidPath(name) {
		return false
	}
	st, err := fs.Stat(fsys, name)
	return err == nil && st.Mode().IsRegular()
}
