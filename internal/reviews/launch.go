package reviews

// Launching a review: a detached worktree at the PR's base branch, the prompt in a file, and a
// session running claude where the settings say. Only an integer PR number, a validated branch
// name and the fixed name and model ever reach an argument; the prompt is read from its file. A
// PR title, an author or anything else from GitHub or Slack is not an input of anything here.

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/repos"
)

const (
	// nameMax is the longest plain review name; a longer one, or one whose owner GitHub would not
	// allow, takes the hashed form.
	nameMax = 120
	// hashShown is how much of the hash a hashed name holds, in hex digits.
	hashShown = 12
	// lineUnits is how much of a failure's first line is kept.
	lineUnits = 400
	// sharedFileMode is the mode a shared file (the clone's exclude file) is made with.
	sharedFileMode = 0o600
	// folderMode is the mode the prompt's folder is made with, as mkdir makes one: the umask
	// takes its share.
	folderMode = 0o777
	// gitWorktree is git's worktree command.
	gitWorktree = "worktree"
	// claudeFixedArguments are the arguments of a review session besides its rules:
	// --disallowedTools, --name and the name, --model and the model, --, and readPrompt.
	claudeFixedArguments = 7
	// readPrompt is a review session's first message: the prompt is in its file.
	readPrompt = "Read .conn/prompt.md and follow it exactly."
)

var (
	errNotLaunchCount = errors.New("not a launch count")
	errNotReviewName  = errors.New("not a review name")
	errNotModel       = errors.New("not a review model")
	errNotToolRule    = errors.New("not a tool rule")
	errNotSha         = errors.New("not a commit sha")

	launchHead  = regexp.MustCompile(`^pr-(\d+)(?:\.(\d+))?-`)
	shaShape    = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
	safeRef     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	control     = regexp.MustCompile(`[\x00-\x1f]`)
	promptField = regexp.MustCompile(`\{(pr|repo)\}`)
	remoteShape = regexp.MustCompile(`^(?:` + anyCase("git@github.com:") + `|` +
		anyCase("ssh://git@github.com/") + `|` + anyCase("https://github.com/") +
		`)([^/]+/[^/]+?)(?:` + anyCase(".git") + `)?/?$`)
	connExcluded = regexp.MustCompile(`(?m)^\.conn/?$`)
)

// anyCase is a regexp for text in any ASCII case. Go's (?i) folds more than JavaScript's i does
// (a long s for an s, a Kelvin sign for a k), so the cases are spelled out.
func anyCase(literal string) string {
	var out strings.Builder

	for _, char := range literal {
		upper := strings.ToUpper(string(char))
		lower := strings.ToLower(string(char))

		if upper == lower {
			out.WriteString(regexp.QuoteMeta(string(char)))

			continue
		}

		out.WriteString("[" + lower + upper + "]")
	}

	return out.String()
}

// splitRepo is the owner and name of `owner/repo`, lowercase.
func splitRepo(repo string) (string, string, error) {
	if !repos.IsName(repo) {
		return "", "", errNotRepo
	}

	owner, name, _ := strings.Cut(strings.ToLower(repo), "/")

	return owner, name, nil
}

// Names is how review sessions are named: the pattern a name must match.
type Names struct {
	Pattern *regexp.Regexp
}

// ReviewName is the session name of a review, and of its worktree folder:
// `pr-<n>-<owner>--<repo>` for the first launch, `pr-<n>.<k>-<owner>--<repo>` for the k-th (k
// from 2; previous is how many launches the PR already has). It is one-to-one over (owner, repo,
// number, launch): a GitHub owner never holds two dashes in a row or ends with one, and a repo
// never starts with one, so the first `--` ends the owner. An owner that breaks that rule, or a
// name longer than nameMax, takes `pr-<n>[.<k>]-x<first 12 hex of sha1(owner/repo)>` instead,
// which holds no `--` and so never reads as a plain one.
func (n Names) ReviewName(repo string, number int64, previous int) (string, error) {
	owner, name, err := splitRepo(repo)
	if err != nil {
		return "", err
	}

	if !safeNumber(number) {
		return "", errNotNumber
	}

	if previous < 0 {
		return "", errNotLaunchCount
	}

	head := "pr-" + strconv.FormatInt(number, 10)
	if previous > 0 {
		head += "." + strconv.Itoa(previous+1)
	}

	full := head + "-" + owner + "--" + name
	if hashedName(owner, full) {
		// SHA-1 here is a short, stable name for a repo, not a security measure.
		sum := sha1.Sum([]byte(owner + "/" + name))
		full = head + "-x" + hex.EncodeToString(sum[:])[:hashShown]
	}

	if !n.Pattern.MatchString(full) {
		return "", errNotReviewName
	}

	return full, nil
}

