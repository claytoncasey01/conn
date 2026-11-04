package reviews

import (
	"path"
	"path/filepath"
	"strings"
)

// Node's posix path functions where they differ from Go's: a trailing slash is kept by normalize
// and ignored by dirname and basename, an empty path is "" to basename, and "." to dirname. The
// folders a launch record names are compared as Bun compared them.

// nodeBase is path.basename.
func nodeBase(name string) string {
	trimmed := strings.TrimRight(name, "/")
	if trimmed == "" {
		return ""
	}

	return trimmed[strings.LastIndexByte(trimmed, '/')+1:]
}

// nodeDir is path.dirname.
func nodeDir(name string) string {
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

// nodeNormalize is path.normalize: Clean, keeping a trailing slash.
func nodeNormalize(name string) string {
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

// nodeResolve is path.resolve(base, name): name when it is absolute, else name under base,
// cleaned, with no trailing slash.
func nodeResolve(base, name string) string {
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
