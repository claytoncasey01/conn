package gallery_test

import (
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/gallery"
	"github.com/claytoncasey01/conn/internal/testtime"
)

func fetch(t *testing.T, env *world) (gallery.Index, string) {
	t.Helper()

	return env.gallery().FetchIndex(t.Context())
}

func indexIDs(index gallery.Index) []string {
	ids := make([]string, 0, len(index.Themes))
	for _, entry := range index.Themes {
		ids = append(ids, entry.ID)
	}

	return ids
}

func TestAGoodIndexIsReadFromThePinnedHostAndPathOnly(t *testing.T) {
	t.Parallel()

	env := newWorld(t, entry(t, "ember"), entry(t, "paper"))

	index, problem := fetch(t, env)
	if problem != "" {
		t.Fatal(problem)
	}

	expectList(t, indexIDs(index), "ember", "paper")

	if index.Dropped != 0 {
		t.Errorf("dropped = %d", index.Dropped)
	}

	if index.CheckedAt != clock {
		t.Errorf("checkedAt = %d", index.CheckedAt)
	}

	first := index.Themes[0]
	if first.Commit != commitA {
		t.Errorf("commit = %s", first.Commit)
	}

	if first.Sha256 != shaB {
		t.Errorf("sha256 = %s", first.Sha256)
	}

	if first.Manifest.ID != "ember" {
		t.Errorf("manifest id = %s", first.Manifest.ID)
	}

	expectList(t, env.host.urls(), indexAt)
}

func TestAnEntryMayLeaveOutFiles(t *testing.T) {
	t.Parallel()

	index, problem := fetch(t, newWorld(t, entry(t, "ember", "files", nil)))
	if problem != "" {
		t.Fatal(problem)
	}

	if len(index.Themes) != 1 {
		t.Fatalf("themes = %d", len(index.Themes))
	}

	if index.Themes[0].Files != nil {
		t.Errorf("files = %q, want none", index.Themes[0].Files)
	}
}

func TestIndexFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		answer answer
		want   string
	}{
		{
			name: "html", answer: reply(200, "<html>nope</html>"),
			want: "The theme index is not valid JSON",
		},
		{name: "empty", answer: reply(200, ""), want: "The theme index is not valid JSON"},
		{
			name: "null", answer: reply(200, "null"),
			want: "The theme index is not an index: it has no themes list",
		},
		{
			name: "list", answer: reply(200, "[]"),
			want: "The theme index is not an index: it has no themes list",
		},
		{
			name: "object", answer: reply(200, "{}"),
			want: "The theme index is not an index: it has no themes list",
		},
		{
			name: "themes a string", answer: reply(200, `{"themes":"x"}`),
			want: "The theme index is not an index: it has no themes list",
		},
		{
			name: "over 512 KB", answer: reply(200, strings.Repeat(" ", gallery.IndexBytesMax+1)),
			want: "The theme index is larger than 512 KB",
		},
		{
			name:   "over 512 KB in bytes, not characters",
			answer: reply(200, strings.Repeat("\u00e9", gallery.IndexBytesMax/2+1)),
			want:   "The theme index is larger than 512 KB",
		},
		{
			name: "403", answer: reply(403, "x"),
			want: "GitHub is limiting requests (403); try the theme index again in a few minutes",
		},
		{
			name: "429", answer: reply(429, "x"),
			want: "GitHub is limiting requests (429); try the theme index again in a few minutes",
		},
		{
			name: "404", answer: reply(404, "x"),
			want: "me/mine has no index.json on its main branch (404 not found)",
		},
		{name: "500", answer: reply(500, "x"), want: "GitHub answered 500 for the theme index"},
		{
			name: "offline", answer: offline,
			want: "Could not reach raw.githubusercontent.com to read the theme index",
		},
		{
			name: "a body that fails to read", answer: failingBody,
			want: "Could not reach raw.githubusercontent.com to read the theme index",
		},
		{
			name: "a redirect", answer: redirect,
			want: "Could not reach raw.githubusercontent.com to read the theme index",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newWorld(t)
			env.host.index = tt.answer

			_, problem := fetch(t, env)
			if problem != tt.want {
				t.Errorf("problem = %q, want %q", problem, tt.want)
			}
		})
	}
}

