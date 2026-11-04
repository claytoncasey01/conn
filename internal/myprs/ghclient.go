package myprs

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	// failureUnitsMax is how much of a search's failure is kept.
	failureUnitsMax = 200
	// shownUnitsMax is how much of a login or a pattern an error repeats.
	shownUnitsMax = 60
	// ghAPI and ghGraphQL are gh's api command and its GraphQL endpoint.
	ghAPI     = "api"
	ghGraphQL = "graphql"
	// msPerSecond turns GitHub's reset time in seconds into ms.
	msPerSecond = 1000
)

var (
	errNotLogin    = errors.New("not a GitHub login")
	errNotPattern  = errors.New("not a repository pattern")
	errRemoveSome  = errors.New("remove some in Settings")
	errNotThreadID = errors.New("not a thread id")
	errNotJSON     = errors.New("gh returned output that is not JSON")
	errNoSearch    = errors.New("gh did not return a search result")
	errNoThread    = errors.New("gh did not return a review thread")
)

// FailureError is a GitHub call that failed, in the words Conn shows.
type FailureError struct {
	Message string
}

// Error is the message.
func (f *FailureError) Error() string { return f.Message }

// Exec runs gh with these arguments (no shell) and answers what it printed; on a failure it
// answers what gh printed too (gh prints a GraphQL answer that carries errors and exits
// non-zero).
type Exec func(ctx context.Context, args []string) (string, error)

// Gh is the GitHub source for My PRs: gh.Large, or a fake.
type Gh struct {
	Exec Exec
	// Trigger is the Greptile ask (config.GreptileTrigger): my own are left out of a PR's
	// LastCommentAt.
	Trigger string
}

// SearchFailure is a search that could not be made, by pattern, with gh's own first line
// cleaned.
type SearchFailure struct {
	Pattern string `json:"pattern"`
	Error   string `json:"error"`
}

// Search is what PRs answers: the PRs, whether the list may be short, the patterns whose
// search failed (the rest were read), and the cost seen.
type Search struct {
	PRs    []*PR
	Capped bool
	Failed []SearchFailure
	Rate   *ghbudget.RateInfo
}

// Login is the signed-in GitHub login.
func (g Gh) Login(ctx context.Context) (string, error) {
	login, err := reviews.Gh{Exec: reviews.GhExec(g.Exec)}.Login(ctx)
	if err != nil {
		return "", fmt.Errorf("%w", err)
	}

	return login, nil
}

// PRs are the open PRs of me (a login, or `@me` for gh's own user) in the allowed patterns, one
// search per pattern, each at most SearchLimit, merged and deduplicated by `owner/repo#number`;
// a PR outside every pattern is dropped. Capped is true when the list may miss a PR. A search
// with no result does not fail the rest: its pattern is in Failed and the others are read; this
// fails only when every search failed (the first failure, pattern in front), or with
// *ghbudget.RateLimitError as soon as GitHub says the limit is used up (the reset time from `gh
// api rate_limit` when it answers). It fails before gh is touched when me is not a login, when
// there are more than settings.PatternsMax patterns or one is not a pattern. No patterns is no
// PRs and no call.
func (g Gh) PRs(ctx context.Context, login string, patterns []string) (Search, error) {
	queries, err := checkSearch(login, patterns)
	if err != nil {
		return Search{}, err
	}

	byKey := js.NewMap[*PR]()
	out := Search{PRs: nil, Capped: false, Failed: []SearchFailure{}, Rate: nil}

	for _, query := range queries {
		one, err := g.searchOne(ctx, login, query.text)
		if err != nil {
			if _, limited := errors.AsType[*ghbudget.RateLimitError](err); limited {
				return Search{}, &ghbudget.RateLimitError{ResetAt: g.resetOf(ctx)}
			}

			out.Failed = append(out.Failed, SearchFailure{
				Pattern: query.pattern, Error: Clean(err.Error(), failureUnitsMax),
			})

			continue
		}

		if one.Capped {
			out.Capped = true
		}

		out.Rate = ghbudget.AddRate(out.Rate, one.Rate)

		// The search is by pattern, the check is by pattern too: whatever GitHub sends back that
		// no pattern covers is not listed.
		for _, pull := range one.PRs {
			if repos.Allowed(pull.Repo, patterns) {
				byKey.Set(lowerKey(pull.Repo, pull.Number), pull)
			}
		}
	}

	return finishSearch(out, byKey, len(queries))
}

func finishSearch(out Search, byKey *js.Map[*PR], queries int) (Search, error) {
	if queries > 0 {
		if len(out.Failed) == queries {
			first := out.Failed[0]

			return Search{}, &FailureError{Message: first.Pattern + ": " + first.Error}
		}
	}

	out.PRs = make([]*PR, 0, byKey.Len())

	for _, key := range byKey.Keys() {
		pull, _ := byKey.Get(key)
		out.PRs = append(out.PRs, pull)
	}

	return out, nil
}

