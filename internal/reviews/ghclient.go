package reviews

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
)

// The GraphQL searches and reads by number that Reviews polls with. Every value from outside (a
// pattern, a repo, a number) goes in as a GraphQL variable, never into the query text, and every
// PR that comes back is read field by field: a malformed one is dropped.

// How many of each list one PR asks for: what the rows need, and small enough that a query stays
// far under GitHub's node limit.
const (
	RequestsFirst = 20
	ReviewsFirst  = 20
	ContextsFirst = 50
	// Page is how many PRs one search page asks for (GitHub's most).
	Page = 100
	// FetchBatch is the most PRs a read by number asks for in one call.
	FetchBatch = 50
	// SearchesPerQuery is the most searches that go into one query.
	SearchesPerQuery = 6
	// messageUnits is how much of GitHub's or gh's error text is kept.
	messageUnits = 300
	// graphqlArgsFixed is how many arguments a GraphQL call has before its variables.
	graphqlArgsFixed = 4
	// repoVariables is how many string variables name one repo: its owner and its name.
	repoVariables = 2
)

// PRFragment is the fields of one PR, for both the searches and the reads by number.
const PRFragment = `fragment PrFields on PullRequest {
  number title url isDraft state createdAt updatedAt mergedAt closedAt baseRefName headRefOid
  author { login __typename }
  repository { nameWithOwner }
  reviewRequests(first: 20) { nodes { requestedReviewer { __typename ... on User { login } } } }
  latestReviews(first: 20) { nodes { author { login } state submittedAt commit { oid } } }
  commits(last: 1) { nodes { commit { statusCheckRollup { state contexts(first: 50) { nodes { ` +
	`__typename ... on CheckRun { name status conclusion } ` +
	`... on StatusContext { context state } ` +
	`} } } } } }
}`

const (
	textNotJSON     = "gh returned output that is not JSON"
	textNoData      = "gh returned no data"
	textNoLogin     = "gh did not report a login"
	textSearchFails = "GitHub could not run the search"
	textSearchFail  = "GitHub could not run this search"
	typeNotFound    = "NOT_FOUND"
	typeRateLimited = "RATE_LIMITED"
)

// FailureError is a GitHub call that failed, in the words Conn shows.
type FailureError struct {
	Message string
}

// Error is the message.
func (f *FailureError) Error() string { return f.Message }

func failure(message string) error { return &FailureError{Message: message} }

// asIsError is an error from a call outside the package, kept word for word: its text reaches
// the page or the API as it was worded, so the wrapping adds nothing to it.
type asIsError struct {
	err error
}

// Error is the inner error's text.
func (e *asIsError) Error() string { return e.err.Error() }

// Unwrap is the inner error, so errors.Is and errors.As still see it.
func (e *asIsError) Unwrap() error { return e.err }

// asIs wraps err without changing its text; nil stays nil.
func asIs(err error) error {
	if err == nil {
		return nil
	}

	return &asIsError{err: err}
}

// GhExec runs gh with these arguments (no shell) and answers what it printed; on a failure it
// answers the output too (gh prints a GraphQL answer that carries errors and exits non-zero).
type GhExec func(ctx context.Context, args []string) (string, error)

// Gh is the GraphQL source of the Reviews poll.
type Gh struct {
	Exec GhExec
}

// Query is one search and the most PRs it reads.
type Query struct {
	Q     string
	Limit int
}

// SearchResult is what one search found.
type SearchResult struct {
	PRs []*PR
	// Capped is that there were more than Limit matches: the list is cut off.
	Capped bool
	// Error is why GitHub could not run this search (nil when it ran): the others still count.
	Error *string
}

// Login is the signed-in GitHub login.
func (g Gh) Login(ctx context.Context) (string, error) {
	out, err := g.Exec(ctx, []string{"api", "user", "--jq", ".login"})
	if err != nil {
		return "", err
	}

	name := js.Trim(out)
	if name == "" {
		return "", failure(textNoLogin)
	}

	return name, nil
}

