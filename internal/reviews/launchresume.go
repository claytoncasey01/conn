package reviews

import (
	"os"
	"path/filepath"
	"regexp"

	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/pty"
)

// resumeFixedArguments are the arguments of a resumed review session besides its rules:
// --resume and the id, --name and the name, --model and the model.
const resumeFixedArguments = 6

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
	// Beside is whether a session named name, in any folder, or one in worktree runs now: as
	// the module's own launch refuses a second review beside one.
	Beside func(name, worktree string) bool
	// Launched is the model of the kind's launch, in the module's own store, that started the
	// session name in worktree on Claude's conversation sessionID and has not been closed, and
	// true; or false when the module launched no such session.
	Launched func(worktree, name, sessionID string) (string, bool)
}

// ResumeAtLaunch is the session that resumes, when Conn launches again, the review session named
// name that ran in worktree on Claude's conversation sessionID at the last quit, and true; or
// false when it may not be resumed: the module is off, the id is not a UUID, the name is not the
// kind's, the worktree is not exactly two folders under Root (each, and Root, a real folder of
// the person's own, no link), is not a git worktree, its repository is not allowed, a session
// of the name or in the worktree runs already, or the module's store holds no open launch of it.
// The session is claude resuming that conversation under its name, on the model it was launched
// with, with the kind's disallowed rules.
func ResumeAtLaunch(rule ResumeRule, name, worktree, sessionID string) (pty.Spec, bool) {
	if !rule.On() {
		return pty.Spec{}, false
	}

	model, launched := rule.Launched(worktree, name, sessionID)
	if !launched {
		return pty.Spec{}, false
	}

	args, err := ResumeArgs(sessionID, name, model, rule.Pattern, rule.Disallowed)
	if err != nil {
		return pty.Spec{}, false
	}

	if !ownWorktree(rule.Root, worktree) {
		return pty.Spec{}, false
	}

	if !rule.Allowed(worktree) {
		return pty.Spec{}, false
	}

	if rule.Beside(name, worktree) {
		return pty.Spec{}, false
	}

	return ReviewSpec(rule.Claude, args, worktree, name), true
}

// ResumeArgs are the arguments claude resumes a review session with, after the program: the
// rules it can never use, then the conversation, its name and its model, each one argument. The
// id must be a UUID, the name the kind's and the model one Conn launches with: claude takes the
// model per process, so a resume without it would run on claude's default.
func ResumeArgs(sessionID, name, model string, pattern *regexp.Regexp, disallowed []string) (
	[]string, error,
) {
	if !cmux.IsSessionID(sessionID) {
		return nil, cmux.ErrNotSessionID
	}

	if !pattern.MatchString(name) {
		return nil, errNotReviewName
	}

	if !isModel(model) {
		return nil, errNotModel
	}

	args, err := ruleArgs(disallowed, resumeFixedArguments)
	if err != nil {
		return nil, err
	}

	// The rules' list ends at --resume, the next option.
	return append(args, "--resume", sessionID, "--name", name, "--model", model), nil
}

// LaunchedIn is the model of the newest launch in file that started the session name in worktree
// and has not been closed, on Claude's conversation sessionID (or one not pinned yet), and true;
// or false when there is none.
func LaunchedIn(file *File, worktree, name, sessionID string) (string, bool) {
	model, found := "", false

	for _, key := range file.PRs.Keys() {
		entry, _ := file.PRs.Get(key)

		for _, launch := range entry.Launches {
			if launchedAs(launch, worktree, name, sessionID) {
				model, found = launch.Model, true
			}
		}
	}

	return model, found
}

// launchedAs is whether launch started the session name in worktree on sessionID (or on a
// conversation not pinned yet) and has not been closed.
func launchedAs(launch *Launch, worktree, name, sessionID string) bool {
	if launch.ClosedAt != nil {
		return false
	}

	if launch.Worktree != worktree {
		return false
	}

	if launch.Name != name {
		return false
	}

	if launch.SessionID == nil {
		return true
	}

	return *launch.SessionID == sessionID
}

// ownWorktree is whether worktree is <root>/<folder>/<clone name>, written plainly, with root,
// the folder and the worktree each a real folder of the person's own, and a .git in it that is a
// folder or a file (a worktree's pointer), never a link: a link could lend it another clone's
// repository.
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

	info, err := os.Lstat(filepath.Join(worktree, ".git"))
	if err != nil {
		return false
	}

	if info.IsDir() {
		return true
	}

	return info.Mode().IsRegular()
}
