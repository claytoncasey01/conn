package reviews

import (
	"path/filepath"
	"strconv"

	"github.com/claytoncasey01/conn/internal/js"
)

// The draft a review session writes is taken on the tick: the newest launch of each PR whose
// worktree has a review.json newer than what was last read from it has the file read and
// accepted holding the mutex (a stat, and at most MaxDraftFileBytes read), and the PR's diff is
// read without it, on a goroutine of its own, before the draft is stored on the launch. A stored
// draft is set aside, never deleted, once it is no longer the review to post: a review of mine
// landed on GitHub after it was written, or a newer launch of the PR started.

// connFolder is the folder in a worktree that Conn and the session share.
const connFolder = ".conn"

// What makes a draft file unreadable before its contents are looked at.
const (
	problemNotRead = "the file could not be read"
	problemNotJSON = "the file is not JSON"
)

// bytesPerKiB is how a size is said in KiB.
const bytesPerKiB = 1024

// takeDrafts takes the draft files that are new since they were last read, and answers whether
// anything changed (a draft stored, or a file found unreadable), so the snapshot moves. A draft
// whose diff is read on a goroutine is stored, and the snapshot moved, when that read answers.
func (r *Reviews) takeDrafts(refs []launchRef) bool {
	if anyOf(r.deps.DraftAt == nil, r.deps.ReadDraft == nil) {
		return false
	}

	changed := false

	for _, ref := range refs {
		launch := r.draftLaunch(ref)
		if launch == nil {
			continue
		}

		at := r.deps.DraftAt(launch.Worktree)
		if !draftDue(launch, at) {
			continue
		}

		if r.takeDraft(ref.key, launch, *at) {
			changed = true
		}
	}

	return changed
}

// draftLaunch is the launch of a PR whose draft file is looked at: its newest (the worktree is
// the PR's, so the file is that session's), while it is open, is Conn's own, has no diff being
// read for it and has not posted its draft; nil when there is none.
func (r *Reviews) draftLaunch(ref launchRef) *Launch {
	if len(ref.entry.Launches) == 0 {
		return nil
	}

	launch := ref.entry.Launches[len(ref.entry.Launches)-1]
	if anyOf(launch.ClosedAt != nil, r.drafting[launch], posted(launch)) {
		return nil
	}

	if !r.ownsWorktree(ref.key, launch) {
		return nil
	}

	return launch
}

// posted is whether a launch's draft was posted: it is never replaced.
func posted(launch *Launch) bool {
	if launch.Draft == nil {
		return false
	}

	return launch.Draft.PostedAt != nil
}

// draftDue is whether a draft file with mtime fileAt is to be read for launch: it is there,
// written after the launch, and newer than both the draft stored and the last write found
// unreadable.
func draftDue(launch *Launch, fileAt *float64) bool {
	if fileAt == nil {
		return false
	}

	if anyOf(*fileAt <= launch.At, *fileAt <= launch.DraftProblemAt) {
		return false
	}

	if launch.Draft == nil {
		return true
	}

	return *fileAt > launch.Draft.FileAt
}

// takeDraft reads and accepts the draft file of launch, written at fileAt: an unreadable one is
// recorded, a readable one is placed on the diff and stored. It answers whether it changed the
// launch now. While GitHub is on and the PR has not been read yet (a tick before the first poll),
// nothing is read: the next tick after the poll reads it.
func (r *Reviews) takeDraft(key string, launch *Launch, fileAt float64) bool {
	current, _ := r.settings()
	gitHubOn := r.githubOn(&current)
	pull, known := r.pullOf(key)

	if all(gitHubOn, !known) {
		return false
	}

	content, read := r.deps.ReadDraft(filepath.Join(launch.Worktree, connFolder, DraftFile))

	draft, problems := draftOf(content, read)
	if len(problems) > 0 {
		launch.DraftProblem = problems
		launch.DraftProblemAt = fileAt

		r.save()

		return true
	}

	stored := newStoredDraft(draft, fileAt)

	// GitHub is not called while it is off or its rate limit is used up, and a base that is not
	// safe in a path is never put in one: the draft is stored with every finding in the body.
	if !gitHubOn {
		r.storeDraft(key, launch, stored)

		return true
	}

	if anyOf(r.pausedUntil != nil, !IsSafeRef(pull.BaseRef)) {
		r.storeDraft(key, launch, stored)

		return true
	}

	r.placeDraft(key, launch, stored, pull)

	return false
}

