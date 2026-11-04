package provided

import "slices"

// Seen is an item and what the disk showed at its link's place.
type Seen struct {
	Item  Item
	State State
}

// AutoLinks are the agents to link at start: those with nothing in their place. A skill is never
// linked here, and a place in conflict is left as it is.
func AutoLinks(seen []Seen) []Item {
	links := []Item{}

	for _, one := range seen {
		if one.Item.Kind != KindAgent {
			continue
		}

		if one.State != StateOff {
			continue
		}

		links = append(links, one.Item)
	}

	return links
}

// LinkEntry is a link found in Claude's folder for a kind.
type LinkEntry struct {
	// Path is the link.
	Path string
	// Name is the name it has there, such as "search.md" or "deep-code-review".
	Name string
	// Target is where it points, as written.
	Target string
}

// StaleLinks are the links of Conn's own that no item answers for any more: they point into the
// data folder's folder for the kind, at a name the catalog no longer has. A link to anywhere else
// is the user's, and stays.
func StaleLinks(layout Layout, kind Kind, entries []LinkEntry, items []Item) []string {
	kept := []string{}

	for _, item := range items {
		if item.Kind == kind {
			kept = append(kept, copyName(item))
		}
	}

	stale := []string{}

	for _, entry := range entries {
		if !Inside(layout.CopiesDir(kind), entry.Target) {
			continue
		}

		if slices.Contains(kept, entry.Name) {
			continue
		}

		stale = append(stale, entry.Path)
	}

	return stale
}
