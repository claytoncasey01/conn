package gallery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/theme"
	"github.com/claytoncasey01/conn/internal/themes"
)

// Deps is what the gallery reads from and writes to.
type Deps struct {
	// Transport makes the HTTPS requests; nil is http.DefaultTransport.
	Transport http.RoundTripper
	// Now is the clock, in Unix milliseconds.
	Now func() int64
	// Builtin is the built-in themes: ids a gallery theme cannot take, and Default.
	Builtin themes.Shelf
	// User is the user's theme folder, where installs go.
	User string
	// Repo is the index repo, owner/name.
	Repo string
	// Timeout bounds each request; zero is Timeout.
	Timeout time.Duration
	// Files makes the folder changes; nil is the disk.
	Files Files
}

// Gallery reads the index and installs and removes gallery themes.
type Gallery struct {
	deps Deps

	guard sync.Mutex
	// busy are the ids being installed: two installs of one id at once would race on the rename,
	// and an uninstall during one would be undone by it.
	busy map[string]bool
}

// New makes the gallery.
func New(deps Deps) *Gallery {
	if deps.Timeout == 0 {
		deps.Timeout = Timeout
	}

	if deps.Files == nil {
		deps.Files = Disk()
	}

	return &Gallery{deps: deps, guard: sync.Mutex{}, busy: map[string]bool{}}
}

// Result is an install's or an uninstall's answer: a status code and its JSON body.
type Result struct {
	Code int
	Body any
}

// done is {ok: true, id, version}.
type done struct {
	OK      bool   `json:"ok"`
	ID      string `json:"id"`
	Version string `json:"version"`
}

type failed struct {
	Error string `json:"error"`
}

func refuse(code int, message string) Result {
	return Result{Code: code, Body: failed{Error: message}}
}

// Install installs id from the gallery, or updates it when it is installed from there already.
// Only an id comes in: the entry is looked up in a fresh index, its theme.json fetched at the
// entry's pinned commit, which must hash to the index's sha256 and pass the theme check as a
// gallery theme. It is written to a temp folder in the user folder and renamed into place; any
// failure removes the temp folder and keeps the old theme.
func (g *Gallery) Install(ctx context.Context, raw any) Result {
	themeID, isText := raw.(string)
	if !isText {
		return refuse(http.StatusBadRequest, "That is not a theme id")
	}

	if !IsID(themeID) {
		return refuse(http.StatusBadRequest, "That is not a theme id")
	}

	if !g.claim(themeID) {
		return refuse(http.StatusConflict, themeID+" is already being installed")
	}

	defer g.release(themeID)

	base := themes.BaseOf(g.deps.Builtin)
	if base.IDs[themeID] {
		return refuse(http.StatusBadRequest,
			"A built-in theme cannot be installed from the gallery")
	}

	// A failure nobody planned for is this answer, never a stopped server.
	result := refuse(http.StatusInternalServerError, "The theme could not be installed")

	func() {
		defer func() { _ = recover() }()

		result = g.install(context.WithoutCancel(ctx), themeID, base)
	}()

	return result
}

// Uninstall removes an installed gallery theme: only a real directory (not a link) holding a
// .source.json regular file.
func (g *Gallery) Uninstall(raw any) Result {
	themeID, isText := raw.(string)
	if !isText {
		return refuse(http.StatusBadRequest, "That is not a theme id")
	}

	if !themes.IsFolderName(themeID) {
		return refuse(http.StatusBadRequest, "That is not a theme id")
	}

	// An update in flight would put the theme back after this removed it.
	if g.installing(themeID) {
		return refuse(http.StatusConflict, themeID+" is being installed, so it was not removed")
	}

	target := filepath.Join(g.deps.User, themeID)

	switch g.slot(target) {
	case slotUnreadable:
		return unreadable(themeID)
	case slotAbsent:
		return refuse(http.StatusNotFound, themeID+" is not installed")
	case slotOther:
		return refuse(http.StatusConflict, themeID+
			" was not installed from the gallery, so it was "+
			"not removed")
	case slotGallery:
	}

	err := g.deps.Files.RemoveAll(target)
	if err != nil {
		return refuse(http.StatusInternalServerError, themeID+" could not be removed")
	}

	return Result{Code: http.StatusOK, Body: done{OK: true, ID: themeID, Version: ""}}
}

// claim marks id as being installed, and is false when it already is.
func (g *Gallery) claim(themeID string) bool {
	g.guard.Lock()
	defer g.guard.Unlock()

	if g.busy[themeID] {
		return false
	}

	g.busy[themeID] = true

	return true
}

// slotState is what is at a theme's place in the user folder right now.
type slotState int

const (
	slotAbsent slotState = iota
	// slotGallery is a gallery install: a real directory holding a .source.json regular file.
	slotGallery
	// slotOther is the user's own: a link, a file, or a folder with no marker.
	slotOther
	// slotUnreadable is any lstat error but a missing file: nothing is known, so nothing is
	// touched.
	slotUnreadable
)

func (g *Gallery) release(themeID string) {
	g.guard.Lock()
	defer g.guard.Unlock()

	delete(g.busy, themeID)
}

func unreadable(themeID string) Result {
	return refuse(http.StatusInternalServerError,
		themeID+"'s folder could not be read, so nothing was changed")
}

func yours(themeID string) Result {
	return refuse(http.StatusConflict, themeID+" is a folder of your own, so it was not replaced")
}

// full is the answer for a new theme past the list's limit: it would be written and never
// listed. An update of an installed one is not new.
func full(themeID string) Result {
	return refuse(http.StatusConflict, "conn lists at most "+strconv.Itoa(theme.UserThemesMax)+
		" themes of yours and you have that many, so "+themeID+
		" was not installed; remove one first")
}