func failingBody(request *http.Request) (*http.Response, error) {
	return respond(request, http.StatusOK, io.NopCloser(errorReader{}), -1), nil
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errSocket }

func redirect(request *http.Request) (*http.Response, error) {
	response := respond(request, http.StatusFound, io.NopCloser(strings.NewReader("")), 0)
	response.Header.Set("Location", "https://example.com/index.json")

	return response, nil
}

// stalled answers the headers, then a body that never sends a byte.
func stalled(request *http.Request) (*http.Response, error) {
	return respond(request, http.StatusOK, io.NopCloser(waitReader{request: request}), -1), nil
}

type waitReader struct{ request *http.Request }

func (wait waitReader) Read([]byte) (int, error) {
	<-wait.request.Context().Done()

	return 0, fmt.Errorf("body: %w", wait.request.Context().Err())
}

func TestASlowHostTimesOut(t *testing.T) {
	t.Parallel()

	for name, slow := range map[string]answer{"no answer": hang, "a stalled body": stalled} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := newWorld(t)
			env.host.index = slow
			env.timeout = 20 * time.Millisecond

			began := time.Now()

			_, problem := fetch(t, env)
			if problem != "The theme index timed out after 10 s" {
				t.Errorf("problem = %q", problem)
			}

			if time.Since(began) > testtime.Scaled(2*time.Second) {
				t.Error("the read outlived its deadline")
			}
		})
	}
}

// stream is an endless body of chunk-byte pieces that counts its reads and notes a close.
type stream struct {
	guard  sync.Mutex
	chunk  int
	reads  int
	bytes  int
	closed bool
}

func (s *stream) Read(buffer []byte) (int, error) {
	s.guard.Lock()
	defer s.guard.Unlock()

	s.reads++

	count := min(len(buffer), s.chunk)
	for index := range count {
		buffer[index] = ' '
	}

	s.bytes += count

	return count, nil
}

func (s *stream) Close() error {
	s.guard.Lock()
	defer s.guard.Unlock()

	s.closed = true

	return nil
}

func (s *stream) seen() (int, int, bool) {
	s.guard.Lock()
	defer s.guard.Unlock()

	return s.reads, s.bytes, s.closed
}

func TestAnEndlessBodyIsCutOffAtTheCap(t *testing.T) {
	t.Parallel()

	body := &stream{guard: sync.Mutex{}, chunk: 64 * 1024}
	env := newWorld(t)
	env.host.index = func(request *http.Request) (*http.Response, error) {
		return respond(request, http.StatusOK, body, -1), nil
	}

	_, problem := fetch(t, env)
	if problem != "The theme index is larger than 512 KB" {
		t.Errorf("problem = %q", problem)
	}

	_, read, closed := body.seen()
	if !closed {
		t.Error("the body was not closed")
	}

	// Reading stops the moment the cap is passed: at most one chunk past it is ever read.
	if read > gallery.IndexBytesMax+64*1024 {
		t.Errorf("read %d bytes", read)
	}
}

func TestADeclaredLengthOverTheCapIsNeverRead(t *testing.T) {
	t.Parallel()

	body := &stream{guard: sync.Mutex{}, chunk: 10}
	env := newWorld(t)
	env.host.index = func(request *http.Request) (*http.Response, error) {
		return respond(request, http.StatusOK, body, gallery.IndexBytesMax+1), nil
	}

	_, problem := fetch(t, env)
	if problem != "The theme index is larger than 512 KB" {
		t.Errorf("problem = %q", problem)
	}

	reads, _, _ := body.seen()
	if reads != 0 {
		t.Errorf("read %d times", reads)
	}
}

