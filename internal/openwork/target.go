package openwork

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/nodepath"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	// slugWordsMax and slugUnitsMax bound the title's part of a branch name.
	slugWordsMax = 6
	slugUnitsMax = 48
	// worktreeTimeout bounds the git calls that ask about a worktree.
	worktreeTimeout = 15 * time.Second
	worktreePrefix  = "worktree "
	// gitDirLines is what `git rev-parse --git-dir --git-common-dir` prints: two folders.
	gitDirLines  = 2
	argRevParse  = "rev-parse"
	argPorcelain = "--porcelain"
	argWorktree  = "worktree"
	// buildReposMax is the most repositories the list of places to build offers.
	buildReposMax = 200
)

var (
	// originHead is the line `git clone` writes in refs/remotes/origin/HEAD.
	originHead = regexp.MustCompile(`(?m)^ref:[` + js.SpaceClass + `]*refs/remotes/origin/(.+?)[` +
		js.SpaceClass + `]*$`)
	// pullURL is a GitHub pull request URL.
	pullURL = regexp.MustCompile(
		`^https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/pull/[0-9]+$`)
	notSlug    = regexp.MustCompile(`[^a-z0-9]+`)
	edgeDashes = regexp.MustCompile(`^-+|-+$`)
	endDashes  = regexp.MustCompile(`-+$`)
)

// Sh runs a command without a shell, in dir ("" is the server's folder), stopped after timeout
// (0 is two minutes); its error's message is the command's stderr, trimmed and cut at 400
// characters, or the reason it failed when it printed nothing.
type Sh func(ctx context.Context, name string, args []string, dir string,
	timeout time.Duration) (string, error)

// ShInput is Sh with input written to the command's standard input.
type ShInput func(ctx context.Context, name string, args []string, dir, input string,
	timeout time.Duration) (string, error)

// Target is one repository a run may build in: the repository, its local clone, and the branch
// a run starts from there. Reason says what to fix when a run cannot start there, "" when it can.
type Target struct {
	Repo   string
	Clone  string
	Base   string
	Reason string
}

// CloneFinder is the local clone of a repository per the settings, false when there is none.
type CloneFinder func(repo string, current settings.Settings) (string, bool)

// BuildTargets are the repositories a run may build in, each with its clone and the branch runs
// start from there (its own base branch, else atlassian.jira.baseBranch, else the clone's
// default branch). One is every allowed repository Settings names (the default repository, an
// exact allowlist entry, a clone set in Settings, a repository with a base branch of its own) and
// every allowed one in seen, the repositories found on this machine. A pattern alone names none.
// They are in allowlist order (the first pattern that allows each), and in the order they were
// named within a pattern.
func BuildTargets(current settings.Settings, seen []string, find CloneFinder,
	defaultOf func(clone string) (string, bool),
) []Target {
	names := AllowedRepos(current, seen)
	targets := make([]Target, 0, len(names))

	for _, repo := range names {
		targets = append(targets, targetFor(repo, current, find, defaultOf))
	}

	return targets
}

// AllowedRepos are the allowed repositories Settings names and seen holds, once each in any case,
// in allowlist order and in the order named within a pattern; at most buildReposMax. Open work
// offers them as places to build, and the Sessions page as places to start a session.
func AllowedRepos(current settings.Settings, seen []string) []string {
	jira := current.Atlassian.Jira

	// The default repository comes first, so a name in two places is spelled as it is.
	named := []string{}
	if jira.BuildRepo != nil {
		named = append(named, *jira.BuildRepo)
	}

	named = slices.Concat(named, current.Repos.Allow, current.Repos.Clones.Keys(), seen,
		slices.Sorted(maps.Keys(jira.BaseBranches)))

	type ranked struct {
		name    string
		pattern int
	}

	listed := []ranked{}
	once := map[string]bool{}

	for _, name := range named {
		pattern, allowed := patternOf(name, current.Repos.Allow)
		if !allowed {
			continue
		}

		if once[js.Lower(name)] {
			continue
		}

		once[js.Lower(name)] = true
		listed = append(listed, ranked{name: name, pattern: pattern})
	}

	slices.SortStableFunc(listed, func(left, right ranked) int {
		return left.pattern - right.pattern
	})

	// Cut after the order is set, so the cap keeps the allowlist's order.
	listed = listed[:min(len(listed), buildReposMax)]

	names := make([]string, 0, len(listed))
	for _, entry := range listed {
		names = append(names, entry.name)
	}

	return names
}

