package reviews_test

// Post review: a stored draft's ticked findings and chosen verdict go to GitHub as one review,
// only when the person clicks, and only while the draft is still the one to post.

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// errGitHubRefused is how gh words GitHub refusing a posted review.
var errGitHubRefused = errors.New("gh: Unprocessable Entity (HTTP 422)")

// errUnclear is a post that failed with no answer from GitHub: it may have taken the review.
var errUnclear = errors.New("context deadline exceeded")

// errBadGateway is how gh words GitHub failing with a server error.
var errBadGateway = errors.New("gh: Bad Gateway (HTTP 502)")

// pRefusedText is how gh words GitHub refusing a posted review with a client error.
const pRefusedText = "gh: Unprocessable Entity (HTTP 422)"

// pComments are the line comments of the findings at indexes of rDraft.
func pComments(indexes ...int) []reviews.PostComment {
	findings := rDraft(nil).Findings
	comments := make([]reviews.PostComment, 0, len(indexes))

	for _, index := range indexes {
		finding := findings[index]
		comments = append(comments, reviews.PostComment{
			Path: finding.Path, Line: finding.Line, Side: finding.Side, Body: finding.Body,
		})
	}

	return comments
}

// pBody is the review body of rDraft with the findings at indexes outside the diff.
func pBody(indexes ...int) string {
	stored := rDraft(nil)
	outside := make([]reviews.Finding, 0, len(indexes))

	for _, index := range indexes {
		outside = append(outside, stored.Findings[index])
	}

	return reviews.ReviewBody(stored.Summary, outside)
}

func pSeverity(index int, severity string) func(stored *reviews.StoredDraft) {
	return func(stored *reviews.StoredDraft) { stored.Findings[index].Severity = severity }
}

