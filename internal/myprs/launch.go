package myprs

// Launching a self-review: a worktree of the local clone on a new branch at the PR's head, the
// prompt in a file, and a session running claude where the settings say. Only an integer PR
// number and round, a validated repo and branch name, the fixed name and model and the constant
// deny rules ever reach an argument or the prompt. A PR title, an author, a thread or anything
// else from GitHub or Slack is not an input of anything here.

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/nodepath"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	// partUnitsMax is the longest repo owner or name a self-review folder takes.
	partUnitsMax = 60
	// hashShown is how much of the repo's hash a folder name holds, in hex digits.
	hashShown = 8
	// gitTool is the git program's command name in a tool rule.
	worktreeCommand = "worktree"
)

var (
	errNameTooLong   = errors.New("the repo name is too long")
	errOwnerTooLong  = errors.New("the repo owner is too long")
	errNotARound     = errors.New("not a round")
	errNotSelfName   = errors.New("not a self-review name")
	errUnsafeRef     = errors.New("not a safe branch name")
	errNotWorktree   = errors.New("not a worktree path")
	errNotThisRound  = errors.New("the name is not this PR round's")
	errNoRepoPath    = errors.New("no repo path")
	errNoDataFolder  = errors.New("no data folder")
	controlCharacter = regexp.MustCompile(`[\x00-\x1f]`)
	shaShape         = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	promptField      = regexp.MustCompile(`\{(repo|pr|round|worktree|headRef|branch)\}`)
)

// SelfDisallowed is what a self-review session can never do, whatever its settings allow: the
// obvious ways to break the prompt's own rules (post to Slack, comment on, review or merge a
// PR, force a push). Rules are prefixes, so they block the plain forms only, and `gh api
// graphql`, which can write too, stays usable, since the session lists the threads with it.
func SelfDisallowed() []string {
	return append(claude.SlackWrite(),
		"Bash(gh pr comment:*)",
		"Bash(gh pr review:*)",
		"Bash(gh pr merge:*)",
		"Bash(git push --force:*)",
		"Bash(git push -f:*)",
		"Bash(git push --force-with-lease:*)",
	)
}

// Self is how self-review sessions are named and what they start with.
type Self struct {
	// Pattern is the shape a self-review name has (config.SelfNamePattern).
	Pattern *regexp.Regexp
	// Prompt is the prompt; {repo}, {pr}, {round}, {worktree}, {headRef} and {branch} are
	// filled in.
	Prompt string
}

// repoName is the part of `owner/repo` after the slash, lowercased.
func repoName(repo string) (string, error) {
	if !repos.IsName(repo) {
		return "", errNotRepoName
	}

	_, name, _ := strings.Cut(strings.ToLower(repo), "/")
	if js.Len(name) > partUnitsMax {
		return "", errNameTooLong
	}

	return name, nil
}

// ownerOf is the owner part of `owner/repo`, lowercased.
func ownerOf(repo string) (string, error) {
	if !repos.IsName(repo) {
		return "", errNotRepoName
	}

	owner, _, _ := strings.Cut(strings.ToLower(repo), "/")
	if js.Len(owner) > partUnitsMax {
		return "", errOwnerTooLong
	}

	return owner, nil
}

// Name is the session name of a self-review: self-<repo>-<n> for round 1, self-<repo>-<n>-<round>
// after.
func (s Self) Name(repo string, number, round float64) (string, error) {
	name, err := repoName(repo)
	if err != nil {
		return "", err
	}

	if !isCount(number) {
		return "", errNotNumber
	}

	if !isCount(round) {
		return "", errNotARound
	}

	out := "self-" + name + "-" + js.Number(number)
	if round > 1 {
		out += "-" + js.Number(round)
	}

	if !s.Pattern.MatchString(out) {
		return "", errNotSelfName
	}

	return out, nil
}

// Folder is the folder of a PR's self-review worktrees:
// `<dataDir>/myprs/<owner>-<repo-name>-<hash>-<n>`, the hash the first 8 hex of the sha1 of
// `owner/repo` lowercased. The owner keeps same-named repos apart, and the hash keeps apart the
// splits that join to the same text (`a-b/c` and `a/b-c`).
func Folder(dataDir, repo string, number float64) (string, error) {
	owner, err := ownerOf(repo)
	if err != nil {
		return "", err
	}

	name, err := repoName(repo)
	if err != nil {
		return "", err
	}

	if !isCount(number) {
		return "", errNotNumber
	}

	// SHA-1 here is a short, stable name for a repo, not a security measure.
	sum := sha1.Sum([]byte(owner + "/" + name))
	hash := hex.EncodeToString(sum[:])[:hashShown]

	return filepath.Join(dataDir, "myprs", owner+"-"+name+"-"+hash+"-"+js.Number(number)), nil
}