// Search runs several searches, each to at most its limit, at most SearchesPerQuery to a call:
// the first page of every search, then the next pages of those that have more. A search GitHub
// could not run carries its Error and the rest still count; this fails only when every search
// failed, and with *ghbudget.RateLimitError as soon as GitHub says the budget is used up.
func (g Gh) Search(
	ctx context.Context, queries []Query,
) ([]SearchResult, *ghbudget.RateInfo, error) {
	out := make([]SearchResult, len(queries))
	pending := make([]pendingSearch, 0, len(queries))

	for index, item := range queries {
		out[index] = SearchResult{PRs: []*PR{}, Capped: false, Error: nil}
		pending = append(pending, pendingSearch{
			index: index, q: item.Q, limit: max(1, item.Limit), seen: 0, after: nil,
		})
	}

	var rate *ghbudget.RateInfo

	// A round that reads anything moves every search on by a PR at least, so a search ends within
	// its limit's rounds; one GitHub keeps answering with empty pages is cut off there.
	roundsMax := 1
	for _, item := range pending {
		roundsMax = max(roundsMax, item.limit+1)
	}

	for rounds := 0; len(pending) > 0; rounds++ {
		if rounds >= roundsMax {
			for _, item := range pending {
				out[item.index].Capped = true
			}

			break
		}

		next, err := g.searchRound(ctx, pending, out, &rate)
		if err != nil {
			return nil, nil, err
		}

		pending = next
	}

	if len(out) > 0 {
		if everyFailed(out) {
			return nil, nil, failure(*out[0].Error)
		}
	}

	return out, rate, nil
}

// Fetch reads PRs by number, FetchBatch to a call: by key, the PR as GitHub has it now (open,
// merged or closed), or nil when GitHub says it is not there (the PR or its repo is gone, or the
// repo was renamed away from this name). A PR whose read failed for another reason is left out,
// so the caller keeps what it knew. An error when a whole call fails, *ghbudget.RateLimitError when
// the budget is used up. onRate hears what each call cost.
func (g Gh) Fetch(
	ctx context.Context, prs []KeyParts, onRate func(rate *ghbudget.RateInfo),
) (*js.Map[*PR], error) {
	got := js.NewMap[*PR]()

	var valid []KeyParts

	for _, pr := range prs {
		if all(repos.IsName(pr.Repo), safeNumber(pr.Number)) {
			valid = append(valid, pr)
		}
	}

	for at := 0; at < len(valid); at += FetchBatch {
		batch := valid[at:min(at+FetchBatch, len(valid))]

		err := g.fetchBatch(ctx, batch, got, onRate)
		if err != nil {
			return nil, err
		}
	}

	return got, nil
}

// variable is one GraphQL variable as gh takes it.
type variable struct {
	name  string
	value string
}

type answer struct {
	data   *js.Object
	errors []graphqlError
	rate   *ghbudget.RateInfo
}

type graphqlError struct {
	path, kind, message string
}

// graphql runs one query (with the rate limit asked for): strings go as -f (never read as a
// file or a number), ints as -F. An answer with no data at all is an error; one that says the
// rate limit is used up is a *ghbudget.RateLimitError.
func (g Gh) graphql(ctx context.Context, query string, strs, ints []variable) (answer, error) {
	args := make([]string, 0, graphqlArgsFixed+2*len(strs)+2*len(ints))
	args = append(args, "api", "graphql", "-f", "query="+query)

	for _, item := range strs {
		args = append(args, "-f", item.name+"="+item.value)
	}

	for _, item := range ints {
		args = append(args, "-F", item.name+"="+item.value)
	}

	out, failed := g.Exec(ctx, args)

	parsed, err := js.Parse([]byte(out))
	if err != nil {
		return answer{}, notAnswered(failed, textNotJSON)
	}

	errs := readErrors(parsed)
	rate := ghbudget.ParseRate(parsed)

	for _, item := range errs {
		if item.kind == typeRateLimited {
			return answer{}, limited(rate)
		}

		if ghbudget.RateLimitText.MatchString(item.message) {
			return answer{}, limited(rate)
		}
	}

	data := object(memberOf(object(parsed), "data"))
	if data == nil {
		fallback := textNoData

		if len(errs) > 0 {
			if errs[0].message != "" {
				fallback = errs[0].message
			}
		}

		return answer{}, notAnswered(failed, fallback)
	}

	return answer{data: data, errors: errs, rate: rate}, nil
}