func TestBuildPost(t *testing.T) {
	t.Parallel()

	allInline := func(stored *reviews.StoredDraft) { stored.Inline = []bool{true, true} }
	notRead := func(stored *reviews.StoredDraft) { stored.Inline = nil }
	// longBody fills the body with findings outside the diff up to units UTF-16 units: each
	// "é" is one unit and two bytes, so a count in bytes would refuse the exact fit.
	longBody := func(units int) func(stored *reviews.StoredDraft) {
		return func(stored *reviews.StoredDraft) {
			stored.Inline = nil
			stored.Findings = stored.Findings[:1]
			stored.Findings[0].Body = "x"
			fill := units - len([]rune(reviews.ReviewBody(stored.Summary, stored.Findings))) + 1
			stored.Findings[0].Body = strings.Repeat("é", fill)
		}
	}

	for name, row := range map[string]struct {
		change       func(stored *reviews.StoredDraft)
		ticked       []int
		verdict      string
		wantErr      error
		wantEvent    string
		wantComments []reviews.PostComment
		wantBody     string
		// wantUnits is the body's length in UTF-16 units, checked instead of wantBody.
		wantUnits int
	}{
		"every finding ticked and inline": {
			change: allInline, ticked: []int{0, 1}, verdict: reviews.VerdictComment,
			wantEvent: "COMMENT", wantComments: pComments(0, 1), wantBody: pBody(),
		},
		"a finding outside the diff goes in the body": {
			ticked: []int{1, 0}, verdict: reviews.VerdictComment,
			wantEvent: "COMMENT", wantComments: pComments(0), wantBody: pBody(1),
		},
		"a diff not read puts every finding in the body": {
			change: notRead, ticked: []int{0, 1}, verdict: reviews.VerdictComment,
			wantEvent: "COMMENT", wantComments: pComments(), wantBody: pBody(0, 1),
		},
		"nothing ticked is the summary alone": {
			ticked: []int{}, verdict: reviews.VerdictApprove,
			wantEvent: "APPROVE", wantComments: pComments(), wantBody: pBody(),
		},
		"an unticked finding goes nowhere": {
			ticked: []int{0}, verdict: reviews.VerdictComment,
			wantEvent: "COMMENT", wantComments: pComments(0), wantBody: pBody(),
		},
		"approve beside an unticked blocker": {
			change: pSeverity(0, "P0"), ticked: []int{1}, verdict: reviews.VerdictApprove,
			wantEvent: "APPROVE", wantComments: pComments(), wantBody: pBody(1),
		},
		"approve with a ticked P1": {
			change: pSeverity(0, "P1"), ticked: []int{0}, verdict: reviews.VerdictApprove,
			wantErr: reviews.ErrBlockerApproved,
		},
		"approve with a ticked P0": {
			change: pSeverity(1, "P0"), ticked: []int{1}, verdict: reviews.VerdictApprove,
			wantErr: reviews.ErrBlockerApproved,
		},
		"an index past the findings": {
			ticked: []int{2}, verdict: reviews.VerdictComment, wantErr: reviews.ErrBadFindings,
		},
		"a negative index": {
			ticked: []int{-1}, verdict: reviews.VerdictComment, wantErr: reviews.ErrBadFindings,
		},
		"a repeated index": {
			ticked: []int{0, 0}, verdict: reviews.VerdictComment,
			wantErr: reviews.ErrBadFindings,
		},
		"request changes is never sent": {
			ticked: []int{0}, verdict: "request_changes", wantErr: reviews.ErrBadVerdict,
		},
		"a body over GitHub's limit": {
			change: longBody(reviews.MaxReviewBodyUnits + 1), ticked: []int{0},
			verdict: reviews.VerdictComment, wantErr: reviews.ErrReviewTooLong,
		},
		"a body at GitHub's limit": {
			change: longBody(reviews.MaxReviewBodyUnits), ticked: []int{0},
			verdict: reviews.VerdictComment, wantEvent: "COMMENT", wantComments: pComments(),
			wantUnits: reviews.MaxReviewBodyUnits,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			post, err := reviews.BuildPost(*rDraft(row.change), row.ticked, row.verdict)
			if row.wantErr != nil {
				if !errors.Is(err, row.wantErr) {
					t.Errorf("error %v, want %v", err, row.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if post.Commit != rCommit {
				t.Errorf("commit_id %q", post.Commit)
			}

			if post.Event != row.wantEvent {
				t.Errorf("event %q", post.Event)
			}

			if !slices.Equal(post.Comments, row.wantComments) {
				t.Errorf("comments %+v", post.Comments)
			}

			if row.wantUnits > 0 {
				if units := len(utf16.Encode([]rune(post.Body))); units != row.wantUnits {
					t.Errorf("body of %d units, want %d", units, row.wantUnits)
				}

				return
			}

			if post.Body != row.wantBody {
				t.Errorf("body %q", post.Body)
			}
		})
	}
}

// TestBuildPostReportsEveryProblem: a bad verdict and bad indexes are both said.
func TestBuildPostReportsEveryProblem(t *testing.T) {
	t.Parallel()

	_, err := reviews.BuildPost(*rDraft(nil), []int{5}, "reject")

	if !errors.Is(err, reviews.ErrBadVerdict) {
		t.Errorf("no bad verdict in %v", err)
	}

	if !errors.Is(err, reviews.ErrBadFindings) {
		t.Errorf("no bad findings in %v", err)
	}
}

// pRow is one post through Handle: the world it runs in and what it answers.
type pRow struct {
	// change changes the PR's tracking before the post (nil: one launch with rDraft).
	change func(entry *reviews.Entry)
	// settings are the settings the post runs with (nil: the world's).
	settings *settingsOpts
	body     string
	postErr  error
	wantCode int
	wantBody string
	// wantPosts is how many reviews reached GitHub.
	wantPosts int
	// wantPosted is that the draft is stored as posted, in reviews.json too.
	wantPosted bool
}

// pPostBody is a post's body naming the PR, the launch, the ticked findings and the verdict.
func pPostBody(key string, launchAt float64, findings, verdict string) string {
	return `{"key":"` + key + `","launchAt":` + strconv.FormatFloat(launchAt, 'f', -1, 64) +
		`,"findings":` + findings + `,"verdict":"` + verdict + `"}`
}

// pAll is the post of both findings of rDraft, as a comment.
func pAll() string { return pPostBody(rKey, rLaunchAt, "[0,1]", reviews.VerdictComment) }

func pSeedLaunch(entry *reviews.Entry, change func(launch *reviews.Launch)) {
	entry.Launches = []*reviews.Launch{rLaunch(rLaunchAt, change)}
}

func pSeedDraft(change func(stored *reviews.StoredDraft)) func(entry *reviews.Entry) {
	return func(entry *reviews.Entry) {
		pSeedLaunch(entry, func(launch *reviews.Launch) { launch.Draft = rDraft(change) })
	}
}

func pRows() map[string]pRow {
	busy := func(why string) string { return `{"error":"` + why + `"}` }
	allowOther := settingsOpts{allow: []string{"other/*"}}
	off := false

	return map[string]pRow{
		"a post lands": {
			body: pAll(), wantCode: 200, wantBody: `{"ok":true,"url":"` + worldReviewURL + `"}`,
			wantPosts: 1, wantPosted: true,
		},
		"a merged PR is not posted to": {
			change: func(entry *reviews.Entry) {
				pSeedDraft(nil)(entry)
				entry.Ended = &reviews.Ended{How: reviews.EndedMerged, At: worldT0 - minuteMs}
			},
			body: pAll(), wantCode: 409, wantBody: busy(reviews.TextPostEnded),
		},
		"a posted draft is not posted again": {
			change: pSeedDraft(rPosted), body: pAll(), wantCode: 409,
			wantBody: busy(reviews.TextPostPosted), wantPosted: true,
		},
		"a posted marker is not posted again": {
			change: pSeedDraft(rMarker), body: pAll(), wantCode: 409,
			wantBody: busy(reviews.TextPostPosted), wantPosted: true,
		},
		"a set-aside draft is not posted": {
			change: pSeedDraft(rSetAside), body: pAll(), wantCode: 409,
			wantBody: busy(reviews.TextPostSetAside),
		},
		"a repo no longer allowed is not posted to": {
			settings: &allowOther, body: pAll(), wantCode: 409,
			wantBody: busy(reviews.TextPostNotAllowed),
		},
		"GitHub turned off is not posted to": {
			settings: &settingsOpts{github: &off}, body: pAll(), wantCode: 409,
			wantBody: busy(reviews.TextPostGitHubOff),
		},
		"an unknown PR": {
			body:     pPostBody("acme/widgets#99", rLaunchAt, "[0]", reviews.VerdictComment),
			wantCode: 404, wantBody: busy(reviews.TextPostNoPR),
		},
		"an unknown launch": {
			body:     pPostBody(rKey, rLaunchAt+1, "[0]", reviews.VerdictComment),
			wantCode: 404, wantBody: busy(reviews.TextPostNoDraft),
		},
		"a launch with no draft": {
			change: func(entry *reviews.Entry) { pSeedLaunch(entry, nil) }, body: pAll(),
			wantCode: 404, wantBody: busy(reviews.TextPostNoDraft),
		},
		"GitHub refuses": {
			body: pAll(), postErr: reviews.RefusedPost(pRefusedText), wantCode: 502,
			wantBody: busy(errGitHubRefused.Error()), wantPosts: 1,
		},
		"approve with a ticked blocker": {
			change:   pSeedDraft(pSeverity(0, "P1")),
			body:     pPostBody(rKey, rLaunchAt, "[0]", reviews.VerdictApprove),
			wantCode: 400, wantBody: busy(reviews.ErrBlockerApproved.Error()),
		},
		"a body that names nothing": {
			body: `{"key":7,"launchAt":"x","findings":[0.5]}`, wantCode: 400,
			wantBody: busy("key: not a PR; launchAt: not a launch; " +
				"findings: not a list of finding numbers; verdict: not approve or comment"),
		},
	}
}

func TestPostRoute(t *testing.T) {
	t.Parallel()

	for name, row := range pRows() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tab, fakes := pReady(t, row)
			reply := pPost(t, tab, row.body)

			if reply.Code != row.wantCode {
				t.Errorf("code %d", reply.Code)
			}

			if got := string(js.Marshal(reply.Body)); got != row.wantBody {
				t.Errorf("body %s", got)
			}

			if len(fakes.posts) != row.wantPosts {
				t.Errorf("posts %d", len(fakes.posts))
			}

			if posted := pStored(t, tab).PostedAt != nil; posted != row.wantPosted {
				t.Errorf("held as posted %v", posted)
			}

			saved := pLaunch(t, reviews.LoadFile(fakes.file)).Draft
			if posted := saved != nil && saved.PostedAt != nil; posted != row.wantPosted {
				t.Errorf("saved as posted %v", posted)
			}
		})
	}
}