func TestBadEntriesAreDroppedAndCounted(t *testing.T) {
	t.Parallel()

	badManifest := manifest(t, "badmanifest", "modes", []any{"sepia"})
	bad := []any{
		nil, "ember", 7.0,
		[]any{},
		entry(t, "Upper"), entry(t, "-lead"), entry(t, "../etc"), entry(t, strings.Repeat("a", 41)),
		entry(t, "crt", "manifest", manifest(t, "crt")),
		entry(t, "twin"), entry(t, "twin"),
		entry(t, "nocommit", "commit", nil),
		entry(t, "shortcommit", "commit", "abc123"),
		entry(t, "upcommit", "commit", strings.Repeat("A", 40)),
		entry(t, "badsha", "sha256", "xyz"),
		entry(t, "shortsha", "sha256", strings.Repeat("b", 63)),
		entry(t, "extrafile", "files", []any{"theme.json", "theme.css"}),
		entry(t, "cssonly", "files", []any{"theme.css"}),
		entry(t, "filesstr", "files", "theme.json"),
		entry(t, "nomanifest", "manifest", nil),
		entry(t, "badmanifest", "manifest", badManifest),
		entry(t, "mismatch", "manifest", manifest(t, "other")),
	}

	entries := append([]any{entry(t, "ok1")}, bad...)
	entries = append(entries, entry(t, "nofiles", "files", nil), entry(t, "ok2"))

	index, problem := fetch(t, newWorld(t, entries...))
	if problem != "" {
		t.Fatal(problem)
	}

	expectList(t, indexIDs(index), "ok1", "twin", "nofiles", "ok2")

	// The first twin is kept, its repeat dropped.
	if index.Dropped != len(bad)-1 {
		t.Errorf("dropped = %d, want %d", index.Dropped, len(bad)-1)
	}
}

func TestABuiltinIDIsDropped(t *testing.T) {
	t.Parallel()

	for _, themeID := range []string{"default", "crt", "matrix", "neon"} {
		index, problem := fetch(t, newWorld(t, entry(t, themeID)))
		if problem != "" {
			t.Fatal(problem)
		}

		if len(index.Themes) != 0 {
			t.Errorf("%s is kept", themeID)
		}

		if index.Dropped != 1 {
			t.Errorf("%s: dropped = %d", themeID, index.Dropped)
		}
	}
}

func TestMoreThan500EntriesAreCut(t *testing.T) {
	t.Parallel()

	many := make([]any, 0, 520)

	for index := range 520 {
		themeID := "t" + strconv.Itoa(index)
		tiny := map[string]any{
			"id": themeID, "name": themeID, "version": "1", "author": "", "license": "",
			"description": "", "modes": []any{"dark"}, "accent": "fixed", "fonts": []any{},
		}
		many = append(many, entry(t, themeID, "manifest", tiny))
	}

	index, problem := fetch(t, newWorld(t, many...))
	if problem != "" {
		t.Fatal(problem)
	}

	if len(index.Themes) != gallery.IndexEntriesMax {
		t.Fatalf("themes = %d", len(index.Themes))
	}

	if index.Themes[499].ID != "t499" {
		t.Errorf("last = %s", index.Themes[499].ID)
	}

	if index.Dropped != 20 {
		t.Errorf("dropped = %d", index.Dropped)
	}
}

func TestAnEntryShowsItsValidatedManifestsFields(t *testing.T) {
	t.Parallel()

	shown := manifest(t, "ember", "name", "Ember", "author", "Ann", "version", "2.0",
		"description", "warm")

	index, problem := fetch(t, newWorld(t, entry(t, "ember", "name", strings.Repeat("X", 5000),
		"manifest", shown)))
	if problem != "" {
		t.Fatal(problem)
	}

	got := index.Themes[0]
	fields := []string{got.Name, got.Author, got.Version, got.Description}

	if !slices.Equal(fields, []string{"Ember", "Ann", "2.0", "warm"}) {
		t.Errorf("fields = %q", fields)
	}
}

func TestARepoThatIsNotOwnerSlashNameIsRefusedBeforeAnyRequest(t *testing.T) {
	t.Parallel()

	for _, repo := range []string{"", "me", "me/mine/x", "../x", "me/..", "me/mine?x"} {
		env := newWorld(t)
		env.repo = repo

		_, problem := fetch(t, env)
		if problem != "The theme index repo is not a GitHub owner/repo name" {
			t.Errorf("%q: %q", repo, problem)
		}

		if len(env.host.urls()) != 0 {
			t.Errorf("%q: asked %q", repo, env.host.urls())
		}
	}
}
