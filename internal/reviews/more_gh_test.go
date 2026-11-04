package reviews_test

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/settings"
)

// Bun's reviewGh.test.ts, the GraphQL source: what the recorded calls do not hold (the chunking
// of a full allowlist, the node limit, a fetch's gone and rate-free answers).

// dNodeLimit is GitHub's limit on the nodes one GraphQL call may return.
const dNodeLimit = 500_000

var (
	errDNotLoggedIn = errors.New("gh: not logged in")
	errDHTTP502     = errors.New("HTTP 502")
	errDBad         = errors.New("bad")
)

// dStep is one canned gh answer: its output, or a failure carrying that output.
type dStep struct {
	out    string
	failed error
}

// dGh answers each call with the next step and keeps the calls.
type dGh struct {
	steps []dStep
	calls [][]string
}

func (g *dGh) exec(_ context.Context, args []string) (string, error) {
	g.calls = append(g.calls, args)
	if len(g.steps) == 0 {
		return "", errDNoAnswers
	}

	step := g.steps[0]
	g.steps = g.steps[1:]

	return step.out, step.failed
}

var errDNoAnswers = errors.New("no more answers")

// dQueryOf is the query text of a call.
func dQueryOf(args []string) string {
	for index, arg := range args {
		if index == 0 {
			continue
		}

		if args[index-1] == "-f" {
			if strings.HasPrefix(arg, "query=") {
				return arg
			}
		}
	}

	return ""
}

// dNode is a PR node of acme/widgets as GitHub answers it, under repo's name.
func dNode(number int, repo string) string {
	return `{"number":` + strconv.Itoa(number) + `,"title":"T",` +
		`"url":"https://github.com/acme/widgets/pull/` + strconv.Itoa(number) + `",` +
		`"isDraft":false,"state":"OPEN","createdAt":"2026-09-30T08:00:00Z",` +
		`"updatedAt":"2026-10-01T12:00:00Z","mergedAt":null,"closedAt":null,` +
		`"baseRefName":"main","headRefOid":"abc123","author":{"login":"sam","__typename":"User"},` +
		`"repository":{"nameWithOwner":"` + repo + `"},"reviewRequests":{"nodes":[]},` +
		`"latestReviews":{"nodes":[]},"commits":{"nodes":[]}}`
}

// TestDSearchCutsAFullAllowlistIntoChunks: 25 patterns with both searches on are 50 searches,
// ceil(50/6) calls with at most six searches each.
func TestDSearchCutsAFullAllowlistIntoChunks(t *testing.T) {
	t.Parallel()

	empty := make([]string, 0, reviews.SearchesPerQuery)
	for index := range reviews.SearchesPerQuery {
		empty = append(empty, `"s`+strconv.Itoa(index)+`":{"issueCount":0,`+
			`"pageInfo":{"hasNextPage":false},"nodes":[]}`)
	}

	calls := (2*settings.PatternsMax + reviews.SearchesPerQuery - 1) / reviews.SearchesPerQuery
	source := &dGh{steps: nil, calls: nil}

	for range calls {
		source.steps = append(source.steps,
			dStep{out: `{"data":{` + strings.Join(empty, ",") + `}}`, failed: nil})
	}

	queries := make([]reviews.Query, 0, 2*settings.PatternsMax)
	for index := range 2 * settings.PatternsMax {
		queries = append(queries, reviews.Query{Q: "q" + strconv.Itoa(index), Limit: 200})
	}

	_, _, err := reviews.Gh{Exec: source.exec}.Search(t.Context(), queries)
	dMust(t, err)

	if len(source.calls) != calls {
		t.Errorf("%d calls, want %d", len(source.calls), calls)
	}

	for index, call := range source.calls {
		searches := strings.Count(dQueryOf(call), ": search(")
		if searches > reviews.SearchesPerQuery {
			t.Errorf("call %d holds %d searches", index, searches)
		}
	}
}

