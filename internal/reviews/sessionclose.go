package reviews

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/claytoncasey01/conn/internal/nodepath"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	statusIdle = "idle"
	inputEmpty = "empty"
	codeOK     = 200
	codeBusy   = 409
	// foldersUpMax bounds the walk up a path that does not exist yet.
	foldersUpMax = 4096
)

var gitdirLine = regexp.MustCompile(`^gitdir:[` + js.SpaceClass + `]*(.+?)[` + js.SpaceClass +
	`]*$`)

// WorktreeClone is the clone a linked worktree belongs to: its .git file says `gitdir:
// <clone>/.git/worktrees/<name>`, and that gitdir's commondir leads back to `<clone>/.git`. ""
// when the folder is not such a worktree. Read from the disk, never from git.
func WorktreeClone(worktree string) string {
	pointer, err := os.ReadFile(filepath.Join(worktree, ".git"))
	if err != nil {
		return ""
	}

	match := gitdirLine.FindStringSubmatch(firstFileLine(js.DecodeUTF8(pointer, false)))
	if match == nil {
		return ""
	}

	gitDir := nodepath.Resolve(worktree, match[1])

	common, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return ""
	}

	shared := js.Trim(firstFileLine(js.DecodeUTF8(common, false)))
	if shared == "" {
		shared = "."
	}

	dotGit := nodepath.Resolve(gitDir, shared)
	if nodepath.Base(dotGit) != ".git" {
		return ""
	}

	return nodepath.Dir(dotGit)
}

// firstFileLine is the text before the first \r\n or \n.
func firstFileLine(content string) string {
	line, _, _ := strings.Cut(content, "\n")

	return strings.TrimSuffix(line, "\r")
}

// Real is a path with its symlinks resolved. A part that does not exist is kept as written,
// after the deepest part that does.
func Real(path string) string {
	var rest []string

	current := path

	for range foldersUpMax {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			absolute, absErr := filepath.Abs(resolved)
			if absErr == nil {
				parts := append([]string{absolute}, reversed(rest)...)

				return filepath.Join(parts...)
			}
		}

		if nodepath.Dir(current) == current {
			return path
		}

		rest = append(rest, nodepath.Base(current))
		current = nodepath.Dir(current)
	}

	return path
}

func reversed(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range slices.Backward(items) {
		out = append(out, item)
	}

	return out
}

// Within is whether cwd is folder or under it.
func Within(cwd, folder string) bool {
	if cwd == folder {
		return true
	}

	return strings.HasPrefix(cwd, folder+"/")
}

// ownsWorktree is whether a launch's worktree is exactly where Conn puts that PR's, or where it
// put it before Reviews read several repos (`reviews/pr-<n>/<clone folder>`): only such a launch
// is ever matched to a session, closed or swept.
func (r *Reviews) ownsWorktree(key string, launch *Launch) bool {
	parts, ok := ParseKey(key)
	if !ok {
		return false
	}

	if nodepath.Normalize(launch.Worktree) != launch.Worktree {
		return false
	}

	current, err := r.names.ReviewWorktree(r.dataDir(), parts.Repo, parts.Number)
	if err != nil {
		return false
	}

	if launch.Worktree == current {
		return true
	}

	old := filepath.Join(r.dataDir(), "reviews", "pr-"+strconv.FormatInt(parts.Number, 10))
	folder := nodepath.Base(launch.Worktree)

	if nodepath.Dir(launch.Worktree) != old {
		return false
	}

	return !anyOf(folder == "", folder == ".", folder == "..")
}

// launchRef is one launch with the key of the PR it is for.
type launchRef struct {
	key   string
	entry *Entry
}

// entries are the file's records as they are now, for a walk that lets go of the mutex.
func (r *Reviews) entries() []launchRef {
	if r.file == nil {
		return nil
	}

	out := make([]launchRef, 0, r.file.PRs.Len())

	for _, key := range r.file.PRs.Keys() {
		entry, _ := r.file.PRs.Get(key)
		out = append(out, launchRef{key: key, entry: entry})
	}

	return out
}

