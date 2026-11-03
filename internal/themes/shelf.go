// Package themes is the theme list: the built-in themes Conn carries and the user's own
// ($CONN_DATA/themes). A user theme folder is untrusted input: nothing is read before an lstat
// shows a regular file, no link is followed, sizes are checked before reading, and the list never
// fails as a whole.
package themes

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"

	"github.com/claytoncasey01/conn/internal/js"
)

// The files a theme folder holds.
const (
	ManifestFile = "theme.json"
	StyleFile    = "theme.css"
	// SourceFile marks a gallery install; a stylesheet beside it is never served.
	SourceFile = ".source.json"
)

// folderName is a theme folder name that is an id: only such a name is ever joined into a path.
var folderName = regexp.MustCompile(`^[a-z0-9-]+$`)

// IsFolderName reports whether name is a theme folder name Conn reads (a-z, 0-9 and -).
func IsFolderName(name string) bool { return folderName.MatchString(name) }

// Why is why a file could not be read.
type Why int

// The reasons a read fails.
const (
	Read Why = iota
	Missing
	NotFile
	TooBig
	Unreadable
)

// Marker is what a folder's .source.json is.
type Marker int

// The states of a .source.json.
const (
	MarkerAbsent Marker = iota
	MarkerPresent
	MarkerNotFile
	MarkerUnreadable
)

// Folder is one entry of a shelf that may be a theme: a folder, or a link (listed, then refused).
type Folder struct {
	Name string
	Link bool
	// IsID is whether the name is an id: one that is not is listed invalid and never read.
	IsID bool
}

// Shelf is a folder of theme folders. Names passed to it are folder names from Folders, or ids.
type Shelf interface {
	// Folders are the folders and links to them, skipping `_` and dot folders, sorted by name as
	// JavaScript sorts strings. An unreadable shelf has none.
	Folders() []Folder
	// FolderProblem is why the folder itself cannot be read, as it is now (a link or anything
	// but a directory is refused), or "".
	FolderProblem(name string) string
	// Marker is the state of the folder's .source.json.
	Marker(name string) Marker
	// Read is a regular file's bytes, or why not: a link, a directory or a pipe is refused, and a
	// file over bytesMax is never read.
	Read(name, file string, bytesMax int64) ([]byte, Why)
	// Exists is whether anything is at the folder (file "") or at a file in it, a link included.
	Exists(name, file string) bool
	// Entries are the names of everything in the folder, dot files included.
	Entries(name string) ([]string, error)
}

// Disk is a shelf on disk at root.
func Disk(root string) DiskShelf { return DiskShelf{root: root} }

// DiskShelf is a folder of theme folders on disk.
type DiskShelf struct{ root string }

// Folders lists the folders, as Shelf.Folders says.
func (d DiskShelf) Folders() []Folder {
	entries, err := os.ReadDir(d.root)
	if err != nil {
		return nil
	}

	folders := []Folder{}

	for _, entry := range entries {
		kind := entry.Type()
		link := kind&fs.ModeSymlink != 0

		if !link {
			if !entry.IsDir() {
				continue
			}
		}

		folder, kept := folderOf(entry.Name(), link)
		if kept {
			folders = append(folders, folder)
		}
	}

	return sorted(folders)
}

// folderOf is the folder a directory entry is, or false for a `_` or dot folder.
func folderOf(raw string, link bool) (Folder, bool) {
	name := js.DecodeUTF8([]byte(raw), false)
	if strings.HasPrefix(name, "_") {
		return Folder{}, false
	}

	if strings.HasPrefix(name, ".") {
		return Folder{}, false
	}

	// A name that does not survive decoding is never an id, so it is never joined into a path.
	isID := false
	if name == raw {
		isID = IsFolderName(name)
	}

	return Folder{Name: name, Link: link, IsID: isID}, true
}

func sorted(folders []Folder) []Folder {
	slices.SortStableFunc(folders, func(left, right Folder) int {
		return js.CompareUTF16(left.Name, right.Name)
	})

	return folders
}

// FolderProblem is why a folder cannot be read, as Shelf.FolderProblem says.
func (d DiskShelf) FolderProblem(name string) string {
	return FolderProblem(filepath.Join(d.root, name))
}

// FolderProblem is why the folder at dir cannot be read as a theme folder, or "".
func FolderProblem(dir string) string {
	info, err := os.Lstat(dir)
	if err != nil {
		return "folder could not be read"
	}

	if info.Mode()&fs.ModeSymlink != 0 {
		return "folder is a symbolic link, so it was not read"
	}

	if !info.IsDir() {
		return "folder is not a directory"
	}

	return ""
}

// Marker is the folder's .source.json state.
func (d DiskShelf) Marker(name string) Marker {
	info, err := os.Lstat(filepath.Join(d.root, name, SourceFile))
	if errors.Is(err, fs.ErrNotExist) {
		return MarkerAbsent
	}

	if err != nil {
		return MarkerUnreadable
	}

	if !info.Mode().IsRegular() {
		return MarkerNotFile
	}

	return MarkerPresent
}