func hashedName(owner, plain string) bool {
	if strings.Contains(owner, "--") {
		return true
	}

	if strings.HasSuffix(owner, "-") {
		return true
	}

	return len(plain) > nameMax
}

// IsReviewNameOf is whether name is the name of some launch of PR number of repo (the first, or
// the k-th): it is rebuilt, never parsed for a repo.
func (n Names) IsReviewNameOf(name, repo string, number int64) bool {
	match := launchHead.FindStringSubmatch(name)
	if match == nil {
		return false
	}

	if js.ToNumber(match[1]) != float64(number) {
		return false
	}

	nth := 1.0
	least := 1.0

	if match[2] != "" {
		nth = js.ToNumber(match[2])
		least = 2
	}

	if nth > safeIntegerMax {
		return false
	}

	if nth < least {
		return false
	}

	rebuilt, err := n.ReviewName(repo, number, int(nth)-1)
	if err != nil {
		return false
	}

	return rebuilt == name
}

// ReviewFolder is the folder of a PR's review worktrees under `<dataDir>/reviews`: the name of
// its first launch.
func (n Names) ReviewFolder(repo string, number int64) (string, error) {
	return n.ReviewName(repo, number, 0)
}

// ReviewWorktree is where a PR's review worktree is: `<dataDir>/reviews/<folder>/<repo name>`.
func (n Names) ReviewWorktree(dataDir, repo string, number int64) (string, error) {
	folder, err := n.ReviewFolder(repo, number)
	if err != nil {
		return "", err
	}

	_, name, _ := splitRepo(repo)

	return filepath.Join(dataDir, "reviews", folder, name), nil
}

// Prompts are the texts a review session starts with.
type Prompts struct {
	// Review is the prompt; {pr} and {repo} are filled in.
	Review string
	// ReReview is the line a re-review adds; {sha} is the commit of the last review.
	ReReview string
}

// BuildPrompt is what the review session starts with. The number is an integer, the repo an
// `owner/repo` and the sha hex, so nothing else can be written into the file.
func (p Prompts) BuildPrompt(number int64, reReviewSha *string, repo string) (string, error) {
	if !safeNumber(number) {
		return "", errNotNumber
	}

	if !repos.IsName(repo) {
		return "", errNotRepo
	}

	values := map[string]string{"pr": strconv.FormatInt(number, 10), "repo": repo}
	first := promptField.ReplaceAllStringFunc(p.Review, func(field string) string {
		return values[strings.Trim(field, "{}")]
	})

	if reReviewSha == nil {
		return first, nil
	}

	if !shaShape.MatchString(*reReviewSha) {
		return "", errNotSha
	}

	return first + "\n" + strings.Replace(p.ReReview, "{sha}", *reReviewSha, 1), nil
}

// ClaudeArgs are the arguments claude starts a review session with, after the program: the
// rules it can never use, its name, its model, and the one instruction to read the prompt from
// its file, each one argument, never a shell string. The prompt itself is only in the file, so no
// text of it is ever in an argument or a command line. pattern is the name shape the session
// kind allows. disallowed are permission rules the session can never use; the list is variadic,
// so it goes first and --name ends it.
func ClaudeArgs(name, model string, pattern *regexp.Regexp, disallowed []string) (
	[]string, error,
) {
	if !pattern.MatchString(name) {
		return nil, errNotReviewName
	}

	if !isModel(model) {
		return nil, errNotModel
	}

	args := make([]string, 0, len(disallowed)+claudeFixedArguments)

	if len(disallowed) > 0 {
		args = append(args, "--disallowedTools")
	}

	for _, rule := range disallowed {
		if rule == "" {
			return nil, errNotToolRule
		}

		if control.MatchString(rule) {
			return nil, errNotToolRule
		}

		args = append(args, rule)
	}

	// -- ends the options (and the rules' list): the prompt after it is never read as one.
	return append(args, "--name", name, "--model", model, "--", readPrompt), nil
}

// IsSafeRef is whether a branch name is safe as a git argument and in a path: it starts with a
// letter or digit, holds no '..', and keeps to a short plain alphabet.
func IsSafeRef(ref string) bool {
	if !safeRef.MatchString(ref) {
		return false
	}

	return !strings.Contains(ref, "..")
}

// RemoteMatches is whether a git remote URL is GitHub's ssh or https address of repo
// ("owner/name"), whatever the case or a trailing .git.
func RemoteMatches(url, repo string) bool {
	if repo == "" {
		return false
	}

	match := remoteShape.FindStringSubmatch(url)
	if match == nil {
		return false
	}

	return same(match[1], repo)
}

