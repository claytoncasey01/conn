package js_test

import (
	"math"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/claytoncasey01/conn/internal/js"
)

// TestDateParseMatchesTheRecording holds DateParse to what the recorded Date.parse gave for each
// form, with the local zone America/New_York.
func TestDateParseMatchesTheRecording(t *testing.T) {
	t.Parallel()

	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}

	nan := math.NaN()
	tests := []struct {
		text string
		want float64
	}{
		{"2026-11-03T12:34:56.789Z", 1793709296789},
		{"2026-11-03T12:34:56Z", 1793709296000},
		{"2026-11-03T12:34Z", 1793709240000},
		{"2026-11-03T12:34:56.789", 1793727296789},
		{"2026-11-03T12:34:56", 1793727296000},
		{"2026-11-03", 1793664000000},
		{"2026-11", 1793491200000},
		{"2026", 1767225600000},
		{"+002026-11-03T00:00:00Z", 1793664000000},
		{"-000001-01-01T00:00:00Z", -62198755200000},
		{"2026-11-03T12:34:56.789+02:00", 1793702096789},
		{"2026-11-03T12:34:56.789-0230", 1793718296789},
		{"2026-11-03 12:34:56", 1793727296000},
		{"2026-11-03T12:34:56.7891234Z", 1793709296789},
		{"2026-11-03T12:34:56.7Z", 1793709296700},
		{"2026-11-03T24:00:00Z", 1793750400000},
		{"2026-11-03T24:00:01Z", nan},
		{"2026-02-30T00:00:00Z", 1772409600000},
		{"2026-13-01", nan},
		{"garbage", nan},
		{"", nan},
		{"2026-11-03t12:34:56z", 1793709296000},
		{"1700000000000", nan},
		{"2026-11-03T12:34:56.789z", 1793709296789},
		{" 2026-11-03T12:34:56Z", nan},
		{"2026-11-03T12:34:60Z", nan},
		{"2026-11-03T12:34:56,789Z", nan},
		{"275760-09-13", nan},
		{"+275760-09-13T00:00:00Z", 8640000000000000},
		{"+275760-09-13T00:00:00.001Z", nan},
		{"2026-11-03T1:2:3Z", nan},
		{"2026-11-03T12:34:56+0200", 1793702096000},
	}

	for _, tt := range tests {
		got := js.DateParseIn(tt.text, newYork)
		if math.IsNaN(tt.want) {
			if !math.IsNaN(got) {
				t.Errorf("%q: got %v, want NaN", tt.text, got)
			}

			continue
		}

		if got != tt.want {
			t.Errorf("%q: got %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestClipCollapsesAndCutsInUTF16(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text  string
		units int
		want  string
	}{
		{"  a \n\t b  ", 10, "a b"},
		{"abcdef", 4, "abc…"},
		{"abcd", 4, "abcd"},
		{"ab\U0001F600cd", 4, "ab�…"},
		{"ab\U0001F600cd", 5, "ab\U0001F600…"},
		{"a  b", 9, "a b"},
	}

	for _, tt := range tests {
		got := js.Clip(tt.text, tt.units)
		if got != tt.want {
			t.Errorf("Clip(%q, %d) = %q, want %q", tt.text, tt.units, got, tt.want)
		}
	}
}

func TestNumWritesNonFiniteAsNull(t *testing.T) {
	t.Parallel()

	got := js.Stringify([]any{js.Num(math.NaN()), js.Num(1.5e-7), js.Num(3)})
	if got != "[null,1.5e-7,3]" {
		t.Errorf("got %s", got)
	}
}
