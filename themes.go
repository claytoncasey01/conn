package conn

import (
	"embed"
	"io/fs"
)

// The built-in themes: each folder's theme.json and theme.css. The template (themes/_template)
// comes along and is skipped like any `_` folder.
//
//go:embed themes/*/theme.json themes/*/theme.css
var builtinThemes embed.FS

// Themes is the built-in theme folders, rooted at themes/.
func Themes() fs.FS {
	sub, err := fs.Sub(builtinThemes, "themes")
	if err != nil {
		panic(err) // a constant, valid path: cannot fail
	}

	return sub
}
