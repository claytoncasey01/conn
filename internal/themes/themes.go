package themes

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/theme"
)

const (
	// shownUnitsMax is how much of a folder name an entry or a message quotes.
	shownUnitsMax = 40

	defaultID = "default"
	limitID   = "_limit"

	kibibyte = 1024
)

// Entry is one theme in the list: valid with its manifest, or invalid with the reason.
type Entry struct {
	// ID is the folder name; JSON clips it to 40 UTF-16 units, as an invalid entry's id is.
	ID     string
	Source theme.Source
	HasCSS bool
	// CSSSha256 is the hex sha256 of the stylesheet as served, or "" (null) when there is none.
	CSSSha256 string
	Manifest  *theme.Manifest
	Invalid   string
}

// MarshalJSON writes {id, source, hasCss, cssSha256, manifest | invalid}.
func (e Entry) MarshalJSON() ([]byte, error) {
	var sha any
	if e.CSSSha256 != "" {
		sha = e.CSSSha256
	}

	object := js.NewObject()
	object.Set("id", clip(e.ID))
	object.Set("source", string(e.Source))
	object.Set("hasCss", e.HasCSS)
	object.Set("cssSha256", sha)

	if e.Manifest != nil {
		object.Set("manifest", e.Manifest)
	} else {
		object.Set("invalid", e.Invalid)
	}

	return []byte(js.Stringify(object)), nil
}

// clip is an id as JSON: a name over 40 UTF-16 units is cut and marked, exactly as
// s.slice(0, 40) + "..." would be.
type clip string

func (c clip) MarshalJSON() ([]byte, error) {
	return []byte(js.QuoteClip(string(c), shownUnitsMax)), nil
}

// show quotes a name for a message, clipped.
func show(name string) string { return js.QuoteClip(name, shownUnitsMax) }

// Base is what every theme is judged against: the built-in ids a user folder may not reuse, and
// Default, which every theme's missing slots read from.
type Base struct {
	Folders  []Folder
	IDs      map[string]bool
	Fallback theme.Manifest
}

// BaseOf reads the built-in shelf's ids and Default. Without a usable Default nothing can be
// contrast-checked, so everything lists invalid rather than guessed at.
func BaseOf(builtin Shelf) Base {
	folders := []Folder{}
	ids := map[string]bool{}

	for _, folder := range builtin.Folders() {
		if folder.Link {
			continue
		}

		folders = append(folders, folder)

		// A built-in folder whose name is not an id is listed invalid, and is no id a user
		// folder is kept from.
		if folder.IsID {
			ids[folder.Name] = true
		}
	}

	fallback := theme.Manifest{
		ID: defaultID, Modes: []string{}, Accent: theme.Accent{Kind: theme.AccentFixed},
		Fonts: []string{}, Tokens: []theme.ModeTokens{}, Effects: []theme.Effect{},
	}

	data, why := builtin.Read(defaultID, ManifestFile, theme.ManifestBytesMax)
	if why == Read {
		context := theme.Context{Source: theme.SourceBuiltin, BuiltinIDs: ids}

		parsed, problems := theme.ParseManifest(js.DecodeUTF8(data, false), context)
		if len(problems) == 0 {
			fallback = parsed
		}
	}

	return Base{Folders: folders, IDs: ids, Fallback: fallback}
}

// Library is the theme list over the built-in shelf and the user's folder.
type Library struct {
	builtin Shelf
	user    Shelf
	logger  *log.Logger

	guard  sync.Mutex
	logged map[string]bool
}

// New makes the list over the built-in shelf and the user's shelf. Problems nobody planned for
// go to logger, each line once.
func New(builtin, user Shelf, logger *log.Logger) *Library {
	return &Library{
		builtin: builtin,
		user:    user,
		logger:  logger,
		guard:   sync.Mutex{},
		logged:  map[string]bool{},
	}
}

// List is the built-in themes, then the user's, each sorted by id. A user folder past
// theme.UserThemesMax is left out, and one entry says how many were.
func (l *Library) List() []Entry {
	base := BaseOf(l.builtin)
	entries := []Entry{}

	for _, folder := range base.Folders {
		loaded := l.load(l.builtin, folder.Name, theme.SourceBuiltin, base)
		if loaded.entry.Invalid != "" {
			l.logOnce("built-in theme " + show(folder.Name) + " is invalid: " +
				loaded.entry.Invalid)
		}

		entries = append(entries, loaded.entry)
	}

	kept, dropped := userFolders(l.user)
	for _, folder := range kept {
		entries = append(entries, l.load(l.user, folder.Name, theme.SourceUser, base).entry)
	}

	if dropped > 0 {
		// `_limit` cannot be a folder (those are skipped), so the note never shadows a theme.
		plural := "s"
		if dropped == 1 {
			plural = ""
		}

		entries = append(entries, Entry{
			ID: limitID, Source: theme.SourceUser,
			Invalid: "limit of " + strconv.Itoa(theme.UserThemesMax) + " user themes reached, " +
				strconv.Itoa(dropped) + " folder" + plural + " not listed",
		})
	}

	return entries
}

// UserFolderCount is how many user folders the list counts toward its limit: an install of a new
// id at the limit would never be listed.
func UserFolderCount(user Shelf) int { return len(user.Folders()) }

func userFolders(user Shelf) ([]Folder, int) {
	folders := user.Folders()
	if len(folders) <= theme.UserThemesMax {
		return folders, 0
	}

	return folders[:theme.UserThemesMax], len(folders) - theme.UserThemesMax
}

