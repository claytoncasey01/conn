package pty

import "strings"

const (
	// connPrefix and cmuxPrefix start the names of variables a hosted program must not see:
	// Conn's own settings (its data folder, its port, and any secret one may ever carry), and
	// cmux's, which would make a hosted session look like a cmux pane.
	connPrefix = "CONN_"
	cmuxPrefix = "CMUX_"
	// termName is the variable naming the terminal type, which is the host's, not the parent's.
	termName = "TERM"
	// childTerm is the terminal type a hosted program is told it has.
	childTerm = "xterm-256color"
)

// ChildEnv is the environment a hosted program gets from parent: every entry of parent, in order,
// except those whose name starts with CONN_ or CMUX_, in any case; TERM, which is replaced by the
// host's own; and entries that are not a name, "=" and a value (no "=", or nothing before it).
// Only names are matched, never values. The result is never nil, so it can never stand for "the
// parent's environment" to os/exec.
func ChildEnv(parent []string) []string {
	child := make([]string, 0, len(parent)+1)

	for _, entry := range parent {
		if keepVariable(entry) {
			child = append(child, entry)
		}
	}

	return append(child, termName+"="+childTerm)
}

// keepVariable says whether the environment entry may reach a hosted program.
func keepVariable(entry string) bool {
	name, _, found := strings.Cut(entry, "=")
	if !found {
		return false
	}

	if name == "" {
		return false
	}

	upper := strings.ToUpper(name)

	if strings.HasPrefix(upper, connPrefix) {
		return false
	}

	if strings.HasPrefix(upper, cmuxPrefix) {
		return false
	}

	return upper != termName
}