// newStoredDraft is draft as read from a file written at fileAt: live, not posted, and with no
// findings placed yet.
func newStoredDraft(draft Draft, fileAt float64) StoredDraft {
	return StoredDraft{
		Draft: draft, Inline: nil, FileAt: fileAt, PostedAt: nil, URL: nil, SetAside: nil,
	}
}

// draftOf is the draft in a file's text, or every reason it cannot be read: it could not be
// read, is over MaxDraftFileBytes, is not JSON, or AcceptDraft's problems.
func draftOf(content string, read bool) (Draft, []string) {
	if !read {
		return Draft{}, []string{problemNotRead}
	}

	if len(content) > MaxDraftFileBytes {
		return Draft{}, []string{
			"the file is over " + strconv.Itoa(MaxDraftFileBytes/bytesPerKiB) + " KiB",
		}
	}

	parsed, err := js.Parse([]byte(content))
	if err != nil {
		return Draft{}, []string{problemNotJSON}
	}

	return AcceptDraft(parsed)
}

// placeDraft reads the PR's diff without the mutex, on a goroutine of its own, then stores the
// draft with its findings placed on it; with every finding in the review's body when the diff
// cannot be read. The diff is read once per write of the file.
func (r *Reviews) placeDraft(key string, launch *Launch, draft StoredDraft, pull *PR) {
	r.drafting[launch] = true
	repo, baseRef := pull.Repo, pull.BaseRef

	r.spawn(func() {
		defer delete(r.drafting, launch)

		var err error

		// The diff is placed without the mutex too: it may be megabytes, and the work is pure.
		r.outside(func() {
			var diff string

			diff, err = r.deps.Gh.Diff(r.ctx, repo, baseRef, draft.Commit)
			if err == nil {
				draft.Inline = Inline(draft.Findings, Positions(diff))
			}
		})

		if err != nil {
			r.logf("reviews: the diff of %s was not read %v", key, err)
		}

		r.storeDraft(key, launch, draft)
		r.bump()
	})
}

// storeDraft stores draft on launch, unless the launch is no longer in the file, its draft was
// posted, or what it has is from the same write or a newer one. A draft stored on a launch that a
// newer one followed is set aside at once; one stored on the newest sets aside the older ones'.
func (r *Reviews) storeDraft(key string, launch *Launch, draft StoredDraft) {
	if r.file == nil {
		return
	}

	entry, found := r.file.PRs.Get(key)
	if !found {
		return
	}

	index := indexOf(entry.Launches, launch)
	if index < 0 {
		return
	}

	if launch.Draft != nil {
		if anyOf(posted(launch), launch.Draft.FileAt >= draft.FileAt) {
			return
		}
	}

	launch.Draft = &draft
	launch.DraftProblem = nil
	launch.DraftProblemAt = 0

	if index < len(entry.Launches)-1 {
		why := SetAsideNewer
		launch.Draft.SetAside = &why
	} else {
		setAside(entry.Launches[:index], SetAsideNewer)
	}

	r.save()
}

func indexOf(launches []*Launch, launch *Launch) int {
	for index, candidate := range launches {
		if candidate == launch {
			return index
		}
	}

	return -1
}

// setAside sets aside the live, unposted drafts of launches, for why; it answers whether it set
// any aside.
func setAside(launches []*Launch, why string) bool {
	changed := false

	for _, launch := range launches {
		if anyOf(!liveDraft(launch), posted(launch)) {
			continue
		}

		reason := why
		launch.Draft.SetAside = &reason
		changed = true
	}

	return changed
}

