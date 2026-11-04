package reviews_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"testing"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// fileOf is a recorded version 2 file as Go holds it, read the way ReadFile reads one.
func fileOf(t *testing.T, value any) *reviews.File {
	t.Helper()

	return reviews.LoadFile(writeText(t, js.Stringify(value)))
}

func TestStoreFilesMatchBun(t *testing.T) {
	t.Parallel()

	for index, row := range section(t, answers(t), "files") {
		label := "file " + strconv.Itoa(index)
		read := reviews.ReadFile(writeText(t, str(get(row, "text"))))
		check(t, label+" kind", read.Kind, get(row, "kind"))

		switch read.Kind {
		case reviews.ReadV2:
			check(t, label+" file", read.V2, get(row, "file"))
			path := writeText(t, "")

			err := reviews.SaveFile(path, read.V2, nil)
			if err != nil {
				t.Fatal(err)
			}

			saved, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			check(t, label+" saved", string(saved), get(row, "saved"))
		case reviews.ReadV1:
			check(t, label+" v1", read.V1, get(row, "v1"))
			migrated, err := reviews.MigrateV1(read.V1, migrateContext(row))
			check(t, label+" migrated", outOrError(migrated, err), get(row, "migrated"))
		case reviews.ReadUnreadable:
			check(t, label+" why", read.Why, get(row, "why"))
		}
	}
}

// migrateContext is the recorded migration's context: its origins, and its answers to exists in
// the order they were asked.
func migrateContext(row any) reviews.MigrateContext {
	answersLeft := items(get(row, "exists"))
	origins := get(row, "origins")

	return reviews.MigrateContext{
		Allow: strs(get(row, "allow")), Seen: strs(get(row, "seen")),
		OriginOf: func(folder string) string { return str(get(origins, folder)) },
		Exists: func(string) bool {
			if len(answersLeft) == 0 {
				return false
			}

			answer := answersLeft[0] == true
			answersLeft = answersLeft[1:]

			return answer
		},
	}
}

func TestStoreChangesMatchBun(t *testing.T) {
	t.Parallel()

	data := answers(t)

	for index, row := range section(t, data, "placeLegacy") {
		label := "placeLegacy " + strconv.Itoa(index)
		file := fileOf(t, get(row, "file"))
		moved, err := reviews.PlaceLegacy(file, str(get(row, "which")), str(get(row, "repo")),
			answersT0)
		check(t, label+" moved", outOrError(moved, err), get(row, "moved"))
		check(t, label+" after", file, get(row, "after"))
	}

	for index, row := range section(t, data, "prune") {
		label := "prune " + strconv.Itoa(index)
		file := fileOf(t, get(row, "file"))
		open := strs(get(row, "open"))
		keep := strs(get(row, "keep"))

		var allow []string
		if get(row, "allow") != nil {
			allow = strs(get(row, "allow"))
		}

		reviews.Prune(file, func(key string) bool { return slices.Contains(open, key) }, allow,
			num(get(row, "now")),
			func(key string, _ *reviews.Entry) bool { return slices.Contains(keep, key) })
		check(t, label, file, get(row, "after"))
	}

	keyChecks(t, data)
}

func keyChecks(t *testing.T, data *js.Object) {
	t.Helper()

	for _, row := range section(t, data, "prKey") {
		got, err := reviews.PRKey(str(get(row, "repo")), asNumber(get(row, "n")))
		check(t, "prKey "+js.Stringify(row), outOrError(got, err), get(row, "out"))
	}

	for _, row := range section(t, data, "parseKey") {
		var got any

		if parts, ok := reviews.ParseKey(str(get(row, "key"))); ok {
			got = parts
		}

		check(t, "parseKey "+str(get(row, "key")), got, get(row, "out"))
	}

	for _, row := range section(t, data, "nthBackup") {
		got := reviews.NthBackup("/d/reviews.json.v1.bak", int(num(get(row, "n"))))
		check(t, "nthBackup", got, get(row, "out"))
	}
}

// scriptedGh answers each gh call with the next recorded step, and keeps the calls.
type scriptedGh struct {
	steps []any
	calls [][]string
}

