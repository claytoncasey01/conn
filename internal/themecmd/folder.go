// Package themecmd is `conn theme check` and `conn theme index`: the judgment the server passes
// on a theme folder, run by hand, and the builder of what the conn-themes gallery repo serves.
package themecmd

import (
	"bytes"
	"io"
	"regexp"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/claytoncasey01/conn/internal/gallery"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/theme"
	"github.com/claytoncasey01/conn/internal/themes"
)

const (
	// shownUnitsMax is how much of a name from the disk a line quotes.
	shownUnitsMax = 40
	kibibyte      = 1024
)

// plainName is a name from the disk shown as it is: anything else is quoted, so it cannot start a
// line of its own.
var plainName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,40}$`)

// quoted is a name from the disk as a line shows it.
func quoted(name string) string {
	if plainName.MatchString(name) {
		return name
	}

	return js.QuoteClip(name, shownUnitsMax)
}

func kb(size int) string { return strconv.Itoa(size/kibibyte) + " KB" }

// IO is where the commands print and what they read.
type IO struct {
	Out io.Writer
	Err io.Writer
	// Builtin is the built-in themes, and BuiltinDir the folder on disk they are read from (""
	// when they are the ones conn carries): a folder given by path there is judged as a
	// built-in.
	Builtin    themes.Shelf
	BuiltinDir string
	// User is the user's theme folder ($CONN_DATA/themes).
	User string
	// Write writes a file; nil is the disk. Only a test sets it, to break one write.
	Write func(path string, data []byte) error
}

func (o IO) out(line string) { _, _ = io.WriteString(o.Out, line+"\n") }

func (o IO) err(line string) { _, _ = io.WriteString(o.Err, line+"\n") }

// checked is a gallery folder judged: its manifest and the bytes that were hashed, or its
// problems, one line each without the folder's name.
type checked struct {
	manifest theme.Manifest
	bytes    []byte
	problems []string
}

// checkGalleryFolder judges a folder as a gallery theme: an id for a name, a real folder holding
// theme.json alone, a regular UTF-8 file with no byte order mark under the size cap that passes
// the theme check as a gallery theme, and an id equal to the folder's name. `conn theme index`
// and `conn theme check --gallery` both call it, so a folder one passes the other passes; the
// gallery install applies the same rules to the downloaded text.
func checkGalleryFolder(shelf themes.Shelf, name string, base themes.Base) checked {
	bad := func(problems ...string) checked { return checked{problems: problems} }

	if !gallery.IsID(name) {
		return bad("folder name must be 1 to 40 characters of a-z, 0-9 and -, not starting with -")
	}

	problem := shelf.FolderProblem(name)
	if problem != "" {
		return bad(problem)
	}

	extra := extraFiles(shelf, name)
	if len(extra) > 0 {
		return bad(extra...)
	}

	data, why := shelf.Read(name, themes.ManifestFile, theme.ManifestBytesMax)
	if why != themes.Read {
		return bad(galleryManifestProblem(why))
	}

	problem = encodingProblem(data)
	if problem != "" {
		return bad(problem)
	}

	context := theme.Context{Source: theme.SourceGallery, BuiltinIDs: base.IDs}

	manifest, problems := theme.CheckTheme(string(data), context, &base.Fallback)
	if len(problems) > 0 {
		return bad(problems...)
	}

	if manifest.ID != name {
		return bad("folder " + js.Stringify(name) + " does not match the manifest id " +
			js.Stringify(manifest.ID))
	}

	return checked{manifest: manifest, bytes: data, problems: nil}
}

// extraFiles are the problems of everything in the folder but theme.json: a stylesheet, a
// .source.json or an editor's litter.
func extraFiles(shelf themes.Shelf, name string) []string {
	entries, err := shelf.Entries(name)
	if err != nil {
		return []string{"folder could not be read"}
	}

	slices.Sort(entries)

	extra := []string{}

	for _, entry := range entries {
		if entry != themes.ManifestFile {
			extra = append(extra, quoted(entry)+
				" is not allowed (a gallery theme is theme.json alone)")
		}
	}

	return extra
}

// encodingProblem: the app hashes the text it decoded, so the file must be valid UTF-8 with no
// byte order mark; only then is decode-then-encode the identity.
func encodingProblem(data []byte) string {
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		return "theme.json starts with a byte order mark; save it as UTF-8 without one"
	}

	if !utf8.Valid(data) {
		return "theme.json is not valid UTF-8"
	}

	return ""
}

func galleryManifestProblem(why themes.Why) string {
	switch why {
	case themes.Missing:
		return "theme.json is missing"
	case themes.TooBig:
		return "theme.json is larger than " + kb(theme.ManifestBytesMax)
	case themes.NotFile:
		return "theme.json is not a regular file, so it was not read"
	case themes.Read, themes.Unreadable:
	}

	return "theme.json could not be read"
}
