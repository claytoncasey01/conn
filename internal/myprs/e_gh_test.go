package myprs_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
)

// The gh reads. The recorded answers (clean, cleanMultiline, parse, searchFor,
// searchPatterns, prs, threadInfo) cover the parsing of every field, the searches' argv and
// query, the failures and the rate; here are what they do not reach: the exact cleaning cases,
// the size bounds (they hold at most 5 threads and 3 nodes), the grammar lists and the merge of
// several searches.

var errENotLoggedIn = errors.New("gh: not logged in")

// eNodeJSON is org/repo#7 with one thread, review and comment.
const eNodeJSON = `{"number":7,"title":"Add a thing","url":"https://github.com/org/repo/pull/7",` +
	`"isDraft":false,"createdAt":"2026-09-30T08:00:00Z","updatedAt":"2026-10-01T12:00:00Z",` +
	`"headRefName":"feature/a","headRefOid":"abc123","baseRefName":"development",` +
	`"repository":{"nameWithOwner":"org/repo","defaultBranchRef":{"name":"development"}},` +
	`"headRepository":{"nameWithOwner":"org/repo"},` +
	`"commits":{"nodes":[{"commit":{"committedDate":"2026-10-01T07:00:00Z"}}]},` +
	`"reviewThreads":{"pageInfo":{"hasPreviousPage":false},"nodes":[` + eThreadJSON + `]},` +
	`"reviews":{"nodes":[{"author":{"login":"devin-ai-integration"},` +
	`"submittedAt":"2026-10-01T10:00:00Z","commit":{"oid":"abc123"}}]},` +
	`"comments":{"nodes":[` + eCommentJSON + `]},"firstComments":{"nodes":[` + eCommentJSON +
	`]}}`

const eThreadJSON = `{"id":"PRRT_1","isResolved":false,"isOutdated":false,"path":"src/a.ts",` +
	`"line":12,"comments":{"nodes":[{"author":{"login":"greptile-apps"},` +
	`"createdAt":"2026-10-01T09:00:00Z","body":"Handle the empty case."}]}}`

const eCommentJSON = `{"id":"IC_1","author":{"login":"greptile-apps"},` +
	`"createdAt":"2026-10-01T11:00:00Z","lastEditedAt":"2026-10-01T11:30:00Z","body":"Summary"}`

// eParsed is JSON text parsed as JavaScript reads it.
func eParsed(t *testing.T, text string) any {
	t.Helper()

	parsed, err := js.Parse([]byte(text))
	if err != nil {
		t.Fatalf("%s: %v", text, err)
	}

	return parsed
}

// eNode is a node with fields (raw JSON each) set over node()'s.
func eNode(t *testing.T, fields ...string) *js.Object {
	t.Helper()

	node, _ := eParsed(t, eNodeJSON).(*js.Object)

	for index := 0; index+1 < len(fields); index += 2 {
		node.Set(fields[index], eParsed(t, fields[index+1]))
	}

	return node
}

// eRepoNode is node number of repo, its own head and no default branch.
func eRepoNode(t *testing.T, number int, repo string) *js.Object {
	t.Helper()

	name := js.Stringify(repo)

	return eNode(t, "number", strconv.Itoa(number),
		"repository", `{"nameWithOwner":`+name+`,"defaultBranchRef":null}`,
		"headRepository", `{"nameWithOwner":`+name+`}`)
}

// ePayload is a search answer of nodes, as gh prints it.
func ePayload(nodes ...any) string {
	search := js.NewObject()
	search.Set("nodes", append([]any{}, nodes...))

	data := js.NewObject()
	data.Set("search", search)

	out := js.NewObject()
	out.Set("data", data)

	return js.Stringify(out)
}

func eParse(t *testing.T, nodes ...any) []*myprs.PR {
	t.Helper()

	return myprs.ParsePRs(eParsed(t, ePayload(nodes...)), "", trigger)
}

// eAnswering is a fake gh that records each call and answers what answer says for its args.
type eAnswering struct {
	calls  [][]string
	answer func(args []string) (string, error)
}

func (e *eAnswering) run(_ context.Context, args []string) (string, error) {
	e.calls = append(e.calls, args)

	return e.answer(args)
}

func eGh(answer func(args []string) (string, error)) (myprs.Gh, *eAnswering) {
	fake := &eAnswering{calls: nil, answer: answer}

	return myprs.Gh{Exec: fake.run, Trigger: trigger}, fake
}

