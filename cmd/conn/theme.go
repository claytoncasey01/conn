package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/claytoncasey01/conn"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/themecmd"
	"github.com/claytoncasey01/conn/internal/themes"
)

// modulePath is this repository's module: a go.mod naming it marks a conn checkout.
const modulePath = "github.com/claytoncasey01/conn"

const themeUsage = `usage: conn theme <command>

commands:
  check    judge theme folders as conn does ("conn theme check --help" for more)
  index    build the gallery repo's index.json ("conn theme index" with no options for more)
`

// themeCommand runs `conn theme check` or `conn theme index`.
func themeCommand(args []string, lookup config.LookupFunc, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, themeUsage)

		return exitUsage
	}

	streams := themeIO(stdout, stderr, config.FromEnv(lookup).ThemesUser)

	switch args[0] {
	case "check":
		return themecmd.Check(args[1:], streams)
	case "index":
		return themecmd.Index(args[1:], streams)
	}

	_, _ = fmt.Fprintf(stderr, "conn: unknown theme command %q\n\n%s", args[0], themeUsage)

	return exitUsage
}

// themeIO is what the theme commands read and print to. The built-in themes they read are, in a
// conn checkout, its themes/ folder, so an edit to a built-in is judged as it is now and a
// folder given by path there is judged as a built-in; anywhere else, the themes this binary
// carries.
func themeIO(stdout, stderr io.Writer, user string) themecmd.IO {
	streams := themecmd.IO{
		Out: stdout, Err: stderr, Builtin: themes.Embedded(conn.Themes()), BuiltinDir: "",
		User: user, Write: nil,
	}

	dir, found := checkoutThemes()
	if found {
		streams.Builtin, streams.BuiltinDir = themes.Disk(dir), dir
	}

	return streams
}

// checkoutThemes is the themes/ folder of the conn checkout the working folder is in.
func checkoutThemes() (string, bool) {
	current, err := os.Getwd()
	if err != nil {
		return "", false
	}

	for {
		if isConnModule(filepath.Join(current, "go.mod")) {
			dir := filepath.Join(current, "themes")

			info, err := os.Stat(dir)
			if err != nil {
				return "", false
			}

			return dir, info.IsDir()
		}

		up := filepath.Dir(current)
		if up == current {
			return "", false
		}

		current = up
	}
}

// isConnModule is whether the go.mod at path declares this repository's module.
func isConnModule(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}

	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		module, found := strings.CutPrefix(strings.TrimSpace(scanner.Text()), "module ")
		if found {
			return strings.TrimSpace(module) == modulePath
		}
	}

	return false
}
