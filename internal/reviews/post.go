package reviews

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
)

// Posting a stored draft: its ticked findings and the verdict chosen go to GitHub as one review,
// only when the person presses Post review. The press is checked holding the mutex (the PR is
// open and allowed, the draft is live and unposted, no post of it is in flight), the review is
// built from the draft as stored, and the one gh call runs without the mutex. Only a post GitHub
// took is recorded, so a failed one can be pressed again.

const (
	// eventApprove and eventComment are the review events Conn sends. Request changes is never
	// one of them.
	eventApprove = "APPROVE"
	eventComment = "COMMENT"
	// codeBadGateway is GitHub refusing or failing a post.
	codeBadGateway = 502
	// postTimeout stops one post to GitHub: gh's own limit on a call.
	postTimeout = 30 * time.Second
)

// Why a press of Post review is refused.
const (
	textPostDry        = "a dry run posts nothing"
	textPostNoPR       = "that PR is not in the list"
	textPostNoDraft    = "that launch has no review to post"
	textPostInFlight   = "this review is being posted already"
	textPostReading    = "the review file is being read again: try again in a moment"
	textPostUnread     = "the PR has not been read from GitHub yet: try again after the next poll"
	textPostPosted     = "this review was posted already"
	textPostSetAside   = "this draft was set aside: a newer review replaced it"
	textPostEnded      = "the PR has merged or closed, so its review is not posted"
	textPostNotAllowed = "the PR's repo is no longer in the allowed repos"
	textPostGitHubOff  = "GitHub is off in Settings"
	textPostBadKey     = "key: not a PR"
	textPostBadLaunch  = "launchAt: not a launch"
	textPostBadTicked  = "findings: not a list of finding numbers"
)

// Why a review cannot be built from a draft.
var (
	errBadVerdict  = errors.New("verdict: not approve or comment")
	errBadFindings = errors.New(
		"findings: a finding number is past the draft's findings or repeated")
	errBlockerApproved = errors.New(
		"a ticked P0 or P1 finding cannot be approved: comment instead, or untick it")
)

// ReviewPost is one review as GitHub's create-review call takes it. Gh.PostReview sends it
// with GitHub's own key for each field (commit_id, event, body, comments).
type ReviewPost struct {
	// Commit is the head the session reviewed: the comments' lines are of it.
	Commit string
	// Event is APPROVE or COMMENT.
	Event    string
	Body     string
	Comments []PostComment
}

