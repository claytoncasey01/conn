package themecmd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/gallery"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/themes"
)

// IndexUsage is `conn theme index`'s usage.
const IndexUsage = "usage: conn theme index --src <folder of <id>/theme.json> " +
	"--out <new or empty folder> --commit <40 hex> --repo owner/name"

const (
	optionSrc    = "--src"
	optionOut    = "--out"
	optionCommit = "--commit"
	optionRepo   = "--repo"

	indexFile = "index.json"

	// The modes a folder and a file get when made as mkdir and a plain write make them.
	handMadeFolderMode = 0o777
	handMadeFileMode   = 0o666
)

// commitForm is the shape the app's reader accepts, so an index built here is never dropped
// there.
var commitForm = regexp.MustCompile(`^[0-9a-f]{40}$`)

// built is a source folder judged: the index, the errors that kept a folder out of it, and each
// theme's bytes as hashed, by id in index order.
type built struct {
	entries []gallery.Entry
	errors  []string
	files   []themeFile
}

type themeFile struct {
	id    string
	bytes []byte
}

// serialized is index.json: compact, since the app refuses one over 512 KB and every entry
// carries its whole manifest, and one newline at the end.
func serialized(repo string, entries []gallery.Entry) string {
	themesList := make([]any, 0, len(entries))
	for _, entry := range entries {
		themesList = append(themesList, entry)
	}

	object := js.NewObject()
	object.Set("version", 1)
	object.Set("repo", repo)
	object.Set("themes", themesList)

	return js.Stringify(object) + "\n"
}

// build judges every theme folder in src. Only `<id>/theme.json` folders are themes: a loose
// file at the root and a dot folder are the repo's own business, never read. Each theme is
// judged exactly as the app judges an install.
func build(src, repo, commit string, builtin themes.Shelf) built {
	result := built{entries: []gallery.Entry{}, errors: nil, files: nil}

	if !config.IsRepoName(repo) {
		result.errors = append(result.errors, "repo must be a GitHub owner/name")
	}

	if !commitForm.MatchString(commit) {
		result.errors = append(result.errors, "commit must be 40 lowercase hex characters")
	}

	if len(result.errors) > 0 {
		return result
	}

	names, err := sourceFolders(src)
	if err != nil {
		result.errors = append(result.errors, "source folder could not be read")

		return result
	}

	base := themes.BaseOf(builtin)
	shelf := themes.Disk(src)

	for _, name := range names {
		judged := checkGalleryFolder(shelf, name, base)
		if len(judged.problems) > 0 {
			for _, problem := range judged.problems {
				result.errors = append(result.errors, quoted(name)+": "+problem)
			}

			continue
		}

		sum := sha256.Sum256(judged.bytes)
		manifest := judged.manifest
		result.entries = append(result.entries, gallery.Entry{
			ID: manifest.ID, Name: manifest.Name, Author: manifest.Author,
			Version: manifest.Version, Description: manifest.Description, Commit: commit,
			Sha256: hex.EncodeToString(sum[:]), Files: []string{themes.ManifestFile},
			Manifest: manifest,
		})
		result.files = append(result.files, themeFile{id: name, bytes: judged.bytes})
	}

	return limits(result, repo)
}

// sourceFolders are src's folders and links to them, dot folders skipped, sorted as JavaScript
// sorts strings.
func sourceFolders(src string) ([]string, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		return nil, fmt.Errorf("reading the source folder: %w", err)
	}

	names := []string{}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		if entry.IsDir() {
			names = append(names, entry.Name())

			continue
		}

		if entry.Type()&os.ModeSymlink != 0 {
			names = append(names, entry.Name())
		}
	}

	slices.SortFunc(names, js.CompareUTF16)

	return names, nil
}

// limits refuses an index the app would not read whole.
func limits(result built, repo string) built {
	if len(result.entries) > gallery.IndexEntriesMax {
		result.errors = append(result.errors, "the index would list "+
			strconv.Itoa(len(result.entries))+" themes; the app reads at most "+
			strconv.Itoa(gallery.IndexEntriesMax))
	}

	size := len(serialized(repo, result.entries))
	if size > gallery.IndexBytesMax {
		result.errors = append(result.errors, "index.json would be "+
			strconv.Itoa(int(math.Ceil(float64(size)/kibibyte)))+" KB; the app reads at most "+
			kb(gallery.IndexBytesMax))
	}

	return result
}

// options reads `--name value` pairs: each of the four once, nothing else.
func options(args []string) (map[string]string, string) {
	known := []string{optionSrc, optionOut, optionCommit, optionRepo}
	values := map[string]string{}

	for index := 0; index < len(args); index += 2 {
		key := args[index]
		if !slices.Contains(known, key) {
			return nil, "unknown option " + js.Stringify(key)
		}

		_, seen := values[key]
		if seen {
			return nil, key + " needs one value"
		}

		if index+1 >= len(args) {
			return nil, key + " needs one value"
		}

		if strings.HasPrefix(args[index+1], "--") {
			return nil, key + " needs one value"
		}

		values[key] = args[index+1]
	}

	lacking := []string{}

	for _, key := range known {
		_, given := values[key]
		if !given {
			lacking = append(lacking, key)
		}
	}

	if len(lacking) == 1 {
		return nil, lacking[0] + " is required"
	}

	if len(lacking) > 1 {
		return nil, strings.Join(lacking, ", ") + " are required"
	}

	return values, ""
}

