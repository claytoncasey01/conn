// Package origin finds the GitHub repo of a folder, read from its git config on disk: no git
// process. A folder inside a repo is in that repo (git walks up the same way), a worktree's .git
// file points at a gitdir whose commondir holds the real config, and what was read is kept per
// config file until its modification time changes. Everything read here is data: only a GitHub
// remote that passes repos.FromRemote comes out, so nothing from a config file reaches a search
// string, a prompt or a path unchecked.
//
// It also says what the allowlist means for something that may or may not belong to a repo (a
// session's folder, a History row): a thing in no repo is visible, since the allowlist is about
// repos and hiding scratch folders would make Conn look broken; a thing in a repo is visible
// when the list allows it. While there is no list (setup) a repo is allowed nowhere, so only
// what is in no repo shows.
package origin

import (
	"cmp"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	// maxConfigBytes: a config larger than this is not read. A real one is a few hundred bytes.
	maxConfigBytes = 1_000_000
	// maxPointerBytes bounds a .git pointer file and a commondir file.
	maxPointerBytes = 4096
	// maxFoldersUp is how many folders up the search for a .git goes.
	maxFoldersUp = 64
	// maxCacheEntries is how many configs are remembered before the cache starts over.
	maxCacheEntries = 1000
	// maxFolderEntries bounds the entries of a clone folder (the clone root, ~/projects) read to
	// find a repository's folder in it.
	maxFolderEntries = 4096

	// headRefPrefix starts the merge ref of a branch that tracks a branch.
	headRefPrefix = "refs/heads/"
)

// space is JavaScript's \s, which the page's own reading of these files used.
const space = `[` + js.SpaceClass + `]`

// notEndOfLine is any character but a line terminator, JavaScript's dot.
const notEndOfLine = `[^\n\r\x{2028}\x{2029}]`

var (
	gitdirLine = regexp.MustCompile(`^gitdir:` + space + `*(` + notEndOfLine + `+?)` + space +
		`*$`)
	sectionLine = regexp.MustCompile(`^\[` + space + `*([A-Za-z0-9.-]+)(?:` + space +
		`+"((?:[^"\\]|\\` + notEndOfLine + `)*)")?` + space + `*\]`)
	urlLine   = regexp.MustCompile(`(?i)^url` + space + `*=` + space + `*(` + notEndOfLine + `*)$`)
	trackLine = regexp.MustCompile(`(?i)^(remote|merge)` + space + `*=` + space + `*(` +
		notEndOfLine + `*)$`)
	quoted    = regexp.MustCompile(`^"((?:[^"\\]|\\` + notEndOfLine + `)*)"`)
	comment   = regexp.MustCompile(`[#;]`)
	escaped   = regexp.MustCompile(`\\(` + notEndOfLine + `)`)
	lineBreak = regexp.MustCompile(`\r?\n`)
)

// How says which step found a clone.
type How string

// The steps, in the order they are tried.
const (
	HowSettings How = "settings"
	HowRoot     How = "root"
	HowProjects How = "projects"
)

// Clone is where a repo's local clone is and which step found it.
type Clone struct {
	Path string `json:"path"`
	How  How    `json:"how"`
}

// cached is what one config file said, and when it was last changed.
type cached struct {
	modified time.Time
	repo     string
}

// Reader reads folders' repos, remembering each config file's answer until it changes.
type Reader struct {
	mu    sync.Mutex
	cache map[string]cached
}

// NewReader is a reader that remembers nothing yet.
func NewReader() *Reader {
	return &Reader{mu: sync.Mutex{}, cache: map[string]cached{}}
}

// Of is the lowercase owner/repo of a folder's GitHub origin, or "": not in a repo, no origin,
// an origin that is not on GitHub, or a config that cannot be read.
func (r *Reader) Of(folder string) string {
	config, found := folderConfig(folder)
	if !found {
		return ""
	}

	info, err := os.Stat(config)

	r.mu.Lock()
	defer r.mu.Unlock()

	if err != nil {
		delete(r.cache, config)

		return ""
	}

	if !info.Mode().IsRegular() {
		return ""
	}

	hit, isCached := r.cache[config]
	if isCached {
		if hit.modified.Equal(info.ModTime()) {
			return hit.repo
		}
	}

	repo := originRepo(config, info.Size())

	if len(r.cache) >= maxCacheEntries {
		clear(r.cache)
	}

	r.cache[config] = cached{modified: info.ModTime(), repo: repo}

	return repo
}

