// Package server is Conn's HTTP server: the API under /api and the page bundle for everything
// else. It listens on 127.0.0.1 only, and answers only requests addressed to this machine from
// pages served here, because the transcripts it reads hold secrets.
package server

import (
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/claytoncasey01/conn/internal/gallery"
	"github.com/claytoncasey01/conn/internal/historyapi"
	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/openwork"
	"github.com/claytoncasey01/conn/internal/provided"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settingsapi"
	"github.com/claytoncasey01/conn/internal/themes"
)

// Host is the only address Conn listens on.
const Host = "127.0.0.1"

// DistMissing is the answer when the page bundle has no index.html.
const DistMissing = "dist missing: run bun run build in the conn repository folder"

const (
	// shellName is the page shell every page route gets.
	shellName = "index.html"

	// Built assets carry a content hash in their name, so they never change once served.
	cacheAssets = "public, max-age=31536000, immutable"
	cacheFiles  = "no-cache"
	cacheAPI    = "no-store"

	// The errors every API answers the same way.
	textForbidden = "forbidden"
	textNotFound  = "not found"
	textMethod    = "method not allowed"
	// textFailed is what a request that failed inside the server answers.
	textFailed = "the server could not handle that request"
)

// Server answers Conn's HTTP requests.
type Server struct {
	// UI is the built page bundle (index.html at its root).
	UI fs.FS
	// Clients reports how many pages are listening for live updates; nil is none.
	Clients func() int
	// Settings answers /api/settings; nil leaves the routes out.
	Settings *settingsapi.API
	// Themes answers /api/themes; nil leaves the routes out.
	Themes *themes.Library
	// Gallery answers /api/gallery; nil leaves the routes out.
	Gallery *gallery.API
	// Provided answers /api/provided; nil leaves the routes out.
	Provided *provided.API
	// Sessions answers /api/snapshot, the agent pages and /api/events; nil leaves them out.
	Sessions *sessions.Loop
	// History answers /api/history; nil while History is off, which HistoryOff explains.
	History    *historyapi.API
	HistoryOff string
	// Reviews answers /api/reviews; nil leaves the routes out.
	Reviews *reviews.Reviews
	// MyPrs answers /api/myprs; nil leaves the routes out.
	MyPrs *myprs.MyPrs
	// Work answers Open work's /api/work and /api/runs; nil leaves the routes out.
	Work *openwork.Work
	// Logger is told about a request that failed.
	Logger *log.Logger
	// Secret is what every /api request must carry (see gate.go). The zero Secret refuses all.
	Secret secret.Secret
	// Terminals answers the terminal route, /api/term/<id>; nil answers it not found.
	Terminals *Terminals
	// TerminalBackground is told the background the page's terminal is drawn on
	// (/api/term/background), for the host's answer to OSC 11; nil answers the route not found.
	TerminalBackground func(colour pty.RGB)
}

type errorBody struct {
	Error string `json:"error"`
}

type healthBody struct {
	OK      bool `json:"ok"`
	Clients int  `json:"clients"`
}

// ServeHTTP refuses a request from anywhere but this machine, answers the API to a request the
// gate lets through, and serves the page bundle.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, s.dispatch)
}

// dispatch answers a request under /api that the gate let through. A path no API owns gets the
// page shell, as any other path does.
func (s *Server) dispatch(w http.ResponseWriter, r *http.Request, urlPath string) {
	if urlPath == "/api/health" {
		sendJSON(w, http.StatusOK, healthBody{OK: true, Clients: s.clients()})

		return
	}

	if s.api(w, r, urlPath) {
		return
	}

	s.static(w, r.URL.Path)
}

// api answers a route under /api that one of the APIs owns, and is false when none does.
func (s *Server) api(w http.ResponseWriter, r *http.Request, urlPath string) bool {
	owners := []func(http.ResponseWriter, *http.Request, string) bool{
		s.terminal, s.actions, s.work, s.history, s.tabs,
	}

	for _, owns := range owners {
		if owns(w, r, urlPath) {
			return true
		}
	}

	if s.services(w, r, urlPath) {
		return true
	}

	return s.sessions(w, r, urlPath)
}