// PromptOpts are what a self-review prompt is filled in with.
type PromptOpts struct {
	Repo     string
	Number   float64
	Round    float64
	Worktree string
	HeadRef  string
}

// BuildPrompt is what the session starts with: the prompt with its placeholders filled in one
// pass, so a value is never read as a placeholder.
func (s Self) BuildPrompt(opts PromptOpts) (string, error) {
	_, err := repoName(opts.Repo)
	if err != nil {
		return "", err
	}

	if !isCount(opts.Number) {
		return "", errNotNumber
	}

	if !isCount(opts.Round) {
		return "", errNotARound
	}

	if !reviews.IsSafeRef(opts.HeadRef) {
		return "", errUnsafeRef
	}

	if opts.Worktree == "" {
		return "", errNotWorktree
	}

	if controlCharacter.MatchString(opts.Worktree) {
		return "", errNotWorktree
	}

	values := map[string]string{
		"repo": opts.Repo, "pr": js.Number(opts.Number), "round": js.Number(opts.Round),
		"worktree": opts.Worktree, "headRef": opts.HeadRef,
		"branch": "conn-self-" + js.Number(opts.Number),
	}

	return promptField.ReplaceAllStringFunc(s.Prompt, func(found string) string {
		return values[strings.Trim(found, "{}")]
	}), nil
}

// LaunchOpts are what one self-review launch opens.
type LaunchOpts struct {
	Repo    string
	Number  float64
	HeadRef string
	Model   string
	Name    string
	// RepoPath is the repo's local clone, which the worktree is made from.
	RepoPath string
	DataDir  string
	Round    float64
}

// LaunchResult is how a launch went: the worktree and the commit it was put at (read there, so
// it is the head the round starts from, not the last poll's). When only the session's start
// failed (or the settings ask for the command), the worktree and the prompt are in place and the
// command to paste comes with both.
type LaunchResult struct {
	OK            bool
	Worktree      string
	HeadSha       string
	Error         string
	ManualCommand string
}

func launchFailed(why string) LaunchResult {
	return LaunchResult{OK: false, Worktree: "", HeadSha: "", Error: why, ManualCommand: ""}
}

// launchPlan is what the checks give a launch: the prompt, claude's arguments and the worktree.
type launchPlan struct {
	prompt, worktree string
	args             []string
}

// Launch opens a self-review: it refuses a bad repo, number, round, ref, name or model before
// any call; fetches the PR branch; puts the worktree (`Folder(...)/<clone-name>`) on a local
// branch `conn-self-<n>` at it (after pruning stale worktree records); writes
// `.conn/prompt.md`; starts the session. A worktree that exists is reused only when it
// belongs to this clone, is clean and is on that branch, and is then fast-forwarded; one left
// holding a commit that is not on the PR branch is refused. A leftover branch with no worktree
// is reused only when it holds nothing the PR branch lacks. Nothing here deletes or forces
// anything. A git or write failure gives the first line of its message and opens nothing. A
// session that did not start leaves the worktree and returns the command to paste, with the
// worktree and its commit.
func (s Self) Launch(ctx context.Context, deps reviews.LaunchDeps, opts LaunchOpts) LaunchResult {
	plan, err := s.checks(opts)
	if err != nil {
		return launchFailed(err.Error())
	}

	worktree := plan.worktree
	steps := launchSteps{
		deps: deps, opts: opts, worktree: worktree,
		branch: "conn-self-" + js.Number(opts.Number), remote: "origin/" + opts.HeadRef,
		again: "press Self-review again",
	}

	// The button as the row shows it: a round has already run once the number is above 1.
	if opts.Round > 1 {
		steps.again = "press Re-review again"
	}

	// A folder that is not a worktree is left alone: nothing here deletes anything.
	folder := deps.Exists(worktree)
	if folder {
		if !deps.Exists(filepath.Join(worktree, ".git")) {
			return launchFailed(worktree + " exists but is not a git worktree; remove it and " +
				steps.again)
		}
	}

	headSha, refusal, err := steps.checkOut(ctx, folder)
	if err != nil {
		return launchFailed(reviews.FirstLine(err))
	}

	if refusal != "" {
		return launchFailed(refusal)
	}

	return steps.open(ctx, plan, headSha)
}

