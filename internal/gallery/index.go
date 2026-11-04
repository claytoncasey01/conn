// Package gallery is the Themes gallery: a read-only index of themes kept in a public GitHub
// repo, and installing one from it into the user's theme folder. Reads are plain HTTPS GETs to
// raw.githubusercontent.com with no token and no redirect followed. The index is untrusted input:
// its size is checked before it is parsed, and a bad entry is dropped and counted.
package gallery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/theme"
	"github.com/claytoncasey01/conn/internal/themes"
)

// The gallery's limits: how long one request may take, how large the index and a theme may be,
// and how many index entries are read.
const (
	Timeout         = 10 * time.Second
	IndexBytesMax   = 512 * kibibyte
	ThemeBytesMax   = theme.ManifestBytesMax
	IndexEntriesMax = 500

	kibibyte = 1024
	rawHost  = "https://raw.githubusercontent.com/"
)

var (
	// idForm is the id shape the reader and the installer accept.
	idForm     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	commitForm = regexp.MustCompile(`^[0-9a-f]{40}$`)
	sha256Form = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

var (
	errTimedOut   = errors.New("timed out")
	errRedirected = errors.New("a redirect is never followed")
)

// IsID reports whether id is a gallery theme id: 1 to 40 of a-z, 0-9 and -, not starting with -.
func IsID(themeID string) bool { return idForm.MatchString(themeID) }

// Entry is one theme the index lists, pinned at a commit with the sha256 of its theme.json there.
type Entry struct {
	ID          string
	Name        string
	Author      string
	Version     string
	Description string
	Commit      string
	Sha256      string
	// Files is nil when the entry did not list them.
	Files    []string
	Manifest theme.Manifest
}

// MarshalJSON writes the entry as the index and the page have it.
func (e Entry) MarshalJSON() ([]byte, error) {
	object := js.NewObject()
	object.Set("id", e.ID)
	object.Set("name", e.Name)
	object.Set("author", e.Author)
	object.Set("version", e.Version)
	object.Set("description", e.Description)
	object.Set("commit", e.Commit)
	object.Set("sha256", e.Sha256)

	if e.Files != nil {
		object.Set("files", slices.Clone(e.Files))
	}

	object.Set("manifest", &e.Manifest)

	return []byte(js.Stringify(object)), nil
}

// Index is a read of the index: the entries kept, how many were dropped, and when (Unix ms).
type Index struct {
	Themes    []Entry
	Dropped   int
	CheckedAt int64
}

// got is one GET: its status, and its body as text unless it was over the cap.
type got struct {
	ok     bool
	status int
	body   string
	tooBig bool
}

func (g *Gallery) client() *http.Client {
	return &http.Client{
		Transport: g.deps.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errRedirected
		},
		Jar:     nil,
		Timeout: 0,
	}
}

// get is one GET and its body as text, all under one deadline. An error status's body is never
// read; a declared length over bytesMax is refused unread, and a body is cut off the moment it
// passes bytesMax. The error is errTimedOut when the deadline passed.
func (g *Gallery) get(ctx context.Context, url string, bytesMax int64) (got, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, g.deps.Timeout, errTimedOut)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return got{}, fmt.Errorf("making the request: %w", err)
	}

	response, err := g.client().Do(request)
	if err != nil {
		return got{}, timedOut(ctx, err)
	}

	defer func() { _ = response.Body.Close() }()

	const statusOKMax = 299

	if response.StatusCode < http.StatusOK {
		return got{ok: false, status: response.StatusCode}, nil
	}

	if response.StatusCode > statusOKMax {
		return got{ok: false, status: response.StatusCode}, nil
	}

	if response.ContentLength > bytesMax {
		return got{ok: true, status: response.StatusCode, tooBig: true}, nil
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, bytesMax+1))
	if err != nil {
		return got{}, timedOut(ctx, err)
	}

	if int64(len(data)) > bytesMax {
		return got{ok: true, status: response.StatusCode, tooBig: true}, nil
	}

	// TextDecoder's reading: a leading byte order mark dropped, bad bytes replaced.
	return got{ok: true, status: response.StatusCode, body: js.DecodeUTF8(data, true)}, nil
}

// timedOut is errTimedOut when ctx's deadline passed, else err.
func timedOut(ctx context.Context, err error) error {
	if errors.Is(context.Cause(ctx), errTimedOut) {
		return errTimedOut
	}

	return err
}

// seconds is the timeout as the messages give it: the fixed limit, whatever a test set.
func seconds() string { return strconv.Itoa(int(Timeout/time.Second)) + " s" }

// FetchIndex reads and vets the gallery index. The error is a message the page can show.
func (g *Gallery) FetchIndex(ctx context.Context) (Index, string) {
	base := themes.BaseOf(g.deps.Builtin)

	return g.fetchIndex(ctx, base)
}

