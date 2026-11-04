// Package realpath is the one place a path is made comparable: made absolute with every link
// followed, so a link to a folder and the folder itself are the same path.
package realpath

import "path/filepath"

// Resolve is the path with every link followed when it exists (a link to the real folder is the
// real folder), else as written, made absolute.
func Resolve(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		absolute, absErr := filepath.Abs(resolved)
		if absErr == nil {
			return absolute
		}
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return path
	}

	return absolute
}