// dNodesPerPR walks the fragment's own text: every connection's first or last, times its
// parent's count, plus the PR.
func dNodesPerPR(t *testing.T) int {
	t.Helper()

	total := 1
	pending := 0
	stack := []int{}

	for _, match := range regexp.MustCompile(`\((?:first|last):\s*(\d+)\)|\{|\}`).
		FindAllStringSubmatch(reviews.PRFragment, -1) {
		switch {
		case match[1] != "":
			pending, _ = strconv.Atoi(match[1])
		case match[0] == "{":
			parent := 1
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}

			count := parent * max(pending, 1)
			if pending > 0 {
				total += count
			}

			stack = append(stack, count)
			pending = 0
		default:
			stack = stack[:len(stack)-1]
		}
	}

	if len(stack) != 0 {
		t.Fatalf("unbalanced fragment: %d open", len(stack))
	}

	return total
}

// TestDNoQueryNearsTheNodeLimit: a full chunk of first-page searches, and a full batch of reads
// by number, each return under a fifth of GitHub's limit.
func TestDNoQueryNearsTheNodeLimit(t *testing.T) {
	t.Parallel()

	perPR := dNodesPerPR(t)

	if search := reviews.SearchesPerQuery * reviews.Page * perPR; search >= dNodeLimit/5 {
		t.Errorf("a search call can return %d nodes", search)
	}

	if fetch := reviews.FetchBatch * perPR; fetch >= dNodeLimit/5 {
		t.Errorf("a fetch call can return %d nodes", fetch)
	}
}

// TestDFetchTellsTheCostOnlyWhenTheAnswerHasIt: an answer with no rateLimit tells nothing, and a
// caller that asks for nothing is not troubled.
func TestDFetchTellsTheCostOnlyWhenTheAnswerHasIt(t *testing.T) {
	t.Parallel()

	quiet := &dGh{steps: []dStep{{out: `{"data":{"p0":{"pullRequest":` +
		dNode(1, "acme/widgets") + `}}}`, failed: nil}}, calls: nil}
	heard := 0
	key := []reviews.KeyParts{{Repo: "acme/widgets", Number: 1}}

	_, err := reviews.Gh{Exec: quiet.exec}.Fetch(t.Context(), key,
		func(*ghbudget.RateInfo) { heard++ })
	dMust(t, err)

	if heard != 0 {
		t.Errorf("heard %d rates", heard)
	}

	rated := &dGh{steps: []dStep{{out: `{"data":{"rateLimit":{"cost":1,"remaining":1,` +
		`"resetAt":"2026-11-02T13:00:00Z"},"p0":{"pullRequest":` + dNode(1, "acme/widgets") +
		`}}}`, failed: nil}}, calls: nil}

	got, err := reviews.Gh{Exec: rated.exec}.Fetch(t.Context(), key, nil)
	dMust(t, err)

	if got.Len() != 1 {
		t.Errorf("got %v", got.Keys())
	}
}

// TestDFetchGoneAndForbidden: gone is a NOT_FOUND or a repo answered under another name; any
// other error leaves the PR out; gh failing with the answer on stdout still answers.
func TestDFetchGoneAndForbidden(t *testing.T) {
	t.Parallel()

	stdout := `{"data":{"p0":{"pullRequest":` + dNode(7, "acme/widgets") + `},"p1":null,` +
		`"p2":{"pullRequest":null},"p3":null,"p4":{"pullRequest":` + dNode(9, "acme/renamed") +
		`}},"errors":[{"type":"NOT_FOUND","path":["p1"],` +
		`"message":"Could not resolve to a Repository"},{"type":"NOT_FOUND",` +
		`"path":["p2","pullRequest"],"message":"Could not resolve to a PullRequest"},` +
		`{"type":"FORBIDDEN","path":["p3"],"message":"SAML"}]}`
	source := &dGh{steps: []dStep{{out: stdout, failed: errDNotLoggedIn}}, calls: nil}

	got, err := reviews.Gh{Exec: source.exec}.Fetch(t.Context(), []reviews.KeyParts{
		{Repo: "acme/widgets", Number: 7},
		{Repo: "acme/gone", Number: 1},
		{Repo: "acme/widgets", Number: 999},
		{Repo: "sso/locked", Number: 2},
		{Repo: "acme/old", Number: 9},
	}, nil)
	dMust(t, err)

	tests := []struct {
		key     string
		present bool
		open    bool
	}{
		{"acme/widgets#7", true, true},
		{"acme/gone#1", true, false},
		{"acme/widgets#999", true, false},
		{"sso/locked#2", false, false},
		{"acme/old#9", true, false},
	}

	for _, tt := range tests {
		read, present := got.Get(tt.key)
		if present != tt.present {
			t.Errorf("%s present %v", tt.key, present)
		}

		if (read != nil) != tt.open {
			t.Errorf("%s read %s", tt.key, js.Marshal(read))
		}
	}
}