// FolderRepo is the repo of a folder that EXISTS, else "". Of walks up through parents that are
// not there until it meets a .git, so a folder that has since been deleted would take the repo of
// whatever encloses it (a home folder that is itself a repo, say): wrong for a session or a
// History row, which is attributed only when its own folder can be read.
func (r *Reader) FolderRepo(folder string) string {
	if !isFolder(folder) {
		return ""
	}

	return r.Of(folder)
}

// CloneFor finds where the local clone of repo is, in order: the settings entry,
// <cloneRoot>/<repo name>, <home>/projects/<repo name>. A folder counts only when it is a repo
// root (.git directly in it) whose origin is repo; one that is not falls through to the next. The
// folder is looked for under the repo name in the casing given and in lowercase, and the path
// returned is the folder as it is on disk. Nothing is cloned.
func (r *Reader) CloneFor(repo string, current settings.Settings, home string) (Clone, bool) {
	if !repos.IsName(repo) {
		return Clone{Path: "", How: ""}, false
	}

	name := strings.ToLower(repo)

	for _, candidate := range candidates(repo, current, home) {
		if !isRepoRoot(candidate.Path) {
			continue
		}

		if r.Of(candidate.Path) == name {
			return candidate, true
		}
	}

	return Clone{Path: "", How: ""}, false
}

// ClonesIn are the repositories cloned directly in parents (a clone folder, ~/projects), parent
// by parent and by folder name within one, as CloneFor would find them there: a repo root whose
// origin is on GitHub and whose folder is named for the repository, in any case. At most
// maxFolders folders are listed in all, so a huge folder is never read whole (which ones a cut
// listing holds is the file system's order); a parent that cannot be listed holds none.
func (r *Reader) ClonesIn(parents []string, maxFolders int) []string {
	found := []string{}
	left := maxFolders

	for _, parent := range parents {
		if left <= 0 {
			break
		}

		names := listNames(parent, left)
		left -= len(names)

		for _, name := range names {
			if repo := r.cloneIn(filepath.Join(parent, name)); repo != "" {
				found = append(found, repo)
			}
		}
	}

	return found
}

// listNames are the names of at most maxCount entries of folder, sorted; none when it cannot be
// opened or read.
func listNames(folder string, maxCount int) []string {
	dir, err := os.Open(folder)
	if err != nil {
		return nil
	}

	// Only read: nothing useful can be done with a failed close.
	defer func() { _ = dir.Close() }()

	names, err := dir.Readdirnames(maxCount)
	if err != nil {
		return nil
	}

	slices.Sort(names)

	return names
}

// cloneIn is the repository folder is a clone of when CloneFor would find it there, else "".
func (r *Reader) cloneIn(folder string) string {
	if !isRepoRoot(folder) {
		return ""
	}

	repo := r.Of(folder)
	_, name, _ := strings.Cut(repo, "/")

	if name != strings.ToLower(filepath.Base(folder)) {
		return ""
	}

	return repo
}

// candidates are the folders CloneFor tries, in order.
func candidates(repo string, current settings.Settings, home string) []Clone {
	_, segment, _ := strings.Cut(repo, "/")

	var tries []Clone

	configured := current.Repos.Clones.Get(strings.ToLower(repo))
	if configured != "" {
		tries = append(tries, Clone{Path: configured, How: HowSettings})
	}

	if current.Repos.CloneRoot != nil {
		for _, path := range namedLike(*current.Repos.CloneRoot, segment) {
			tries = append(tries, Clone{Path: path, How: HowRoot})
		}
	}

	for _, path := range namedLike(filepath.Join(home, "projects"), segment) {
		tries = append(tries, Clone{Path: path, How: HowProjects})
	}

	return tries
}

// Visible reports whether something in repo ("" for in none) may be shown, kept or fetched
// under allow.
func Visible(repo string, allow []string) bool {
	if repo == "" {
		return true
	}

	return repos.Allowed(repo, allow)
}

// UpstreamOf is the branch on origin that a local branch tracks, as git config names it
// (branch.<b>.remote = origin, branch.<b>.merge = refs/heads/<name>): a self-review worktree's
// conn-self-<n> tracks the PR's own branch. "" when the branch tracks nothing on origin, the
// name is not a plain branch name, or the config cannot be read. Read from disk, no git process,
// nothing cached.
func UpstreamOf(folder, branch string) string {
	if branch == "" {
		return ""
	}

	config, found := folderConfig(folder)
	if !found {
		return ""
	}

	text, readable := readConfig(config)
	if !readable {
		return ""
	}

	tracking := branchTracking(text, branch)
	if tracking.remote != "origin" {
		return ""
	}

	name, isHead := strings.CutPrefix(tracking.merge, headRefPrefix)
	if !isHead {
		return ""
	}

	parsed, _ := settings.ParseBranch(name)

	return parsed
}

