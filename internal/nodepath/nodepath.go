// Package nodepath is Node's posix path functions where they differ from Go's: a trailing slash is
// kept by Normalize and ignored by Dir and Base, an empty path is "" to Base and "." to Dir. The
// folders a record names are compared as Bun compared them.
package nodepath

import (
	"path"
	"path/filepath"
	"strings"
)

// Base is path.basename.
func Base(name string) string {
	trimmed := strings.TrimRight(name, "/")
	if trimmed == "" {
		return ""
	}

	return trimmed[strings.LastIndexByte(trimmed, '/')+1:]
}

// Dir is path.dirname.
func Dir(name string) string {
	if name == "" {
		return "."
	}

	hasRoot := name[0] == '/'
	end := -1
	matchedSlash := true

	for index := len(name) - 1; index >= 1; index-- {
		if name[index] == '/' {
			if !matchedSlash {
				end = index

				break
			}

			continue
		}

		matchedSlash = false
	}

	if end == -1 {
		if hasRoot {
			return "/"
		}

		return "."
	}

	if hasRoot {
		if end == 1 {
			return "//"
		}
	}

	return name[:end]
}

// Normalize is path.normalize: Clean, keeping a trailing slash.
func Normalize(name string) string {
	if name == "" {
		return "."
	}

	clean := path.Clean(name)
	if strings.HasSuffix(name, "/") {
		if clean != "/" {
			return clean + "/"
		}
	}

	return clean
}

// Resolve is path.resolve(base, name): name when it is absolute, else name under base,
// cleaned, with no trailing slash.
func Resolve(base, name string) string {
	if filepath.IsAbs(name) {
		return filepath.Clean(name)
	}

	joined := filepath.Join(base, name)

	absolute, err := filepath.Abs(joined)
	if err != nil {
		return joined
	}

	return absolute
}