// checks refuse a bad repo, number, round, ref, name or model before any call.
func (s Self) checks(opts LaunchOpts) (launchPlan, error) {
	clone := nodepath.Base(opts.RepoPath)

	// `HEAD` would make the fetch write through the clone's `origin/HEAD` symref onto
	// `origin/<default branch>`; a `refs/` name is no branch name.
	for _, bad := range []bool{
		!reviews.IsSafeRef(opts.HeadRef), opts.HeadRef == "HEAD",
		strings.HasPrefix(opts.HeadRef, "refs/"),
	} {
		if bad {
			return launchPlan{}, errUnsafeRef
		}
	}

	name, err := s.Name(opts.Repo, opts.Number, opts.Round)
	if err != nil {
		return launchPlan{}, err
	}

	if opts.Name != name {
		return launchPlan{}, errNotThisRound
	}

	args, err := reviews.ClaudeArgs(opts.Name, opts.Model, s.Pattern, SelfDisallowed())
	if err != nil {
		return launchPlan{}, fmt.Errorf("%w", err)
	}

	return s.place(opts, clone, args)
}

func (s Self) place(opts LaunchOpts, clone string, args []string) (launchPlan, error) {
	switch clone {
	case "", ".", "..":
		return launchPlan{}, errNoRepoPath
	}

	if opts.DataDir == "" {
		return launchPlan{}, errNoDataFolder
	}

	// Two PRs never share a worktree: the folder is the PR's own (owner, repo, a hash of both,
	// number).
	folder, err := Folder(opts.DataDir, opts.Repo, opts.Number)
	if err != nil {
		return launchPlan{}, err
	}

	worktree := filepath.Join(folder, clone)

	prompt, err := s.BuildPrompt(PromptOpts{
		Repo: opts.Repo, Number: opts.Number, Round: opts.Round, Worktree: worktree,
		HeadRef: opts.HeadRef,
	})
	if err != nil {
		return launchPlan{}, err
	}

	return launchPlan{prompt: prompt, args: args, worktree: worktree}, nil
}

// launchSteps are the git steps of one launch.
type launchSteps struct {
	deps                            reviews.LaunchDeps
	opts                            LaunchOpts
	worktree, branch, remote, again string
}

func (l launchSteps) git(ctx context.Context, args ...string) (string, error) {
	out, err := l.deps.Git(ctx, args, "")
	if err != nil {
		return "", fmt.Errorf("%w", err)
	}

	return js.Trim(out), nil
}

// checkOut puts the worktree at the PR's head and answers the commit it is at, or why not (a
// refusal, or a git failure).
func (l launchSteps) checkOut(ctx context.Context, folder bool) (string, string, error) {
	// With a destination, so origin/<headRef> moves even in a clone whose fetch refspec does not
	// map this branch (a plain `fetch origin <ref>` only sets FETCH_HEAD).
	_, err := l.git(ctx, "-C", l.opts.RepoPath, "fetch", "origin",
		"+refs/heads/"+l.opts.HeadRef+":refs/remotes/"+l.remote)
	if err != nil {
		return "", "", err
	}

	var refusal string

	// behind is whether the worktree starts on a branch that already exists, and so must be
	// brought up to the PR's head.
	behind := folder

	if folder {
		refusal, err = l.reuse(ctx)
	} else {
		behind, refusal, err = l.add(ctx)
	}

	if err != nil {
		return "", "", err
	}

	if refusal != "" {
		return "", refusal, nil
	}

	return l.settle(ctx, folder, behind)
}

// reuse checks a worktree that is there: this clone's, clean, and on the round's branch.
func (l launchSteps) reuse(ctx context.Context) (string, error) {
	// A worktree of another clone (the same folder name under another path) must never be
	// reused: its origin is not this repo's.
	common, err := l.git(ctx, "-C", l.worktree, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}

	if !l.sameClone(common) {
		return l.worktree + " is not a worktree of the clone at " + l.opts.RepoPath +
			"; remove the folder and " + l.again, nil
	}

	status, err := l.git(ctx, "-C", l.worktree, "status", "--porcelain")
	if err != nil {
		return "", err
	}

	if status != "" {
		return l.worktree + " has uncommitted changes; commit or discard them and " + l.again,
			nil
	}

	current, err := l.git(ctx, "-C", l.worktree, "branch", "--show-current")
	if err != nil {
		return "", err
	}

	if current != l.branch {
		shown := current
		if shown == "" {
			shown = "a detached HEAD"
		}

		return l.worktree + " is on " + shown + ", not " + l.branch + "; check out " +
			l.branch + " there or remove the folder and " + l.again, nil
	}

	return "", nil
}