func TestEClean(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, text string
		multiline  bool
		max        int
		want       string
	}{
		{
			"hidden characters", "a\u202eb\u0085c\u200bd\ufeffe\u200df\u001bg\u007fh", false,
			myprs.TextUnitsMax, "abcdefgh",
		},
		{"line breaks", "a\nb\tc\u2028d\u2029e", false, myprs.TextUnitsMax, "a b c d e"},
		{
			"lines and a fence", "Fixed:\n- renamed X\n- added test\n\n```ts\nfoo()\n```", true,
			myprs.NoCut, "Fixed:\n- renamed X\n- added test\n\n```ts\nfoo()\n```",
		},
		{"each break once", "a\r\nb\rc\u2028d\u2029e\tf", true, myprs.NoCut, "a\nb\nc\nd\ne f"},
		{"a run of newlines", "a\n\n\n\n\nb\n\nc", true, myprs.NoCut, "a\n\nb\n\nc"},
		{"a run of every kind", "a\r\n\r\n\r\nb\n\u200b\n\nc", true, myprs.NoCut, "a\n\nb\n\nc"},
		{
			"hidden and trimmed", "  \n a\u202eb\u0007c\u200bd\u0085e\u001bf\u007fg\ufeffh\n\n  ",
			true, myprs.NoCut, "abcdefgh",
		},
		{"vertical tab and form feed", "x\u000by\u000cz", true, myprs.NoCut, "xyz"},
		{"the rest", "x\u0000\u000b\u000c\u061c\u2066y\nz", true, myprs.NoCut, "xy\nz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := myprs.Clean(tt.text, tt.max)
			if tt.multiline {
				got = myprs.CleanMultiline(tt.text, tt.max)
			}

			if got != tt.want {
				t.Errorf("got %q", got)
			}
		})
	}

	for _, tt := range []struct {
		name string
		got  string
		want int
	}{
		{"clean to 300", myprs.Clean(strings.Repeat("x", 500), 300), 300},
		{"clean to 400", myprs.Clean(strings.Repeat("x", 500), myprs.TextUnitsMax), 400},
		{"multiline to 300", myprs.CleanMultiline(strings.Repeat("x", 500), 300), 300},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if js.Len(tt.got) != tt.want {
				t.Errorf("%d long", js.Len(tt.got))
			}
		})
	}
}

func TestEIsOwnAsk(t *testing.T) {
	t.Parallel()

	tests := []struct {
		by, body, me string
		want         bool
	}{
		{"JANE", trigger, "jane", true},
		{"jane", " " + trigger + " ", "jane", true},
		{"sam", trigger, "jane", false},
		{"jane", "thanks", "jane", false},
		{"", trigger, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.by+" "+tt.body, func(t *testing.T) {
			t.Parallel()

			comment := myprs.Comment{By: tt.by, At: 1, EditedAt: nil, Body: tt.body}
			if myprs.IsOwnAsk(comment, tt.me, trigger) != tt.want {
				t.Errorf("not %v", tt.want)
			}
		})
	}
}

func TestEARepoOutsideTheGrammarIsDropped(t *testing.T) {
	t.Parallel()

	repos := []string{
		"org/repo", "org/re po", "org/repo?x=1", "org/repo#2", "org/..", "../x", "a/b/c",
		"org/-lead", "org\u2044repo", "org/rep\u202eo", "org/rep\u0131", "org/repo\n",
		"org/re\u202epo\u200b\u001b",
	}
	nodes := make([]any, 0, len(repos))

	for index, repo := range repos {
		nodes = append(nodes, eRepoNode(t, index+1, repo))
	}

	listed := eParse(t, nodes...)
	if len(listed) != 1 {
		t.Fatalf("%d listed", len(listed))
	}

	if listed[0].Repo != "org/repo" {
		t.Errorf("listed %s", listed[0].Repo)
	}
}

// eThreadsJSON is a reviewThreads connection of count threads, with pageInfo (raw JSON, ""
// for none).
func eThreadsJSON(t *testing.T, count int, pageInfo string) string {
	t.Helper()

	nodes := make([]string, 0, count)
	for index := range count {
		thread, _ := eParsed(t, eThreadJSON).(*js.Object)
		thread.Set("id", "T"+strconv.Itoa(index))
		nodes = append(nodes, js.Stringify(thread))
	}

	info := ""
	if pageInfo != "" {
		info = `"pageInfo":` + pageInfo + ","
	}

	return "{" + info + `"nodes":[` + strings.Join(nodes, ",") + "]}"
}

