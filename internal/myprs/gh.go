package myprs

// The GitHub source for My PRs: one GraphQL search per allowed pattern returns the open PRs of
// the signed-in user in it, with the threads, reviews and comments the rules read. Everything gh
// prints is data: a malformed piece is dropped, and text that came from a comment is stripped of
// control and bidi characters before anything shows or uses it.

import (
	"math"
	"regexp"

	"github.com/claytoncasey01/conn/internal/js"
)

// The windows below make at most 100 * (1 + 100 + 100 + 50 + 50 + 5) + 100 = 30,700 nodes, far
// under GitHub's 500,000 per query.
const (
	// SearchLimit is the most PRs one search reads.
	SearchLimit = 100
	// ThreadsLast is how many of a PR's newest review threads are read.
	ThreadsLast = 100
	// ReviewsLast is how many of a PR's newest reviews are read.
	ReviewsLast = 50
	// CommentsLast and CommentsFirst are how many of a PR's newest and first issue comments are
	// read.
	CommentsLast  = 50
	CommentsFirst = 5
	// titleUnitsMax is where a PR title is cut.
	titleUnitsMax = 300
)

// searchText is the start of every search: the open PRs of one author.
const searchText = "is:pr is:open author:"

// commentFields are what is read of an issue comment.
const commentFields = "id author { login } createdAt lastEditedAt body"

// searchQuery is the search. The search goes in as a variable, so nothing in it can change the
// query.
const searchQuery = `query($q: String!) {
  rateLimit { cost remaining resetAt }
  search(query: $q, type: ISSUE, first: 100) {
    nodes {
      ... on PullRequest {
        number title url isDraft createdAt updatedAt headRefName headRefOid baseRefName
        repository { nameWithOwner defaultBranchRef { name } }
        headRepository { nameWithOwner }
        commits(last: 1) { nodes { commit { committedDate } } }
        reviewThreads(last: 100) {
          pageInfo { hasPreviousPage }
          nodes {
            id isResolved isOutdated path line
            comments(first: 1) { nodes { author { login } createdAt body } }
          }
        }
        reviews(last: 50) { nodes { author { login } submittedAt commit { oid } } }
        comments(last: 50) { nodes { ` + commentFields + ` } }
        firstComments: comments(first: 5) { nodes { ` + commentFields + ` } }
      }
    }
  }
}`

// ThreadQuery is what the fresh read before a thread write needs of a thread: the id is a
// variable, never part of the text.
const ThreadQuery = `query($id: ID!) {
  node(id: $id) {
    ... on PullRequestReviewThread {
      id isResolved
      comments(last: 10) { nodes { author { login } body createdAt } }
      pullRequest { number state author { login } repository { nameWithOwner } }
    }
  }
}`