type patternQuery struct {
	pattern, text string
}

func checkSearch(login string, patterns []string) ([]patternQuery, error) {
	if login != "@me" {
		if !loginShape.MatchString(login) {
			return nil, fmt.Errorf("%w: %s", errNotLogin, Clean(login, shownUnitsMax))
		}
	}

	if len(patterns) > settings.PatternsMax {
		return nil, fmt.Errorf("more than %d repository patterns: %w", settings.PatternsMax,
			errRemoveSome)
	}

	queries := []patternQuery{}

	for _, pattern := range SearchPatterns(patterns) {
		query, ok := SearchFor(login, pattern)
		if !ok {
			return nil, fmt.Errorf("%w: %s", errNotPattern, Clean(pattern, shownUnitsMax))
		}

		queries = append(queries, patternQuery{pattern: pattern, text: query})
	}

	return queries, nil
}

// lowerKey is `owner/repo#number` in lowercase, what two answers for one PR share.
func lowerKey(repo string, number float64) string {
	return EntryKey(strings.ToLower(repo), number)
}

// oneSearch is what one search found.
type oneSearch struct {
	PRs    []*PR
	Capped bool
	Rate   *ghbudget.RateInfo
}

// unreadable is why output that is not JSON gave nothing: the rate limit, gh's own failure, or
// the output itself.
func unreadable(failed error) error {
	if failed == nil {
		return errNotJSON
	}

	if ghbudget.RateLimitText.MatchString(failed.Error()) {
		return &ghbudget.RateLimitError{ResetAt: nil}
	}

	return failed
}

// rateLimited is whether gh's answer (the JSON it printed, or its error line) says the rate
// limit is used up.
func rateLimited(answer any, failed error) bool {
	errs, _ := field(answer, "errors").([]any)

	for _, one := range errs {
		if field(one, "type") == "RATE_LIMITED" {
			return true
		}

		message := field(one, "message")
		if message == nil {
			message = ""
		}

		if ghbudget.RateLimitText.MatchString(js.String(message)) {
			return true
		}
	}

	if failed == nil {
		return false
	}

	return ghbudget.RateLimitText.MatchString(failed.Error())
}

// ThreadInfo is one review thread as GitHub has it now (read only: the check right before a
// write). It fails before gh is touched for an id that is not a node id, and when gh fails or
// the answer is not a review thread with its PR.
func (g Gh) ThreadInfo(ctx context.Context, threadID string) (*ThreadInfo, error) {
	if !ThreadID.MatchString(threadID) {
		return nil, errNotThreadID
	}

	out, err := g.Exec(ctx, []string{
		ghAPI, ghGraphQL, "-f", "query=" + ThreadQuery, "-f", "id=" + threadID,
	})
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	answer, err := js.Parse([]byte(out))
	if err != nil {
		return nil, errNotJSON
	}

	info := parseThreadInfo(answer)
	if info == nil {
		return nil, errNoThread
	}

	return info, nil
}

// searchOne is one search: its PRs, whether it may be short and the cost. A gh that fails but
// prints a search result still gives its PRs; *ghbudget.RateLimitError when the limit is used
// up, else an error when there is no result.
func (g Gh) searchOne(ctx context.Context, login, query string) (oneSearch, error) {
	out, failed := g.Exec(ctx, []string{
		ghAPI, ghGraphQL, "-f", "query=" + searchQuery, "-f", "q=" + query,
	})

	answer, err := js.Parse([]byte(out))
	if err != nil {
		return oneSearch{}, unreadable(failed)
	}

	if !hasSearch(answer) {
		if rateLimited(answer, failed) {
			return oneSearch{}, &ghbudget.RateLimitError{ResetAt: nil}
		}

		if failed != nil {
			return oneSearch{}, failed
		}

		return oneSearch{}, errNoSearch
	}

	found := searchNodes(answer)
	prs := ParsePRs(answer, login, g.Trigger)
	errs, _ := field(answer, "errors").([]any)

	capped := len(found) >= SearchLimit
	if len(prs) < len(found) {
		capped = true
	}

	if len(errs) > 0 {
		capped = true
	}

	return oneSearch{PRs: prs, Capped: capped, Rate: ghbudget.ParseRate(answer)}, nil
}

// resetOf is when the GraphQL rate limit resets (ms), from `gh api rate_limit` (a call that does
// not count against the limit); nil when gh cannot say.
func (g Gh) resetOf(ctx context.Context) *float64 {
	out, err := g.Exec(ctx, []string{ghAPI, "rate_limit"})
	if err != nil {
		return nil
	}

	answer, err := js.Parse([]byte(out))
	if err != nil {
		return nil
	}

	reset, isNumber := field(field(field(answer, "resources"), "graphql"), "reset").(float64)
	if !isNumber {
		return nil
	}

	if !isFinite(reset) {
		return nil
	}

	at := reset * msPerSecond

	return &at
}
