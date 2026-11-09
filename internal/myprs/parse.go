package myprs

import (
	"math"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
)

// sameLogin compares two logins whatever their case, as their toLowerCase compares:
// strings.EqualFold also folds letters lowercasing leaves apart (a long s and an s).
func sameLogin(first, second string) bool {
	lower := strings.ToLower(first)

	return lower == strings.ToLower(second)
}

// IsOwnAsk is whether an issue comment is Conn's own Greptile ask: written by me (any case) and
// saying exactly the trigger.
func IsOwnAsk(comment Comment, me, trigger string) bool {
	if me == "" {
		return false
	}

	if !sameLogin(comment.By, me) {
		return false
	}

	return js.Trim(comment.Body) == trigger
}

// ParsePRs reads a `gh api graphql` answer as PRs. A malformed node is dropped; anything that is
// not a search result gives none. me is the login whose Greptile asks (trigger) are left out of
// LastCommentAt.
func ParsePRs(answer any, me, trigger string) []*PR {
	out := []*PR{}

	for _, value := range searchNodes(answer) {
		if pull := parseOne(value, me, trigger); pull != nil {
			out = append(out, pull)
		}
	}

	return out
}

// searchNodes are the search nodes of an answer; nil when it has no search result.
func searchNodes(answer any) []any {
	found, _ := field(field(field(answer, "data"), "search"), "nodes").([]any)

	return found
}

func hasSearch(answer any) bool {
	_, found := field(field(field(answer, "data"), "search"), "nodes").([]any)

	return found
}

// prHead are the fields of a PR that must all be there, or it is not listed.
type prHead struct {
	repo, title, url, headRef, headSha, baseRef string
	number, createdAt, updatedAt                float64
}

func readHead(pull *js.Object) (prHead, bool) {
	// The name as GitHub sent it must already be one by the grammar: cleaning first could turn a
	// lookalike (a bidi or zero-width character inside it) into a valid name. Every later use of
	// the repo (a path, a prompt, a write) trusts this check.
	repo, _ := text(field(field(pull, "repository"), "nameWithOwner"))
	if !repos.IsName(repo) {
		return prHead{}, false
	}

	number, isNumber := field(pull, "number").(float64)
	if !isNumber {
		return prHead{}, false
	}

	if !wholePositive(number) {
		return prHead{}, false
	}

	head := prHead{
		repo: repo, number: number, createdAt: isoTime(field(pull, "createdAt")),
		updatedAt: isoTime(field(pull, "updatedAt")),
	}

	return head, readTexts(pull, &head)
}

func wholePositive(number float64) bool {
	if math.IsInf(number, 0) {
		return false
	}

	if math.Trunc(number) != number {
		return false
	}

	return number > 0
}

func readTexts(pull *js.Object, head *prHead) bool {
	fields := []struct {
		key  string
		into *string
	}{
		{"title", &head.title},
		{"url", &head.url},
		{"headRefName", &head.headRef},
		{"headRefOid", &head.headSha},
		{"baseRefName", &head.baseRef},
	}

	for _, one := range fields {
		found, isText := text(field(pull, one.key))
		if !isText {
			return false
		}

		*one.into = found
	}

	if math.IsNaN(head.createdAt) {
		return false
	}

	return !math.IsNaN(head.updatedAt)
}

func parseOne(value any, login, trigger string) *PR {
	pull := object(value)
	if pull == nil {
		return nil
	}

	head, ok := readHead(pull)
	if !ok {
		return nil
	}

	threads := eachThread(field(pull, "reviewThreads"))
	reviews := eachReview(field(pull, "reviews"))
	comments := parseComments(pull)
	headDate := isoTime(field(field(firstOf(nodes(field(pull, "commits"))), "commit"),
		"committedDate"))

	if math.IsNaN(headDate) {
		headDate = 0
	}

	headRepo := textOrNil(field(field(pull, "headRepository"), "nameWithOwner"))

	return &PR{
		Repo: head.repo, Number: head.number, Title: Clean(head.title, maxTitleUnits),
		URL: head.url, Draft: field(pull, "isDraft") == true, CreatedAt: head.createdAt,
		UpdatedAt: head.updatedAt, LastCommentAt: lastCommentAt(comments, reviews, threads,
			login, trigger),
		HeadRef: head.headRef, HeadSha: head.headSha, HeadDate: headDate,
		BaseRef:       head.baseRef,
		DefaultBranch: defaultBranch(pull),
		// A head repository that is gone (a deleted fork) is a fork too.
		Fork:    isFork(headRepo, head.repo),
		Threads: threads, Reviews: reviews, Comments: comments, Partial: partial(pull),
	}
}

func defaultBranch(pull *js.Object) *string {
	return textOrNil(field(field(field(pull, "repository"), "defaultBranchRef"), "name"))
}

func isFork(headRepo *string, repo string) bool {
	if headRepo == nil {
		return true
	}

	return Clean(*headRepo, MaxTextUnits) != repo
}

func firstOf(list []any) any {
	if len(list) == 0 {
		return nil
	}

	return list[0]
}

func eachThread(value any) []Thread {
	out := []Thread{}

	for _, node := range nodes(value) {
		if thread := parseThread(node); thread != nil {
			out = append(out, *thread)
		}
	}

	return out
}