func (g *Gallery) fetchIndex(ctx context.Context, base themes.Base) (Index, string) {
	// The repo goes into the URL, so it is checked again here whatever the caller did.
	if !config.IsRepoName(g.deps.Repo) {
		return Index{}, "The theme index repo is not a GitHub owner/repo name"
	}

	reply, err := g.get(ctx, rawHost+g.deps.Repo+"/main/index.json", IndexBytesMax)
	if errors.Is(err, errTimedOut) {
		return Index{}, "The theme index timed out after " + seconds()
	}

	if err != nil {
		return Index{}, "Could not reach raw.githubusercontent.com to read the theme index"
	}

	if !reply.ok {
		return Index{}, indexStatus(g.deps.Repo, reply.status)
	}

	if reply.tooBig {
		return Index{}, "The theme index is larger than " + kb(IndexBytesMax)
	}

	parsed, err := js.Parse([]byte(reply.body))
	if err != nil {
		return Index{}, "The theme index is not valid JSON"
	}

	listed, ok := themeList(parsed)
	if !ok {
		return Index{}, "The theme index is not an index: it has no themes list"
	}

	kept := []Entry{}
	seen := map[string]bool{}

	// Entries past the cap are dropped unread.
	for _, raw := range listed[:min(len(listed), IndexEntriesMax)] {
		entry, keep := keepEntry(raw, base, seen)
		if keep {
			kept = append(kept, entry)
		}
	}

	return Index{Themes: kept, Dropped: len(listed) - len(kept), CheckedAt: g.deps.Now()}, ""
}

func kb(bytes int) string { return strconv.Itoa(bytes/kibibyte) + " KB" }

func indexStatus(repo string, status int) string {
	switch status {
	case http.StatusForbidden, http.StatusTooManyRequests:
		return "GitHub is limiting requests (" + strconv.Itoa(status) +
			"); try the theme index again in a few minutes"
	case http.StatusNotFound:
		return repo + " has no index.json on its main branch (404 not found)"
	}

	return "GitHub answered " + strconv.Itoa(status) + " for the theme index"
}

func themeList(parsed any) ([]any, bool) {
	object, isObject := parsed.(*js.Object)
	if !isObject {
		return nil, false
	}

	raw, _ := object.Get("themes")
	list, isList := raw.([]any)

	return list, isList
}

// keepEntry is one index entry as it is kept, or false: it is dropped for any shape, id, pin,
// hash, file list or manifest problem.
func keepEntry(raw any, base themes.Base, seen map[string]bool) (Entry, bool) {
	object, isObject := raw.(*js.Object)
	if !isObject {
		return Entry{}, false
	}

	themeID, commit, sha, shaped := pins(object)
	if !shaped {
		return Entry{}, false
	}

	if base.IDs[themeID] {
		return Entry{}, false
	}

	if seen[themeID] {
		return Entry{}, false
	}

	files, ok := fileList(object)
	if !ok {
		return Entry{}, false
	}

	given, _ := object.Get("manifest")
	context := theme.Context{Source: theme.SourceGallery, BuiltinIDs: base.IDs}

	// The embedded manifest is what a card previews, so it must be the entry's own.
	manifest, problems := theme.ValidateManifest(given, context)
	if len(problems) > 0 {
		return Entry{}, false
	}

	if manifest.ID != themeID {
		return Entry{}, false
	}

	seen[themeID] = true

	return Entry{
		ID: themeID, Name: manifest.Name, Author: manifest.Author, Version: manifest.Version,
		Description: manifest.Description, Commit: commit, Sha256: sha, Files: files,
		Manifest: manifest,
	}, true
}

// pins are an entry's id, commit and sha256, each of the right shape.
func pins(object *js.Object) (string, string, string, bool) {
	text := func(key string) string {
		raw, _ := object.Get(key)
		value, _ := raw.(string)

		return value
	}

	themeID, commit, sha := text("id"), text("commit"), text("sha256")
	shaped := []bool{IsID(themeID), commitForm.MatchString(commit), sha256Form.MatchString(sha)}

	return themeID, commit, sha, !slices.Contains(shaped, false)
}

// fileList is an entry's files: absent (nil), or a list of nothing but theme.json.
func fileList(object *js.Object) ([]string, bool) {
	raw, present := object.Get("files")
	if !present {
		return nil, true
	}

	list, isList := raw.([]any)
	if !isList {
		return nil, false
	}

	files := make([]string, 0, len(list))

	for _, item := range list {
		if item != themes.ManifestFile {
			return nil, false
		}

		files = append(files, themes.ManifestFile)
	}

	return files, true
}
