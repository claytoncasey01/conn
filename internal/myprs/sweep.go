package myprs

import (
	"slices"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// startSweep is called after a successful poll: one sweep at a time, never waited for.
func (m *MyPrs) startSweep(gone []keyedEntry) {
	for _, one := range gone {
		m.sweepQueue.Set(one.key, one.entry)
	}

	if m.sweeping {
		return
	}

	m.sweeping = true

	m.spawn(func() {
		defer func() { m.sweeping = false }()

		m.sweep()
	})
}

// sweep removes the worktrees of PRs that have left the open list. The worktrees come from the
// launches the file records (never from the folders on the disk, whose names say nothing sure
// about the owner or the round), and the branch `conn-self-<n>` stays. Entries queued by a poll
// that came while this ran are taken in the next round.
func (m *MyPrs) sweep() {
	for m.sweepQueue.Len() > 0 {
		batch := make([]keyedEntry, 0, m.sweepQueue.Len())

		for _, key := range m.sweepQueue.Keys() {
			entry, _ := m.sweepQueue.Get(key)
			batch = append(batch, keyedEntry{key: key, entry: entry})
		}

		m.sweepQueue = js.NewMap[*Entry]()

		for _, one := range batch {
			m.sweepEntry(one.key, one.entry)
		}
	}
}

func (m *MyPrs) sweepEntry(key string, entry *Entry) {
	if m.prs.Has(key) {
		return
	}

	repo := repoOfKey(key)
	todo, paths := m.unswept(entry)

	for _, path := range paths {
		settled, err := m.sweepOne(key, repo, path)
		if err != nil {
			why := message(err)
			if m.sweepFailed[path] != why {
				m.logf("myprs: could not remove the worktree %s", why)
			}

			m.sweepFailed[path] = why

			continue
		}

		if settled {
			for _, launch := range todo {
				if launch.Worktree == path {
					m.swept[launch] = true
				}
			}
		}

		delete(m.sweepFailed, path)
	}
}

// unswept are the launches whose worktree has not been swept, and their worktrees, each once.
func (m *MyPrs) unswept(entry *Entry) ([]*Launch, []string) {
	todo := []*Launch{}
	paths := []string{}

	for _, launch := range entry.Launches {
		if m.swept[launch] {
			continue
		}

		todo = append(todo, launch)

		if !slices.Contains(paths, launch.Worktree) {
			paths = append(paths, launch.Worktree)
		}
	}

	return todo, paths
}

// sweepOne removes one worktree of a PR that is not in the open list, when nothing may be using
// it: the PR is not back on the list or being started, no live session works in the folder (or
// under it), and `git status` is clean. Both are checked again right before the removal, since
// the status call takes a moment. A dirty one is kept without a word and looked at again on the
// next poll. Never forced. It answers whether the path is settled (removed, or not there, or not
// one Conn wrote), so it is not looked at again.
func (m *MyPrs) sweepOne(key, repo, path string) (bool, error) {
	// Not a path Conn wrote (not strictly inside <dataDir>/myprs, which also rules out a
	// relative one or one git would read as an option): never handed to git, and settled.
	if !m.owned(path) {
		return true, nil
	}

	if !m.deps.Exists(path) {
		return true, nil
	}

	clone := m.deps.CloneFor(repo)
	if clone == "" {
		return false, nil
	}

	if m.busy(key, path) {
		return false, nil
	}

	var (
		status string
		err    error
	)

	m.outside(func() {
		status, err = m.deps.Git(m.ctx, []string{"status", "--porcelain"}, path, gitClean)
	})

	if err != nil {
		return false, err
	}

	if js.Trim(status) != "" {
		return false, nil
	}

	if m.busy(key, path) {
		return false, nil
	}

	m.outside(func() {
		_, err = m.deps.Git(m.ctx, []string{"worktree", "remove", path}, clone, gitClean)
	})

	if err != nil {
		return false, err
	}

	return true, nil
}

func (m *MyPrs) busy(key, path string) bool {
	if m.prs.Has(key) {
		return true
	}

	if m.starting[key] {
		return true
	}

	folder := reviews.Real(path)

	for _, session := range m.deps.Sessions() {
		if reviews.Within(reviews.Real(session.Cwd), folder) {
			return true
		}
	}

	return false
}