// CSS is a valid theme's own stylesheet: a built-in's, or a user folder's without a
// .source.json. A user theme's is served only when version is the sha256 of the bytes on disk
// now: consent was given to that hash, and a file edited after the list was read must not ride
// on it. A built-in's ships with Conn, so its version is only a cache buster.
func (l *Library) CSS(themeID, version string) (string, bool) {
	if !IsFolderName(themeID) {
		return "", false
	}

	base := BaseOf(l.builtin)

	// A built-in id is only ever read from the built-in shelf: a user folder of that name never
	// gets a look.
	if base.IDs[themeID] {
		loaded := l.load(l.builtin, themeID, theme.SourceBuiltin, base)

		return loaded.css, served(loaded.entry)
	}

	kept, _ := userFolders(l.user)

	listed := false

	for _, folder := range kept {
		if folder.Name == themeID {
			listed = true
		}
	}

	if !listed {
		return "", false
	}

	loaded := l.load(l.user, themeID, theme.SourceUser, base)
	if loaded.entry.Source != theme.SourceUser {
		return "", false
	}

	if version == "" {
		return "", false
	}

	if version != loaded.entry.CSSSha256 {
		return "", false
	}

	return loaded.css, served(loaded.entry)
}

func (l *Library) logOnce(line string) {
	l.guard.Lock()
	defer l.guard.Unlock()

	if l.logged[line] {
		return
	}

	l.logged[line] = true
	l.logger.Print(line)
}

func served(entry Entry) bool {
	if entry.Invalid != "" {
		return false
	}

	return entry.HasCSS
}

// loaded is one folder judged, with its stylesheet's text when it is served-safe.
type loaded struct {
	entry Entry
	css   string
}

// load judges one folder. An invalid entry's id is the folder name; a valid one equals a
// manifest id, at most 40 characters anyway.
func (l *Library) load(shelf Shelf, name string, source theme.Source, base Base) loaded {
	// A problem nobody planned for leaves this fixed reason: its own text can carry a path, so
	// the list gets this and the log the detail.
	result := invalid(name, source, "unreadable theme folder")

	l.guarded(source, name, func() { result = l.judge(shelf, name, source, base) })

	return result
}

// guarded runs judge, logging a panic in it once instead of letting it stop the list.
func (l *Library) guarded(source theme.Source, name string, judge func()) {
	defer func() {
		recovered := recover()
		if recovered != nil {
			l.logOnce(fmt.Sprintf("%s theme folder %s could not be read: %v", source, show(name),
				recovered))
		}
	}()

	judge()
}

func invalid(name string, source theme.Source, reason string) loaded {
	return loaded{entry: Entry{ID: name, Source: source, Invalid: reason}, css: ""}
}

func (l *Library) judge(shelf Shelf, name string, source theme.Source, base Base) loaded {
	// Checked before any path is built: an id has no separator and no dot, so the path stays
	// inside the shelf.
	if !IsFolderName(name) {
		return invalid(name, source, "folder name must be a-z, 0-9 and -")
	}

	problem := shelf.FolderProblem(name)
	if problem != "" {
		return invalid(name, source, problem)
	}

	gallery := false

	switch shelf.Marker(name) {
	case MarkerNotFile:
		return invalid(name, source, ".source.json is not a regular file")
	case MarkerUnreadable:
		return invalid(name, source, ".source.json could not be read")
	case MarkerPresent:
		gallery = true
	case MarkerAbsent:
	}

	kind := source
	if source != theme.SourceBuiltin {
		if gallery {
			kind = theme.SourceGallery
		}

		if base.IDs[name] {
			return invalid(name, kind, "id "+show(name)+" is a built-in theme, a user folder "+
				"cannot reuse it")
		}
	}

	return judgeFiles(shelf, name, kind, gallery, base)
}

func judgeFiles(shelf Shelf, name string, kind theme.Source, gallery bool, base Base) loaded {
	data, why := shelf.Read(name, ManifestFile, theme.ManifestBytesMax)
	if why != Read {
		return invalid(name, kind, manifestProblem(why))
	}

	context := theme.Context{Source: kind, BuiltinIDs: base.IDs}

	manifest, problems := theme.CheckTheme(js.DecodeUTF8(data, false), context, &base.Fallback)
	if len(problems) > 0 {
		return invalid(name, kind, joinProblems(problems))
	}

	if manifest.ID != name {
		return invalid(name, kind, "folder "+show(name)+" does not match the manifest id "+
			show(manifest.ID))
	}

	entry := Entry{ID: name, Source: kind, Manifest: &manifest}
	if gallery {
		return loaded{entry: entry, css: ""}
	}

	style, why := shelf.Read(name, StyleFile, theme.CSSBytesMax)

	switch why {
	case Read:
		// The hash is of the text as served, so a browser that remembers it sees what it
		// accepted.
		css := js.DecodeUTF8(style, false)
		sum := sha256.Sum256([]byte(css))
		entry.HasCSS = true
		entry.CSSSha256 = hex.EncodeToString(sum[:])

		return loaded{entry: entry, css: css}
	case Missing:
		return loaded{entry: entry, css: ""}
	case TooBig:
		return invalid(name, kind, "theme.css is larger than "+kb(theme.CSSBytesMax))
	case NotFile:
		return invalid(name, kind, "theme.css is not a regular file, so it was not read")
	case Unreadable:
	}

	return invalid(name, kind, "theme.css could not be read")
}

func manifestProblem(why Why) string {
	switch why {
	case Missing:
		return "folder has no theme.json"
	case TooBig:
		return "theme.json is larger than " + kb(theme.ManifestBytesMax)
	case NotFile:
		return "theme.json is not a regular file, so it was not read"
	case Read, Unreadable:
	}

	return "theme.json could not be read"
}

func kb(bytes int) string { return strconv.Itoa(bytes/kibibyte) + " KB" }

func joinProblems(problems []string) string { return strings.Join(problems, "; ") }
