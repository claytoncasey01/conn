package js_test

import (
	"encoding/hex"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
)

// The expected values in this file are the recorded outputs for the same inputs.

func TestDecodeUTF8(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in, buffer, decoder string // hex: the bytes, Buffer.toString, TextDecoder.decode
	}{
		{in: "", buffer: "", decoder: ""},
		{in: "41", buffer: "41", decoder: "41"},
		{in: "c3a9", buffer: "c3a9", decoder: "c3a9"},
		{in: "ff", buffer: "efbfbd", decoder: "efbfbd"},
		{in: "c0af", buffer: "efbfbdefbfbd", decoder: "efbfbdefbfbd"},
		{in: "e282", buffer: "efbfbd", decoder: "efbfbd"},
		{in: "e282ac", buffer: "e282ac", decoder: "e282ac"},
		{in: "e28241", buffer: "efbfbd41", decoder: "efbfbd41"},
		{in: "eda080", buffer: "efbfbdefbfbdefbfbd", decoder: "efbfbdefbfbdefbfbd"},
		{in: "f09f9880", buffer: "f09f9880", decoder: "f09f9880"},
		{in: "f09f98", buffer: "efbfbd", decoder: "efbfbd"},
		{in: "f09f9841", buffer: "efbfbd41", decoder: "efbfbd41"},
		{in: "f4908080", buffer: "efbfbdefbfbdefbfbdefbfbd", decoder: "efbfbdefbfbdefbfbdefbfbd"},
		{in: "efbbbf41", buffer: "efbbbf41", decoder: "41"},
		{in: "efbbbfefbbbf", buffer: "efbbbfefbbbf", decoder: "efbbbf"},
		{in: "80", buffer: "efbfbd", decoder: "efbfbd"},
		{in: "e0808f", buffer: "efbfbdefbfbdefbfbd", decoder: "efbfbdefbfbdefbfbd"},
		{in: "f0808080", buffer: "efbfbdefbfbdefbfbdefbfbd", decoder: "efbfbdefbfbdefbfbdefbfbd"},
		{in: "c2", buffer: "efbfbd", decoder: "efbfbd"},
		{in: "41c2", buffer: "41efbfbd", decoder: "41efbfbd"},
		{in: "e0a080", buffer: "e0a080", decoder: "e0a080"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			data, err := hex.DecodeString(tt.in)
			if err != nil {
				t.Fatal(err)
			}

			buffer := hex.EncodeToString([]byte(js.DecodeUTF8(data, false)))
			if buffer != tt.buffer {
				t.Errorf("kept BOM = %s, want %s", buffer, tt.buffer)
			}

			decoder := hex.EncodeToString([]byte(js.DecodeUTF8(data, true)))
			if decoder != tt.decoder {
				t.Errorf("stripped BOM = %s, want %s", decoder, tt.decoder)
			}
		})
	}
}

func TestFixed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   float64
		want string
	}{
		{in: 0, want: "0.00"},
		{in: 1, want: "1.00"},
		{in: 0.125, want: "0.13"},
		{in: 0.135, want: "0.14"},
		{in: 1.005, want: "1.00"},
		{in: 2.675, want: "2.67"},
		{in: 4.4999, want: "4.50"},
		{in: 4.5, want: "4.50"},
		{in: 21, want: "21.00"},
		{in: 1.5, want: "1.50"},
		{in: -0.001, want: "-0.00"},
		{in: 3.14159, want: "3.14"},
		{in: 0.005, want: "0.01"},
		{in: 12.345, want: "12.35"},
		{in: 1e21, want: "1e+21"},
		{in: 1e-7, want: "0.00"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			got := js.Fixed(tt.in, 2)
			if got != tt.want {
				t.Errorf("Fixed(%v, 2) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}

func TestCompareUTF16SortsAsJavaScriptDoes(t *testing.T) {
	t.Parallel()

	names := []string{"b", "a", "\U0001F600", "", "�", "Z", "é", "aa"}
	slices.SortFunc(names, js.CompareUTF16)

	want := []string{"Z", "a", "aa", "b", "é", "\U0001F600", "", "�"}
	if !slices.Equal(names, want) {
		t.Errorf("sorted = %q, want %q", names, want)
	}
}

func TestQuoteClip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in    string
		units int
		want  string
	}{
		{in: "abc", units: 3, want: `"abc"`},
		{in: "abcd", units: 3, want: `"abc..."`},
		{
			in:    strings.Repeat("a", 39) + "\U0001F600x",
			units: 40,
			want:  `"` + strings.Repeat("a", 39) + `\ud83d..."`,
		},
		{
			in:    strings.Repeat("a", 40) + "b",
			units: 40,
			want:  `"` + strings.Repeat("a", 40) + `..."`,
		},
		{
			in:    strings.Repeat("é", 41),
			units: 40,
			want:  `"` + strings.Repeat("é", 40) + `..."`,
		},
		{in: "\"q\"\n", units: 40, want: `"\"q\"\n"`},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			got := js.QuoteClip(tt.in, tt.units)
			if got != tt.want {
				t.Errorf("QuoteClip = %s, want %s", got, tt.want)
			}
		})
	}
}
