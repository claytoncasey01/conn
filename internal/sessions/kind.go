package sessions

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Session kinds.
const (
	KindBuild      = "build"
	KindAdhoc      = "adhoc"
	KindReview     = "review"
	KindSelfReview = "selfreview"
)

// KindRules are what tells the kinds apart: the review and self-review name patterns, and where
// conn keeps its review worktrees.
type KindRules struct {
	ReviewName  *regexp.Regexp
	SelfName    *regexp.Regexp
	ReviewsRoot string
}

// KindOf is a session's kind. One Open work started is named after its run key; one a Reviews
// click started is named after its PR and works in a review worktree under ReviewsRoot, so a
// session that merely looks like one elsewhere is not labelled a review; one a My PRs
// Self-Review press started is named after its repo and PR; every other session (an unnamed one
// too, whose name is its session-id prefix) is ad hoc. A run key wins over the patterns. A nil
// cwd is a session whose folder is not known: its name alone decides.
func KindOf(name string, cwd *string, runKeys map[string]bool, rules KindRules) string {
	if runKeys[name] {
		return KindBuild
	}

	if rules.ReviewName.MatchString(name) {
		if cwd == nil {
			return KindReview
		}

		if strings.HasPrefix(*cwd, rules.ReviewsRoot+string(filepath.Separator)) {
			return KindReview
		}
	}

	if rules.SelfName.MatchString(name) {
		return KindSelfReview
	}

	return KindAdhoc
}

// within is whether child is parent or inside it: a path comparison, nothing read from disk.
func within(parent, child string) bool {
	parentPath, err := filepath.Abs(parent)
	if err != nil {
		return false
	}

	childPath, err := filepath.Abs(child)
	if err != nil {
		return false
	}

	relative, err := filepath.Rel(parentPath, childPath)
	if err != nil {
		return false
	}

	if relative == "." {
		return true
	}

	if strings.HasPrefix(relative, "..") {
		return false
	}

	return !filepath.IsAbs(relative)
}

// StartedByConn is whether conn itself started a session, so it stays on Sessions while its
// repo is off the list.
//   - An Open work run: the session carries the run's key as its name and works in that run's
//     recorded worktree (runWorktree, "" when there is no run). A name alone is a Jira key anyone
//     can give a session.
//   - A review or self-review is known by its name, which anyone can give a session, so it
//     counts only with a folder under conn's data folder.
func StartedByConn(kind, cwd, dataDir, runWorktree string) bool {
	if kind == KindBuild {
		if runWorktree == "" {
			return false
		}

		return within(runWorktree, cwd)
	}

	switch kind {
	case KindReview, KindSelfReview:
	default:
		return false
	}

	dataPath, err := filepath.Abs(dataDir)
	if err != nil {
		return false
	}

	cwdPath, err := filepath.Abs(cwd)
	if err != nil {
		return false
	}

	if dataPath == cwdPath {
		return false
	}

	return within(dataDir, cwd)
}