// notAnswered is the error of a call with no usable answer: a rate limit gh named, else gh's own
// failure, else fallback.
func notAnswered(failed error, fallback string) error {
	if failed == nil {
		return failure(fallback)
	}

	if ghbudget.RateLimitText.MatchString(failed.Error()) {
		return &ghbudget.RateLimitError{ResetAt: nil}
	}

	return failed
}

func limited(rate *ghbudget.RateInfo) error {
	if rate == nil {
		return &ghbudget.RateLimitError{ResetAt: nil}
	}

	resetAt := rate.ResetAt

	return &ghbudget.RateLimitError{ResetAt: &resetAt}
}

func readErrors(parsed any) []graphqlError {
	list, _ := memberOf(object(parsed), "errors").([]any)
	out := make([]graphqlError, 0, len(list))

	for _, item := range list {
		raw := object(item)
		path := ""

		if steps, isList := memberOf(raw, "path").([]any); isList {
			if len(steps) > 0 {
				path, _ = steps[0].(string)
			}
		}

		kind, _ := memberOf(raw, "type").(string)
		message, _ := memberOf(raw, "message").(string)
		out = append(out, graphqlError{
			path: path, kind: kind, message: js.Slice(message, messageUnits),
		})
	}

	return out
}

func query(declarations, parts []string) string {
	return "query(" + strings.Join(declarations, ", ") +
		") {\nrateLimit { cost remaining resetAt }\n" + strings.Join(parts, "\n") + "\n}\n" +
		PRFragment
}

// pendingSearch is one search with pages still to read.
type pendingSearch struct {
	index int
	q     string
	limit int
	seen  int
	after *string
}

func everyFailed(results []SearchResult) bool {
	for _, result := range results {
		if result.Error == nil {
			return false
		}
	}

	return true
}

// searchRound reads one page of every pending search and answers those with more to read.
func (g Gh) searchRound(
	ctx context.Context, pending []pendingSearch, out []SearchResult, rate **ghbudget.RateInfo,
) ([]pendingSearch, error) {
	var next []pendingSearch

	for at := 0; at < len(pending); at += SearchesPerQuery {
		chunk := pending[at:min(at+SearchesPerQuery, len(pending))]

		more, err := g.searchChunk(ctx, chunk, out, rate)
		if err != nil {
			return nil, err
		}

		next = append(next, more...)
	}

	return next, nil
}

func (g Gh) searchChunk(
	ctx context.Context, chunk []pendingSearch, out []SearchResult, rate **ghbudget.RateInfo,
) ([]pendingSearch, error) {
	vars := make([]variable, 0, len(chunk))
	parts := make([]string, 0, len(chunk))
	decl := make([]string, 0, len(chunk))

	for index, item := range chunk {
		number := strconv.Itoa(index)
		vars = append(vars, variable{name: "q" + number, value: item.q})
		declared := "$q" + number + ": String!"
		after := ""

		if item.after != nil {
			vars = append(vars, variable{name: "a" + number, value: *item.after})
			declared += ", $a" + number + ": String!"
			after = ", after: $a" + number
		}

		first := strconv.Itoa(min(Page, item.limit-item.seen))
		parts = append(parts, "s"+number+": search(query: $q"+number+", type: ISSUE, first: "+
			first+after+") { issueCount pageInfo { hasNextPage endCursor } nodes { ...PrFields } }")
		decl = append(decl, declared)
	}

	got, err := g.graphql(ctx, query(decl, parts), vars, nil)
	if err != nil {
		if _, isLimit := errors.AsType[*ghbudget.RateLimitError](err); isLimit {
			return nil, err
		}

		why := js.Slice(firstLineOf(err.Error()), messageUnits)
		if why == "" {
			why = textSearchFails
		}

		for _, item := range chunk {
			out[item.index].Error = &why
		}

		return nil, nil
	}

	*rate = ghbudget.AddRate(*rate, got.rate)

	return readSearches(chunk, got, out), nil
}