// pReady is Reviews with PR 7 tracked as row says, GitHub answering row's post.
func pReady(t *testing.T, row pRow) (*reviews.Reviews, *world) {
	t.Helper()

	tab, fakes := bReady(t, setupOpts{})

	if row.settings != nil {
		fakes.setSettings(settingsOf(*row.settings))
	}

	change := row.change
	if change == nil {
		change = pSeedDraft(nil)
	}

	fakes.with(func(w *world) {
		w.postResult = func() (string, error) { return worldReviewURL, row.postErr }
	})

	tab.WithFile(func(file *reviews.File) {
		entry := rEntry()
		change(entry)
		file.PRs.Set(rKey, entry)
	})

	err := reviews.SaveFile(fakes.file, pFile(tab), nil)
	if err != nil {
		t.Fatal(err)
	}

	return tab, fakes
}

// pFile is the file Reviews holds.
func pFile(tab *reviews.Reviews) *reviews.File {
	var held *reviews.File

	tab.WithFile(func(file *reviews.File) { held = file })

	return held
}

func pPost(t *testing.T, tab *reviews.Reviews, body string) reviews.Reply {
	t.Helper()

	parsed, err := js.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}

	object, _ := parsed.(*js.Object)

	reply, err := tab.Handle(t.Context(), reviews.RoutePost, object)
	if err != nil {
		t.Fatal(err)
	}

	return reply
}

