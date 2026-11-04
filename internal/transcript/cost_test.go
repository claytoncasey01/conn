package transcript_test

import (
	"math"
	"os"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/transcript"
)

// The answers in testdata/answers/sessions/cost.json were recorded for seeded usage blocks and
// sequences of messages through one meter.
func readAnswers(t *testing.T, name string) *js.Object {
	t.Helper()

	data, err := os.ReadFile("../../testdata/answers/sessions/" + name)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := js.Parse(data)
	if err != nil {
		t.Fatal(err)
	}

	object, _ := parsed.(*js.Object)

	return object
}

func list(object *js.Object, key string) []any {
	raw, _ := object.Get(key)
	items, _ := raw.([]any)

	return items
}

func same(left, right float64) bool { return math.Float64bits(left) == math.Float64bits(right) }

func TestUsageCostMatchesTheRecording(t *testing.T) {
	t.Parallel()

	for index, item := range list(readAnswers(t, "cost.json"), "singles") {
		row, _ := item.([]any)
		model, _ := row[0].(string)
		usage, _ := row[1].(*js.Object)

		got, priced := transcript.UsageCost(model, usage)

		if row[2] == nil {
			if priced {
				t.Errorf("%d %s: priced at %v, want no price", index, model, got)
			}

			continue
		}

		want, _ := row[2].(float64)
		if !priced {
			t.Errorf("%d %s: no price, want %v", index, model, want)

			continue
		}

		if !same(got, want) {
			t.Errorf("%d %s %s: %v, want %v", index, model, js.Stringify(usage), got, want)
		}
	}
}

func TestUsageMeterMatchesTheRecording(t *testing.T) {
	t.Parallel()

	for index, item := range list(readAnswers(t, "cost.json"), "sequences") {
		steps, _ := item.([]any)
		meter := transcript.NewUsageMeter()

		for step, raw := range steps {
			pair, _ := raw.([]any)
			message, _ := pair[0].(*js.Object)
			want, _ := pair[1].([]any)

			meter.Add(message)

			got := []float64{meter.USD, meter.Output, meter.Unpriced}

			for field, value := range want {
				expected, _ := value.(float64)
				if !same(got[field], expected) {
					t.Errorf("sequence %d step %d field %d: %v, want %v", index, step, field,
						got[field], expected)
				}
			}
		}
	}
}