// Read reads a file in a folder, as Shelf.Read says.
func (d DiskShelf) Read(name, file string, bytesMax int64) ([]byte, Why) {
	return ReadRegular(filepath.Join(d.root, name, file), bytesMax)
}

// Exists is whether anything is at the folder or file.
func (d DiskShelf) Exists(name, file string) bool {
	_, err := os.Lstat(filepath.Join(d.root, name, file))

	return err == nil
}

// Entries are the names in a folder.
func (d DiskShelf) Entries(name string) ([]string, error) {
	file, err := os.Open(filepath.Join(d.root, name))
	if err != nil {
		return nil, fmt.Errorf("opening the theme folder: %w", err)
	}

	defer func() { _ = file.Close() }()

	names, err := file.Readdirnames(-1)
	if err != nil {
		return nil, fmt.Errorf("reading the theme folder: %w", err)
	}

	decoded := make([]string, 0, len(names))
	for _, entry := range names {
		decoded = append(decoded, js.DecodeUTF8([]byte(entry), false))
	}

	return decoded, nil
}

// ReadRegular is a regular file's bytes, or why not: a link, a directory or a pipe is refused,
// and a file over bytesMax is never read.
func ReadRegular(name string, bytesMax int64) ([]byte, Why) {
	info, err := os.Lstat(name)
	if err != nil {
		return nil, failure(err)
	}

	if !info.Mode().IsRegular() {
		return nil, NotFile
	}

	if info.Size() > bytesMax {
		return nil, TooBig
	}

	// O_NOFOLLOW and a second look at the open handle close the gap between the lstat and the
	// read.
	return ReadNoFollow(name, bytesMax)
}

// ReadNoFollow opens and reads one file without following a link. O_NONBLOCK keeps the open
// from waiting forever if a pipe took the file's place after the lstat; the fstat then refuses
// it.
func ReadNoFollow(name string, bytesMax int64) ([]byte, Why) {
	file, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, failure(err)
	}

	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, Unreadable
	}

	if !info.Mode().IsRegular() {
		return nil, NotFile
	}

	if info.Size() > bytesMax {
		return nil, TooBig
	}

	data, err := io.ReadAll(io.LimitReader(file, bytesMax+1))
	if err != nil {
		return nil, Unreadable
	}

	if int64(len(data)) > bytesMax {
		return nil, TooBig
	}

	return data, Read
}

func failure(err error) Why {
	if errors.Is(err, fs.ErrNotExist) {
		return Missing
	}

	return Unreadable
}

// Embedded is a shelf in a file system the binary carries: the built-in themes.
func Embedded(fsys fs.FS) EmbeddedShelf { return EmbeddedShelf{fsys: fsys} }

// EmbeddedShelf is a folder of theme folders in a file system with no links.
type EmbeddedShelf struct{ fsys fs.FS }

// Folders lists the folders, as Shelf.Folders says.
func (e EmbeddedShelf) Folders() []Folder {
	entries, err := fs.ReadDir(e.fsys, ".")
	if err != nil {
		return nil
	}

	folders := []Folder{}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		folder, kept := folderOf(entry.Name(), false)
		if kept {
			folders = append(folders, folder)
		}
	}

	return sorted(folders)
}

// FolderProblem is why a folder cannot be read, as Shelf.FolderProblem says.
func (e EmbeddedShelf) FolderProblem(name string) string {
	info, err := fs.Stat(e.fsys, name)
	if err != nil {
		return "folder could not be read"
	}

	if !info.IsDir() {
		return "folder is not a directory"
	}

	return ""
}

// Marker is the folder's .source.json state.
func (e EmbeddedShelf) Marker(name string) Marker {
	_, err := fs.Stat(e.fsys, path.Join(name, SourceFile))
	if err != nil {
		return MarkerAbsent
	}

	return MarkerPresent
}

// Read reads a file in a folder, as Shelf.Read says.
func (e EmbeddedShelf) Read(name, file string, bytesMax int64) ([]byte, Why) {
	inside := path.Join(name, file)

	info, err := fs.Stat(e.fsys, inside)
	if err != nil {
		return nil, failure(err)
	}

	if !info.Mode().IsRegular() {
		return nil, NotFile
	}

	if info.Size() > bytesMax {
		return nil, TooBig
	}

	data, err := fs.ReadFile(e.fsys, inside)
	if err != nil {
		return nil, Unreadable
	}

	return data, Read
}

// Exists is whether anything is inside the folder or file.
func (e EmbeddedShelf) Exists(name, file string) bool {
	_, err := fs.Stat(e.fsys, path.Join(name, file))

	return err == nil
}

// Entries are the names in a folder.
func (e EmbeddedShelf) Entries(name string) ([]string, error) {
	entries, err := fs.ReadDir(e.fsys, name)
	if err != nil {
		return nil, fmt.Errorf("reading the theme folder: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names, nil
}