// pLaunch is the launch of PR 7 in file.
func pLaunch(t *testing.T, file *reviews.File) *reviews.Launch {
	t.Helper()

	entry, found := file.PRs.Get(rKey)
	if !found {
		t.Fatal("PR 7 is not tracked")
	}

	if len(entry.Launches) == 0 {
		t.Fatal("PR 7 has no launch")
	}

	return entry.Launches[0]
}

// pStored is the draft of PR 7's launch as Reviews holds it now, copied holding its lock.
func pStored(t *testing.T, tab *reviews.Reviews) reviews.StoredDraft {
	t.Helper()

	var stored reviews.StoredDraft

	tab.WithFile(func(file *reviews.File) {
		if draft := pLaunch(t, file).Draft; draft != nil {
			stored = *draft
		}
	})

	return stored
}

// TestAPostLandsAsOneReview: the post carries the draft's commit, the ticked findings and the
// chosen verdict, to the PR's repo and number, and the draft records where it is.
func TestAPostLandsAsOneReview(t *testing.T) {
	t.Parallel()

	tab, fakes := pReady(t, pRow{})
	bumps := fakes.snapshotCalls().bump

	pPost(t, tab, pPostBody(rKey, rLaunchAt, "[0,1]", reviews.VerdictApprove))

	if len(fakes.posts) != 1 {
		t.Fatalf("posts %d", len(fakes.posts))
	}

	sent := fakes.posts[0]
	if sent.repo != worldRepo || sent.number != 7 {
		t.Errorf("posted to %s#%d", sent.repo, sent.number)
	}

	want := reviews.ReviewPost{
		Commit: rCommit, Event: "APPROVE", Body: pBody(1), Comments: pComments(0),
	}
	if !pSamePost(sent.post, want) {
		t.Errorf("post %+v", sent.post)
	}

	stored := pStored(t, tab)
	if stored.PostedAt == nil || *stored.PostedAt != worldT0 {
		t.Errorf("postedAt %v", stored.PostedAt)
	}

	if textValue(stored.URL) != worldReviewURL {
		t.Errorf("url %q", textValue(stored.URL))
	}

	if fakes.snapshotCalls().bump == bumps {
		t.Error("the page was not told")
	}
}

func pSamePost(got, want reviews.ReviewPost) bool {
	same := got.Commit == want.Commit && got.Event == want.Event && got.Body == want.Body

	return same && slices.Equal(got.Comments, want.Comments)
}

// TestASecondPostWhileOneIsInFlightIsRefused: the second click is refused and calls nothing,
// and the first one lands.
func TestASecondPostWhileOneIsInFlightIsRefused(t *testing.T) {
	t.Parallel()

	tab, fakes := pReady(t, pRow{})
	held := newGate()

	fakes.with(func(w *world) { w.postGate = held })

	first := pPostLater(t, tab, pAll())

	held.reached(t)

	second := pPost(t, tab, pAll())
	if second.Code != 409 {
		t.Errorf("second code %d", second.Code)
	}

	if got := string(js.Marshal(second.Body)); got != `{"error":"`+reviews.TextPostInFlight+`"}` {
		t.Errorf("second body %s", got)
	}

	held.open()

	if reply := <-first; reply.Code != 200 {
		t.Errorf("first code %d", reply.Code)
	}

	if len(fakes.posts) != 1 {
		t.Errorf("posts %d", len(fakes.posts))
	}
}