// WriteLaunchFile writes a file, making its folder first. A file of Conn's own (the prompt) is
// private: whatever is at the path is removed first, so a link is never followed and the file is
// always 0600. A shared one (the clone's exclude file) is written in place, its mode and link
// kept.
func WriteLaunchFile(path, content string, shared bool) error {
	err := os.MkdirAll(filepath.Dir(path), folderMode)
	if err != nil {
		return asIs(err)
	}

	if shared {
		return asIs(os.WriteFile(path, []byte(content), sharedFileMode))
	}

	// A link is removed, never followed; a folder is left for the write to fail on.
	info, err := os.Lstat(path)
	if err == nil {
		if !info.IsDir() {
			err = os.Remove(path)
			if err != nil {
				return asIs(err)
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return asIs(err)
	}

	// O_EXCL refuses whatever appeared at the path since: the file is a new one, made 0600.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFileMode)
	if err != nil {
		return asIs(err)
	}

	_, err = file.WriteString(content)
	closeErr := file.Close()

	if err != nil {
		return asIs(err)
	}

	if closeErr != nil {
		return asIs(closeErr)
	}

	return asIs(os.Chmod(path, privateFileMode))
}

// FirstLine is the first line of an error that is not blank, trimmed and cut; "git failed" when
// there is none.
func FirstLine(err error) string {
	for line := range strings.SplitSeq(err.Error(), "\n") {
		trimmed := js.Trim(line)
		if trimmed != "" {
			return js.Slice(trimmed, lineUnits)
		}
	}

	return "git failed"
}

// LaunchDeps are the effects of launching a review.
type LaunchDeps struct {
	// Git runs git without a shell; its error's message is git's stderr, so its first line is
	// the reason.
	Git func(ctx context.Context, args []string, dir string) (string, error)
	// Open starts the review session where the settings say sessions open.
	Open launch.Opener
	// Claude is the claude program as configured (CONN_CLAUDE): the session's program.
	Claude string
	// Write writes a file (WriteLaunchFile).
	Write func(path, content string, shared bool) error
	// Exists is whether a path exists.
	Exists func(path string) bool
	// Read is the text of a file that exists.
	Read func(path string) (string, error)
	// Origin is a folder's GitHub origin, lowercase ("" for none).
	Origin func(folder string) string
	Names  Names
	// Prompts are the review prompts.
	Prompts Prompts
}

// LaunchOpts are what one launch opens.
type LaunchOpts struct {
	Repo    string
	Number  int64
	BaseRef string
	Model   string
	Name    string
	// RepoPath is the repo's local clone, which the worktree is made from.
	RepoPath string
	DataDir  string
	// ReReviewSha is the commit of the last review, for a re-review; nil for a first one.
	ReReviewSha *string
}

// LaunchResult is how a launch went: the worktree, or why not (with the command to paste when
// only the start of the session failed, or the settings ask for the command).
type LaunchResult struct {
	OK            bool
	Worktree      string
	Error         string
	ManualCommand string
}

func launchFailed(why string) LaunchResult {
	return LaunchResult{OK: false, Worktree: "", Error: why, ManualCommand: ""}
}

// ExcludeConn adds `.conn/` to the worktree's exclude file; best effort, since the review does
// not depend on it.
func ExcludeConn(ctx context.Context, deps LaunchDeps, worktree string) {
	out, err := deps.Git(ctx, []string{"rev-parse", "--git-path", "info/exclude"}, worktree)
	if err != nil {
		return
	}

	exclude := js.Trim(out)

	path := exclude
	if !strings.HasPrefix(exclude, "/") {
		path = filepath.Join(worktree, exclude)
	}

	current := ""

	if deps.Exists(path) {
		current, err = deps.Read(path)
		if err != nil {
			return
		}
	}

	if connExcluded.MatchString(jsLines(current)) {
		return
	}

	separator := ""

	if current != "" {
		if !strings.HasSuffix(current, "\n") {
			separator = "\n"
		}
	}

	_ = deps.Write(path, current+separator+".conn/\n", true)
}

// jsLines makes every line end JavaScript's multiline mode knows (\r, U+2028, U+2029) a \n, so
// Go's (?m) sees the same lines.
func jsLines(content string) string {
	return strings.NewReplacer("\r", "\n", " ", "\n", " ", "\n").Replace(content)
}

// Open opens a review: it refuses a bad repo, number, ref, name, model or sha before any call;
// fetches the base branch in the clone; puts the worktree on it detached (after pruning stale
// worktree records); writes .conn/prompt.md; starts the session (ReviewSpec) where the settings
// say. A git or write failure gives the first line of its message and opens nothing. A session
// that did not start leaves the worktree and returns the command to paste. An existing folder is
// reused only when it is a worktree whose origin is this repo.
func Open(ctx context.Context, deps LaunchDeps, opts LaunchOpts) LaunchResult {
	prompt, args, err := launchChecks(deps, opts)
	if err != nil {
		return launchFailed(err.Error())
	}

	worktree, err := deps.Names.ReviewWorktree(opts.DataDir, opts.Repo, opts.Number)
	if err != nil {
		return launchFailed(err.Error())
	}

	// A folder that is not a worktree is left alone: nothing here deletes anything.
	folder := deps.Exists(worktree)
	if folder {
		if !deps.Exists(filepath.Join(worktree, ".git")) {
			return launchFailed(worktree +
				" exists but is not a git worktree; remove it and press REVIEW again")
		}

		// A worktree of another repo's clone is never reused.
		if deps.Origin(worktree) != strings.ToLower(opts.Repo) {
			return launchFailed(worktree + " is not a worktree of " + opts.Repo +
				"; remove it and press REVIEW again")
		}
	}

	err = checkOut(ctx, deps, opts, worktree, folder)
	if err != nil {
		return launchFailed(FirstLine(err))
	}

	err = deps.Write(filepath.Join(worktree, ".conn", "prompt.md"), prompt, false)
	if err != nil {
		return launchFailed(FirstLine(nodeError(err)))
	}

	ExcludeConn(ctx, deps, worktree)

	opened := deps.Open(ctx, ReviewSpec(deps.Claude, args, worktree, opts.Name))
	if !opened.OK {
		// With a command to paste, the worktree is the launch's: the session it starts is
		// tracked like any other.
		kept := ""
		if opened.ManualCommand != "" {
			kept = worktree
		}

		return LaunchResult{
			OK: false, Worktree: kept, Error: opened.Error, ManualCommand: opened.ManualCommand,
		}
	}

	return LaunchResult{OK: true, Worktree: worktree, Error: "", ManualCommand: ""}
}

var (
	errUnsafeRef = errors.New("not a safe branch name")
	errNotThisPR = errors.New("the name is not this PR's")
	errNoClone   = errors.New("no clone of the repo")
	errNoData    = errors.New("no data folder")
)

// ReviewSpec is what a review session runs: the claude program, its arguments (ClaudeArgs), in
// the worktree, under the review's name.
func ReviewSpec(claude string, args []string, worktree, name string) pty.Spec {
	return pty.Spec{
		Argv: append([]string{claude}, args...), Dir: worktree, Env: nil, Name: name,
	}
}

// launchChecks refuses what must never reach a command, and answers the prompt and claude's
// arguments.
func launchChecks(deps LaunchDeps, opts LaunchOpts) (string, []string, error) {
	if !IsSafeRef(opts.BaseRef) {
		return "", nil, errUnsafeRef
	}

	prompt, err := deps.Prompts.BuildPrompt(opts.Number, opts.ReReviewSha, opts.Repo)
	if err != nil {
		return "", nil, err
	}

	args, err := ClaudeArgs(opts.Name, opts.Model, deps.Names.Pattern, nil)
	if err != nil {
		return "", nil, err
	}

	if !deps.Names.IsReviewNameOf(opts.Name, opts.Repo, opts.Number) {
		return "", nil, errNotThisPR
	}

	if !absolutePath(opts.RepoPath) {
		return "", nil, errNoClone
	}

	if !strings.HasPrefix(opts.DataDir, "/") {
		return "", nil, errNoData
	}

	return prompt, args, nil
}

func absolutePath(path string) bool {
	if !strings.HasPrefix(path, "/") {
		return false
	}

	return !strings.Contains(path, "\x00")
}

// checkOut puts the worktree on the base branch, detached: the folder that is there, or a new
// worktree after forgetting one whose folder was deleted by hand.
func checkOut(
	ctx context.Context, deps LaunchDeps, opts LaunchOpts, worktree string, folder bool,
) error {
	base := "origin/" + opts.BaseRef

	_, err := deps.Git(ctx, []string{"-C", opts.RepoPath, "fetch", "origin", opts.BaseRef}, "")
	if err != nil {
		return err
	}

	if folder {
		_, err = deps.Git(ctx, []string{"-C", worktree, "checkout", "--detach", base}, "")

		return err
	}

	_, err = deps.Git(ctx, []string{"-C", opts.RepoPath, gitWorktree, "prune"}, "")
	if err != nil {
		return err
	}

	_, err = deps.Git(ctx, []string{
		"-C", opts.RepoPath, gitWorktree, "add", "--detach", worktree, base,
	}, "")

	return err
}