// autoClose closes the sessions Conn launched that are finished, one at a time. It starts on
// the tick: every launch is looked at there until one needs its screen read, and the rest (that
// read, and the launches after it) continues on a goroutine of its own. So a launch whose
// session is gone is marked closed before the tick starts a poll, whose sweep then sees it
// closed.
func (r *Reviews) autoClose(refs []launchRef, refAt, launchAt int, inline bool) {
	for ; refAt < len(refs); refAt++ {
		ref := refs[refAt]

		// The launches as they are when each is reached: one started meanwhile is looked at too.
		for ; launchAt < len(ref.entry.Launches); launchAt++ {
			launch := ref.entry.Launches[launchAt]
			if launch.ClosedAt != nil {
				continue
			}

			// A launch that points anywhere else is not one Conn made: nothing is matched or
			// closed for it, and it is not looked at again.
			if !r.ownsWorktree(ref.key, launch) {
				r.markClosed(launch, r.now())

				continue
			}

			check := r.checkLaunch(ref.key, launch)
			if check == nil {
				continue
			}

			if inline {
				nextRef, nextLaunch := refAt, launchAt+1

				r.spawn(func() {
					r.readScreen(check)
					r.autoClose(refs, nextRef, nextLaunch, false)
				})

				return
			}

			r.readScreen(check)
		}

		launchAt = 0
	}

	r.closing = false
}

func (r *Reviews) markClosed(launch *Launch, now float64) {
	launch.ClosedAt = &now

	if launch.SessionID != nil {
		delete(r.idleSince, *launch.SessionID)
		delete(r.nextLook, *launch.SessionID)
	}

	r.save()
	r.bump()
}

// sessionOf is the live session a launch is running in, by the same rules checkLaunch pins by,
// with no side effect; nil when there is none.
func (r *Reviews) sessionOf(launch *Launch, live []sessions.LiveSession) *sessions.LiveSession {
	if launch.ClosedAt != nil {
		return nil
	}

	if launch.SessionID == nil {
		return find(live, func(x *sessions.LiveSession) bool {
			return all(x.Cwd == launch.Worktree, x.Name == launch.Name)
		})
	}

	found := find(live, func(x *sessions.LiveSession) bool {
		return all(x.SessionID == *launch.SessionID, x.Cwd == launch.Worktree)
	})
	if found != nil {
		return found
	}

	pid, seen := r.seenPid[launch]
	if !seen {
		return nil
	}

	return find(live, func(x *sessions.LiveSession) bool {
		return all(x.PID == pid, x.Cwd == launch.Worktree)
	})
}

func find(
	live []sessions.LiveSession, match func(*sessions.LiveSession) bool,
) *sessions.LiveSession {
	for index := range live {
		if match(&live[index]) {
			return &live[index]
		}
	}

	return nil
}

// claimed are the session ids some launch has pinned.
func (r *Reviews) claimed() map[string]bool {
	out := map[string]bool{}

	for _, ref := range r.entries() {
		for _, launch := range ref.entry.Launches {
			if launch.SessionID != nil {
				out[*launch.SessionID] = true
			}
		}
	}

	return out
}

// pinned is the live session of a launch: the pinned one, in the launch's own folder; for a
// launch not pinned yet, an unclaimed session in that folder named for the launch; for one whose
// session id is gone, a live unclaimed session in the folder that is the same process it was
// last seen as (a /clear keeps the pid and changes the id).
func (r *Reviews) pinned(launch *Launch, live []sessions.LiveSession) *sessions.LiveSession {
	claimed := r.claimed()

	if launch.SessionID == nil {
		return find(live, func(x *sessions.LiveSession) bool {
			return all(x.Cwd == launch.Worktree, x.Name == launch.Name, !claimed[x.SessionID])
		})
	}

	found := find(live, func(x *sessions.LiveSession) bool {
		return all(x.SessionID == *launch.SessionID, x.Cwd == launch.Worktree)
	})
	if found != nil {
		return found
	}

	pid, seen := r.seenPid[launch]
	if !seen {
		return nil
	}

	return find(live, func(x *sessions.LiveSession) bool {
		return all(x.PID == pid, x.Cwd == launch.Worktree, !claimed[x.SessionID])
	})
}

// screenCheck is a launch whose session's screen is to be read: the time alone says a close is
// due. pid is the id the session list's actions take for it (rowID).
type screenCheck struct {
	key    string
	launch *Launch
	id     string
	pid    string
	facts  CloseFacts
}

