package openwork

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
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

// Target is where a run builds: the build repository from Settings, its local clone, and the
// branch it starts from. Reason says what to fix when a run cannot start, "" when it can.
type Target struct {
	Repo   string
	Clone  string
	Base   string
	Reason string
}

// CloneFinder is the local clone of a repository per the settings, false when there is none.
type CloneFinder func(repo string, current settings.Settings) (string, bool)

// BuildTarget is where a run builds now: the build repository, its clone, and the branch runs
// start from (atlassian.jira.baseBranch, else the clone's default branch).
func BuildTarget(current settings.Settings, find CloneFinder,
	defaultOf func(clone string) (string, bool),
) Target {
	repo := current.Atlassian.Jira.BuildRepo
	if repo == nil {
		return Target{
			Repo: "", Clone: "", Base: "",
			Reason: "No build repository is set: choose one in Settings.",
		}
	}

	if !repos.Allowed(*repo, current.Repos.Allow) {
		return Target{
			Repo: *repo, Clone: "", Base: "",
			Reason: *repo + " is not an allowed repository: add it in Settings.",
		}
	}

	clone, found := find(*repo, current)
	if !found {
		return Target{
			Repo: *repo, Clone: "", Base: "",
			Reason: "No local clone of " + *repo + ": set a folder in Settings.",
		}
	}

	if base := current.Atlassian.Jira.BaseBranch; base != nil {
		return Target{Repo: *repo, Clone: clone, Base: *base, Reason: ""}
	}

	base, known := defaultOf(clone)
	if !known {
		return Target{Repo: *repo, Clone: clone, Base: "", Reason: "conn could not tell the " +
			"default branch of " + nodepath.Base(clone) + ": set a Base branch on the Atlassian " +
			"card in Settings."}
	}

	return Target{Repo: *repo, Clone: clone, Base: base, Reason: ""}
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