// pPostLater is a post through Handle on a goroutine of its own; its reply comes on the channel.
func pPostLater(t *testing.T, tab *reviews.Reviews, body string) <-chan reviews.Reply {
	t.Helper()

	parsed, err := js.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}

	object, _ := parsed.(*js.Object)
	replies := make(chan reviews.Reply, 1)

	go func() {
		reply, _ := tab.Handle(context.WithoutCancel(t.Context()), reviews.RoutePost, object)
		replies <- reply
	}()

	return replies
}

// pDraftPost is the post of both findings of the draft the harness's launch stored.
func pDraftPost(run *bHarness) string {
	return pPostBody(keyOf(run.number), run.launch().At, "[0,1]", reviews.VerdictComment)
}

// pSummary is the summary of the draft the harness's launch holds now, and whether it is posted.
func pSummary(run *bHarness) (string, bool) {
	draft := dLaunches(run)[0].Draft
	if draft == nil {
		return "", false
	}

	return draft.Summary, draft.PostedAt != nil
}

// TestADraftRewrittenWhileItIsPostedIsKept: the draft posted is the one recorded, and a newer
// file the session wrote meanwhile is read once the post is refused.
func TestADraftRewrittenWhileItIsPostedIsKept(t *testing.T) {
	t.Parallel()

	for name, row := range map[string]struct {
		postErr    error
		wantCode   int
		wantAfter  string
		wantPosted bool
	}{
		"the post lands": {wantCode: 200, wantAfter: "Sound.", wantPosted: true},
		"GitHub refuses": {
			postErr: reviews.RefusedPost(pRefusedText), wantCode: 502, wantAfter: "Newer.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
			dWrite(run, 1, dDraft("Sound."))
			run.at(2)

			held := newGate()

			run.fakes.with(func(w *world) {
				w.postGate = held
				w.postResult = func() (string, error) { return worldReviewURL, row.postErr }
			})

			replies := pPostLater(t, run.tab, pDraftPost(run))
			held.reached(t)
			dWrite(run, 3, dDraft("Newer."))
			run.at(4)

			if summary, _ := pSummary(run); summary != "Sound." {
				t.Errorf("while posting the draft is %q", summary)
			}

			held.open()

			if reply := <-replies; reply.Code != row.wantCode {
				t.Errorf("code %d", reply.Code)
			}

			run.at(5)

			summary, posted := pSummary(run)
			if summary != row.wantAfter {
				t.Errorf("after the post the draft is %q", summary)
			}

			if posted != row.wantPosted {
				t.Errorf("posted %v", posted)
			}
		})
	}
}

// TestAPostWaitsForTheDiffBeingRead: a draft whose diff is being read is not posted, so the
// draft that read stores is never one other than the one posted.
func TestAPostWaitsForTheDiffBeingRead(t *testing.T) {
	t.Parallel()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	dWrite(run, 1, dDraft("Sound."))
	run.at(2)

	held := newGate()

	run.fakes.with(func(w *world) {
		w.diff = func(string, string, string) (string, error) {
			held.pass()

			return worldDiff, nil
		}
	})

	dWrite(run, 3, dDraft("Newer."))
	run.fakes.with(func(fakes *world) { fakes.now = worldT0 + 4*minuteMs })
	run.tab.Tick()
	held.reached(t)

	reply := pPost(t, run.tab, pDraftPost(run))

	held.open()
	run.tab.Settle()

	if got := string(js.Marshal(reply.Body)); got != `{"error":"`+reviews.TextPostReading+`"}` {
		t.Errorf("body %s", got)
	}

	if len(run.fakes.posts) != 0 {
		t.Errorf("posts %d", len(run.fakes.posts))
	}
}

// TestAPRNotReadYetIsNotPostedTo: a tracked PR the poll has not read is not known to be open.
func TestAPRNotReadYetIsNotPostedTo(t *testing.T) {
	t.Parallel()

	tab, fakes := pReady(t, pRow{})
	other := keyOf(8)

	tab.WithFile(func(file *reviews.File) {
		entry := rEntry()
		pSeedDraft(nil)(entry)
		file.PRs.Set(other, entry)
	})

	reply := pPost(t, tab, pPostBody(other, rLaunchAt, "[0]", reviews.VerdictComment))

	if got := string(js.Marshal(reply.Body)); got != `{"error":"`+reviews.TextPostUnread+`"}` {
		t.Errorf("body %s", got)
	}

	if len(fakes.posts) != 0 {
		t.Errorf("posts %d", len(fakes.posts))
	}
}

