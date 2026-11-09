package reviews

import (
	"math"
	"regexp"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/prstatus"
	"github.com/claytoncasey01/conn/internal/repos"
)

// PRListFields are the fields asked of `gh pr list --json`.
const PRListFields = "number,title,author,isDraft,baseRefName,headRefOid,createdAt,updatedAt," +
	"url,reviewRequests,latestReviews,statusCheckRollup"

// What a PR's state reads as.
const (
	stateMerged = "MERGED"
	stateClosed = "CLOSED"
	typeUser    = "User"
	checksNone  = "none"
	// checksFailing and checksPending are what a rollup that failed, or is still running,
	// reads as.
	checksFailing = "failing"
	checksPending = "pending"
)

var ownerShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// isReviewKind is whether state is one of the review states GitHub reports.
func isReviewKind(state string) bool {
	switch state {
	case "APPROVED", "CHANGES_REQUESTED", "COMMENTED", "DISMISSED", "PENDING":
		return true
	}

	return false
}

func login(value any) string {
	name, _ := memberOf(object(value), "login").(string)

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

func parseReviews(value any) []Review {
	out := []Review{}

	list, _ := value.([]any)
	for _, item := range list {
		raw := object(item)
		author := login(memberOf(raw, "author"))
		state, _ := memberOf(raw, "state").(string)
		submittedAt := isoTime(memberOf(raw, "submittedAt"))

		// A pending review is a draft someone is composing, not a submitted review.
		if !all(raw != nil, author != "", isReviewKind(state),
			state != reviewPending, !math.IsNaN(submittedAt)) {
			continue
		}

		commit, _ := memberOf(object(memberOf(raw, "commit")), "oid").(string)
		out = append(out, Review{
			Author: author, State: state, Commit: commit, SubmittedAt: submittedAt,
		})
	}

	return out
}

// checksState is the rollup reduced to a state; a rollup that cannot be read (a null entry, a
// name that is not text) is none.
func checksState(rollup any) string {
	list, _ := rollup.([]any)
	for _, item := range list {
		if item == nil {
			return checksNone
		}

		check := object(item)
		if check == nil {
			continue
		}

		if !textName(check) {
			return checksNone
		}
	}

	return prstatus.SummarizeChecks(rollup).State
}

// textName is whether a check's name (name, else context, when present and not null) is text,
// as cleaning the name needs.
func textName(check *js.Object) bool {
	for _, key := range []string{"name", "context"} {
		value, present := check.Get(key)
		if !present {
			continue
		}

		if value == nil {
			continue
		}

		_, isText := value.(string)

		return isText
	}

	return true
}

// safeInteger is a number that is a positive safe integer.
func safeInteger(value any) (int64, bool) {
	found, isNumber := value.(float64)
	if !isNumber {
		return 0, false
	}

	if found != math.Trunc(found) {
		return 0, false
	}

	if found <= 0 {
		return 0, false
	}

	if found > maxSafeInteger {
		return 0, false
	}

	return int64(found), true
}

// prText are the text fields a PR needs.
type prText struct {
	title, url, baseRef, headSha string
}

func readPRText(raw *js.Object) (prText, bool) {
	title, titleOK := memberOf(raw, "title").(string)
	url, urlOK := memberOf(raw, "url").(string)
	baseRef, baseOK := memberOf(raw, "baseRefName").(string)
	headSha, headOK := memberOf(raw, "headRefOid").(string)

	return prText{title: title, url: url, baseRef: baseRef, headSha: headSha},
		all(titleOK, urlOK, baseOK, headOK)
}

func listOne(value any, repo string) *PR {
	raw := object(value)
	if raw == nil {
		return nil
	}

	author := object(memberOf(raw, "author"))
	created := isoTime(memberOf(raw, "createdAt"))
	updated := isoTime(memberOf(raw, "updatedAt"))
	fields, fieldsOK := readPRText(raw)
	name := login(author)

	number, numberOK := wholeNumber(memberOf(raw, "number"))
	if !all(numberOK, author != nil, name != "", fieldsOK, !math.IsNaN(created),
		!math.IsNaN(updated)) {
		return nil
	}

	requested := []string{}

	// Only user requests count; a team request (no login) is ignored.
	list, _ := memberOf(raw, "reviewRequests").([]any)
	for _, item := range list {
		request := object(item)
		if memberOf(request, "__typename") == typeUser {
			if found := login(request); found != "" {
				requested = append(requested, found)
			}
		}
	}

	bot := memberOf(author, "is_bot") == true
	if strings.HasPrefix(name, "app/") {
		bot = true
	}

	return &PR{
		Repo: repo, Number: number, Title: fields.title, Author: name, AuthorIsBot: bot,
		URL: fields.url, BaseRef: fields.baseRef, HeadSha: fields.headSha,
		IsDraft: memberOf(raw, "isDraft") == true, CreatedAt: created, UpdatedAt: updated,
		Checks: checksState(memberOf(raw, "statusCheckRollup")), Requested: requested,
		Reviews: parseReviews(memberOf(raw, "latestReviews")), Ended: nil, EndedAt: nil,
	}
}

// wholeNumber is a positive integer (Number.isInteger, safe or not).
func wholeNumber(value any) (int64, bool) {
	found, isNumber := value.(float64)
	if !isNumber {
		return 0, false
	}

	if math.IsInf(found, 0) {
		return 0, false
	}

	if found != math.Trunc(found) {
		return 0, false
	}

	if found <= 0 {
		return 0, false
	}

	return int64(found), true
}

// ParsePRList is `gh pr list --json PRListFields` output as PR records. A malformed entry is
// skipped; anything that is not a list gives none.
func ParsePRList(list any, repo string) []*PR {
	out := []*PR{}

	items, _ := list.([]any)
	for _, item := range items {
		if pr := listOne(item, repo); pr != nil {
			out = append(out, pr)
		}
	}

	return out
}

// Ending is how, and when, a PR ended; Ended is nil while it is open.
type Ending struct {
	Ended *string  `json:"ended"`
	At    *float64 `json:"at"`
}

// endedOf is merged or closed for a PR state, else nil.
func endedOf(state any) *string {
	switch state {
	case stateMerged:
		ended := EndedMerged

		return &ended
	case stateClosed:
		ended := EndedClosed

		return &ended
	}

	return nil
}

// ParseEnded is `gh pr view --json state,mergedAt,closedAt` output as how, and when, the PR
// ended.
func ParseEnded(view any) Ending {
	raw := object(view)

	ended := endedOf(memberOf(raw, "state"))
	if ended == nil {
		return Ending{Ended: nil, At: nil}
	}

	at := isoTime(memberOf(raw, endedField(*ended)))
	if math.IsNaN(at) {
		return Ending{Ended: ended, At: nil}
	}

	return Ending{Ended: ended, At: &at}
}

func endedField(ended string) string {
	if ended == EndedMerged {
		return "mergedAt"
	}

	return "closedAt"
}

// The searches' kinds.
const (
	SearchRequested = "requested"
	SearchOpen      = "open"
)

// SearchFor is the search for one allowlist pattern: requested lists the open PRs where GitHub
// asks for my review (as a user, not through a team), open every open, non-draft PR I did not
// write. An exact pattern is `repo:`, a wildcard is `user:` (which GitHub reads for an
// organization too). "" for a pattern that is not `owner/repo` or `owner/*`, so nothing else can
// reach the search string.
func SearchFor(kind, pattern string) string {
	var scope string

	if owner, isWildcard := strings.CutSuffix(pattern, "/*"); isWildcard {
		if !ownerShape.MatchString(owner) {
			return ""
		}

		scope = "user:" + owner
	} else {
		if !repos.IsName(pattern) {
			return ""
		}

		scope = "repo:" + pattern
	}

	if kind == SearchRequested {
		return "is:pr is:open archived:false user-review-requested:@me " + scope
	}

	return "is:pr is:open draft:false archived:false -author:@me " + scope
}

func nodesOf(value any) []any {
	nodes, _ := memberOf(object(value), "nodes").([]any)

	return nodes
}

func firstNode(value any) any {
	nodes := nodesOf(value)
	if len(nodes) == 0 {
		return nil
	}

	return nodes[0]
}

// rollupOf is the check rollup of the PR's newest commit, as the flat list SummarizeChecks reads.
func rollupOf(commits any) any {
	commit := object(memberOf(object(firstNode(commits)), "commit"))
	rollup := object(memberOf(commit, "statusCheckRollup"))

	return nodesOrEmpty(memberOf(rollup, "contexts"))
}

func nodesOrEmpty(value any) []any {
	nodes := nodesOf(value)
	if nodes == nil {
		return []any{}
	}

	return nodes
}

// checksOf is the checks of the newest commit. Only the first contexts are read, so the
// rollup's own state wins when it says failing or pending: a failure past the read ones is never
// shown as passing.
func checksOf(commits any) string {
	fromContexts := checksState(rollupOf(commits))
	commit := object(memberOf(object(firstNode(commits)), "commit"))
	state := memberOf(object(memberOf(commit, "statusCheckRollup")), "state")

	switch state {
	case "FAILURE", "ERROR":
		return checksFailing
	case "PENDING", "EXPECTED":
		if fromContexts != checksFailing {
			return checksPending
		}
	}

	return fromContexts
}

// ParsePRNode is one PR node of a search or a read as a record; nil when a field it needs is
// missing or malformed.
func ParsePRNode(value any) *PR {
	raw := object(value)
	if raw == nil {
		return nil
	}

	repo, _ := memberOf(object(memberOf(raw, "repository")), "nameWithOwner").(string)
	created := isoTime(memberOf(raw, "createdAt"))
	updated := isoTime(memberOf(raw, "updatedAt"))
	fields, fieldsOK := readPRText(raw)

	number, numberOK := safeInteger(memberOf(raw, "number"))
	if !all(numberOK, repos.IsName(repo), fieldsOK, !math.IsNaN(created), !math.IsNaN(updated)) {
		return nil
	}

	author := object(memberOf(raw, "author"))

	// A deleted account is GitHub's "ghost".
	name := login(author)
	if name == "" {
		name = "ghost"
	}

	bot := memberOf(author, "__typename") == "Bot"
	if strings.HasPrefix(name, "app/") {
		bot = true
	}

	pull := &PR{
		Repo: repo, Number: number, Title: fields.title, Author: name, AuthorIsBot: bot,
		URL: fields.url, BaseRef: fields.baseRef, HeadSha: fields.headSha,
		IsDraft: memberOf(raw, "isDraft") == true, CreatedAt: created, UpdatedAt: updated,
		Checks: checksOf(memberOf(raw, "commits")), Requested: nodeRequests(raw),
		Reviews: parseReviews(nodesOrEmpty(memberOf(raw, "latestReviews"))), Ended: nil,
		EndedAt: nil,
	}

	pull.Ended = endedOf(memberOf(raw, "state"))
	if pull.Ended != nil {
		at := isoTime(memberOf(raw, endedField(*pull.Ended)))
		if !math.IsNaN(at) {
			pull.EndedAt = &at
		}
	}

	return pull
}

// nodeRequests are the users whose review is requested; a team request (or one GitHub will not
// show) is ignored.
func nodeRequests(raw *js.Object) []string {
	requested := []string{}

	for _, item := range nodesOf(memberOf(raw, "reviewRequests")) {
		reviewer := object(memberOf(object(item), "requestedReviewer"))
		if memberOf(reviewer, "__typename") != typeUser {
			continue
		}

		if found := login(reviewer); found != "" {
			requested = append(requested, found)
		}
	}

	return requested
}
