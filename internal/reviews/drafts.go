package reviews

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"

	"github.com/claytoncasey01/conn/internal/js"
)

// The draft a review session writes is taken on the tick: the newest launch of each PR whose
// worktree has a review.json newer than what was last read from it has the file read and
// accepted holding the mutex (a stat, and at most MaxDraftFileBytes read), and the PR's diff is
// read without it, on a goroutine of its own, before the draft is stored on the launch. A draft
// whose diff could not be read is stored with every finding in the body, and its diff read again
// on a later tick, a bounded number of times. A stored draft is set aside, never deleted, once it
// is no longer the review to post: a review of mine landed on GitHub after it was written, or a
// newer launch of the PR started.

// connFolder is the folder in a worktree that Conn and the session share.
const connFolder = ".conn"

const (
	// maxDiffTries is the most failed reads of a draft's diff, per write of its file; past it
	// the findings stay in the review's body.
	maxDiffTries = 5
	// diffRetryMs is how long after a failed read of a draft's diff it is read again: a minute,
	// so a short outage is waited out rather than used up on consecutive ticks.
	diffRetryMs = 60_000
)

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
		if r.takeFile(ref) {
			changed = true
		}

		// After the file: a draft it just stored is being placed already.
		r.placeAgain(ref)
	}

	return changed
}

// takeFile takes the draft file of ref's newest launch when it is new since it was last read,
// and answers whether that changed the launch now.
func (r *Reviews) takeFile(ref launchRef) bool {
	launch := r.draftLaunch(ref)
	if launch == nil {
		return false
	}

	changed := false

	fileAt := r.deps.DraftAt(launch.Worktree)
	if draftDue(launch, fileAt) {
		if r.draftWaits(ref) {
			return false
		}

		changed = r.takeDraft(ref.key, launch, *fileAt)
	}

	// A closed launch's session writes nothing more: its file is looked at once, after it
	// closed, so one written just before (or while Conn was down) is still taken.
	if launch.ClosedAt != nil {
		r.draftLooked[launch] = true
	}

	return changed
}

// placeAgain reads the diff again for the draft of ref's newest launch when its findings were
// never placed: it is live and unposted, nothing is reading its diff or posting it, fewer than
// maxDiffTries reads of its diff failed, the last failed one was diffRetryMs ago or more, and the
// diff may be read now. A draft stored while GitHub could not be called is placed this way too.
// The launch's session may have closed: the draft is still the review to post.
func (r *Reviews) placeAgain(ref launchRef) {
	if len(ref.entry.Launches) == 0 {
		return
	}

	launch := ref.entry.Launches[len(ref.entry.Launches)-1]
	if !unplaced(launch) {
		return
	}

	if anyOf(r.drafting[launch], r.held(ref.key, launch)) {
		return
	}

	triedAt, tried := r.diffTriedAt[launch]
	if all(tried, r.now()-triedAt < diffRetryMs) {
		return
	}

	pull, readable := r.diffReadable(ref.key)
	if !readable {
		return
	}

	r.placeDraft(ref.key, launch, *launch.Draft, pull)
}

// unplaced is whether launch has a live, unposted draft with findings that were never placed on
// the diff, and whose diff may be read again. A draft with no findings has nothing to place.
func unplaced(launch *Launch) bool {
	if anyOf(!liveDraft(launch), posted(launch)) {
		return false
	}

	draft := launch.Draft

	return all(len(draft.Findings) > 0, draft.Inline == nil, draft.DiffTries < maxDiffTries)
}

// diffReadable is the PR key names when its diff may be read now: GitHub is on and its rate
// limit not used up, the poll has read the PR (so its base is known), and the base is safe in
// a path.
func (r *Reviews) diffReadable(key string) (*PR, bool) {
	current, _ := r.settings()
	pull, known := r.pullOf(key)

	if !all(r.githubOn(&current), known, r.pausedUntil == nil) {
		return nil, false
	}

	return pull, IsSafeRef(pull.BaseRef)
}

// draftWaits is whether a due draft waits for the poll: GitHub is on and the PR, open as far as
// Conn knows, has not been read yet (a tick before the first poll, or a poll that keeps
// failing), so its base is not known. The worktree sweep leaves a worktree with such a file.
func (r *Reviews) draftWaits(ref launchRef) bool {
	current, _ := r.settings()
	if !r.githubOn(&current) {
		return false
	}

	if ref.entry.Ended != nil {
		return false
	}

	_, known := r.pullOf(ref.key)

	return !known
}