var (
	// ownerShape is an owner as the allowlist grammar has it: nothing that could add a search
	// qualifier (no space, colon, quote or slash).
	ownerShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	// loginShape is a GitHub login: letters, digits, hyphens and `_` (an Enterprise Managed
	// User's handle_shortcode), not starting with either; nothing that could add a qualifier.
	loginShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,99}$`)
	// ThreadID is a GitHub node id (a review thread's): letters, digits, `_`, `-` and `=`, of a
	// bounded length.
	ThreadID = regexp.MustCompile(`^[A-Za-z0-9_=-]{8,200}$`)
)

// Thread is one review thread of a PR.
type Thread struct {
	ID       string `json:"id"`
	Resolved bool   `json:"resolved"`
	Outdated bool   `json:"outdated"`
	// By is the login of the first comment's author, "" when the account is gone.
	By   string   `json:"by"`
	Path *string  `json:"path"`
	Line *float64 `json:"line"`
	// At is when the first comment was posted (ms).
	At float64 `json:"at"`
	// Text is the first comment, cleaned and cut to TextUnitsMax.
	Text string `json:"text"`
}

// Review is a submitted review: who, of which commit, when.
type Review struct {
	By  string  `json:"by"`
	Sha string  `json:"sha"`
	At  float64 `json:"at"`
}

// Comment is an issue comment; Body is cleaned and cut to TextUnitsMax.
type Comment struct {
	By       string   `json:"by"`
	At       float64  `json:"at"`
	EditedAt *float64 `json:"editedAt"`
	Body     string   `json:"body"`
}

// PR is one open PR of mine, as the rules read it.
type PR struct {
	Repo      string  `json:"repo"`
	Number    float64 `json:"number"`
	Title     string  `json:"title"`
	URL       string  `json:"url"`
	Draft     bool    `json:"draft"`
	CreatedAt float64 `json:"createdAt"`
	UpdatedAt float64 `json:"updatedAt"`
	// LastCommentAt is the newest of the comments, reviews and thread comments, Conn's own
	// Greptile ask left out; 0 when there are none.
	LastCommentAt float64 `json:"lastCommentAt"`
	HeadRef       string  `json:"headRef"`
	HeadSha       string  `json:"headSha"`
	HeadDate      float64 `json:"headDate"`
	BaseRef       string  `json:"baseRef"`
	// DefaultBranch is the repo's default branch; nil when GitHub has none (an empty repo).
	DefaultBranch *string   `json:"defaultBranch"`
	Fork          bool      `json:"fork"`
	Threads       []Thread  `json:"threads"`
	Reviews       []Review  `json:"reviews"`
	Comments      []Comment `json:"comments"`
	// Partial is that older threads than the newest ThreadsLast exist, or that GitHub answered
	// null for one of the PR's lists, so what the rules read may be short.
	Partial bool `json:"partial"`
}

// ThreadInfo is a thread as GitHub has it right now, and the PR it is on.
type ThreadInfo struct {
	Resolved bool    `json:"resolved"`
	Number   float64 `json:"number"`
	// State is OPEN, CLOSED or MERGED.
	State string `json:"state"`
	// Author is the login of the PR's author, "" when the account is gone.
	Author string `json:"author"`
	Repo   string `json:"repo"`
	// Last is the thread's newest comment (raw), nil when there is none or it is unreadable.
	Last *LastComment `json:"last"`
	// Recent are its newest comments (at most 10), oldest first, raw, an unreadable one left
	// out.
	Recent []RecentComment `json:"recent"`
}

// LastComment is a thread's newest comment, raw.
type LastComment struct {
	By   string `json:"by"`
	Body string `json:"body"`
}

// RecentComment is one of a thread's newest comments, raw; At is nil when GitHub gave no
// readable time.
type RecentComment struct {
	By   string   `json:"by"`
	Body string   `json:"body"`
	At   *float64 `json:"at"`
}

// ---- reading what gh printed ----

func object(value any) *js.Object {
	found, _ := value.(*js.Object)

	return found
}

func field(value any, key string) any {
	found, _ := object(value).Get(key)

	return found
}

func text(value any) (string, bool) {
	found, isText := value.(string)

	return found, isText
}

func textOrNil(value any) *string {
	found, isText := value.(string)
	if !isText {
		return nil
	}

	return &found
}

func loginOf(value any) string {
	name, _ := field(value, "login").(string)

	return name
}

// isoTime is an ISO time in ms; NaN when it is not one.
func isoTime(value any) float64 {
	found, isText := value.(string)
	if !isText {
		return math.NaN()
	}

	return js.DateParse(found)
}

func nodes(value any) []any {
	found, _ := field(value, "nodes").([]any)

	return found
}

// answered is a list GitHub answered (null is what a field it could not resolve becomes).
func answered(value any) bool {
	_, isList := field(value, "nodes").([]any)

	return isList
}

func parseThread(value any) *Thread {
	thread := object(value)
	if thread == nil {
		return nil
	}

	list := nodes(field(thread, "comments"))
	if len(list) == 0 {
		return nil
	}

	first := object(list[0])
	threadID, _ := text(field(thread, "id"))

	if first == nil {
		return nil
	}

	if threadID == "" {
		return nil
	}

	postedAt := isoTime(field(first, "createdAt"))
	if math.IsNaN(postedAt) {
		return nil
	}

	resolved, _ := field(thread, "isResolved").(bool)
	outdated, _ := field(thread, "isOutdated").(bool)
	body, _ := text(field(first, "body"))

	var line *float64
	if number, isNumber := field(thread, "line").(float64); isNumber {
		line = &number
	}

	return &Thread{
		ID: threadID, Resolved: resolved, Outdated: outdated, By: loginOf(field(first, "author")),
		Path: textOrNil(field(thread, "path")), Line: line, At: postedAt,
		Text: Clean(body, TextUnitsMax),
	}
}

func parseReview(value any) *Review {
	review := object(value)
	// A pending review has no submit time and is not a review yet.
	submittedAt := isoTime(field(review, "submittedAt"))

	if review == nil {
		return nil
	}

	if math.IsNaN(submittedAt) {
		return nil
	}

	sha, _ := text(field(field(review, "commit"), "oid"))

	return &Review{By: loginOf(field(review, "author")), Sha: sha, At: submittedAt}
}

func parseComment(value any) *Comment {
	comment := object(value)
	madeAt := isoTime(field(comment, "createdAt"))

	if comment == nil {
		return nil
	}

	if math.IsNaN(madeAt) {
		return nil
	}

	var editedAt *float64
	if edited := isoTime(field(comment, "lastEditedAt")); !math.IsNaN(edited) {
		editedAt = &edited
	}

	body, _ := text(field(comment, "body"))

	return &Comment{
		By: loginOf(field(comment, "author")), At: madeAt, EditedAt: editedAt,
		Body: Clean(body, TextUnitsMax),
	}
}

// parseComments are both comment windows, each comment once (by its id; one without an id
// cannot be matched to its copy and is dropped), oldest first.
func parseComments(pull *js.Object) []Comment {
	byID := js.NewMap[*Comment]()

	all := append(append([]any{}, nodes(field(pull, "firstComments"))...),
		nodes(field(pull, "comments"))...)

	for _, value := range all {
		commentID, _ := text(field(value, "id"))
		comment := parseComment(value)

		if commentID == "" {
			continue
		}

		if comment != nil {
			byID.Set(commentID, comment)
		}
	}

	out := make([]Comment, 0, byID.Len())
	for _, key := range byID.Keys() {
		comment, _ := byID.Get(key)
		out = append(out, *comment)
	}

	js.Sort(out, func(left, right Comment) float64 { return left.At - right.At })

	return out
}