// checkLaunch is one launch's turn, up to the screen read: the check to read the screen for, or
// nil when no close is due. The screen is read only once the time alone says a close is due.
func (r *Reviews) checkLaunch(key string, launch *Launch) *screenCheck {
	now := r.now()

	session := r.pinned(launch, r.deps.Sessions())
	if session == nil {
		// Gone, or not provably the same session any more: the launch is marked closed and
		// never auto-closed afterwards.
		if anyOf(launch.SessionID != nil, now-launch.At >= appearMs) {
			r.markClosed(launch, now)
		}

		return nil
	}

	r.seenPid[launch] = session.PID
	r.pin(launch, session.SessionID)
	sessionID := session.SessionID

	if session.Status != statusIdle {
		delete(r.idleSince, sessionID)

		return nil
	}

	if _, idle := r.idleSince[sessionID]; !idle {
		r.idleSince[sessionID] = now
	}

	facts := r.closeFacts(key, launch, now-r.idleSince[sessionID])
	if !anyOf(facts.IdleForMs >= facts.CapMs,
		all(facts.ReviewedSinceLaunch, facts.IdleForMs >= facts.AfterMs)) {
		return nil
	}

	if now < r.nextLook[sessionID] {
		return nil
	}

	r.nextLook[sessionID] = now + lookAgainMs

	return &screenCheck{
		key: key, launch: launch, id: sessionID, pid: rowID(session), facts: facts,
	}
}

// rowID is the id the session list's actions take for session: the id its host issued for a
// session Conn hosts, so its screen is read and its close is a stop through the host; its
// process id for any other.
func rowID(session *sessions.LiveSession) string {
	if session.HostedID != "" {
		return session.HostedID
	}

	return js.Number(session.PID)
}

// readScreen reads a session's screen, and closes its workspace when the box is empty and the
// close is due.
func (r *Reviews) readScreen(check *screenCheck) {
	var input string

	r.outside(func() { input = r.deps.InputState(r.ctx, check.pid) })

	check.facts.InputEmpty = input == inputEmpty
	if CloseDecision(check.facts) != DecisionClose {
		return
	}

	r.closeLaunch(check.key, check.launch, check.id, check.pid)
}

// pin follows the session a launch runs in by its id.
func (r *Reviews) pin(launch *Launch, sessionID string) {
	if launch.SessionID != nil {
		if *launch.SessionID == sessionID {
			return
		}

		delete(r.idleSince, *launch.SessionID)
		delete(r.nextLook, *launch.SessionID)
	}

	launch.SessionID = &sessionID

	r.save()
}

func (r *Reviews) closeFacts(key string, launch *Launch, idleForMs float64) CloseFacts {
	pull, found := r.prs.Get(key)
	if !found {
		pull, found = r.endedPrs.Get(key)
	}

	reviewed := false

	if found {
		if r.me != nil {
			for _, review := range pull.Reviews {
				if all(same(review.Author, *r.me), review.State != reviewPending,
					review.SubmittedAt > launch.At) {
					reviewed = true
				}
			}
		}
	}

	return CloseFacts{
		IdleForMs: idleForMs, InputEmpty: false, ReviewedSinceLaunch: reviewed,
		AfterMs: max(0, r.deps.Config.CloseAfterMs), CapMs: max(0, r.deps.Config.CloseCapMs),
	}
}

// closeLaunch closes the workspace of a session that is still idle in the same folder (the
// screen read took a moment).
func (r *Reviews) closeLaunch(key string, launch *Launch, id, pid string) {
	still := find(r.deps.Sessions(), func(x *sessions.LiveSession) bool {
		return all(x.SessionID == id, x.Cwd == launch.Worktree)
	})
	if still == nil {
		return
	}

	if still.Status != statusIdle {
		return
	}

	r.finishing[key] = true

	var (
		code int
		why  string
	)

	r.outside(func() { code, why = r.deps.CloseWorkspace(r.ctx, pid) })

	delete(r.finishing, key)

	if code != codeOK {
		if code != codeBusy {
			r.logf("reviews: workspace not closed %s", why)
		}

		return
	}

	// The worktree stays: History's RESUME needs it while the PR is open, and the sweep removes it
	// once the PR has ended.
	r.markClosed(launch, r.now())
}

// startSweep starts one sweep at a time, after a successful poll.
func (r *Reviews) startSweep(current settings.Settings, stale func(repo string) bool) {
	if r.sweeping {
		return
	}

	r.sweeping = true

	r.spawn(func() {
		defer func() { r.sweeping = false }()

		r.sweep(current, stale)
	})
}

