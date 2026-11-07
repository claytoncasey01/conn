package reviews

import (
	"os"
	"path/filepath"
	"regexp"

	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/pty"
)

// resumeFixedArguments are the arguments of a resumed review session besides its rules:
// --resume and the id, --name and the name.
const resumeFixedArguments = 4

// ResumeRule is what a launch checks before it resumes a review or self-review session from the
// record of the hosted sessions: where Conn makes that kind's worktrees, and what its sessions
// are started with.
type ResumeRule struct {
	// Claude is the claude program as configured (CONN_CLAUDE).
	Claude string
	// Root is Conn's own folder of the kind's worktrees: each is <Root>/<folder>/<clone name>.
	Root string
	// Pattern is the shape the kind's session names have.
	Pattern *regexp.Regexp
	// Disallowed are the permission rules the kind's sessions can never use.
	Disallowed []string
	// On is whether the module that starts the kind's sessions is on now.
	On func() bool
	// Allowed is whether the repository of a folder is on the allowlist now.
	Allowed func(folder string) bool
}

// ResumeAtLaunch is the session that resumes, when Conn launches again, the review session named
// name that ran in worktree on Claude's conversation sessionID at the last quit, and true; or
// false when it may not be resumed: the module is off, the id is not a UUID, the name is not the
// kind's, the worktree is not exactly two folders under Root (each, and Root, a real folder of
// the person's own, no link), is not a git worktree, or its repository is not allowed. The session
// is claude resuming that conversation under its name, with the kind's disallowed rules.
func ResumeAtLaunch(rule ResumeRule, name, worktree, sessionID string) (pty.Spec, bool) {
	if !rule.On() {
		return pty.Spec{}, false
	}

	args, err := ResumeArgs(sessionID, name, rule.Pattern, rule.Disallowed)
	if err != nil {
		return pty.Spec{}, false
	}

	if !ownWorktree(rule.Root, worktree) {
		return pty.Spec{}, false
	}

	if !rule.Allowed(worktree) {
		return pty.Spec{}, false
	}

	return ReviewSpec(rule.Claude, args, worktree, name), true
}

// ResumeArgs are the arguments claude resumes a review session with, after the program: the
// rules it can never use, then the conversation and its name, each one argument. The id must be a
// UUID and the name the kind's.
func ResumeArgs(sessionID, name string, pattern *regexp.Regexp, disallowed []string) (
	[]string, error,
) {
	if !cmux.IsSessionID(sessionID) {
		return nil, cmux.ErrNotSessionID
	}

	if !pattern.MatchString(name) {
		return nil, errNotReviewName
	}

	args, err := ruleArgs(disallowed, resumeFixedArguments)
	if err != nil {
		return nil, err
	}

	// The rules' list ends at --resume, the next option.
	return append(args, "--resume", sessionID, "--name", name), nil
}

// ownWorktree is whether worktree is <root>/<folder>/<clone name>, written plainly, with root,
// the folder and the worktree each a real folder of the person's own, and a .git in it.
func ownWorktree(root, worktree string) bool {
	if !filepath.IsAbs(worktree) {
		return false
	}

	if filepath.Clean(worktree) != worktree {
		return false
	}

	parent := filepath.Dir(worktree)
	if filepath.Dir(parent) != root {
		return false
	}

	for _, folder := range []string{root, parent, worktree} {
		if CheckOwnFolder(folder) != nil {
			return false
		}
	}

	_, err := os.Lstat(filepath.Join(worktree, ".git"))

	return err == nil
}
