package reviews_test

// The row of a PR whose review session left a draft: the REVIEW READY state, the draft shown on
// the row, and the cases where the draft is shown but is not the state.

import (
	"slices"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

const (
	rCommit   = "0123456789abcdef0123456789abcdef01234567"
	rKey      = "acme/widgets#7"
	rLaunchAt = 1_799_990_000_000.0
)

// rDraft is a stored draft of two findings, the second outside the diff, live and unposted.
func rDraft(change func(stored *reviews.StoredDraft)) *reviews.StoredDraft {
	stored := &reviews.StoredDraft{
		Inline: []bool{true, false}, FileAt: rLaunchAt + 1, PostedAt: nil, URL: nil, SetAside: nil,
	}
	stored.Commit, stored.Summary = rCommit, "Sound; two nits."
	stored.Verdict, stored.Dropped = reviews.VerdictComment, 1
	stored.Findings = []reviews.Finding{
		{Path: "a.go", Line: 2, Side: reviews.SideRight, Severity: "P2", Body: "Cap it."},
		{Path: "docs/README.md", Line: 4, Side: reviews.SideRight, Severity: "P3", Body: "Why."},
	}

	if change != nil {
		change(stored)
	}

	return stored
}

// rLaunch is a launch at the given time with what the test changes.
func rLaunch(at float64, change func(launch *reviews.Launch)) *reviews.Launch {
	launch := &reviews.Launch{
		At: at, Model: reviews.ModelSonnet, Name: "pr-7", HeadSha: "aaa", Worktree: "/w",
		SessionID: nil, ClosedAt: nil, Clone: "", Draft: nil, DraftProblem: nil,
		DraftProblemAt: 0,
	}

	if change != nil {
		change(launch)
	}

	return launch
}

func rEntry(launches ...*reviews.Launch) *reviews.Entry {
	return &reviews.Entry{
		Slack: nil, SlackAskAt: nil, DM: nil, DMLastAt: nil, FirstSeenAt: rLaunchAt,
		Launches: launches, Scan: reviews.Scan{Tries: 0, LastAt: 0},
		ThreadScan: reviews.ThreadScan{Tries: 0, LastAt: 0, HeadSha: ""}, Requested: true,
		Ended: nil, SeenAt: rLaunchAt, PlacedAt: nil,
	}
}

func rDerive(pull *reviews.PR, tracking *reviews.Entry, live bool) *reviews.Row {
	var session *reviews.RowSession
	if live {
		session = &reviews.RowSession{Name: "pr-7", Status: "idle"}
	}

	return reviews.DeriveRow(pull, tracking, &reviews.DeriveContext{
		Me: worldMe, Session: session, Now: worldT0,
		Sources: reviews.Sources{Requested: true, Channels: nil, DMs: false, AllOpen: false},
	}, rKey)
}

func rApproved(commit string) reviews.Review {
	return myReview(func(review *reviews.Review) {
		review.State, review.Commit = "APPROVED", commit
	})
}

func rEnded(pull *reviews.PR) {
	var endedAt float64 = worldT0 - hourMs

	merged := reviews.EndedMerged
	pull.Ended, pull.EndedAt = &merged, &endedAt
}

func rPosted(stored *reviews.StoredDraft) {
	postedAt := rLaunchAt + 5
	stored.PostedAt = &postedAt
}

func rSetAside(stored *reviews.StoredDraft) {
	why := reviews.SetAsideNewer
	stored.SetAside = &why
}

// rMarker is what is kept of a posted draft that no longer reads back: no commit, no findings.
func rMarker(stored *reviews.StoredDraft) {
	rPosted(stored)
	stored.Draft = reviews.Draft{
		Commit: "", Summary: "", Verdict: "", Findings: []reviews.Finding{}, Dropped: 0,
	}
	stored.Inline = nil
}

func rWithDraft(change func(stored *reviews.StoredDraft)) *reviews.Launch {
	return rLaunch(rLaunchAt, func(launch *reviews.Launch) { launch.Draft = rDraft(change) })
}

func TestTheStateOfARowWithADraft(t *testing.T) {
	t.Parallel()

	approved := func(pr *reviews.PR) { pr.Reviews = []reviews.Review{rApproved("aaa")} }
	problemOnly := rLaunch(rLaunchAt, func(launch *reviews.Launch) {
		launch.DraftProblem = []string{"the file is not a JSON object"}
	})
	draftAndProblem := rLaunch(rLaunchAt, func(launch *reviews.Launch) {
		launch.Draft = rDraft(nil)
		launch.DraftProblem = []string{"verdict: not approve or comment"}
	})

	for name, row := range map[string]struct {
		pull    *reviews.PR
		launch  *reviews.Launch
		live    bool
		want    string
		hasView bool
	}{
		"a draft waiting, session open": {
			pull: prOf(nil), launch: rWithDraft(nil), live: true, want: reviews.StateReady,
			hasView: true,
		},
		"a draft waiting, session closed": {
			pull: prOf(nil), launch: rWithDraft(nil), live: false, want: reviews.StateReady,
			hasView: true,
		},
		"a draft waiting beside my older review": {
			pull: prOf(approved), launch: rWithDraft(nil), live: false, want: reviews.StateReady,
			hasView: true,
		},
		"a posted draft takes the state from GitHub": {
			pull: prOf(approved), launch: rWithDraft(rPosted), live: false,
			want: reviews.StateDone, hasView: true,
		},
		"a posted marker is posted, never ready": {
			pull: prOf(nil), launch: rWithDraft(rMarker), live: false, want: reviews.StateNew,
			hasView: true,
		},
		"a set-aside draft is not shown": {
			pull: prOf(nil), launch: rWithDraft(rSetAside), live: false, want: reviews.StateNew,
			hasView: false,
		},
		"an ended PR with a draft is done, the draft kept": {
			pull: prOf(rEnded), launch: rWithDraft(nil), live: false, want: reviews.StateDone,
			hasView: true,
		},
		"only problems, session open": {
			pull: prOf(nil), launch: problemOnly, live: true, want: reviews.StateReviewing,
			hasView: true,
		},
		"a draft and a newer problem stays ready": {
			pull: prOf(nil), launch: draftAndProblem, live: true, want: reviews.StateReady,
			hasView: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := rDerive(row.pull, rEntry(row.launch), row.live)
			if got == nil {
				t.Fatal("the row is not listed")
			}

			if got.State != row.want {
				t.Errorf("state = %q, want %q", got.State, row.want)
			}

			if (got.Draft != nil) != row.hasView {
				t.Errorf("draft shown = %v, want %v", got.Draft != nil, row.hasView)
			}
		})
	}
}