// TestADryRunPostsNothing: a dry Reviews refuses a post and calls no dependency.
func TestADryRunPostsNothing(t *testing.T) {
	t.Parallel()

	tab := aDryReviews(t, filepath.Join(t.TempDir(), "never-written.json"), aFixedNow)

	aNoPanic(t, "post", func() {
		reply := pPost(t, tab, pAll())
		if reply.Code != 409 {
			t.Errorf("code %d", reply.Code)
		}
	})
}

// pSend is a scripted gh run with input: what it was given, and what it answers.
type pSend struct {
	args   []string
	input  string
	answer string
	err    error
	calls  int
}

func (s *pSend) send(_ context.Context, args []string, input string) (string, error) {
	s.calls++
	s.args, s.input = args, input

	return s.answer, s.err
}

// pPostError checks a post's error: it is want, says wantText when that is set, and is a
// refusal (GitHub surely took no review) as wantRefused says.
func pPostError(t *testing.T, err, want error, wantText string, wantRefused bool) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Errorf("error %v", err)
	}

	if refused := errors.Is(err, reviews.ErrPostRefused); refused != wantRefused {
		t.Errorf("refused %v", refused)
	}

	if wantText != "" && err.Error() != wantText {
		t.Errorf("error text %q", err.Error())
	}
}

func TestGhPostReview(t *testing.T) {
	t.Parallel()

	post := reviews.ReviewPost{
		Commit: rCommit, Event: "COMMENT", Body: "Sound.", Comments: pComments(0),
	}
	wantInput := `{"commit_id":"` + rCommit + `","event":"COMMENT","body":"Sound.",` +
		`"comments":[{"path":"a.go","line":2,"side":"RIGHT","body":"Cap it."}]}`
	wantArgs := []string{
		"api", "--method", "POST", "repos/acme/widgets/pulls/7/reviews", "--input", "-",
	}

	for name, row := range map[string]struct {
		repo     string
		answer   string
		err      error
		wantURL  string
		wantErr  error
		wantText string
		// wantRefused is that GitHub surely took no review.
		wantRefused bool
		wantCalls   int
	}{
		"the review's url": {
			repo: worldRepo, answer: `{"id":1,"html_url":"` + worldReviewURL + `"}`,
			wantURL: worldReviewURL, wantCalls: 1,
		},
		"an answer with no url is still posted": {
			repo: worldRepo, answer: `not json`, wantURL: "", wantCalls: 1,
		},
		"gh fails with no answer: GitHub may have taken it": {
			repo: worldRepo, err: errGitHubRefused, wantErr: errGitHubRefused, wantCalls: 1,
		},
		"a client error with GitHub's message is a refusal": {
			repo: worldRepo, err: errGitHubRefused, answer: `{"message":"Validation Failed"}`,
			wantErr: errGitHubRefused, wantText: errGitHubRefused.Error(), wantRefused: true,
			wantCalls: 1,
		},
		"a server error is not a refusal": {
			repo: worldRepo, err: errBadGateway, wantErr: errBadGateway,
			answer: `{"message":"Server Error"}`, wantCalls: 1,
		},
		"GitHub's reasons follow gh's message": {
			repo: worldRepo, err: errGitHubRefused, wantErr: errGitHubRefused, wantCalls: 1,
			answer: `{"message":"Unprocessable Entity","errors":["Can not approve your own ` +
				`pull request",{"resource":"PullRequestReview","message":"Line is gone"}]}`,
			wantText: errGitHubRefused.Error() +
				": Can not approve your own pull request; Line is gone",
			wantRefused: true,
		},
		"a repo that is not safe calls nothing": {
			repo: "../x", wantErr: reviews.ErrPostUnsafe, wantCalls: 0,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := &pSend{answer: row.answer, err: row.err}
			url, err := reviews.Gh{Send: script.send}.PostReview(t.Context(), row.repo, 7, post)

			pPostError(t, err, row.wantErr, row.wantText, row.wantRefused)

			if url != row.wantURL {
				t.Errorf("url %q", url)
			}

			if script.calls != row.wantCalls {
				t.Fatalf("calls %d", script.calls)
			}

			if row.wantCalls == 0 {
				return
			}

			if !slices.Equal(script.args, wantArgs) {
				t.Errorf("args %s", strings.Join(script.args, " "))
			}

			if script.input != wantInput {
				t.Errorf("input %s", script.input)
			}
		})
	}
}
