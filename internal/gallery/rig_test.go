package gallery_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn"
	"github.com/claytoncasey01/conn/internal/gallery"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/themes"
)

const (
	raw     = "https://raw.githubusercontent.com/me/mine"
	indexAt = raw + "/main/index.json"
	commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	clock   = 1234
)

var (
	errOffline = errors.New("fetch failed")
	errSocket  = errors.New("socket closed")
	errDisk    = errors.New("disk")
)

var themeAt = regexp.MustCompile(
	`^https://raw\.githubusercontent\.com/me/mine/[0-9a-f]{40}/([^/]+)/theme\.json$`)

func defaultObject(t *testing.T) *js.Object {
	t.Helper()

	data, err := os.ReadFile("../../themes/default/theme.json")
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := js.Parse(data)
	if err != nil {
		t.Fatal(err)
	}

	object, _ := parsed.(*js.Object)

	return object
}

// manifest is Default with another id and name, and any fields replaced.
func manifest(t *testing.T, themeID string, fields ...any) *js.Object {
	t.Helper()

	object := defaultObject(t)
	object.Set("id", themeID)
	object.Set("name", themeID)

	for index := 0; index+1 < len(fields); index += 2 {
		key, _ := fields[index].(string)
		object.Set(key, fields[index+1])
	}

	return object
}

func themeText(t *testing.T, themeID string, fields ...any) string {
	t.Helper()

	return js.Stringify(manifest(t, themeID, fields...))
}

// entry is an index entry for themeID; fields replace or (with a nil value) remove its keys.
func entry(t *testing.T, themeID string, fields ...any) *js.Object {
	t.Helper()

	object := js.NewObject()
	object.Set("id", themeID)
	object.Set("name", themeID)
	object.Set("author", "someone")
	object.Set("version", "1.0.0")
	object.Set("description", "a theme")
	object.Set("commit", commitA)
	object.Set("sha256", shaB)
	object.Set("files", []any{"theme.json"})
	object.Set("manifest", manifest(t, themeID))

	for index := 0; index+1 < len(fields); index += 2 {
		key, _ := fields[index].(string)
		if fields[index+1] == nil {
			object.Delete(key)

			continue
		}

		object.Set(key, fields[index+1])
	}

	return object
}

func indexText(entries ...any) string {
	object := js.NewObject()
	object.Set("version", 1)
	object.Set("repo", "me/mine")
	object.Set("themes", entries)

	return js.Stringify(object)
}

func sha(text string) string {
	sum := sha256.Sum256([]byte(text))

	return hex.EncodeToString(sum[:])
}

// served is an entry whose theme text is served and hashed, its manifest the text's own.
func served(t *testing.T, text string, fields ...any) *js.Object {
	t.Helper()

	return entry(t, idOf(text),
		append([]any{"sha256", sha(text), "manifest", parsedText(text)}, fields...)...)
}

// parsedText is theme text parsed, or nil when it is not JSON.
func parsedText(text string) any {
	parsed, err := js.Parse([]byte(text))
	if err != nil {
		return nil
	}

	return parsed
}

// idOf is the id theme text gives itself.
func idOf(text string) string {
	object, _ := parsedText(text).(*js.Object)
	themeID, _ := object.Get("id")
	name, _ := themeID.(string)

	return name
}

// answer is what the fake host does with one request.
type answer func(request *http.Request) (*http.Response, error)

func reply(status int, body string) answer {
	return func(request *http.Request) (*http.Response, error) {
		reader := io.NopCloser(strings.NewReader(body))

		return respond(request, status, reader, int64(len(body))), nil
	}
}

func respond(request *http.Request, status int, body io.ReadCloser, length int64) *http.Response {
	return &http.Response{
		Status: http.StatusText(status), StatusCode: status, Proto: "HTTP/1.1", ProtoMajor: 1,
		ProtoMinor: 1, Header: http.Header{}, Body: body, ContentLength: length,
		Request: request,
	}
}

func offline(*http.Request) (*http.Response, error) { return nil, errOffline }

// hang never answers; the request's context ending is the only way out.
func hang(request *http.Request) (*http.Response, error) {
	<-request.Context().Done()

	return nil, fmt.Errorf("no answer: %w", context.Cause(request.Context()))
}

// host is a fake raw.githubusercontent.com: the index, each theme by id at any commit, and the
// URLs asked for.
type host struct {
	guard   sync.Mutex
	index   answer
	files   map[string]string
	special map[string]answer
	asked   []string
}

func (h *host) RoundTrip(request *http.Request) (*http.Response, error) {
	url := request.URL.String()

	h.guard.Lock()
	h.asked = append(h.asked, url)
	special := h.special[url]
	index := h.index
	h.guard.Unlock()

	if special != nil {
		return special(request)
	}

	if url == indexAt {
		return index(request)
	}

	match := themeAt.FindStringSubmatch(url)
	if match != nil {
		text, ok := h.lookup(match[1])
		if ok {
			return reply(http.StatusOK, text)(request)
		}
	}

	return reply(http.StatusNotFound, "Not Found")(request)
}

