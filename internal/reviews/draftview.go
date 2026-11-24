package reviews

import "slices"

// The draft as a row shows it. A row shows the draft of the newest launch that has one (or that
// found its review file unreadable): older launches' drafts were set aside when that launch
// started.

// FindingView is a finding with where it lands on the diff.
type FindingView struct {
	Finding

	// Inline is whether the finding lands on a line of the diff; nil when the diff was not
	// read, so the finding goes in the review's body.
	Inline *bool `json:"inline"`
}

// DraftView is a launch's draft as the row shows it.
type DraftView struct {
	// LaunchAt names the launch the draft belongs to: the post names it.
	LaunchAt float64 `json:"launchAt"`
	// FileAt names the write of the review file the draft was read from: the post names it,
	// and is refused once a newer write has replaced the draft. 0 with no draft to show.
	FileAt float64 `json:"fileAt"`
	Commit string  `json:"commit"`
	// HeadMoved is that the PR's head is no longer Commit.
	HeadMoved bool          `json:"headMoved"`
	Summary   string        `json:"summary"`
	Verdict   string        `json:"verdict"`
	Findings  []FindingView `json:"findings"`
	Dropped   int           `json:"dropped"`
	PostedAt  *float64      `json:"postedAt"`
	URL       *string       `json:"url"`
	// Problems are why the review file could not be read, else empty.
	Problems []string `json:"problems"`
}

// draftLaunch is the newest launch that has a draft or a problem with its file, or nil.
func draftLaunch(tracking *Entry) *Launch {
	for _, launch := range slices.Backward(tracking.Launches) {
		if launch.Draft != nil || len(launch.DraftProblem) > 0 {
			return launch
		}
	}

	return nil
}

// waitsForPost is whether the row's draft is stored, not posted and not set aside: the draft a
// person is to post. A posted marker has a post time, so it never waits.
func waitsForPost(tracking *Entry) bool {
	launch := draftLaunch(tracking)
	if launch == nil {
		return false
	}

	if !liveDraft(launch) {
		return false
	}

	return !posted(launch)
}

// draftViewOf is the draft a row shows for pull, or nil when there is none to show: no launch
// has one, or the newest one was set aside and its file has no problem to report.
func draftViewOf(tracking *Entry, pull *PR) *DraftView {
	launch := draftLaunch(tracking)
	if launch == nil {
		return nil
	}

	view := &DraftView{
		LaunchAt: launch.At, FileAt: 0, Commit: "", HeadMoved: false, Summary: "", Verdict: "",
		Findings: []FindingView{}, Dropped: 0, PostedAt: nil, URL: nil,
		Problems: append([]string{}, launch.DraftProblem...),
	}

	if liveDraft(launch) {
		fillDraft(view, launch.Draft, pull)

		return view
	}

	if len(view.Problems) == 0 {
		return nil
	}

	return view
}

// fillDraft copies a stored draft into view, and says whether pull's head has moved past it.
func fillDraft(view *DraftView, stored *StoredDraft, pull *PR) {
	view.FileAt = stored.FileAt
	view.Commit, view.Summary, view.Verdict = stored.Commit, stored.Summary, stored.Verdict
	view.Dropped, view.PostedAt, view.URL = stored.Dropped, stored.PostedAt, stored.URL
	view.HeadMoved = stored.Commit != "" && pull.Ended == nil && stored.Commit != pull.HeadSha

	for index, finding := range stored.Findings {
		var inline *bool

		if index < len(stored.Inline) {
			placed := stored.Inline[index]
			inline = &placed
		}

		view.Findings = append(view.Findings, FindingView{Finding: finding, Inline: inline})
	}
}
