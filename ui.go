// Package conn holds what the conn binary carries with it: the built page bundle.
package conn

import (
	"embed"
	"io/fs"
)

// The page bundle, built by `bun run build` into dist/ before the Go build.
//
//go:embed all:dist
var dist embed.FS

// UI is the built page bundle, rooted at dist/.
func UI() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // a constant, valid path: cannot fail
	}

	return sub
}