// sweep removes the worktrees of PRs whose own read said they ended. The folders come from the
// launch records, never from a folder name, and only for allowed repos: a repo that left the
// list keeps its worktrees.
func (r *Reviews) sweep(current settings.Settings, stale func(repo string) bool) {
	done := map[string]bool{}

	for _, ref := range r.entries() {
		parts, ok := ParseKey(ref.key)
		if !ok {
			continue
		}

		if anyOf(!repos.Allowed(parts.Repo, current.Repos.Allow), stale(parts.Repo),
			ref.entry.Ended == nil) {
			continue
		}

		// The length is read again on every step, as a for...of does: a launch added while
		// a removal ran without the mutex is looked at too.
		for index := 0; ; index++ {
			if index >= len(ref.entry.Launches) {
				break
			}

			launch := ref.entry.Launches[index]
			if anyOf(launch.ClosedAt == nil, done[launch.Worktree],
				!r.ownsWorktree(ref.key, launch)) {
				continue
			}

			done[launch.Worktree] = true

			err := r.sweepOne(ref.key, launch)
			if err == nil {
				delete(r.sweepFailed, launch.Worktree)

				continue
			}

			// A folder that git will not remove would fail the same way on every poll: say so
			// once.
			why := message(err)
			if r.sweepFailed[launch.Worktree] != why {
				r.logf("reviews: could not remove the worktree %s", why)
			}

			r.sweepFailed[launch.Worktree] = why
		}
	}
}

// sweepOne removes one ended PR's worktree when nothing may be using it: the PR is not open, no
// review of it is starting, being closed or still open, no live session works in the folder (or
// under it), and `git status` is clean. The last two are checked again right before the removal.
// Never forced; run in the clone the worktree was made from, and not at all when that is not
// known.
func (r *Reviews) sweepOne(key string, launch *Launch) error {
	folder := launch.Worktree
	if !r.exists(folder) {
		return nil
	}

	clone := launch.Clone
	if clone == "" {
		clone = r.worktreeClone(folder)
	}

	if clone == "" {
		return nil
	}

	if r.otherOrigin(key, folder) {
		return nil
	}

	if r.busy(key, folder) {
		return nil
	}

	var (
		status string
		err    error
	)

	r.outside(func() {
		status, err = r.deps.Git(r.ctx, []string{"status", "--porcelain"}, folder, gitClean)
	})

	if err != nil {
		return err
	}

	if anyOf(js.Trim(status) != "", r.busy(key, folder)) {
		return nil
	}

	r.outside(func() {
		_, err = r.deps.Git(r.ctx, []string{gitWorktree, "remove", folder}, clone, gitClean)
	})

	if err != nil {
		return err
	}

	// The now-empty folder of the PR, if nothing else is in it (rmdir never removes content).
	_ = removeEmptyFolder(nodepath.Dir(folder))

	return nil
}

// otherOrigin is whether folder's own origin names another repo than key's (or key is not one):
// such a worktree is never removed for this PR's state.
func (r *Reviews) otherOrigin(key, folder string) bool {
	origin := r.deps.OriginOf(folder)
	if origin == "" {
		return false
	}

	parts, ok := ParseKey(key)
	if !ok {
		return true
	}

	return !same(origin, parts.Repo)
}

func removeEmptyFolder(folder string) error {
	info, err := os.Lstat(folder)
	if err != nil {
		return fmt.Errorf("reading the PR's folder: %w", err)
	}

	if !info.IsDir() {
		return nil
	}

	err = os.Remove(folder)
	if err != nil {
		return fmt.Errorf("removing the PR's folder: %w", err)
	}

	return nil
}

func (r *Reviews) worktreeClone(folder string) string {
	if r.deps.WorktreeClone != nil {
		return r.deps.WorktreeClone(folder)
	}

	return WorktreeClone(folder)
}

// busy is whether anything may be using a PR's worktree.
func (r *Reviews) busy(key, folder string) bool {
	if anyOf(r.prs.Has(key), r.starting[key], r.finishing[key]) {
		return true
	}

	if r.file != nil {
		if entry, found := r.file.PRs.Get(key); found {
			for _, launch := range entry.Launches {
				if launch.ClosedAt == nil {
					return true
				}
			}
		}
	}

	place := Real(folder)

	for _, session := range r.deps.Sessions() {
		if Within(Real(session.Cwd), place) {
			return true
		}
	}

	return false
}
