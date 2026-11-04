// Package provided is the agents and skills Conn carries in its binary. It copies them into the
// data folder and links them from Claude Code's folder: an agent when the server starts, a skill
// only when the user presses INSTALL. The state of a link is read from the disk each time, never
// kept: a link is Conn's only if it points at Conn's own copy.
package provided

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
)

// The limits on what is provided: what a catalog may hold and what is read from it.
const (
	itemsMax         = 64
	filesPerSkillMax = 64
	fileBytesMax     = 256 << 10

	agentSuffix = ".md"
	skillFile   = "SKILL.md"
)

// Kind is an agent or a skill.
type Kind string

// The kinds of item. Their values are folder names under the data folder and Claude's folder.
const (
	KindAgent Kind = "agents"
	KindSkill Kind = "skills"
)

var (
	// errCatalog is a provided item that cannot be used: a program bug, found by a test.
	errCatalog = errors.New("the provided files are not usable")

	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
)

// File is one file of an item, read whole.
type File struct {
	// Path is relative to the item's folder, slash-separated.
	Path string
	Data []byte
}

// Item is one provided agent or skill: its name and every file.
type Item struct {
	Kind  Kind
	Name  string
	Files []File
}

// ReadCatalog reads the agents (each a .md file at the root of agents) and the skills (each a
// folder with a SKILL.md at the root of skills). A skills entry that is not such a folder, such
// as the README, is not an item. An item that breaks a limit or has an unusable name is an
// error: it names the one, and nothing is half-read.
func ReadCatalog(agents, skills fs.FS) ([]Item, error) {
	found, err := readAgents(agents)
	if err != nil {
		return nil, err
	}

	more, err := readSkills(skills)
	if err != nil {
		return nil, err
	}

	return append(found, more...), nil
}

func readAgents(agents fs.FS) ([]Item, error) {
	entries, err := fs.ReadDir(agents, ".")
	if err != nil {
		return nil, fmt.Errorf("reading the provided agents: %w", err)
	}

	items := []Item{}

	for _, entry := range entries {
		name, isAgent := strings.CutSuffix(entry.Name(), agentSuffix)
		if !isAgent {
			continue
		}

		if len(items) == itemsMax {
			return nil, fmt.Errorf("%w: more than %d agents", errCatalog, itemsMax)
		}

		file, err := readFile(agents, entry.Name(), entry.Name())
		if err != nil {
			return nil, err
		}

		err = checkName(name)
		if err != nil {
			return nil, err
		}

		items = append(items, Item{Kind: KindAgent, Name: name, Files: []File{file}})
	}

	return items, nil
}

func readSkills(skills fs.FS) ([]Item, error) {
	entries, err := fs.ReadDir(skills, ".")
	if err != nil {
		return nil, fmt.Errorf("reading the provided skills: %w", err)
	}

	items := []Item{}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		_, err := fs.Stat(skills, path.Join(entry.Name(), skillFile))
		if err != nil {
			continue
		}

		if len(items) == itemsMax {
			return nil, fmt.Errorf("%w: more than %d skills", errCatalog, itemsMax)
		}

		item, err := readSkill(skills, entry.Name())
		if err != nil {
			return nil, err
		}

		items = append(items, item)
	}

	return items, nil
}

func readSkill(skills fs.FS, name string) (Item, error) {
	err := checkName(name)
	if err != nil {
		return Item{}, err
	}

	files := []File{}

	err = fs.WalkDir(skills, name, func(entry string, dir fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if dir.IsDir() {
			return nil
		}

		if len(files) == filesPerSkillMax {
			return fmt.Errorf("%w: skill %s has more than %d files", errCatalog, name,
				filesPerSkillMax)
		}

		relative := strings.TrimPrefix(entry, name+"/")

		file, readErr := readFile(skills, entry, relative)
		if readErr != nil {
			return readErr
		}

		files = append(files, file)

		return nil
	})
	if err != nil {
		return Item{}, fmt.Errorf("reading skill %s: %w", name, err)
	}

	return Item{Kind: KindSkill, Name: name, Files: files}, nil
}

func readFile(fsys fs.FS, name, relative string) (File, error) {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return File{}, fmt.Errorf("reading %s: %w", name, err)
	}

	if info.Size() > fileBytesMax {
		return File{}, fmt.Errorf("%w: %s is larger than %d bytes", errCatalog, name, fileBytesMax)
	}

	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return File{}, fmt.Errorf("reading %s: %w", name, err)
	}

	return File{Path: relative, Data: data}, nil
}

func checkName(name string) error {
	if namePattern.MatchString(name) {
		return nil
	}

	return fmt.Errorf("%w: %q is not a name (lowercase letters, digits, hyphens)", errCatalog, name)
}

// Find is the item of a kind with a name.
func Find(items []Item, kind Kind, name string) (Item, bool) {
	index := slices.IndexFunc(items, func(item Item) bool {
		return item.Kind == kind && item.Name == name
	})
	if index < 0 {
		return Item{}, false
	}

	return items[index], true
}
