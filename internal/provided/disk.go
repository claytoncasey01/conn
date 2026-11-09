package provided

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The modes of what is made: the data folder is the user's alone, and Claude's folders are made
// as a person would make them.
const (
	dataDirMode   = 0o700
	claudeDirMode = 0o755
	fileMode      = 0o600
	scriptMode    = 0o700

	// maxLinks is the most entries of one Claude folder looked at for a stale link.
	maxLinks = 4096

	scriptStart = "#!"
	tempPrefix  = ".conn-tmp-"
)

// Look is what is at a link's place, without following a link.
func Look(link string) (Found, error) {
	info, err := os.Lstat(link)
	if errors.Is(err, fs.ErrNotExist) {
		return Found{}, nil
	}

	if err != nil {
		return Found{}, fmt.Errorf("looking at %s: %w", link, err)
	}

	if info.Mode()&fs.ModeSymlink == 0 {
		return Found{Exists: true}, nil
	}

	target, err := os.Readlink(link)
	if err != nil {
		return Found{}, fmt.Errorf("reading the link %s: %w", link, err)
	}

	return Found{Exists: true, IsLink: true, Target: target}, nil
}

// See is the state of each item's place.
func See(layout Layout, items []Item) ([]Seen, error) {
	seen := make([]Seen, 0, len(items))

	for _, item := range items {
		found, err := Look(layout.Link(item))
		if err != nil {
			return nil, err
		}

		seen = append(seen, Seen{Item: item, State: Judge(found, layout.Copy(item))})
	}

	return seen, nil
}

// WriteCopies makes the data folder's copy of each item what the binary carries. A copy that is
// the same already is left alone; one that differs is written beside it and renamed over, so a
// session reading an agent never reads half of it. A skill's folder is made whole beside the old
// one and swapped in, so a file the skill no longer has goes too.
func WriteCopies(layout Layout, items []Item) error {
	for _, item := range items {
		same, err := copyIsCurrent(layout, item)
		if err != nil {
			return err
		}

		if same {
			continue
		}

		err = writeCopy(layout, item)
		if err != nil {
			return fmt.Errorf("copying %s %s: %w", item.Kind, item.Name, err)
		}
	}

	return nil
}

// copyFilePath is where a file of the item's copy is: an agent's copy is the one file, a skill's
// is a folder holding the file at its path.
func copyFilePath(item Item, root string, file File) string {
	if item.Kind == KindAgent {
		return root
	}

	return filepath.Join(root, filepath.FromSlash(file.Path))
}

func copyIsCurrent(layout Layout, item Item) (bool, error) {
	root := layout.Copy(item)

	for _, file := range item.Files {
		have, err := os.ReadFile(copyFilePath(item, root, file))
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}

		if err != nil {
			return false, fmt.Errorf("reading %s: %w", root, err)
		}

		if !bytes.Equal(have, file.Data) {
			return false, nil
		}
	}

	if item.Kind == KindAgent {
		return true, nil
	}

	count, err := countFiles(root)
	if err != nil {
		return false, err
	}

	return count == len(item.Files), nil
}

// countFiles is how many files are under root, at most maxFilesPerSkill+1 of them looked at.
func countFiles(root string) (int, error) {
	count := 0

	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() {
			return nil
		}

		count++
		if count > maxFilesPerSkill {
			return fs.SkipAll
		}

		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("counting the files in %s: %w", root, err)
	}

	return count, nil
}

func writeCopy(layout Layout, item Item) error {
	folder := layout.CopiesDir(item.Kind)

	err := os.MkdirAll(folder, dataDirMode)
	if err != nil {
		return fmt.Errorf("making %s: %w", folder, err)
	}

	temp, err := os.MkdirTemp(folder, tempPrefix)
	if err != nil {
		return fmt.Errorf("making a temp folder in %s: %w", folder, err)
	}

	defer func() { _ = os.RemoveAll(temp) }()

	for _, file := range item.Files {
		err = writeFile(filepath.Join(temp, filepath.FromSlash(file.Path)), file.Data)
		if err != nil {
			return err
		}
	}

	return swapIn(temp, item, layout.Copy(item))
}

// swapIn puts what was written to temp where the copy goes. An agent is one file renamed over
// the old; a skill's folder is renamed over a removed one.
func swapIn(temp string, item Item, copyPath string) error {
	if item.Kind == KindAgent {
		err := os.Rename(filepath.Join(temp, item.Files[0].Path), copyPath)
		if err != nil {
			return fmt.Errorf("replacing %s: %w", copyPath, err)
		}

		return nil
	}

	err := os.RemoveAll(copyPath)
	if err != nil {
		return fmt.Errorf("removing the old %s: %w", copyPath, err)
	}

	err = os.Rename(temp, copyPath)
	if err != nil {
		return fmt.Errorf("replacing %s: %w", copyPath, err)
	}

	return nil
}

func writeFile(path string, data []byte) error {
	err := os.MkdirAll(filepath.Dir(path), dataDirMode)
	if err != nil {
		return fmt.Errorf("making %s: %w", filepath.Dir(path), err)
	}

	mode := os.FileMode(fileMode)
	if strings.HasPrefix(string(data), scriptStart) {
		mode = scriptMode
	}

	err = os.WriteFile(path, data, mode)
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

// MakeLink links the item's place in Claude's folder to its copy. The caller has seen the place
// empty: a link that appeared since makes this fail rather than replace it.
func MakeLink(layout Layout, item Item) error {
	folder := layout.LinksDir(item.Kind)

	err := os.MkdirAll(folder, claudeDirMode)
	if err != nil {
		return fmt.Errorf("making %s: %w", folder, err)
	}

	err = os.Symlink(layout.Copy(item), layout.Link(item))
	if err != nil {
		return fmt.Errorf("linking %s: %w", layout.Link(item), err)
	}

	return nil
}

// RemoveLink removes the item's link. The caller has seen it is Conn's.
func RemoveLink(layout Layout, item Item) error {
	err := os.Remove(layout.Link(item))
	if err != nil {
		return fmt.Errorf("removing %s: %w", layout.Link(item), err)
	}

	return nil
}

// LinksIn are the links in Claude's folder for a kind: at most maxLinks entries are looked at.
func LinksIn(layout Layout, kind Kind) ([]LinkEntry, error) {
	folder := layout.LinksDir(kind)

	entries, err := os.ReadDir(folder)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", folder, err)
	}

	links := []LinkEntry{}

	for index, entry := range entries {
		if index == maxLinks {
			break
		}

		if entry.Type()&fs.ModeSymlink == 0 {
			continue
		}

		path := filepath.Join(folder, entry.Name())

		target, err := os.Readlink(path)
		if err != nil {
			return nil, fmt.Errorf("reading the link %s: %w", path, err)
		}

		links = append(links, LinkEntry{Path: path, Name: entry.Name(), Target: target})
	}

	return links, nil
}