func TestTheDraftOnTheRow(t *testing.T) {
	t.Parallel()

	pull := prOf(func(pr *reviews.PR) { pr.HeadSha = "bbb" })
	launch := rLaunch(rLaunchAt, func(launch *reviews.Launch) {
		launch.Draft = rDraft(nil)
		launch.DraftProblem = []string{"newer file unreadable"}
	})

	view := rDerive(pull, rEntry(rLaunch(rLaunchAt-1000, nil), launch), false).Draft
	if view == nil {
		t.Fatal("no draft on the row")
	}

	for name, check := range map[string]func() bool{
		"names its launch":            func() bool { return view.LaunchAt == rLaunchAt },
		"names the write of its file": func() bool { return view.FileAt == rLaunchAt+1 },
		"carries the draft": func() bool {
			return view.Commit == rCommit && view.Summary == "Sound; two nits." && view.Dropped == 1
		},
		"carries the verdict":   func() bool { return view.Verdict == reviews.VerdictComment },
		"carries every finding": func() bool { return len(view.Findings) == 2 },
		"says the head moved":   func() bool { return view.HeadMoved },
		"marks which findings land on the diff": func() bool {
			return *view.Findings[0].Inline && !*view.Findings[1].Inline
		},
		"lists the newer file's problems": func() bool {
			return slices.Equal(view.Problems, []string{"newer file unreadable"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if !check() {
				t.Errorf("draft = %+v", view)
			}
		})
	}
}

func TestTheDraftOnTheRowWhenTheDiffWasNotRead(t *testing.T) {
	t.Parallel()

	launch := rWithDraft(func(stored *reviews.StoredDraft) { stored.Inline = nil })
	pull := prOf(func(pr *reviews.PR) { pr.HeadSha = rCommit })

	view := rDerive(pull, rEntry(launch), false).Draft

	for index, finding := range view.Findings {
		if finding.Inline != nil {
			t.Errorf("finding %d: inline = %v, want nil (not checked)", index, *finding.Inline)
		}
	}

	if view.HeadMoved {
		t.Error("headMoved = true, the head is the draft's commit")
	}

	if view.Problems == nil {
		t.Error("problems is nil: the page reads a list")
	}
}

func TestThePostedMarkerIsShownPosted(t *testing.T) {
	t.Parallel()

	view := rDerive(prOf(nil), rEntry(rWithDraft(rMarker)), false).Draft

	if view.PostedAt == nil {
		t.Error("postedAt is nil: the marker says it was posted")
	}

	if view.HeadMoved {
		t.Error("headMoved = true for a marker, which has no commit to be past")
	}

	if view.Findings == nil {
		t.Error("findings is nil: the page reads a list")
	}
}

func TestTheNewestLaunchWithADraftIsTheRows(t *testing.T) {
	t.Parallel()

	older := rWithDraft(func(stored *reviews.StoredDraft) { stored.Summary = "older" })
	newer := rLaunch(rLaunchAt+1000, func(launch *reviews.Launch) {
		launch.Draft = rDraft(func(stored *reviews.StoredDraft) { stored.Summary = "newer" })
	})
	bare := rLaunch(rLaunchAt+2000, nil)

	view := rDerive(prOf(nil), rEntry(older, newer, bare), false).Draft
	if view == nil || view.Summary != "newer" || view.LaunchAt != rLaunchAt+1000 {
		t.Errorf("draft = %+v, want the newer launch's", view)
	}
}

func TestReadyIsListedAfterRereview(t *testing.T) {
	t.Parallel()

	states := []string{
		reviews.StateDone, reviews.StateWaiting, reviews.StateReviewing, reviews.StateNew,
		reviews.StateReady, reviews.StateRereview,
	}
	rows := make([]*reviews.Row, 0, len(states))

	for _, state := range states {
		rows = append(rows, &reviews.Row{Key: state, State: state})
	}

	got := make([]string, 0, len(rows))
	for _, row := range reviews.SortRows(rows) {
		got = append(got, row.Key)
	}

	want := []string{
		reviews.StateRereview, reviews.StateReady, reviews.StateNew, reviews.StateReviewing,
		reviews.StateWaiting, reviews.StateDone,
	}
	if !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}
