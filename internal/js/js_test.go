package js_test

import (
	"errors"
	"math"
	"strconv"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
)

func TestParseKeepsKeyOrderAndStringifyWritesItBack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, text, want string
	}{
		{
			"nested", `{"b":1,"a":[true,null,"x"],"c":{"z":1,"y":2}}`,
			`{"b":1,"a":[true,null,"x"],"c":{"z":1,"y":2}}`,
		},
		{"repeated key", ` {"a" : 1 , "a" : 2, "b": 3} `, `{"a":2,"b":3}`},
		{"index keys first", `{"b":1,"2":2,"a":3,"1":4}`, `{"1":4,"2":2,"b":1,"a":3}`},
		{
			"numbers", `[1.5, -0, 1e21, 1e-7, 0.000001, 100, 2.50]`,
			`[1.5,0,1e+21,1e-7,0.000001,100,2.5]`,
		},
		{"strings", `"<b>&\u0001\u2028\t"`, `"<b>&\u0001` + "\u2028" + `\t"`},
		{"too large is null", `[1e400]`, `[null]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parsed, err := js.Parse([]byte(tt.text))
			if err != nil {
				t.Fatal(err)
			}

			if got := js.Stringify(parsed); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestParseRefusesWhatJSONParseRefuses(t *testing.T) {
	t.Parallel()

	tests := []string{
		``, `{`, `{"a":1}x`, `{a:1}`, `[1,]`, `'x'`, "\ufeff{}", `NaN`, `{"a":1} {}`,
	}
	for _, text := range tests {
		t.Run(strconv.Quote(text), func(t *testing.T) {
			t.Parallel()

			_, err := js.Parse([]byte(text))
			if !errors.Is(err, js.ErrSyntax) {
				t.Fatalf("got %v, want ErrSyntax", err)
			}
		})
	}
}

func TestParseRefusesNestingPastTheLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		depth   int
		wantErr error
	}{
		{"at the limit", 512, nil},
		{"past it", 513, js.ErrSyntax},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			text := make([]byte, 0, 2*(tt.depth+1))
			for range tt.depth + 1 {
				text = append(text, '[')
			}

			for range tt.depth + 1 {
				text = append(text, ']')
			}

			_, err := js.Parse(text)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestObjectGetSetDelete(t *testing.T) {
	t.Parallel()

	object := js.NewObject()
	object.Set("a", 1.0)
	object.Set("b", nil)

	if _, ok := object.Get("b"); !ok {
		t.Fatal("a key holding null is there")
	}

	if _, ok := object.Get("c"); ok {
		t.Fatal("an absent key is not")
	}

	object.Set("a", 2.0)
	object.Delete("b")

	if got := js.Stringify(object); got != `{"a":2}` {
		t.Fatalf("got %s", got)
	}

	if object.Len() != 1 {
		t.Fatalf("length %d", object.Len())
	}
}

func TestANilObjectIsEmpty(t *testing.T) {
	t.Parallel()

	var none *js.Object

	if _, ok := none.Get("a"); ok {
		t.Fatal("a nil object has no keys")
	}

	if none.Len() != 0 {
		t.Fatal("a nil object has length 0")
	}

	if js.Stringify(none) != "null" {
		t.Fatal("a nil object is null")
	}
}

func TestNumberAsJavaScriptPrintsIt(t *testing.T) {
	t.Parallel()

	// Variables, not constants: Go adds constants exactly, and 0.1 + 0.2 must round.
	first, second := 0.1, 0.2

	tests := []struct {
		value float64
		want  string
	}{
		{1, "1"},
		{-1.5, "-1.5"},
		{first + second, "0.30000000000000004"},
		{123456789012345680000, "123456789012345680000"},
		{1e300, "1e+300"},
		{5e-324, "5e-324"},
		{math.NaN(), "null"},
		{math.Inf(1), "null"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			if got := js.Number(tt.value); got != tt.want {
				t.Fatalf("got %s", got)
			}
		})
	}
}

func TestTrimIsJavaScriptsTrim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text, want string
	}{
		{"  a  ", "a"},
		{"\t\n\v\f\r a", "a"},
		{"\u00a0a\ufeff", "a"},
		{"\u2028a\u2029", "a"},
		{"\u3000\u2000a\u200a", "a"},
		// NEL is not JavaScript whitespace, nor is a zero-width space.
		{"\u0085a", "\u0085a"},
		{"\u200ba", "\u200ba"},
	}
	for _, tt := range tests {
		t.Run(strconv.Quote(tt.text), func(t *testing.T) {
			t.Parallel()

			if got := js.Trim(tt.text); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLenCountsUTF16Units(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text string
		want int
	}{
		{"abc", 3},
		{"é", 1},
		{"😀", 2},
		{"", 0},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			t.Parallel()

			if got := js.Len(tt.text); got != tt.want {
				t.Fatalf("got %d", got)
			}
		})
	}
}

func TestSliceCutsUTF16UnitsAndKeepsPairsWhole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text  string
		units int
		want  string
	}{
		{"abcdef", 3, "abc"},
		{"ab", 5, "ab"},
		{"a😀b", 2, "a"},
		{"a😀b", 3, "a😀"},
		{"x", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.text+strconv.Itoa(tt.units), func(t *testing.T) {
			t.Parallel()

			if got := js.Slice(tt.text, tt.units); got != tt.want {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestStringifyIndentIsTwoSpacesLikeJSONStringify(t *testing.T) {
	t.Parallel()

	parsed, err := js.Parse([]byte(`{"a":[1,2],"b":{},"c":[]}`))
	if err != nil {
		t.Fatal(err)
	}

	want := "{\n  \"a\": [\n    1,\n    2\n  ],\n  \"b\": {},\n  \"c\": []\n}"
	if got := js.StringifyIndent(parsed, "  "); got != want {
		t.Fatalf("got\n%s", got)
	}
}

func TestToNumberIsJavaScriptsNumber(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text string
		want float64
	}{
		{"25", 25},
		{" 25 ", 25},
		{"2.7", 2.7},
		{"-5", -5},
		{"", 0},
		{"  ", 0},
		{"0x10", 16},
		{"0b11", 3},
		{"0o17", 15},
		{"1e2", 100},
		{".5", 0.5},
		{"5.", 5},
		{"+3", 3},
		{"Infinity", math.Inf(1)},
		{"-Infinity", math.Inf(-1)},
	}
	for _, tt := range tests {
		t.Run(strconv.Quote(tt.text), func(t *testing.T) {
			t.Parallel()

			if got := js.ToNumber(tt.text); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToNumberIsNaNForAnythingElse(t *testing.T) {
	t.Parallel()

	tests := []string{
		"abc", "NaN", "inf", "infinity", "1_000", "0x", "-0x10", "0x1p4", "1e", "25px", "0b2",
	}
	for _, text := range tests {
		t.Run(text, func(t *testing.T) {
			t.Parallel()

			if got := js.ToNumber(text); !math.IsNaN(got) {
				t.Fatalf("got %v, want NaN", got)
			}
		})
	}
}