func (h *host) lookup(themeID string) (string, bool) {
	h.guard.Lock()
	defer h.guard.Unlock()

	text, ok := h.files[themeID]

	return text, ok
}

func (h *host) urls() []string {
	h.guard.Lock()
	defer h.guard.Unlock()

	return slices.Clone(h.asked)
}

// serve answers url with with, whatever else the host would do.
func (h *host) serve(url string, with answer) {
	h.guard.Lock()
	defer h.guard.Unlock()

	h.special[url] = with
}

func (h *host) put(text string, entries ...any) {
	h.guard.Lock()
	defer h.guard.Unlock()

	h.files[idOf(text)] = text
	h.index = reply(http.StatusOK, indexText(entries...))
}

// world is a user theme folder in a temp dir and a fake host serving the index and themes.
type world struct {
	root, user string
	host       *host
	files      gallery.Files
	now        int64
	timeout    time.Duration
	repo       string
}

func newWorld(t *testing.T, entries ...any) *world {
	t.Helper()

	root := t.TempDir()
	user := filepath.Join(root, "themes")

	err := os.Mkdir(user, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	return &world{
		root: root, user: user,
		host: &host{
			guard: sync.Mutex{}, index: reply(http.StatusOK, indexText(entries...)),
			files: map[string]string{}, special: map[string]answer{}, asked: nil,
		},
		files: gallery.Disk(), now: clock, timeout: 0, repo: "me/mine",
	}
}

func (env *world) gallery() *gallery.Gallery {
	now := env.now

	return gallery.New(gallery.Deps{
		Transport: env.host, Now: func() int64 { return now },
		Builtin: themes.Embedded(conn.Themes()), User: env.user, Repo: env.repo,
		Timeout: env.timeout, Files: env.files,
	})
}

func (env *world) install(t *testing.T, themeID any) gallery.Result {
	t.Helper()

	return env.gallery().Install(t.Context(), themeID)
}

func listing(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}

func read(t *testing.T, name string) string {
	t.Helper()

	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func body(result gallery.Result) string { return js.Stringify(result.Body) }

func expectCode(t *testing.T, result gallery.Result, code int) {
	t.Helper()

	if result.Code != code {
		t.Fatalf("code = %d, want %d: %s", result.Code, code, body(result))
	}
}

func expectList(t *testing.T, got []string, want ...string) {
	t.Helper()

	if want == nil {
		want = []string{}
	}

	if !slices.Equal(got, want) {
		t.Errorf("listing = %q, want %q", got, want)
	}
}

func matches(t *testing.T, text, pattern string) {
	t.Helper()

	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Errorf("%q does not match %s", text, pattern)
	}
}

// failing is the disk with the after-th call (from 0) to one method failing, or with every
// call from the after-th on failing (onward), or with an lstat of a path ending in denied failing.
type failing struct {
	gallery.DiskFiles

	guard  sync.Mutex
	which  string
	after  int
	onward bool
	calls  int
	denied string
}

func failOn(which string, after int) *failing {
	return &failing{DiskFiles: gallery.Disk(), guard: sync.Mutex{}, which: which, after: after}
}

func denying(suffix string) *failing {
	return &failing{DiskFiles: gallery.Disk(), guard: sync.Mutex{}, denied: suffix}
}

func (f *failing) WriteNew(path, text string) error {
	if f.fails("WriteNew") {
		return errDisk
	}

	err := f.DiskFiles.WriteNew(path, text)
	if err != nil {
		return fmt.Errorf("disk: %w", err)
	}

	return nil
}

func (f *failing) Rename(from, to string) error {
	if f.fails("Rename") {
		return errDisk
	}

	err := f.DiskFiles.Rename(from, to)
	if err != nil {
		return fmt.Errorf("disk: %w", err)
	}

	return nil
}

func (f *failing) RemoveAll(path string) error {
	if f.fails("RemoveAll") {
		return errDisk
	}

	err := f.DiskFiles.RemoveAll(path)
	if err != nil {
		return fmt.Errorf("disk: %w", err)
	}

	return nil
}

func (f *failing) Lstat(path string) (os.FileInfo, error) {
	if f.denied != "" {
		if strings.HasSuffix(path, f.denied) {
			return nil, os.ErrPermission
		}
	}

	info, err := f.DiskFiles.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("disk: %w", err)
	}

	return info, nil
}

func (f *failing) fails(method string) bool {
	f.guard.Lock()
	defer f.guard.Unlock()

	if method != f.which {
		return false
	}

	f.calls++

	if f.onward {
		return f.calls-1 >= f.after
	}

	return f.calls-1 == f.after
}
