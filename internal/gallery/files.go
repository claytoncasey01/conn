package gallery

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/themes"
)

// An installed theme gets the modes a hand-made one gets: a folder and files made as mkdir and a
// plain write make them, the umask taking its share, not a private 0700 temp folder renamed into
// place.
const (
	handMadeFolderMode = 0o777
	handMadeFileMode   = 0o666

	tempRandomBytes  = 6
	asideRandomBytes = 4
)

// Files are the folder changes an install and an uninstall make, so a test can fail one.
type Files interface {
	// MkdirAll makes a folder and any missing above it.
	MkdirAll(path string) error
	// MkdirTemp makes a new folder named prefix and random hex; a taken name fails.
	MkdirTemp(prefix string) (string, error)
	// WriteNew writes a file that must not exist yet.
	WriteNew(path, text string) error
	Rename(from, to string) error
	// RemoveAll removes a folder and everything in it; a missing one is no error.
	RemoveAll(path string) error
	Lstat(path string) (fs.FileInfo, error)
}

// Disk is the file system.
func Disk() DiskFiles { return DiskFiles{} }

// DiskFiles are Files on disk.
type DiskFiles struct{}

// MkdirAll makes a folder and any missing above it.
func (DiskFiles) MkdirAll(path string) error {
	err := os.MkdirAll(path, handMadeFolderMode)
	if err != nil {
		return fmt.Errorf("making a theme folder: %w", err)
	}

	return nil
}

// MkdirTemp makes a new folder named prefix and random hex.
func (DiskFiles) MkdirTemp(prefix string) (string, error) {
	path := prefix + randomHex(tempRandomBytes)

	err := os.Mkdir(path, handMadeFolderMode)
	if err != nil {
		return "", fmt.Errorf("making a temp theme folder: %w", err)
	}

	return path, nil
}

// WriteNew writes a file that must not exist yet.
func (DiskFiles) WriteNew(path, text string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, handMadeFileMode)
	if err != nil {
		return fmt.Errorf("writing a theme file: %w", err)
	}

	_, err = file.WriteString(text)
	closeErr := file.Close()

	if err != nil {
		return fmt.Errorf("writing a theme file: %w", err)
	}

	if closeErr != nil {
		return fmt.Errorf("writing a theme file: %w", closeErr)
	}

	return nil
}

// Rename moves a folder.
func (DiskFiles) Rename(from, to string) error {
	err := os.Rename(from, to)
	if err != nil {
		return fmt.Errorf("moving a theme folder: %w", err)
	}

	return nil
}

// RemoveAll removes a folder and everything in it.
func (DiskFiles) RemoveAll(path string) error {
	err := os.RemoveAll(path)
	if err != nil {
		return fmt.Errorf("removing a theme folder: %w", err)
	}

	return nil
}

// Lstat looks at a path without following a link.
func (DiskFiles) Lstat(path string) (fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("looking at a theme folder: %w", err)
	}

	return info, nil
}

func randomHex(size int) string {
	random := make([]byte, size)
	_, _ = rand.Read(random)

	return hex.EncodeToString(random)
}

// source is the .source.json an install writes beside theme.json.
func source(repo, commit string, installedAt int64, sha string) string {
	files := js.NewObject()
	files.Set(themes.ManifestFile, sha)

	object := js.NewObject()
	object.Set("repo", repo)
	object.Set("commit", commit)
	object.Set("installedAt", installedAt)
	object.Set("files", files)

	return js.StringifyIndent(object, "  ") + "\n"
}

// write puts the theme in place: a temp folder holding theme.json and .source.json, renamed in.
// An update moves the old version aside first, so it comes back if the new one cannot be put in
// place.
func (g *Gallery) write(target string, entry Entry, text string, updating bool) Result {
	files := g.deps.Files
	themeID := entry.ID
	marker := source(g.deps.Repo, entry.Commit, g.deps.Now(), entry.Sha256)

	err := files.MkdirAll(g.deps.User)
	if err != nil {
		return refuse(http.StatusInternalServerError,
			"The theme could not be written, so nothing was changed")
	}

	temp, err := files.MkdirTemp(filepath.Join(g.deps.User, ".tmp-"+themeID+"-"))
	if err != nil {
		return refuse(http.StatusInternalServerError,
			"The theme could not be written, so nothing was changed")
	}

	err = fill(files, temp, text, marker)
	if err == nil {
		return g.place(target, temp, entry, updating)
	}

	_ = files.RemoveAll(temp)

	return refuse(http.StatusInternalServerError,
		"The theme could not be written, so nothing was changed")
}

func fill(files Files, temp, text, marker string) error {
	err := files.WriteNew(filepath.Join(temp, themes.ManifestFile), text)
	if err != nil {
		return fmt.Errorf("writing theme.json: %w", err)
	}

	err = files.WriteNew(filepath.Join(temp, themes.SourceFile), marker)
	if err != nil {
		return fmt.Errorf("writing .source.json: %w", err)
	}

	return nil
}

func (g *Gallery) place(target, temp string, entry Entry, updating bool) Result {
	files := g.deps.Files
	written := Result{
		Code: http.StatusOK, Body: done{OK: true, ID: entry.ID, Version: entry.Version},
	}

	if !updating {
		err := files.Rename(temp, target)
		if err != nil {
			_ = files.RemoveAll(temp)

			return refuse(http.StatusInternalServerError,
				"The theme could not be written, so nothing was changed")
		}

		return written
	}

	aside := temp + "-old-" + randomHex(asideRandomBytes)

	err := files.Rename(target, aside)
	if err != nil {
		_ = files.RemoveAll(temp)

		return refuse(http.StatusInternalServerError,
			"The theme could not be written, so nothing was changed")
	}

	err = files.Rename(temp, target)
	if err == nil {
		_ = files.RemoveAll(aside)

		return written
	}

	err = files.Rename(aside, target)

	_ = files.RemoveAll(temp)

	if err != nil {
		// The old version could not be moved back: it is kept, not removed.
		return refuse(http.StatusInternalServerError, entry.ID+" could not be updated, and its "+
			"previous version could not be put back (it is kept in "+filepath.Base(aside)+")")
	}

	return refuse(http.StatusInternalServerError,
		"The theme could not be written, so nothing was changed")
}