func (g *Gallery) installing(themeID string) bool {
	g.guard.Lock()
	defer g.guard.Unlock()

	return g.busy[themeID]
}

func (g *Gallery) slot(target string) slotState {
	info, err := g.deps.Files.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return slotAbsent
	}

	if err != nil {
		return slotUnreadable
	}

	if info.Mode()&fs.ModeSymlink != 0 {
		return slotOther
	}

	if !info.IsDir() {
		return slotOther
	}

	marker, err := g.deps.Files.Lstat(filepath.Join(target, themes.SourceFile))
	if errors.Is(err, fs.ErrNotExist) {
		return slotOther
	}

	if err != nil {
		return slotUnreadable
	}

	if marker.Mode().IsRegular() {
		return slotGallery
	}

	return slotOther
}

func find(entries []Entry, themeID string) (Entry, bool) {
	for _, entry := range entries {
		if entry.ID == themeID {
			return entry, true
		}
	}

	return Entry{}, false
}

func (g *Gallery) install(ctx context.Context, themeID string, base themes.Base) Result {
	index, problem := g.fetchIndex(ctx, base)
	if problem != "" {
		return refuse(http.StatusBadGateway, problem)
	}

	entry, listed := find(index.Themes, themeID)
	if !listed {
		return refuse(http.StatusNotFound, themeID+" is not in the gallery")
	}

	target := filepath.Join(g.deps.User, themeID)

	_, refused, roomy := g.room(target, themeID)
	if !roomy {
		return refused
	}

	text, refused, fetched := g.download(ctx, entry, base)
	if !fetched {
		return refused
	}

	// The download took up to 10 s, in which the folder may have become the user's own: it is
	// judged again, and nothing waits from here to the renames.
	state, refused, roomy := g.room(target, themeID)
	if !roomy {
		return refused
	}

	return g.write(target, entry, text, state == slotGallery)
}

// room is whether id may be written at target: not over the user's own folder, not one that
// cannot be looked at, and not a new theme past the limit.
func (g *Gallery) room(target, themeID string) (slotState, Result, bool) {
	state := g.slot(target)

	switch state {
	case slotUnreadable:
		return state, unreadable(themeID), false
	case slotOther:
		return state, yours(themeID), false
	case slotAbsent:
		if themes.UserFolderCount(themes.Disk(g.deps.User)) >= theme.UserThemesMax {
			return state, full(themeID), false
		}
	case slotGallery:
	}

	return state, Result{}, true
}

// download fetches the entry's theme.json at its pin and holds it to the index: its hash, the
// theme check, its id and the manifest the card was drawn from.
func (g *Gallery) download(ctx context.Context, entry Entry, base themes.Base) (string, Result,
	bool,
) {
	themeID := entry.ID
	url := rawHost + g.deps.Repo + "/" + entry.Commit + "/" + themeID + "/" + themes.ManifestFile

	reply, err := g.get(ctx, url, ThemeBytesMax)
	if errors.Is(err, errTimedOut) {
		return "", refuse(http.StatusBadGateway, "The theme download timed out after "+seconds()),
			false
	}

	if err != nil {
		return "", refuse(http.StatusBadGateway,
			"Could not reach raw.githubusercontent.com to download the theme"), false
	}

	if !reply.ok {
		return "", downloadStatus(themeID, reply.status), false
	}

	if reply.tooBig {
		return "", refuse(http.StatusBadGateway, themeID+"'s theme.json is larger than "+
			kb(ThemeBytesMax)), false
	}

	refused, ok := matches(entry, reply.body, base)
	if !ok {
		return "", refused, false
	}

	return reply.body, Result{}, true
}

func downloadStatus(themeID string, status int) Result {
	switch status {
	case http.StatusForbidden, http.StatusTooManyRequests:
		return refuse(http.StatusBadGateway, "GitHub is limiting requests ("+strconv.Itoa(status)+
			"); try again in a few minutes")
	}

	return refuse(http.StatusBadGateway, "GitHub answered "+strconv.Itoa(status)+" for "+themeID+
		"'s theme.json")
}

// matches holds the downloaded text to the entry. The hash is of the text as decoded and encoded
// again, and that text is what is checked and written.
func matches(entry Entry, text string, base themes.Base) (Result, bool) {
	themeID := entry.ID
	sum := sha256.Sum256([]byte(text))

	if hex.EncodeToString(sum[:]) != entry.Sha256 {
		return refuse(http.StatusUnprocessableEntity, themeID+
			"'s theme.json does not match the hash "+
			"in the index, so it was not installed"), false
	}

	context := theme.Context{Source: theme.SourceGallery, BuiltinIDs: base.IDs}

	checked, problems := theme.CheckTheme(text, context, &base.Fallback)
	if len(problems) > 0 {
		return refuse(http.StatusUnprocessableEntity, themeID+
			"'s theme.json is not a valid theme: "+
			joinProblems(problems)), false
	}

	if checked.ID != themeID {
		return refuse(http.StatusUnprocessableEntity, themeID+"'s theme.json has the id "+
			js.Stringify(checked.ID)+", so it was not installed"), false
	}

	// The card and its preview were drawn from the index's manifest, so the file must be that
	// theme, key order aside.
	if !theme.Same(&entry.Manifest, &checked) {
		return refuse(http.StatusUnprocessableEntity, themeID+"'s theme.json does not match what "+
			"the index showed for it, so it was not installed"), false
	}

	return Result{}, true
}

func joinProblems(problems []string) string { return strings.Join(problems, "; ") }
