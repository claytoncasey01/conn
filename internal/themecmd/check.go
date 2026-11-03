package themecmd

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/theme"
	"github.com/claytoncasey01/conn/internal/themes"
)

// The exit codes: everything passed, something did not, and bad usage.
const (
	ExitOK      = 0
	ExitFailed  = 1
	ExitUsage   = 2
	galleryFlag = "--gallery"
)

// CheckUsage is `conn theme check`'s usage.
const CheckUsage = "usage: conn theme check [<id> | <path to a theme folder>]...  " +
	"(no argument checks every built-in theme)\n" +
	"       conn theme check --gallery <id | path to a theme folder>...  " +
	"(as conn theme index and the gallery install judge it: theme.json alone, no theme.css)"

var (
	// modeLine is a contrast line ("dark (Green): tx on ..."), which already reads as a field.
	modeLine = regexp.MustCompile(`^(?:light|dark)\b`)
	// fieldStart is the field a problem starts with, which gets a colon after it.
	fieldStart = regexp.MustCompile(`^((?:accent variant "[^"]*" )?[A-Za-z][\w.\[\]-]*) `)
)

// field makes "tokens.dark.tx contains ..." and "id must be ..." read as "tokens.dark.tx:
// contains ...", the shape of the other lines. A contrast line and one that already has its
// colon are left alone.
func field(problem string) string {
	if modeLine.MatchString(problem) {
		return problem
	}

	return fieldStart.ReplaceAllString(problem, "$1: ")
}

// target is one folder to check: its shelf and name, and the source it is judged as.
type target struct {
	shelf   themes.Shelf
	name    string
	source  theme.Source
	problem string
}

// verdict is a folder's problems (empty when it passed) and notes.
type verdict struct {
	problems []string
	notes    []string
}

// Check runs `conn theme check` and returns the exit code. Problems go to Err, passes and notes
// to Out.
func Check(args []string, streams IO) int {
	flags, names := split(args)

	code, done := usage(flags, names, streams)
	if done {
		return code
	}

	strict := len(flags) > 0
	base := themes.BaseOf(streams.Builtin)

	failed := false

	for _, each := range targets(names, streams, base) {
		label := each.name
		if !themes.IsFolderName(label) {
			label = js.Stringify(label)
		}

		judged := judge(each, base, strict)
		for _, problem := range judged.problems {
			streams.err(label + ": " + problem)
		}

		if len(judged.problems) > 0 {
			failed = true
		} else {
			streams.out(label + ": ok")
		}

		for _, note := range judged.notes {
			streams.out(label + ": note: " + note)
		}
	}

	if failed {
		return ExitFailed
	}

	return ExitOK
}

// split parts the arguments into flags and names.
func split(args []string) ([]string, []string) {
	var flags, names []string

	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
		} else {
			names = append(names, arg)
		}
	}

	return flags, names
}

// usage answers --help and bad usage; done is false when the check should run.
func usage(flags, names []string, streams IO) (int, bool) {
	help := func(flag string) bool { return slices.Contains([]string{"-h", "--help"}, flag) }
	if slices.ContainsFunc(flags, help) {
		streams.out(CheckUsage)

		return ExitOK, true
	}

	unknown := []string{}

	for _, flag := range flags {
		if flag != galleryFlag {
			unknown = append(unknown, flag)
		}
	}

	if len(unknown) > 0 {
		for _, flag := range unknown {
			streams.err("unknown option " + js.Stringify(flag))
		}

		streams.err(CheckUsage)

		return ExitUsage, true
	}

	if len(flags) > 0 {
		if len(names) == 0 {
			streams.err("--gallery needs a theme id or folder to check")
			streams.err(CheckUsage)

			return ExitUsage, true
		}
	}

	return ExitOK, false
}

// targets are the folders the arguments name: every built-in with none, else each path or id.
func targets(names []string, streams IO, base themes.Base) []target {
	found := []target{}

	if len(names) == 0 {
		for _, folder := range base.Folders {
			found = append(found, target{
				shelf: streams.Builtin, name: folder.Name, source: theme.SourceBuiltin,
			})
		}

		if len(found) == 0 {
			found = append(found, target{
				name: "themes", problem: "folder: no built-in theme folders were found",
			})
		}
	}

	for _, name := range names {
		found = append(found, targetOf(name, streams))
	}

	return found
}

