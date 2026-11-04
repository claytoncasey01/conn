package provided

import (
	"path/filepath"
	"strings"
)

// State is what is at the place an item's link goes.
type State string

// The states.
const (
	// StateOff is that nothing is there.
	StateOff State = "notInstalled"
	// StateOn is a link to this item's copy in the data folder: Conn's.
	StateOn State = "installed"
	// StateConflict is anything else (a file, a folder, a link to somewhere else): never replaced.
	StateConflict State = "conflict"
)

// Layout is where the copies and the links are.
type Layout struct {
	// Data is the data folder: the copies are in its agents and skills folders.
	Data string
	// Claude is Claude Code's folder: the links are in its agents and skills folders.
	Claude string
}

// Copy is where the item's copy is: a file for an agent, a folder for a skill.
func (l Layout) Copy(item Item) string {
	return filepath.Join(l.CopiesDir(item.Kind), copyName(item))
}

// Link is where the item's link is, and what it points at is Copy.
func (l Layout) Link(item Item) string {
	return filepath.Join(l.LinksDir(item.Kind), copyName(item))
}

// CopiesDir is the data folder's folder for a kind.
func (l Layout) CopiesDir(kind Kind) string { return filepath.Join(l.Data, string(kind)) }

// LinksDir is Claude's folder for a kind.
func (l Layout) LinksDir(kind Kind) string { return filepath.Join(l.Claude, string(kind)) }

func copyName(item Item) string {
	if item.Kind == KindAgent {
		return item.Name + agentSuffix
	}

	return item.Name
}

// Found is what a look at a link's place found.
type Found struct {
	Exists bool
	IsLink bool
	// Target is where the link points, as written, when it is one.
	Target string
}

// Judge is the state of a place that holds found, for a link that should point at want.
func Judge(found Found, want string) State {
	if !found.Exists {
		return StateOff
	}

	if !found.IsLink {
		return StateConflict
	}

	if filepath.Clean(found.Target) != filepath.Clean(want) {
		return StateConflict
	}

	return StateOn
}

// Describe says in words what is at a place that is in conflict, for the page.
func Describe(found Found) string {
	if found.IsLink {
		return "a link to " + found.Target
	}

	return "a file or folder that is not Conn's"
}

// Inside is whether target is in folder: how a link of Conn's is told from the user's.
func Inside(folder, target string) bool {
	relative, err := filepath.Rel(folder, target)
	if err != nil {
		return false
	}

	if relative == "." {
		return false
	}

	if relative == ".." {
		return false
	}

	return !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