func (l launchSteps) sameClone(common string) bool {
	if common == "" {
		return false
	}

	return realOrAsIs(nodepath.Resolve(l.worktree, common)) ==
		realOrAsIs(nodepath.Resolve(l.opts.RepoPath, ".git"))
}

// realOrAsIs is a path with its symlinks resolved, or as written when it cannot be (a folder
// that is not there yet).
func realOrAsIs(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}

	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return path
	}

	return absolute
}

// add makes the worktree: on a leftover branch that holds nothing the PR branch lacks, or on a
// new branch at the PR's head. It answers whether the worktree must be brought up to the head.
func (l launchSteps) add(ctx context.Context) (bool, string, error) {
	repoPath := l.opts.RepoPath

	// Forget a worktree whose folder was deleted by hand, or git refuses to add one at the same
	// path.
	_, err := l.git(ctx, "-C", repoPath, worktreeCommand, "prune")
	if err != nil {
		return false, "", err
	}

	listed, err := l.git(ctx, "-C", repoPath, "branch", "--list", l.branch)
	if err != nil {
		return false, "", err
	}

	if listed == "" {
		_, err = l.git(ctx, "-C", repoPath, worktreeCommand, "add", "-b", l.branch, l.worktree,
			l.remote)

		return false, "", err
	}

	// Commits the leftover branch has that the PR branch lacks; a count is an answer, so a git
	// error stays an error.
	ahead, err := l.git(ctx, "-C", repoPath, "rev-list", "--count", l.remote+".."+l.branch)
	if err != nil {
		return false, "", err
	}

	if ahead != "0" {
		return false, "the branch " + l.branch + " has work that is not on " + l.remote +
			": delete it (or push it, only if it belongs on the PR), then " + l.again, nil
	}

	_, err = l.git(ctx, "-C", repoPath, worktreeCommand, "add", l.worktree, l.branch)

	return true, "", err
}

// settle fast-forwards a worktree that was behind and reads the commit it is at. A reused
// worktree can hold a commit an earlier round made and never pushed, which the merge leaves in
// place: the round would start from a commit that is not the PR's head. It is refused like a
// leftover branch, and nothing is reset.
func (l launchSteps) settle(ctx context.Context, folder, behind bool) (string, string, error) {
	if behind {
		_, err := l.git(ctx, "-C", l.worktree, "merge", "--ff-only", l.remote)
		if err != nil {
			return "", "", err
		}
	}

	headSha, err := l.git(ctx, "-C", l.worktree, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}

	if !folder {
		return headSha, "", nil
	}

	remoteSha, err := l.git(ctx, "-C", l.worktree, "rev-parse", l.remote)
	if err != nil {
		return "", "", err
	}

	if headSha != remoteSha {
		return "", "remove the worktree and branch " + l.branch + " (or push only if it " +
			"belongs on the PR), then " + l.again + ": " + l.worktree + " has work not on " +
			l.remote, nil
	}

	return headSha, "", nil
}

// open writes the prompt and starts the session where the settings say.
func (l launchSteps) open(ctx context.Context, plan launchPlan, headSha string) LaunchResult {
	if !shaShape.MatchString(headSha) {
		return launchFailed("could not read the commit " + l.worktree + " is at")
	}

	err := l.deps.Write(filepath.Join(l.worktree, ".conn", "prompt.md"), plan.prompt, false)
	if err != nil {
		return launchFailed(reviews.FirstLine(&FailureError{Message: settings.NodeError(err)}))
	}

	reviews.ExcludeConn(ctx, l.deps, l.worktree)

	opened := l.deps.Open(ctx, reviews.ReviewSpec(l.deps.Claude, plan.args, l.worktree,
		l.opts.Name))
	if !opened.OK {
		return LaunchResult{
			OK: false, Worktree: l.worktree, HeadSha: headSha, Error: opened.Error,
			ManualCommand: opened.ManualCommand,
		}
	}

	return LaunchResult{
		OK: true, Worktree: l.worktree, HeadSha: headSha, Error: "", ManualCommand: "",
	}
}