func TestEPartialByCount(t *testing.T) {
	t.Parallel()

	many := func(count int, item string) string {
		return `{"nodes":[` + strings.TrimSuffix(strings.Repeat(item+",", count), ",") + "]}"
	}
	review := `{"author":{"login":"a"},"submittedAt":"2026-10-01T12:00:00Z","commit":{"oid":"x"}}`
	tests := []struct {
		name   string
		fields []string
		want   bool
	}{
		{
			"100 and no older",
			[]string{"reviewThreads", eThreadsJSON(t, 100, `{"hasPreviousPage":false}`)},
			false,
		},
		{
			"3 and older",
			[]string{"reviewThreads", eThreadsJSON(t, 3, `{"hasPreviousPage":true}`)},
			true,
		},
		{"99 and no word", []string{"reviewThreads", eThreadsJSON(t, 99, "")}, false},
		{"100 and a null word", []string{"reviewThreads", eThreadsJSON(t, 100, "null")}, true},
		{"50 reviews and comments", []string{
			"reviews", many(50, review), "comments", many(50, eCommentJSON),
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			listed := eParse(t, eNode(t, tt.fields...))
			if len(listed) != 1 {
				t.Fatalf("%d listed", len(listed))
			}

			if listed[0].Partial != tt.want {
				t.Errorf("partial %v", listed[0].Partial)
			}
		})
	}
}

func TestEPRsCappedAtTheSearchLimit(t *testing.T) {
	t.Parallel()

	full := make([]any, 0, myprs.SearchLimit)
	for index := range myprs.SearchLimit {
		full = append(full, eNode(t, "number", strconv.Itoa(index+1)))
	}

	source, fake := eGh(func([]string) (string, error) { return ePayload(full...), nil })

	found, err := source.PRs(context.Background(), "@me", []string{"org/*"})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(fake.calls[0], "q=is:pr is:open author:@me user:org") {
		t.Errorf("argv %q", fake.calls[0])
	}

	if len(found.PRs) != 100 {
		t.Errorf("%d PRs", len(found.PRs))
	}

	if !found.Capped {
		t.Error("not capped")
	}

	few, _ := eGh(func([]string) (string, error) { return ePayload(eNode(t)), nil })

	found, err = few.PRs(context.Background(), "@me", []string{"org/*"})
	if err != nil {
		t.Fatal(err)
	}

	if found.Capped {
		t.Error("one PR is capped")
	}
}

func TestEPRsSearchForALogin(t *testing.T) {
	t.Parallel()

	for _, tt := range []string{"Jane-C1", "jane_acme"} {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()

			source, fake := eGh(func([]string) (string, error) { return ePayload(), nil })

			_, err := source.PRs(context.Background(), tt, []string{"org/*"})
			if err != nil {
				t.Fatal(err)
			}

			if !slices.Contains(fake.calls[0], "q=is:pr is:open author:"+tt+" user:org") {
				t.Errorf("argv %q", fake.calls[0])
			}
		})
	}

	for _, tt := range []string{
		"", "a b", "x repo:other/r", "x:y", "x\"y", "-lead", strings.Repeat("x", 101),
	} {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()

			source, fake := eGh(func([]string) (string, error) { return ePayload(), nil })

			_, err := source.PRs(context.Background(), tt, []string{"org/*"})
			if !errors.Is(err, myprs.ErrENotLogin) {
				t.Errorf("got %v", err)
			}

			if len(fake.calls) != 0 {
				t.Errorf("gh ran: %q", fake.calls)
			}
		})
	}
}

func TestELogin(t *testing.T) {
	t.Parallel()

	source, _ := eGh(func([]string) (string, error) { return "jane\n", nil })

	login, err := source.Login(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if login != "jane" {
		t.Errorf("login %q", login)
	}

	down, _ := eGh(func([]string) (string, error) { return "", errENotLoggedIn })

	_, err = down.PRs(context.Background(), "@me", []string{"org/*"})
	if err == nil {
		t.Fatal("no error")
	}

	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("got %v", err)
	}
}

func TestEThreadInfoRefusesABadID(t *testing.T) {
	t.Parallel()

	for _, tt := range []string{
		"", "short", "PRRT_kwDO abc", strings.Repeat("a", 201), "PRRT_$(x)xx",
	} {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()

			source, fake := eGh(func([]string) (string, error) { return "{}", nil })

			_, err := source.ThreadInfo(context.Background(), tt)
			if !errors.Is(err, myprs.ErrENotThreadID) {
				t.Errorf("got %v", err)
			}

			if len(fake.calls) != 0 {
				t.Errorf("gh ran: %q", fake.calls)
			}
		})
	}
}

func TestESearchFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		me, pattern, want string
	}{
		{"jane", "acme/*", "is:pr is:open author:jane user:acme"},
		{"jane", "acme/widgets", "is:pr is:open author:jane repo:acme/widgets"},
		{"@me", "acme/.github", "is:pr is:open author:@me repo:acme/.github"},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			t.Parallel()

			got, ok := myprs.SearchFor(tt.me, tt.pattern)
			if !ok {
				t.Fatal("no search")
			}

			if got != tt.want {
				t.Errorf("got %q", got)
			}
		})
	}

	for _, tt := range []string{
		"*", "*/*", "acme/re*", "acme", "", "a b/*", "acme:x/*", "acme/x y", "acme/x repo:o/r",
		"ac/me/*", "../x", "acme/..", "-acme/*", "acme/*x",
	} {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()

			if got, ok := myprs.SearchFor("jane", tt); ok {
				t.Errorf("searched %q", got)
			}
		})
	}
}

