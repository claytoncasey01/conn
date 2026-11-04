package js_test

import (
	"compress/gzip"
	"io"
	"math"
	"os"
	"slices"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
)

type sortItem struct {
	value float64
	index int
}

func readGzipJSON(t *testing.T, path string) any {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = file.Close() }()

	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := js.Parse(data)
	if err != nil {
		t.Fatal(err)
	}

	return parsed
}

func listAt(object *js.Object, key string) []any {
	value, _ := object.Get(key)
	list, _ := value.([]any)

	return list
}

// sortedIndexes sorts the values (null standing for NaN) and gives their first places in order.
func sortedIndexes(values []any) []float64 {
	items := make([]sortItem, 0, len(values))

	for index, value := range values {
		typed, isNumber := value.(float64)
		if !isNumber {
			typed = math.NaN()
		}

		items = append(items, sortItem{value: typed, index: index})
	}

	js.Sort(items, func(left, right sortItem) float64 { return left.value - right.value })

	order := make([]float64, 0, len(items))
	for _, item := range items {
		order = append(order, float64(item.index))
	}

	return order
}

// TestSortMatchesTheRecording holds Sort to the recorded orders JavaScript's sort gave seeded
// lists, some of whose items compare as NaN (null in the file): the order then depends on the
// algorithm, not the values.
func TestSortMatchesTheRecording(t *testing.T) {
	t.Parallel()

	cases, _ := readGzipJSON(t, "../../testdata/answers/sort.json.gz").([]any)

	for number, raw := range cases {
		object, _ := raw.(*js.Object)
		values := listAt(object, "values")

		want := make([]float64, 0, len(values))

		for _, index := range listAt(object, "order") {
			typed, _ := index.(float64)
			want = append(want, typed)
		}

		if !slices.Equal(sortedIndexes(values), want) {
			t.Errorf("case %d (%d items): the order differs from the recorded one",
				number, len(values))
		}
	}
}