// services answers a route under /api that one of the smaller APIs owns, and is false when none
// does.
func (s *Server) services(w http.ResponseWriter, r *http.Request, urlPath string) bool {
	if route, ours := gallery.RouteOf(urlPath); ours {
		if s.Gallery != nil {
			s.gallery(w, r, route)

			return true
		}
	}

	if route, ours := settingsapi.RouteOf(urlPath); ours {
		if s.Settings != nil {
			s.settings(w, r, route)

			return true
		}
	}

	if route, ours := provided.RouteOf(urlPath); ours {
		if s.Provided != nil {
			s.provided(w, r, route)

			return true
		}
	}

	if route, ours := themes.RouteOf(urlPath); ours {
		if s.Themes != nil {
			s.themes(w, r, route)

			return true
		}
	}

	return false
}

func (s *Server) clients() int {
	if s.Clients == nil {
		return 0
	}

	return s.Clients()
}

// static serves a file of the page bundle, or the page shell for a path that is not a file in it.
func (s *Server) static(w http.ResponseWriter, urlPath string) {
	name := s.fileName(urlPath)
	if !isFile(s.UI, name) {
		sendJSON(w, http.StatusServiceUnavailable, errorBody{Error: DistMissing})

		return
	}

	file, err := s.UI.Open(name)
	if err != nil {
		sendJSON(w, http.StatusServiceUnavailable, errorBody{Error: DistMissing})

		return
	}

	defer func() { _ = file.Close() }()

	w.Header().Set("Content-Type", contentType(path.Ext(name)))
	w.Header().Set("Cache-Control", cacheControl(name))
	w.WriteHeader(http.StatusOK)

	_, _ = io.Copy(w, file)
}

// fileName is the bundle file a URL path names, or the page shell when it names none.
func (s *Server) fileName(urlPath string) string {
	// path.Clean of a rooted path never climbs above the root, so the name stays in the bundle.
	clean := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if clean == "" {
		return shellName
	}

	if !isFile(s.UI, clean) {
		return shellName
	}

	return clean
}

func isFile(fsys fs.FS, name string) bool {
	if fsys == nil {
		return false
	}

	if !fs.ValidPath(name) {
		return false
	}

	info, err := fs.Stat(fsys, name)
	if err != nil {
		return false
	}

	return info.Mode().IsRegular()
}

func contentType(extension string) string {
	switch extension {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".json":
		return "application/json"
	case ".woff2":
		return "font/woff2"
	}

	return "application/octet-stream"
}

func cacheControl(name string) string {
	if strings.Contains("/"+name, "/assets/") {
		return cacheAssets
	}

	return cacheFiles
}

// local reports whether a request is addressed to this machine and, when it carries an Origin,
// comes from a page served here.
func local(r *http.Request) bool {
	if !loopbackName(withoutPort(r.Host)) {
		return false
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}

	if parsed.Scheme == "" {
		return false
	}

	if parsed.Host == "" {
		return false
	}

	return loopbackName(strings.ToLower(parsed.Hostname()))
}

func loopbackName(host string) bool {
	switch host {
	case "127.0.0.1", "localhost":
		return true
	}

	return false
}

// withoutPort is a Host header without its port (":7777"): a colon and one or more ASCII digits
// at the end. "localhost:" and "localhost:abc" keep theirs, so they are refused.
func withoutPort(host string) string {
	name, digits, found := strings.CutLast(host, ":")
	if !found {
		return host
	}

	if digits == "" {
		return host
	}

	for _, char := range digits {
		if char < '0' {
			return host
		}

		if char > '9' {
			return host
		}
	}

	return name
}

// sendJSON writes body as JSON with code, never cached.
func sendJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", cacheAPI)
	w.WriteHeader(code)

	_, _ = w.Write(marshalJSON(body))
}

// marshalJSON encodes v as compact JSON without escaping <, > and &, the same text a browser's
// JSON.stringify gives.
func marshalJSON(value any) []byte {
	var builder strings.Builder

	encoder := json.NewEncoder(&builder)
	encoder.SetEscapeHTML(false)

	err := encoder.Encode(value)
	if err != nil {
		return []byte("null")
	}

	return []byte(strings.TrimSuffix(builder.String(), "\n"))
}