// PostComment is one line comment of a review.
type PostComment struct {
	Path string `json:"path"`
	Line int64  `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

// launchID names a launch across a call made without the mutex: its PR and when it started.
type launchID struct {
	key string
	at  float64
}

// BuildPost is the review of draft with the findings at the indexes ticked and the verdict
// chosen (VerdictApprove or VerdictComment): a ticked finding on a line of the diff is a line
// comment, any other ticked one goes in the body under the summary, and an unticked one goes
// nowhere. The errors, all of them at once, are a verdict not in the table, an index out of
// range or repeated, and an approval beside a ticked P0 or P1.
func BuildPost(draft StoredDraft, ticked []int, verdict string) (ReviewPost, error) {
	post, problems := buildPost(draft, ticked, verdict)

	return post, errors.Join(problems...)
}

func buildPost(draft StoredDraft, ticked []int, verdict string) (ReviewPost, []error) {
	event, chosen, problems := postProblems(draft.Findings, ticked, verdict)
	if len(problems) > 0 {
		return ReviewPost{Commit: "", Event: "", Body: "", Comments: []PostComment{}}, problems
	}

	comments, outside := placeTicked(draft, chosen)

	return ReviewPost{
		Commit: draft.Commit, Event: event, Body: ReviewBody(draft.Summary, outside),
		Comments: comments,
	}, nil
}

// postProblems are the event of verdict and, per finding, whether it is ticked, or every reason
// they cannot be posted.
func postProblems(findings []Finding, ticked []int, verdict string) (string, []bool, []error) {
	problems := []error{}

	event, known := eventOf(verdict)
	if !known {
		problems = append(problems, errBadVerdict)
	}

	chosen, valid := tickedMask(ticked, len(findings))
	if !valid {
		problems = append(problems, errBadFindings)
	}

	// A list with a bad index has no mask to look for a blocker in.
	if valid && event == eventApprove && blockerTicked(findings, chosen) {
		problems = append(problems, errBlockerApproved)
	}

	return event, chosen, problems
}

// placeTicked are the line comments of the ticked findings of draft on a line of its diff, and
// the ticked findings that go in the body instead: all of them when the diff was not read.
func placeTicked(draft StoredDraft, chosen []bool) ([]PostComment, []Finding) {
	comments := []PostComment{}
	outside := []Finding{}

	for index, finding := range draft.Findings {
		if !chosen[index] {
			continue
		}

		inline := index < len(draft.Inline) && draft.Inline[index]
		if !inline {
			outside = append(outside, finding)

			continue
		}

		comments = append(comments, PostComment{
			Path: finding.Path, Line: finding.Line, Side: finding.Side, Body: finding.Body,
		})
	}

	return comments, outside
}

// eventOf is the review event of a verdict, and false for one not in the table.
func eventOf(verdict string) (string, bool) {
	switch verdict {
	case VerdictApprove:
		return eventApprove, true
	case VerdictComment:
		return eventComment, true
	}

	return "", false
}

// tickedMask is, per finding of count, whether it is ticked; false when an index is out of
// range or repeated.
func tickedMask(ticked []int, count int) ([]bool, bool) {
	mask := make([]bool, count)

	for _, index := range ticked {
		if anyOf(index < 0, index >= count) {
			return nil, false
		}

		if mask[index] {
			return nil, false
		}

		mask[index] = true
	}

	return mask, true
}

// blockerTicked is whether a ticked finding is a P0 or P1, which a review does not approve.
func blockerTicked(findings []Finding, chosen []bool) bool {
	for index, finding := range findings {
		if all(chosen[index], slices.Contains([]string{"P0", "P1"}, finding.Severity)) {
			return true
		}
	}

	return false
}

// postRequest is a press of Post review as the page sent it.
type postRequest struct {
	launch  launchID
	parts   KeyParts
	ticked  []int
	verdict string
}

// postRequestOf is the press in body, or every reason it is not one.
func postRequestOf(body *js.Object) (postRequest, []string) {
	problems := []string{}
	key, _ := text(memberOf(body, "key"))

	parts, isKey := ParseKey(key)
	if !isKey {
		problems = append(problems, textPostBadKey)
	}

	launchAt, isNumber := number(memberOf(body, "launchAt"))
	if !isNumber {
		problems = append(problems, textPostBadLaunch)
	}

	ticked, isList := tickedOf(memberOf(body, "findings"))
	if !isList {
		problems = append(problems, textPostBadTicked)
	}

	verdict, _ := text(memberOf(body, "verdict"))
	if _, known := eventOf(verdict); !known {
		problems = append(problems, errBadVerdict.Error())
	}

	return postRequest{
		launch: launchID{key: key, at: launchAt}, parts: parts, ticked: ticked, verdict: verdict,
	}, problems
}

// tickedOf is a list of finding numbers: whole numbers from 0, no more of them, and none larger,
// than a draft keeps findings.
func tickedOf(value any) ([]int, bool) {
	values, isList := value.([]any)
	if !all(isList, len(values) <= maxDraftFindings) {
		return nil, false
	}

	ticked := make([]int, 0, len(values))

	for _, item := range values {
		found, isNumber := item.(float64)
		if !all(isNumber, found >= 0, found < maxDraftFindings, found == float64(int(found))) {
			return nil, false
		}

		ticked = append(ticked, int(found))
	}

	return ticked, true
}

// post is Post review pressed: it refuses before any call, else posts the review and records
// where it is.
func (r *Reviews) post(ctx context.Context, body *js.Object) Reply {
	request, problems := postRequestOf(body)
	if len(problems) > 0 {
		return bad(codeBadRequest, strings.Join(problems, "; "))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.deps.Config.Dry {
		return bad(codeBusy, textPostDry)
	}

	launch, refusal := r.postable(request.launch)
	if refusal != nil {
		return *refusal
	}

	built, buildProblems := buildPost(*launch.Draft, request.ticked, request.verdict)
	if len(buildProblems) > 0 {
		texts := []string{}
		for _, problem := range buildProblems {
			texts = append(texts, problem.Error())
		}

		return bad(codeBadRequest, strings.Join(texts, "; "))
	}

	return r.send(ctx, request, built)
}

// postable is the launch a press names when its draft may be posted now, else the refusal.
func (r *Reviews) postable(target launchID) (*Launch, *Reply) {
	entry := r.entryOf(target.key)
	if entry == nil {
		reply := bad(codeNotFound, textPostNoPR)

		return nil, &reply
	}

	launch := r.launchOf(target)
	if launch == nil || launch.Draft == nil {
		reply := bad(codeNotFound, textPostNoDraft)

		return nil, &reply
	}

	if why := r.postRefusal(target, entry, launch); why != "" {
		reply := bad(codeBusy, why)

		return nil, &reply
	}

	return launch, nil
}

// postRefusal is why the draft of launch, on entry, may not be posted now; "" when it may.
func (r *Reviews) postRefusal(target launchID, entry *Entry, launch *Launch) string {
	current, _ := r.settings()
	pull, known := r.pullOf(target.key)
	ended := anyOf(entry.Ended != nil, known && pull.Ended != nil)
	parts, _ := ParseKey(target.key)
	_, unsure := r.unsure[target]

	refusals := []struct {
		refused bool
		why     string
	}{
		{refused: r.posting[target], why: textPostInFlight},
		{refused: unsure, why: textPostChecking},
		// A diff read in flight would store a newer draft than the one posted.
		{refused: r.drafting[launch], why: textPostReading},
		{refused: posted(launch), why: textPostPosted},
		{refused: launch.Draft.SetAside != nil, why: textPostSetAside},
		{refused: ended, why: textPostEnded},
		{refused: !known, why: textPostUnread},
		{refused: !repos.Allowed(parts.Repo, current.Repos.Allow), why: textPostNotAllowed},
		{refused: !r.githubOn(&current), why: textPostGitHubOff},
		// A post that could not be recorded would be posted again after a restart.
		{refused: !r.writable, why: r.notSaving()},
		{refused: len(r.posting)+len(r.unsure) >= maxUnsurePosts, why: textPostTooMany},
	}

	for _, refusal := range refusals {
		if refusal.refused {
			return refusal.why
		}
	}

	return ""
}

// send posts built for request without the mutex, the launch marked as posting while it does,
// and records the post GitHub took. A failure answers GitHub's message and records nothing; one
// that leaves unclear whether GitHub took the review holds the launch as unsure until a poll's
// check settles it.
func (r *Reviews) send(ctx context.Context, request postRequest, built ReviewPost) Reply {
	r.posting[request.launch] = true
	defer delete(r.posting, request.launch)

	sentAt := r.now()

	var (
		url string
		err error
	)

	r.outside(func() {
		bounded, cancel := context.WithTimeout(ctx, postTimeout)
		defer cancel()

		url, err = r.deps.Gh.PostReview(bounded, request.parts.Repo, request.parts.Number, built)
	})

	if err != nil {
		r.logf("reviews: the review of %s was not posted %v", request.launch.key, err)

		if !errors.Is(err, errPostRefused) {
			r.unsure[request.launch] = unsurePost{sentAt: sentAt, checking: false}
		}

		return bad(codeBadGateway, err.Error())
	}

	return r.recordPosted(request.launch, url)
}

// recordPosted records on the launch target names that its draft was posted now, to url, and
// saves: the draft is never posted again.
func (r *Reviews) recordPosted(target launchID, url string) Reply {
	launch := r.launchOf(target)
	if launch == nil || launch.Draft == nil {
		if url == "" {
			return okReply("url", nil)
		}

		return okReply("url", url)
	}

	return okReply("url", r.markPosted(launch, r.now(), url))
}

// entryOf is the tracking of the PR key names, nil when it has none.
func (r *Reviews) entryOf(key string) *Entry {
	if r.file == nil {
		return nil
	}

	entry, found := r.file.PRs.Get(key)
	if !found {
		return nil
	}

	return entry
}

// launchOf is the launch target names, nil when it is not in the file.
func (r *Reviews) launchOf(target launchID) *Launch {
	entry := r.entryOf(target.key)
	if entry == nil {
		return nil
	}

	for _, launch := range entry.Launches {
		if launch.At == target.at {
			return launch
		}
	}

	return nil
}