// patternOf is the index of the first allowlist pattern that allows name, false when name is not
// a repository name or none allows it.
func patternOf(name string, allow []string) (int, bool) {
	if !repos.IsName(name) {
		return 0, false
	}

	for index, pattern := range allow {
		if repos.Allowed(name, []string{pattern}) {
			return index, true
		}
	}

	return 0, false
}

// targetFor is repo as a place to build: not allowed, without a clone, without a branch to start
// from, or ready.
func targetFor(repo string, current settings.Settings, find CloneFinder,
	defaultOf func(clone string) (string, bool),
) Target {
	if !repos.Allowed(repo, current.Repos.Allow) {
		return Target{Repo: repo, Clone: "", Base: "", Reason: notAllowed(repo)}
	}

	clone, found := find(repo, current)
	if !found {
		return Target{
			Repo: repo, Clone: "", Base: "",
			Reason: "No local clone of " + repo + ": set a folder in Settings.",
		}
	}

	if base := current.Atlassian.Jira.BaseFor(repo); base != nil {
		return Target{Repo: repo, Clone: clone, Base: *base, Reason: ""}
	}

	base, known := defaultOf(clone)
	if !known {
		return Target{Repo: repo, Clone: clone, Base: "", Reason: "conn could not tell the " +
			"default branch of " + nodepath.Base(clone) + ": set a Base branch on the Atlassian " +
			"card in Settings."}
	}

	return Target{Repo: repo, Clone: clone, Base: base, Reason: ""}
}

func notAllowed(repo string) string {
	return repo + " is not an allowed repository: add it in Settings."
}

// ChooseTargets are the targets a START chose, in the order chosen, and every problem with the
// choice, one sentence each: none chosen (with no fallback, the default repository), more than
// legsMax (and the first legsMax are checked as usual), an empty name, one chosen twice in any
// case, one that is not among all, two that are the same clone folder, and one that is not
// ready. Only targets with no problem are returned.
func ChooseTargets(all []Target, chosen []string, fallback *string) ([]Target, []string) {
	names := chosen
	if len(names) == 0 {
		if fallback == nil {
			return []Target{}, []string{"Choose at least one repository."}
		}

		names = []string{*fallback}
	}

	picked := []Target{}
	problems := []string{}

	// Too many is said, and the first legsMax are checked as usual, so it hides nothing else.
	if len(names) > legsMax {
		problems = append(problems, "Choose at most "+strconv.Itoa(legsMax)+
			" repositories, not "+strconv.Itoa(len(names))+".")
		names = names[:legsMax]
	}

	choice := choosing{names: map[string]bool{}, clones: map[string]string{}}

	for _, name := range names {
		found, problem := choice.choose(all, name)
		if problem != "" {
			problems = append(problems, problem)

			continue
		}

		picked = append(picked, found)
	}

	return picked, problems
}

// choosing is what the names chosen so far hold: their lowercase names, and the repository each
// clone folder was chosen for.
type choosing struct {
	names  map[string]bool
	clones map[string]string
}

// choose is the target name is, or the problem with choosing it after the names before it.
func (c choosing) choose(all []Target, name string) (Target, string) {
	if name == "" {
		return Target{}, "An empty repository name was chosen."
	}

	if c.names[js.Lower(name)] {
		return Target{}, name + " is chosen twice."
	}

	c.names[js.Lower(name)] = true

	index := slices.IndexFunc(all, func(each Target) bool {
		return js.Lower(each.Repo) == js.Lower(name)
	})
	if index < 0 {
		return Target{}, notAllowed(name)
	}

	found := all[index]
	if found.Clone == "" {
		return Target{}, reasonOr(found.Reason)
	}

	folder := filepath.Clean(found.Clone)
	if earlier, taken := c.clones[folder]; taken {
		return Target{}, found.Repo + " is the same clone as " + earlier + ": " + folder + "."
	}

	c.clones[folder] = found.Repo

	if found.Reason != "" {
		return Target{}, found.Reason
	}

	return found, ""
}