func (s *scriptedGh) exec(_ context.Context, args []string) (string, error) {
	s.calls = append(s.calls, args)
	step := s.steps[min(len(s.calls)-1, len(s.steps)-1)]

	failure, failed := get(step, "err").(*js.Object)
	if !failed {
		return str(get(step, "out")), nil
	}

	return str(get(failure, "stdout")), textError(str(get(failure, "message")))
}

func TestGraphQLCallsMatchBun(t *testing.T) {
	t.Parallel()

	for _, row := range section(t, answers(t), "gh") {
		label := str(get(row, "label"))
		script := &scriptedGh{steps: items(get(row, "steps")), calls: nil}
		source := reviews.Gh{Exec: script.exec}

		got, err := ghCall(t, label, source)
		check(t, label+" calls", script.calls, get(row, "calls"))

		if err != nil {
			var limit *ghbudget.RateLimitError

			limited := errors.As(err, &limit)
			check(t, label+" error", err.Error(), get(row, "error"))
			check(t, label+" rateLimited", limited, get(row, "rateLimited"))

			if limited {
				check(t, label+" resetAt", limit.ResetAt, get(row, "resetAt"))
			}

			continue
		}

		check(t, label+" out", got, get(row, "out"))
	}
}

// keptError is an error from the package under test, kept word for word: the check compares its
// text with Bun's.
type keptError struct {
	err error
}

func (e *keptError) Error() string { return e.err.Error() }

func (e *keptError) Unwrap() error { return e.err }

// kept wraps err without changing its text; nil stays nil.
func kept(err error) error {
	if err == nil {
		return nil
	}

	return &keptError{err: err}
}

func ghCall(t *testing.T, label string, source reviews.Gh) (any, error) {
	t.Helper()

	ctx := t.Context()

	switch label {
	case "login", "login empty", "login fails":
		login, err := source.Login(ctx)

		return login, kept(err)
	case "fetch":
		return fetchWithRates(ctx, source)
	case "fetch batches", "fetch fails":
		var prs []reviews.KeyParts

		count := 1
		if label == "fetch batches" {
			count = 51
		}

		for number := range count {
			prs = append(prs, reviews.KeyParts{Repo: "acme/widgets", Number: int64(number + 1)})
		}

		got, err := source.Fetch(ctx, prs, nil)

		return got, kept(err)
	}

	return searchCall(ctx, label, source)
}

func fetchWithRates(ctx context.Context, source reviews.Gh) (any, error) {
	rates := []*ghbudget.RateInfo{}

	got, err := source.Fetch(ctx, []reviews.KeyParts{
		{Repo: "Acme/Widgets", Number: 7},
		{Repo: "acme/widgets", Number: 8},
		{Repo: "acme/widgets", Number: 9},
		{Repo: "acme/widgets", Number: 10},
		{Repo: "acme/widgets", Number: 11},
		{Repo: "bad", Number: 1},
		{Repo: "acme/x", Number: 0},
	}, func(rate *ghbudget.RateInfo) { rates = append(rates, rate) })
	if err != nil {
		return nil, kept(err)
	}

	out := js.NewObject()
	out.Set("got", got)
	out.Set("rates", rates)

	return out, nil
}

func searchCall(ctx context.Context, label string, source reviews.Gh) (any, error) {
	count := map[string]int{"seven searches": 7, "one fails": 2, "all fail": 2}[label]
	if count == 0 {
		count = 1
	}

	queries := make([]reviews.Query, 0, count)

	for index := range count {
		queries = append(queries, reviews.Query{
			Q: "is:pr is:open repo:acme/r" + strconv.Itoa(index), Limit: 100,
		})
	}

	if label == "paged" {
		queries = []reviews.Query{{Q: "a", Limit: 200}, {Q: "b", Limit: 3}}
	}

	results, rate, err := source.Search(ctx, queries)
	if err != nil {
		return nil, kept(err)
	}

	shown := []*js.Object{}

	for _, result := range results {
		item := js.NewObject()
		item.Set("prs", result.PRs)
		item.Set("capped", result.Capped)

		if result.Error != nil {
			item.Set("error", *result.Error)
		}

		shown = append(shown, item)
	}

	out := js.NewObject()
	out.Set("results", shown)
	out.Set("rate", rate)

	return out, nil
}