// draftLaunch is the launch of a PR whose draft file is looked at: its newest (the worktree is
// the PR's, so the file is that session's), while it is Conn's own, has no diff being read for
// it, is not having its review posted or held unsure, has not posted its draft and, once
// closed, has not been looked at since; nil when there is none.
func (r *Reviews) draftLaunch(ref launchRef) *Launch {
	if len(ref.entry.Launches) == 0 {
		return nil
	}

	launch := ref.entry.Launches[len(ref.entry.Launches)-1]
	if anyOf(r.draftLooked[launch], r.drafting[launch], r.held(ref.key, launch), posted(launch)) {
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
// launch now.
func (r *Reviews) takeDraft(key string, launch *Launch, fileAt float64) bool {
	content, read := r.deps.ReadDraft(filepath.Join(launch.Worktree, connFolder, DraftFile))

	draft, problems := draftOf(content, read)
	if len(problems) > 0 {
		launch.DraftProblem = problems
		launch.DraftProblemAt = fileAt

		r.save()

		return true
	}

	stored := newStoredDraft(draft, fileAt)

	// GitHub is not called while it is off or its rate limit is used up, a PR that ended before
	// the poll read it has no base known, and a base that is not safe in a path is never put in
	// one: the draft is stored with every finding in the body, and placeAgain places it once the
	// diff may be read.
	pull, readable := r.diffReadable(key)
	if !readable {
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
// draft with its findings placed on it; with every finding in the review's body, and the failed
// read counted, when the diff cannot be read (placeAgain tries again, up to maxDiffTries).
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

		// A read cut short by Conn stopping says nothing of the diff: nothing is stored or
		// counted, so the file is read, or the diff asked for, again.
		if anyOf(r.ctx.Err() != nil, errors.Is(err, context.Canceled)) {
			return
		}

		r.diffTriedAt[launch] = r.now()

		if err != nil {
			draft.DiffTries++

			r.logf("reviews: the diff of %s was not read %v", key, err)
		}

		if anyOf(err == nil, draft.DiffTries >= maxDiffTries) {
			delete(r.diffTriedAt, launch)
		}

		r.storeDraft(key, launch, draft)
		r.bump()
	})
}

// storeDraft stores draft on launch, unless the launch is no longer in the file, its draft was
// posted, or what it has is from a newer write. One from the same write is a placement tried
// again: only where its findings land, and the reads tried, change. A draft stored on a launch
// that a newer one followed is set aside at once; one stored on the newest sets aside the older
// ones'.
func (r *Reviews) storeDraft(key string, launch *Launch, draft StoredDraft) {
	entry := r.entryOf(key)
	if entry == nil {
		return
	}

	index := indexOf(entry.Launches, launch)
	if index < 0 {
		return
	}

	if launch.Draft != nil {
		if anyOf(posted(launch), launch.Draft.FileAt > draft.FileAt) {
			return
		}

		if launch.Draft.FileAt == draft.FileAt {
			r.placeStored(launch.Draft, draft)

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

// placeStored takes onto stored where placed, read from the same write of the file, put its
// findings and how many reads of the diff it tried, unless stored was placed already. What else
// changed meanwhile (set aside, say) is kept.
func (r *Reviews) placeStored(stored *StoredDraft, placed StoredDraft) {
	if stored.Inline != nil {
		return
	}

	stored.Inline, stored.DiffTries = placed.Inline, placed.DiffTries

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
// any aside. A draft held (being posted, or unsure whether it was) is left: the review may be
// the post itself.
func setAsideReviewed(file *File, login string, pullOf func(key string) (*PR, bool),
	held func(key string, launch *Launch) bool,
) bool {
	changed := false

	for _, key := range file.PRs.Keys() {
		pull, found := pullOf(key)
		if !found {
			continue
		}

		entry, _ := file.PRs.Get(key)

		for _, launch := range entry.Launches {
			if anyOf(!liveDraft(launch), posted(launch), held(key, launch)) {
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
	sentAs := postedVerdictOf(memberOf(raw, "postedVerdict"))

	if !all(len(problems) == 0, draft.Dropped == 0, droppedOK, dropped >= 0,
		dropped == float64(int(dropped)), fileOK, postedOK, urlOK, whyOK) {
		// A posted review stays posted, whatever else no longer reads: its file is never taken
		// again, so it is never posted twice.
		return postedMarker(fileAt, postedAt, url, sentAs)
	}

	draft.Dropped = int(dropped)

	return &StoredDraft{
		Draft: draft, Inline: inlineOf(memberOf(raw, "inline"), len(draft.Findings)),
		DiffTries: diffTriesOf(member(raw, "diffTries")),
		FileAt:    fileAt, PostedAt: postedAt, URL: url, PostedVerdict: sentAs, SetAside: why,
	}
}

// diffTriesOf is a stored count of failed diff reads: 0 when absent (a draft stored before Conn
// counted them), and maxDiffTries when it is not a whole number from 0 to it, so a count that
// does not read never starts the tries over.
func diffTriesOf(value any, present bool) int {
	if !present {
		return 0
	}

	tries, isNumber := number(value)
	if !all(isNumber, tries >= 0, tries <= maxDiffTries, tries == float64(int(tries))) {
		return maxDiffTries
	}

	return int(tries)
}

// postedVerdictOf is the stored verdict a review was posted as; nil when there is none, or it
// is not one Conn posts (the row then takes its state from GitHub alone).
func postedVerdictOf(value any) *Verdict {
	verdict, known := verdictOf(value)
	if !known {
		return nil
	}

	return &verdict
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

// draftUntaken is whether the newest launch of entry, in folder, has a draft file not taken yet
// (or one whose diff is being read): the worktree is not removed from under it.
func (r *Reviews) draftUntaken(entry *Entry, folder string) bool {
	if anyOf(len(entry.Launches) == 0, r.deps.DraftAt == nil) {
		return false
	}

	newest := entry.Launches[len(entry.Launches)-1]
	if anyOf(newest.Worktree != folder, posted(newest)) {
		return false
	}

	if r.drafting[newest] {
		return true
	}

	return draftDue(newest, r.deps.DraftAt(folder))
}

// postedMarker is what is kept of a stored draft that no longer reads back whole: that it was
// posted, when, to which URL and as which verdict, with no findings; nil when it was not posted.
func postedMarker(fileAt float64, postedAt *float64, url *string, verdict *Verdict) *StoredDraft {
	if postedAt == nil {
		return nil
	}

	return &StoredDraft{
		Draft: emptyDraft(), Inline: nil, FileAt: fileAt, PostedAt: postedAt, URL: url,
		PostedVerdict: verdict, SetAside: nil,
	}
}
