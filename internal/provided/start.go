package provided

import (
	"errors"
	"fmt"
	"os"
)

// Report is what Start did, for the log.
type Report struct {
	// Linked are the agents it linked.
	Linked []string
	// Conflicts say what stopped an agent being linked: something of the user's in its place.
	Conflicts []string
	// Removed are the links of Conn's own it removed, because their item is no longer provided.
	Removed []string
}

// Start brings the data folder's copies up to date. When linking is true it also links each
// agent that has nothing in its place and removes Conn's own links to items no longer provided:
// the server does so only for the real data folder, so a dev or dry server never points Claude's
// folder at its scratch copies. A skill is never linked here. Every problem is reported at once.
func Start(layout Layout, items []Item, linking bool) (Report, error) {
	err := WriteCopies(layout, items)
	if err != nil {
		return Report{}, err
	}

	if !linking {
		return Report{}, nil
	}

	seen, err := See(layout, items)
	if err != nil {
		return Report{}, err
	}

	report, linkErr := linkAgents(layout, seen)
	removed, sweepErr := sweepStale(layout, items)
	report.Removed = removed

	return report, errors.Join(linkErr, sweepErr)
}

func linkAgents(layout Layout, seen []Seen) (Report, error) {
	report := Report{}
	failures := []error{}

	for _, item := range AutoLinks(seen) {
		err := MakeLink(layout, item)
		if err != nil {
			failures = append(failures, err)

			continue
		}

		report.Linked = append(report.Linked, item.Name)
	}

	for _, one := range seen {
		if one.Item.Kind == KindAgent {
			if one.State == StateConflict {
				report.Conflicts = append(report.Conflicts, one.Item.Name)
			}
		}
	}

	return report, errors.Join(failures...)
}

// sweepStale removes Conn's own links to agents and skills that are no longer provided.
func sweepStale(layout Layout, items []Item) ([]string, error) {
	removed := []string{}
	failures := []error{}

	for _, kind := range []Kind{KindAgent, KindSkill} {
		entries, err := LinksIn(layout, kind)
		if err != nil {
			failures = append(failures, err)

			continue
		}

		for _, path := range StaleLinks(layout, kind, entries, items) {
			err = os.Remove(path)
			if err != nil {
				failures = append(failures, fmt.Errorf("removing the stale link %s: %w", path, err))

				continue
			}

			removed = append(removed, path)
		}
	}

	return removed, errors.Join(failures...)
}