func readSearches(chunk []pendingSearch, got answer, out []SearchResult) []pendingSearch {
	var next []pendingSearch

	for index, item := range chunk {
		name := "s" + strconv.Itoa(index)

		search := object(memberOf(got.data, name))
		if search == nil {
			why := textSearchFail

			if found := errorAt(got.errors, name, ""); found != nil {
				if found.message != "" {
					why = found.message
				}
			}

			out[item.index].Error = &why

			continue
		}

		nodes := nodesOf(search)
		for _, node := range nodes {
			if pr := ParsePRNode(node); pr != nil {
				out[item.index].PRs = append(out[item.index].PRs, pr)
			}
		}

		info := object(memberOf(search, "pageInfo"))
		cursor, hasCursor := memberOf(info, "endCursor").(string)
		more := memberOf(info, "hasNextPage") == true
		more = all(more, hasCursor)

		// Counted from what GitHub sent, not what was readable: an unreadable entry does not make
		// a cut-off list look whole.
		seen := item.seen + len(nodes)
		if all(more, seen < item.limit) {
			next = append(next, pendingSearch{
				index: item.index, q: item.q, limit: item.limit, seen: seen, after: &cursor,
			})

			continue
		}

		out[item.index].Capped = more
	}

	return next
}

// errorAt is the first error at path (of kind, when kind is not ""), or nil.
func errorAt(errs []graphqlError, path, kind string) *graphqlError {
	for index := range errs {
		if errs[index].path != path {
			continue
		}

		if kind == "" {
			return &errs[index]
		}

		if errs[index].kind == kind {
			return &errs[index]
		}
	}

	return nil
}

func firstLineOf(text string) string {
	line, _, _ := strings.Cut(text, "\n")

	return line
}

func (g Gh) fetchBatch(
	ctx context.Context, batch []KeyParts, got *js.Map[*PR], onRate func(*ghbudget.RateInfo),
) error {
	strs := make([]variable, 0, repoVariables*len(batch))
	ints := make([]variable, 0, len(batch))
	parts := make([]string, 0, len(batch))
	decl := make([]string, 0, len(batch))

	for index, wanted := range batch {
		number := strconv.Itoa(index)
		owner, name, _ := strings.Cut(wanted.Repo, "/")
		strs = append(strs, variable{name: "o" + number, value: owner},
			variable{name: "n" + number, value: name})
		ints = append(ints, variable{
			name: "k" + number, value: strconv.FormatInt(wanted.Number, 10),
		})
		parts = append(parts, "p"+number+": repository(owner: $o"+number+", name: $n"+number+
			") { pullRequest(number: $k"+number+") { ...PrFields } }")
		decl = append(decl, "$o"+number+": String!, $n"+number+": String!, $k"+number+": Int!")
	}

	reply, err := g.graphql(ctx, query(decl, parts), strs, ints)
	if err != nil {
		return err
	}

	if reply.rate != nil {
		if onRate != nil {
			onRate(reply.rate)
		}
	}

	for index, wanted := range batch {
		name := "p" + strconv.Itoa(index)
		key := keyOf(wanted.Repo, wanted.Number)
		node := object(memberOf(object(memberOf(reply.data, name)), "pullRequest"))

		if node != nil {
			read := ParsePRNode(node)
			if read == nil {
				continue
			}

			// A repo renamed or moved answers under its new name: that is not the PR this key
			// names.
			if all(same(read.Repo, wanted.Repo), read.Number == wanted.Number) {
				got.Set(key, read)
			} else {
				got.Set(key, nil)
			}

			continue
		}

		if errorAt(reply.errors, name, typeNotFound) != nil {
			got.Set(key, nil)
		}
	}

	return nil
}