// tracking is what a [branch "<b>"] section says it tracks; "" for a key it does not set.
type tracking struct {
	remote string
	merge  string
}

// branchTracking reads the remote and merge of branch from a config's text. git reads the last
// value of a key.
func branchTracking(text, branch string) tracking {
	var found tracking

	inBranch := false

	for _, line := range lineBreak.Split(text, -1) {
		trimmed := js.Trim(line)
		if skipped(trimmed) {
			continue
		}

		if trimmed[0] == '[' {
			inBranch = parseHeader(trimmed).isBranch(branch)

			continue
		}

		if !inBranch {
			continue
		}

		match := trackLine.FindStringSubmatch(trimmed)
		if match == nil {
			continue
		}

		if strings.EqualFold(match[1], "remote") {
			found.remote = value(match[2])

			continue
		}

		found.merge = value(match[2])
	}

	return found
}

// folderConfig is the config file of the repo that holds folder, for an absolute folder path.
func folderConfig(folder string) (string, bool) {
	if !filepath.IsAbs(folder) {
		return "", false
	}

	if strings.ContainsRune(folder, 0) {
		return "", false
	}

	return configPath(filepath.Clean(folder))
}

// step is what one folder's .git says about the search for a config.
type step int

const (
	stepUp step = iota
	stepFound
	stepFailed
)

// configPath is the config file of the repo that holds folder: the nearest .git going up,
// through a worktree's pointer and its commondir.
func configPath(folder string) (string, bool) {
	current := folder

	for range maxFoldersUp {
		config, outcome := configAt(current)

		switch outcome {
		case stepFound:
			return config, true
		case stepFailed:
			return "", false
		case stepUp:
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}

		current = parent
	}

	return "", false
}

// configAt looks for a .git directly in folder: a folder or a pointer file, never a link, so a link
// to another clone's .git cannot lend folder that clone's repository.
func configAt(folder string) (string, step) {
	dotGit := filepath.Join(folder, ".git")

	info, err := os.Lstat(dotGit)
	if err != nil {
		return "", stepUp
	}

	if info.IsDir() {
		return filepath.Join(dotGit, "config"), stepFound
	}

	if !info.Mode().IsRegular() {
		return "", stepUp
	}

	return worktreeConfig(folder, dotGit)
}

// worktreeConfig follows a .git pointer file to its gitdir, and on to the commondir's config
// when there is one.
func worktreeConfig(folder, dotGit string) (string, step) {
	line, _ := firstLine(dotGit)

	match := gitdirLine.FindStringSubmatch(line)
	if match == nil {
		return "", stepFailed
	}

	gitDir := resolve(folder, match[1])

	common, readable := firstLine(filepath.Join(gitDir, "commondir"))
	if !readable {
		return filepath.Join(gitDir, "config"), stepFound
	}

	common = js.Trim(common)
	if common == "" {
		return filepath.Join(gitDir, "config"), stepFound
	}

	return filepath.Join(resolve(gitDir, common), "config"), stepFound
}

// firstLine is the first line of a small file; false when it cannot be read or is too big.
func firstLine(path string) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}

	defer func() { _ = file.Close() }()

	buffer := make([]byte, maxPointerBytes+1)

	size, err := io.ReadFull(file, buffer)
	if !wholeRead(err) {
		return "", false
	}

	if size > maxPointerBytes {
		return "", false
	}

	text := strings.ToValidUTF8(string(buffer[:size]), "\ufffd")

	return beforeFirst(text, lineBreak), true
}

// wholeRead reports whether io.ReadFull's error only says the file was shorter than the buffer.
func wholeRead(err error) bool {
	if err == nil {
		return true
	}

	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	return errors.Is(err, io.EOF)
}

// resolve is path.resolve(base, path) for an absolute base.
func resolve(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}

	return filepath.Join(base, path)
}

// originRepo is the GitHub repo of a config file's origin, or "".
func originRepo(config string, size int64) string {
	if size > maxConfigBytes {
		return ""
	}

	text, err := os.ReadFile(config)
	if err != nil {
		return ""
	}

	url := originURL(strings.ToValidUTF8(string(text), "\ufffd"))
	if url == "" {
		return ""
	}

	return repos.FromRemote(url)
}