// Index runs `conn theme index` and returns the exit code: 0 written, 1 refused (nothing
// written), 2 bad usage.
func Index(args []string, streams IO) int {
	values, problem := options(args)
	if problem != "" {
		streams.err(problem)
		streams.err(IndexUsage)

		return ExitUsage
	}

	src, srcErr := filepath.Abs(values[optionSrc])
	out, outErr := filepath.Abs(values[optionOut])

	if srcErr != nil {
		src = values[optionSrc]
	}

	if outErr != nil {
		out = values[optionOut]
	}

	result := build(src, values[optionRepo], values[optionCommit], streams.Builtin)
	result.errors = append(result.errors, placeProblems(result, src, out)...)

	if len(result.errors) > 0 {
		for _, line := range result.errors {
			streams.err(line)
		}

		streams.err("nothing was written")

		return ExitFailed
	}

	return streams.writeIndex(out, values[optionRepo], values[optionCommit], result)
}

// placeProblems are why the result may not be written: a source with no theme folder is a typo
// or the wrong folder (and an empty index copied over main would empty the gallery), and the
// out folder must be new or empty and not inside the source.
func placeProblems(result built, src, out string) []string {
	problems := []string{}

	if len(result.errors) == 0 {
		if len(result.entries) == 0 {
			problems = append(problems,
				"--src holds no theme folders, so there is nothing to index")
		}
	}

	realSrc, realOut := realNear(src), realNear(out)
	if realOut == realSrc {
		return append(problems, "--out is inside --src, so it would be read as themes next time")
	}

	if strings.HasPrefix(realOut, realSrc+string(filepath.Separator)) {
		return append(problems, "--out is inside --src, so it would be read as themes next time")
	}

	_, err := os.Stat(out)
	if err != nil {
		return problems
	}

	info, err := os.Lstat(out)
	if err != nil {
		return problems
	}

	if !info.IsDir() {
		return append(problems, "--out exists and is not a folder")
	}

	entries, err := os.ReadDir(out)
	if err == nil {
		if len(entries) > 0 {
			return append(problems, "--out already holds files; give a new or empty folder so "+
				"nothing stale is served")
		}
	}

	return problems
}

// realNear is path with every link resolved as far as it exists, and the rest appended: a link
// anywhere above a folder that is not made yet still counts.
func realNear(path string) string {
	rest := []string{}
	current := path

	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			slices.Reverse(rest)

			return filepath.Join(append([]string{resolved}, rest...)...)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return path
		}

		rest = append(rest, filepath.Base(current))
		current = parent
	}
}

// writeIndex writes <out>/<id>/theme.json for each theme and index.json. On a failure only what
// this run made is undone: the folders it created, or, in an empty folder that was already
// there, its own entries.
func (o IO) writeIndex(out, repo, commit string, result built) int {
	made := []string{}

	err := o.writeAll(out, repo, result, &made)
	if err != nil {
		for _, path := range made {
			_ = os.RemoveAll(path)
		}

		o.err("could not write " + out + ": " + errnoName(err) +
			"; what this run wrote was removed")

		return ExitFailed
	}

	plural := "s"
	if len(result.entries) == 1 {
		plural = ""
	}

	o.out(strconv.Itoa(len(result.entries)) + " theme" + plural + " at " + commit +
		" written to " + out)

	for _, entry := range result.entries {
		o.out("  " + entry.ID + " " + entry.Version + " " + entry.Sha256)
	}

	return ExitOK
}

func (o IO) writeAll(out, repo string, result built, made *[]string) error {
	first, err := firstMissing(out)
	if err != nil {
		return err
	}

	err = os.MkdirAll(out, handMadeFolderMode)
	if err != nil {
		return fmt.Errorf("making the out folder: %w", err)
	}

	if first != "" {
		*made = append(*made, first)
	}

	for _, file := range result.files {
		folder := filepath.Join(out, file.id)
		if first == "" {
			*made = append(*made, folder)
		}

		err = os.Mkdir(folder, handMadeFolderMode)
		if err != nil {
			return fmt.Errorf("making a theme folder: %w", err)
		}

		err = o.write(filepath.Join(folder, themes.ManifestFile), file.bytes)
		if err != nil {
			return err
		}
	}

	if first == "" {
		*made = append(*made, filepath.Join(out, indexFile))
	}

	return o.write(filepath.Join(out, indexFile), []byte(serialized(repo, result.entries)))
}

func (o IO) write(path string, data []byte) error {
	if o.Write != nil {
		return o.Write(path, data)
	}

	err := os.WriteFile(path, data, handMadeFileMode)
	if err != nil {
		return fmt.Errorf("writing a file: %w", err)
	}

	return nil
}

// firstMissing is the topmost folder of path that MkdirAll would make, or "" when path exists.
func firstMissing(path string) (string, error) {
	first := ""
	current := path

	for {
		_, err := os.Stat(current)
		if err == nil {
			return first, nil
		}

		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("looking at the out folder: %w", err)
		}

		first = current

		parent := filepath.Dir(current)
		if parent == current {
			return first, nil
		}

		current = parent
	}
}

// errnoName is an error's errno as node names it (ENOSPC, EACCES), or "unknown error": the
// message itself can carry a path.
func errnoName(err error) string {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return "unknown error"
	}

	names := map[syscall.Errno]string{
		syscall.EACCES: "EACCES", syscall.EPERM: "EPERM", syscall.ENOSPC: "ENOSPC",
		syscall.EEXIST: "EEXIST", syscall.ENOENT: "ENOENT", syscall.ENOTDIR: "ENOTDIR",
		syscall.EISDIR: "EISDIR", syscall.EROFS: "EROFS", syscall.EIO: "EIO",
		syscall.EDQUOT: "EDQUOT", syscall.EMFILE: "EMFILE", syscall.ENAMETOOLONG: "ENAMETOOLONG",
		syscall.ELOOP: "ELOOP",
	}

	name, known := names[errno]
	if !known {
		return "unknown error"
	}

	return name
}