// DefaultBranch is the default branch of a clone, as its git config on disk says
// (refs/remotes/origin/HEAD, which `git clone` sets): no git process, no network. False when
// the clone has none or it is not a branch name Conn accepts.
func DefaultBranch(clone string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(clone, ".git", "refs", "remotes", "origin", "HEAD"))
	if err != nil {
		return "", false
	}

	match := originHead.FindStringSubmatch(jsLines(js.DecodeUTF8(data, false)))
	if match == nil {
		return "", false
	}

	return settings.ParseBranch(match[1])
}

// jsLines makes every line end JavaScript's multiline mode knows (\r, U+2028, U+2029) a \n, so
// Go's (?m) sees the same lines.
func jsLines(content string) string {
	return strings.NewReplacer("\r", "\n", " ", "\n", " ", "\n").Replace(content)
}

// PRURLRepo is the owner/repo of a GitHub pull request URL, "" for anything else.
func PRURLRepo(url string) string {
	match := pullURL.FindStringSubmatch(url)
	if match == nil {
		return ""
	}

	if !repos.IsName(match[1]) {
		return ""
	}

	return match[1]
}

// Slug is the title's part of a branch name: lowercase letters and digits, words joined by
// dashes, at most six words and 48 characters.
func Slug(title string) string {
	slug := notSlug.ReplaceAllString(js.Lower(title), "-")
	slug = edgeDashes.ReplaceAllString(slug, "")

	words := strings.Split(slug, "-")
	slug = strings.Join(words[:min(len(words), slugWordsMax)], "-")
	slug = slug[:min(len(slug), slugUnitsMax)]

	return endDashes.ReplaceAllString(slug, "")
}

// ticketNumber is the number of a ticket key: PROJ-12 is 12.
func ticketNumber(key string) float64 {
	return js.ToNumber(key[strings.LastIndex(key, "-")+1:])
}

// realPath is a path as the disk has it (a folder reached through a link is the same one), or
// as given, made absolute, when it cannot be resolved.
func realPath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return nodepath.Resolve("", resolved)
	}

	return nodepath.Resolve("", path)
}

// LinkedWorktreeRepo is the folder of the repository path is a linked worktree of (the main
// checkout, where `git worktree remove` runs), or false when path is not a linked worktree: not
// in a repository, the main checkout itself, or not one git lists. It asks git, never the run's
// own record.
func LinkedWorktreeRepo(ctx context.Context, path string, run Sh) (string, bool) {
	out, err := run(ctx, "git", []string{argRevParse, "--git-dir", "--git-common-dir"}, path,
		worktreeTimeout)
	if err != nil {
		return "", false
	}

	lines := strings.Split(js.Trim(out), "\n")
	if len(lines) < gitDirLines {
		return "", false
	}

	gitDir := nodepath.Resolve(path, lines[0])
	common := nodepath.Resolve(path, lines[1])

	if gitDir == common {
		return "", false
	}

	if nodepath.Base(common) != ".git" {
		return "", false
	}

	main := nodepath.Dir(common)

	listed, err := run(ctx, "git", []string{argWorktree, "list", argPorcelain}, main,
		worktreeTimeout)
	if err != nil {
		return "", false
	}

	// git lists real paths; a folder reached through a link (macOS /var) is the same one.
	for line := range strings.SplitSeq(listed, "\n") {
		listedPath, found := strings.CutPrefix(line, worktreePrefix)
		if !found {
			continue
		}

		if realPath(listedPath) == realPath(path) {
			return main, true
		}
	}

	return "", false
}

func joinPath(parts ...string) string { return filepath.Join(parts...) }

func dirOf(path string) string { return nodepath.Dir(path) }

func baseOf(path string) string { return nodepath.Base(path) }