func TestESearchPatterns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		patterns []string
		want     []string
	}{
		{
			"repeats and covered repos",
			[]string{"acme/*", "acme/widgets", "jane/tools", "JANE/tools", "acme/*"},
			[]string{"acme/*", "jane/tools"},
		},
		{"another owner", []string{"acme-labs/x", "acme/*"}, []string{"acme-labs/x", "acme/*"}},
		{"none", []string{}, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := myprs.SearchPatterns(tt.patterns)
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %q", got)
			}
		})
	}
}

func TestEPRsMergeTheSearches(t *testing.T) {
	t.Parallel()

	answers := map[string][]any{
		"user:acme": {eRepoNode(t, 1, "acme/web"), eRepoNode(t, 2, "acme/api")},
		"repo:jane/tools": {
			eRepoNode(t, 9, "Jane/Tools"), eRepoNode(t, 1, "acme/web"),
			eRepoNode(t, 5, "other/thing"),
		},
	}
	queries := []string{}
	source, _ := eGh(func(args []string) (string, error) {
		for _, arg := range args {
			if strings.HasPrefix(arg, "q=") {
				queries = append(queries, arg)
				words := strings.Fields(arg)

				return ePayload(answers[words[len(words)-1]]...), nil
			}
		}

		return ePayload(), nil
	})

	found, err := source.PRs(context.Background(), "jane",
		[]string{"acme/*", "acme/web", "jane/tools"})
	if err != nil {
		t.Fatal(err)
	}

	wantQueries := []string{
		"q=is:pr is:open author:jane user:acme", "q=is:pr is:open author:jane repo:jane/tools",
	}
	if !slices.Equal(queries, wantQueries) {
		t.Errorf("queries %q", queries)
	}

	keys := make([]string, 0, len(found.PRs))
	for _, pull := range found.PRs {
		keys = append(keys, myprs.EntryKey(pull.Repo, pull.Number))
	}

	slices.Sort(keys)

	if !slices.Equal(keys, []string{"Jane/Tools#9", "acme/api#2", "acme/web#1"}) {
		t.Errorf("listed %q", keys)
	}

	if found.Capped {
		t.Error("capped")
	}
}

func TestEPRsPatternBounds(t *testing.T) {
	t.Parallel()

	patterns := func(count int, shape string) []string {
		out := make([]string, 0, count)
		for index := range count {
			out = append(out, strings.Replace(shape, "N", strconv.Itoa(index), 1))
		}

		return out
	}
	tests := []struct {
		name     string
		patterns []string
		wantErr  error
		calls    int
	}{
		{"none", []string{}, nil, 0},
		{"26", patterns(26, "acme/rN"), myprs.ErrERemoveSome, 0},
		{"a bad one", []string{"acme/*", "jane/x y"}, myprs.ErrENotPattern, 0},
		{"everything", []string{"*"}, myprs.ErrENotPattern, 0},
		{"25", patterns(25, "acmeN/*"), nil, 25},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source, fake := eGh(func([]string) (string, error) { return ePayload(), nil })

			found, err := source.PRs(context.Background(), "jane", tt.patterns)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v", err)
			}

			if len(fake.calls) != tt.calls {
				t.Errorf("%d calls", len(fake.calls))
			}

			if tt.name == "none" {
				eCheckNone(t, found)
			}
		})
	}
}

func eCheckNone(t *testing.T, found myprs.Search) {
	t.Helper()

	if len(found.PRs) != 0 {
		t.Errorf("%d PRs", len(found.PRs))
	}

	if found.Capped {
		t.Error("capped")
	}

	if found.Failed == nil {
		t.Error("failed is nil, not empty")
	}

	if len(found.Failed) != 0 {
		t.Errorf("failed %v", found.Failed)
	}

	if found.Rate != nil {
		t.Errorf("rate %v", found.Rate)
	}
}

func TestEParseGarbage(t *testing.T) {
	t.Parallel()

	for _, tt := range []string{
		"null", `"x"`, "3", "{}", "[]", `{"data":null}`, `{"data":{"search":{}}}`,
	} {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()

			if got := myprs.ParsePRs(eParsed(t, tt), "", trigger); len(got) != 0 {
				t.Errorf("%d PRs", len(got))
			}
		})
	}
}