// setAsideReviewed sets aside each live, unposted draft of file that a review by login (mine)
// submitted after the draft was written overtook: I reviewed by hand. It answers whether it set
// any aside.
func setAsideReviewed(file *File, login string, pullOf func(key string) (*PR, bool)) bool {
	changed := false

	for _, key := range file.PRs.Keys() {
		pull, found := pullOf(key)
		if !found {
			continue
		}

		entry, _ := file.PRs.Get(key)

		for _, launch := range entry.Launches {
			if anyOf(!liveDraft(launch), posted(launch)) {
				continue
			}

			if reviewedSince(pull, login, launch.Draft.FileAt) {
				changed = setAside([]*Launch{launch}, SetAsideReviewed) || changed
			}
		}
	}

	return changed
}

// reviewedSince is whether a review by login on pull, other than a pending one, was submitted
// after since.
func reviewedSince(pull *PR, login string, since float64) bool {
	for _, review := range pull.Reviews {
		if all(same(review.Author, login), review.State != reviewPending,
			review.SubmittedAt > since) {
			return true
		}
	}

	return false
}

// pullOf is the PR the last poll read for key, open or ended within the day.
func (r *Reviews) pullOf(key string) (*PR, bool) {
	pull, found := r.prs.Get(key)
	if found {
		return pull, true
	}

	return r.endedPrs.Get(key)
}

// asStoredDraft is a draft as reviews.json keeps it; nil when it is not one this Conn wrote. The
// draft is checked by AcceptDraft again and must come through whole: a finding it would drop
// now would leave Inline out of step with the findings.
func asStoredDraft(value any) *StoredDraft {
	raw := object(value)
	if raw == nil {
		return nil
	}

	draft, problems := AcceptDraft(value)
	dropped, droppedOK := number(memberOf(raw, "dropped"))
	fileAt, fileOK := number(memberOf(raw, "fileAt"))
	postedAt, postedOK := numberOrNull(member(raw, "postedAt"))
	url, urlOK := textOrNull(memberOf(raw, "url"))
	why, whyOK := setAsideOf(memberOf(raw, "setAside"))

	if !all(len(problems) == 0, draft.Dropped == 0, droppedOK, dropped >= 0,
		dropped == float64(int(dropped)), fileOK, postedOK, urlOK, whyOK) {
		return nil
	}

	draft.Dropped = int(dropped)

	return &StoredDraft{
		Draft: draft, Inline: inlineOf(memberOf(raw, "inline"), len(draft.Findings)),
		FileAt: fileAt, PostedAt: postedAt, URL: url, SetAside: why,
	}
}

// inlineOf is a stored Inline: one bool per finding, else nil (every finding in the body).
func inlineOf(value any, findings int) []bool {
	values, isList := value.([]any)
	if !all(isList, len(values) == findings) {
		return nil
	}

	inline := make([]bool, 0, findings)

	for _, item := range values {
		flag, isFlag := item.(bool)
		if !isFlag {
			return nil
		}

		inline = append(inline, flag)
	}

	return inline
}

// textOrNull is a text, or nil for null or absent.
func textOrNull(value any) (*string, bool) {
	if value == nil {
		return nil, true
	}

	found, isText := text(value)
	if !isText {
		return nil, false
	}

	return &found, true
}

// setAsideOf is a stored reason a draft was set aside, nil while it is live.
func setAsideOf(value any) (*string, bool) {
	why, ok := textOrNull(value)
	if !all(ok, why != nil) {
		return nil, ok
	}

	return why, anyOf(*why == SetAsideReviewed, *why == SetAsideNewer)
}

// asProblems are the stored reasons a launch's last draft file could not be read.
func asProblems(raw *js.Object) []string {
	values, isList := memberOf(raw, "draftProblem").([]any)
	if !isList {
		return nil
	}

	problems := []string{}

	for _, value := range values {
		if problem, isText := text(value); isText {
			problems = append(problems, problem)
		}
	}

	if len(problems) == 0 {
		return nil
	}

	return problems
}