// targetOf is the folder an argument names: a path is anything with a separator, or a name that
// is already a folder (or a link, which is then refused) here; an id is looked up in the
// built-ins first, then the user's folder.
func targetOf(name string, streams IO) target {
	if strings.Contains(name, "/") {
		return pathTarget(name, streams)
	}

	if folderLike(name) {
		return pathTarget(name, streams)
	}

	if !themes.IsFolderName(name) {
		return target{name: name, problem: "folder: name must be a-z, 0-9 and -"}
	}

	if streams.Builtin.Exists(name, "") {
		return target{shelf: streams.Builtin, name: name, source: theme.SourceBuiltin}
	}

	user := themes.Disk(streams.User)
	if user.Exists(name, "") {
		return target{shelf: user, name: name, source: theme.SourceUser}
	}

	return target{name: name, problem: "folder: no built-in or user theme has that name"}
}

func pathTarget(name string, streams IO) target {
	dir, err := filepath.Abs(name)
	if err != nil {
		return target{name: name, problem: "folder: could not be read"}
	}

	source := theme.SourceUser

	if streams.BuiltinDir != "" {
		builtin, err := filepath.Abs(streams.BuiltinDir)
		if err == nil {
			if filepath.Dir(dir) == builtin {
				source = theme.SourceBuiltin
			}
		}
	}

	return target{shelf: themes.Disk(filepath.Dir(dir)), name: filepath.Base(dir), source: source}
}

// folderLike is whether a folder, or a link (refused later rather than passed over as "no such
// folder"), is at path.
func folderLike(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}

	if info.IsDir() {
		return true
	}

	return info.Mode()&os.ModeSymlink != 0
}

func judge(each target, base themes.Base, strict bool) verdict {
	if each.problem != "" {
		return verdict{problems: []string{each.problem}}
	}

	if !each.shelf.Exists(each.name, "") {
		return verdict{problems: []string{"folder: does not exist"}}
	}

	if strict {
		judged := checkGalleryFolder(each.shelf, each.name, base)

		problems := make([]string, 0, len(judged.problems))
		for _, problem := range judged.problems {
			problems = append(problems, field(problem))
		}

		return verdict{problems: problems}
	}

	return judgeFolder(each, base)
}

// judgeFolder is the server's judgment of a theme folder, for one named by hand.
func judgeFolder(each target, base themes.Base) verdict {
	shelf, name := each.shelf, each.name

	problem := shelf.FolderProblem(name)
	if problem != "" {
		return verdict{problems: []string{"folder: " + strings.TrimPrefix(problem, "folder ")}}
	}

	gallery := false

	switch shelf.Marker(name) {
	case themes.MarkerNotFile:
		return verdict{problems: []string{".source.json: is not a regular file"}}
	case themes.MarkerUnreadable:
		return verdict{problems: []string{".source.json: could not be read"}}
	case themes.MarkerPresent:
		gallery = true
	case themes.MarkerAbsent:
	}

	data, why := shelf.Read(name, themes.ManifestFile, theme.ManifestBytesMax)
	if why != themes.Read {
		return verdict{
			problems: []string{"theme.json: " + readProblem(why, theme.ManifestBytesMax)},
		}
	}

	judged := verdict{}
	context := theme.Context{Source: each.source, BuiltinIDs: base.IDs}

	manifest, problems := theme.CheckTheme(js.DecodeUTF8(data, false), context, &base.Fallback)
	if len(problems) > 0 {
		for _, problem := range problems {
			judged.problems = append(judged.problems, field(problem))
		}
	} else if manifest.ID != name {
		judged.problems = append(judged.problems, "id: folder "+js.Stringify(name)+
			" does not match the manifest id "+js.Stringify(manifest.ID))
	}

	return judgeStyle(judged, each, gallery)
}

// judgeStyle adds what theme.css says: the server never serves a gallery theme's stylesheet, so
// one beside a .source.json is a mistake; a user theme's is applied only after the user agrees.
func judgeStyle(judged verdict, each target, gallery bool) verdict {
	if gallery {
		if each.shelf.Exists(each.name, themes.StyleFile) {
			judged.problems = append(judged.problems, "theme.css: a gallery theme cannot carry CSS")
		}

		return judged
	}

	_, why := each.shelf.Read(each.name, themes.StyleFile, theme.CSSBytesMax)

	switch why {
	case themes.Read:
		if each.source != theme.SourceBuiltin {
			judged.notes = append(judged.notes,
				"theme.css: the app asks for confirmation before applying it")
		}
	case themes.Missing:
	case themes.TooBig, themes.NotFile, themes.Unreadable:
		judged.problems = append(judged.problems, "theme.css: "+readProblem(why, theme.CSSBytesMax))
	}

	return judged
}

func readProblem(why themes.Why, bytesMax int) string {
	switch why {
	case themes.Missing:
		return "is missing"
	case themes.TooBig:
		return "is larger than " + kb(bytesMax)
	case themes.NotFile:
		return "is not a regular file, so it was not read"
	case themes.Read, themes.Unreadable:
	}

	return "could not be read"
}