func readConfig(config string) (string, bool) {
	info, err := os.Stat(config)
	if err != nil {
		return "", false
	}

	if info.Size() > maxConfigBytes {
		return "", false
	}

	text, err := os.ReadFile(config)
	if err != nil {
		return "", false
	}

	return strings.ToValidUTF8(string(text), "\ufffd"), true
}

// originURL is the first url of [remote "origin"], as git reads the file (section words in any
// case, # or ; comments, a quoted value); "" when there is none.
func originURL(text string) string {
	inOrigin := false

	for _, line := range lineBreak.Split(text, -1) {
		trimmed := js.Trim(line)
		if skipped(trimmed) {
			continue
		}

		if trimmed[0] == '[' {
			inOrigin = parseHeader(trimmed).isRemote("origin")

			continue
		}

		if !inOrigin {
			continue
		}

		match := urlLine.FindStringSubmatch(trimmed)
		if match != nil {
			return value(match[1])
		}
	}

	return ""
}

// skipped reports whether a trimmed config line is blank or a comment.
func skipped(trimmed string) bool {
	if trimmed == "" {
		return true
	}

	switch trimmed[0] {
	case '#', ';':
		return true
	}

	return false
}

// sectionHeader is a [section "sub"] header: its word, and its subsection when it has one.
type sectionHeader struct {
	word   string
	sub    string
	hasSub bool
}

// parseHeader reads a header line; a line that is not one reads as a header with no subsection,
// which names no remote and no branch.
func parseHeader(line string) sectionHeader {
	match := sectionLine.FindStringSubmatchIndex(line)
	if match == nil {
		return sectionHeader{word: "", sub: "", hasSub: false}
	}

	header := sectionHeader{word: line[match[2]:match[3]], sub: "", hasSub: false}
	if match[4] >= 0 {
		header.sub = line[match[4]:match[5]]
		header.hasSub = true
	}

	return header
}

// isRemote reports whether the header is [remote "<name>"], the word in any case.
func (h sectionHeader) isRemote(name string) bool {
	if !h.hasSub {
		return false
	}

	if !strings.EqualFold(h.word, "remote") {
		return false
	}

	return h.sub == name
}

// isBranch reports whether the header is [branch "<name>"], the word in any case and the
// subsection's escapes read.
func (h sectionHeader) isBranch(name string) bool {
	if !h.hasSub {
		return false
	}

	if !strings.EqualFold(h.word, "branch") {
		return false
	}

	return escaped.ReplaceAllString(h.sub, "$1") == name
}

// value is a key's value as git reads it: a quoted value, or the text before a comment, trimmed.
func value(raw string) string {
	match := quoted.FindStringSubmatch(js.Trim(raw))
	if match != nil {
		return match[1]
	}

	return js.Trim(beforeFirst(raw, comment))
}

// beforeFirst is text up to the first match of pattern, or all of text.
func beforeFirst(text string, pattern *regexp.Regexp) string {
	location := pattern.FindStringIndex(text)
	if location == nil {
		return text
	}

	return text[:location[0]]
}

func isFolder(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}

	return info.IsDir()
}

// isRepoRoot reports whether .git is directly in the folder, as a folder or as a worktree's
// pointer file. A folder inside a repo is not one.
func isRepoRoot(path string) bool {
	if !isFolder(path) {
		return false
	}

	info, err := os.Stat(filepath.Join(path, ".git"))
	if err != nil {
		return false
	}

	if info.IsDir() {
		return true
	}

	return info.Mode().IsRegular()
}

// namedLike is the folders under parent named name in any case, as they are on disk: the casing
// asked for first, then lowercase, then the rest. At most maxFolderEntries entries of parent are
// read, so a huge folder is never read whole (which ones a cut listing holds is the file
// system's order).
func namedLike(parent, name string) []string {
	return namedLikeWithin(parent, name, maxFolderEntries)
}

// namedLikeWithin is namedLike reading at most maxEntries entries of parent.
func namedLikeWithin(parent, name string, maxEntries int) []string {
	lower := strings.ToLower(name)

	var found []string

	for _, entry := range listNames(parent, maxEntries) {
		if strings.ToLower(entry) == lower {
			found = append(found, entry)
		}
	}

	const (
		rankAsked = iota
		rankLower
		rankOther
	)

	rank := func(entry string) int {
		switch entry {
		case name:
			return rankAsked
		case lower:
			return rankLower
		}

		return rankOther
	}

	slices.SortStableFunc(found, func(first, second string) int {
		return cmp.Compare(rank(first), rank(second))
	})

	paths := make([]string, 0, len(found))
	for _, entry := range found {
		paths = append(paths, filepath.Join(parent, entry))
	}

	return paths
}