// TestDFetchFailuresAndNothingToRead: gh failing with no answer is its error; no PRs is no call.
func TestDFetchFailuresAndNothingToRead(t *testing.T) {
	t.Parallel()

	failing := &dGh{steps: []dStep{{out: "", failed: errDNotLoggedIn}}, calls: nil}

	_, err := reviews.Gh{Exec: failing.exec}.Fetch(t.Context(),
		[]reviews.KeyParts{{Repo: "acme/widgets", Number: 7}}, nil)
	if err == nil {
		t.Fatal("no error")
	}

	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("error %v", err)
	}

	idle := &dGh{steps: nil, calls: nil}

	got, err := reviews.Gh{Exec: idle.exec}.Fetch(t.Context(), nil, nil)
	dMust(t, err)

	if got.Len() != 0 {
		t.Errorf("got %v", got.Keys())
	}

	if len(idle.calls) != 0 {
		t.Errorf("calls %q", idle.calls)
	}
}

// TestDSearchCallFailingFailsOnlyItsChunk: the first chunk's call fails outright; its six
// searches carry the error and the seventh, in the next call, still counts.
func TestDSearchCallFailingFailsOnlyItsChunk(t *testing.T) {
	t.Parallel()

	source := &dGh{steps: []dStep{
		{out: "", failed: errDHTTP502},
		{
			out:    `{"data":{"s0":{"issueCount":0,"pageInfo":{"hasNextPage":false},"nodes":[]}}}`,
			failed: nil,
		},
	}, calls: nil}

	queries := make([]reviews.Query, 0, reviews.SearchesPerQuery+1)
	for index := range reviews.SearchesPerQuery + 1 {
		queries = append(queries, reviews.Query{Q: "q" + strconv.Itoa(index), Limit: 100})
	}

	results, _, err := reviews.Gh{Exec: source.exec}.Search(t.Context(), queries)
	dMust(t, err)

	if len(source.calls) != 2 {
		t.Errorf("%d calls", len(source.calls))
	}

	got := make([]string, 0, len(results))
	for _, result := range results {
		why := "none"
		if result.Error != nil {
			why = *result.Error
		}

		got = append(got, why)
	}

	want := append(slices.Repeat([]string{"HTTP 502"}, reviews.SearchesPerQuery), "none")
	if !slices.Equal(got, want) {
		t.Errorf("errors %q", got)
	}
}

// TestDSearchFailingWholeIsAnError: a search GitHub could not run alone, and gh failing with
// output that is not JSON, are errors.
func TestDSearchFailingWholeIsAnError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		step dStep
		says string
	}{
		{
			"the only search not run",
			dStep{out: `{"data":{"s0":null}}`, failed: nil},
			"could not run this search",
		},
		{"gh fails with output that is not JSON", dStep{out: "not json", failed: errDBad}, "bad"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := &dGh{steps: []dStep{tt.step}, calls: nil}

			_, _, err := reviews.Gh{Exec: source.exec}.Search(t.Context(),
				[]reviews.Query{{Q: "x", Limit: 100}})
			if err == nil {
				t.Fatal("no error")
			}

			if !strings.Contains(err.Error(), tt.says) {
				t.Errorf("error %q, want it to say %q", err, tt.says)
			}
		})
	}
}