func eachReview(value any) []Review {
	out := []Review{}

	for _, node := range nodes(value) {
		if review := parseReview(node); review != nil {
			out = append(out, *review)
		}
	}

	return out
}

// lastCommentAt is the newest comment, review or thread; Conn's own ask is not a comment anyone
// made to me: it would put every PR just asked about at the top.
func lastCommentAt(comments []Comment, reviews []Review, threads []Thread, login,
	trigger string,
) float64 {
	times := []float64{}

	for _, comment := range comments {
		if !IsOwnAsk(comment, login, trigger) {
			times = append(times, comment.At)
		}
	}

	for _, review := range reviews {
		times = append(times, review.At)
	}

	for _, thread := range threads {
		times = append(times, thread.At)
	}

	if len(times) == 0 {
		return 0
	}

	newest := math.Inf(-1)
	for _, at := range times {
		newest = math.Max(newest, at)
	}

	return newest
}

// partial is whether what the rules read may be short. Without GitHub's word on older threads,
// a full page may have been cut (counted before any thread is dropped as unreadable).
func partial(pull *js.Object) bool {
	for _, key := range []string{"reviewThreads", "reviews", "comments", "firstComments"} {
		if !answered(field(pull, key)) {
			return true
		}
	}

	threads := field(pull, "reviewThreads")
	if older, isBool := field(field(threads, "pageInfo"), "hasPreviousPage").(bool); isBool {
		return older
	}

	return len(nodes(threads)) >= ThreadsLast
}

// parseThreadInfo reads a `gh api graphql` answer as a review thread with its PR; nil when it is
// not one.
func parseThreadInfo(answer any) *ThreadInfo {
	thread := object(field(field(answer, "data"), "node"))
	pull := object(field(thread, "pullRequest"))
	repo, _ := text(field(field(pull, "repository"), "nameWithOwner"))
	resolved, isBool := field(thread, "isResolved").(bool)
	number, isNumber := field(pull, "number").(float64)
	state, isState := text(field(pull, "state"))

	for _, ok := range []bool{thread != nil, pull != nil, isBool, isNumber, isState, repo != ""} {
		if !ok {
			return nil
		}
	}

	list := nodes(field(thread, "comments"))

	var last *LastComment

	if len(list) > 0 {
		newest := object(list[len(list)-1])
		if body, isText := text(field(newest, "body")); isText {
			last = &LastComment{By: loginOf(field(newest, "author")), Body: body}
		}
	}

	return &ThreadInfo{
		Resolved: resolved, Number: number, State: state,
		Author: loginOf(field(pull, "author")), Repo: repo, Last: last, Recent: recent(list),
	}
}

func recent(list []any) []RecentComment {
	out := []RecentComment{}

	for _, value := range list {
		body, isText := text(field(value, "body"))
		if object(value) == nil {
			continue
		}

		if !isText {
			continue
		}

		var at *float64
		if found := isoTime(field(value, "createdAt")); isFinite(found) {
			at = &found
		}

		out = append(out, RecentComment{By: loginOf(field(value, "author")), Body: body, At: at})
	}

	return out
}

func isFinite(value float64) bool {
	if math.IsNaN(value) {
		return false
	}

	return !math.IsInf(value, 0)
}

// SearchFor is the search text for one pattern: `owner/*` is every repo of the owner (`user:`
// matches an organization's repos too, as `gh search --owner` relies on), `owner/repo` is that
// repo. False for anything that is not a pattern by the allowlist's grammar, so no text of
// unknown shape ever reaches a search string.
func SearchFor(login, pattern string) (string, bool) {
	var scope string

	switch {
	case strings.HasSuffix(pattern, "/*"):
		owner := strings.TrimSuffix(pattern, "/*")
		if !ownerShape.MatchString(owner) {
			return "", false
		}

		scope = "user:" + owner
	case repos.IsName(pattern):
		scope = "repo:" + pattern
	default:
		return "", false
	}

	return searchText + login + " " + scope, true
}

// SearchPatterns are the patterns one search each is made for: lowercase, a repeat once, and no
// `owner/repo` that an `owner/*` already covers.
func SearchPatterns(patterns []string) []string {
	all := []string{}
	seen := map[string]bool{}

	for _, pattern := range patterns {
		lower := strings.ToLower(pattern)
		if !seen[lower] {
			seen[lower] = true
			all = append(all, lower)
		}
	}

	owners := map[string]bool{}

	for _, pattern := range all {
		if owner, wild := strings.CutSuffix(pattern, "/*"); wild {
			owners[owner] = true
		}
	}

	out := []string{}

	for _, pattern := range all {
		if strings.HasSuffix(pattern, "/*") {
			out = append(out, pattern)

			continue
		}

		if !owners[ownerPart(pattern)] {
			out = append(out, pattern)
		}
	}

	return out
}

// ownerPart is p.slice(0, p.indexOf('/')): with no slash, -1 cuts the last UTF-16 unit.
func ownerPart(pattern string) string {
	if owner, _, found := strings.Cut(pattern, "/"); found {
		return owner
	}

	return js.Cut(pattern, max(0, js.Len(pattern)-1))
}
