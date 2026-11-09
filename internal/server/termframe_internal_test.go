package server

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseClientFrameTakesTheTwoShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want clientFrame
	}{
		{
			"input", `{"t":"input","d":"ls\r"}`,
			clientFrame{kind: frameInput, data: []byte("ls\r"), columns: 0, rows: 0},
		},
		{
			"input in another order, with spaces", ` { "d" : "x" , "t" : "input" } `,
			clientFrame{kind: frameInput, data: []byte("x"), columns: 0, rows: 0},
		},
		{
			"input with an escape and a multibyte letter", `{"t":"input","d":"\u001b[Aé"}`,
			clientFrame{kind: frameInput, data: []byte("\x1b[Aé"), columns: 0, rows: 0},
		},
		{
			"resize", `{"t":"resize","cols":132,"rows":43}`,
			clientFrame{kind: frameResize, data: nil, columns: 132, rows: 43},
		},
		// The host, not the parser, bounds a size: zero is still a size to refuse there.
		{
			"resize to zero", `{"t":"resize","cols":0,"rows":0}`,
			clientFrame{kind: frameResize, data: nil, columns: 0, rows: 0},
		},
		{
			"input of the most bytes",
			`{"t":"input","d":"` + strings.Repeat("a", maxInputFrameBytes) + `"}`,
			clientFrame{
				kind: frameInput, data: bytes.Repeat([]byte("a"), maxInputFrameBytes),
				columns: 0, rows: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseClientFrame([]byte(tt.text))
			if err != nil {
				t.Fatalf("parseClientFrame = %v", err)
			}

			if got.kind != tt.want.kind {
				t.Fatalf("kind %d, want %d", got.kind, tt.want.kind)
			}

			if !bytes.Equal(got.data, tt.want.data) {
				t.Fatalf("data of %d bytes, want %d", len(got.data), len(tt.want.data))
			}

			if got.columns != tt.want.columns {
				t.Fatalf("columns %d, want %d", got.columns, tt.want.columns)
			}

			if got.rows != tt.want.rows {
				t.Fatalf("rows %d, want %d", got.rows, tt.want.rows)
			}
		})
	}
}

// Anything but the two shapes, exactly, is refused: the parser reads a client's text, so it
// takes no member it does not know, no name spelled another way, no name twice, no value of the
// wrong type and nothing after the object.
func TestParseClientFrameRefusesEverythingElse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
	}{
		{"empty", ``},
		{"not JSON", `input ls`},
		{"null", `null`},
		{"an array", `[{"t":"input","d":"x"}]`},
		{"a string", `"input"`},
		{"an empty object", `{}`},
		{"no type", `{"d":"x"}`},
		{"an unknown type", `{"t":"exec","d":"/bin/sh"}`},
		{"a type in capitals", `{"t":"INPUT","d":"x"}`},
		{"a type that is not a string", `{"t":1,"d":"x"}`},
		{"a null type", `{"t":null,"d":"x"}`},
		{"a name in capitals", `{"T":"input","d":"x"}`},
		{"a data name in capitals", `{"t":"input","D":"x"}`},
		{"the type twice", `{"t":"resize","t":"input","d":"x"}`},
		{"the data twice", `{"t":"input","d":"x","d":"y"}`},
		{"an unknown member", `{"t":"input","d":"x","cmd":"rm"}`},
		{"input without data", `{"t":"input"}`},
		{"input with null data", `{"t":"input","d":null}`},
		{"input with empty data", `{"t":"input","d":""}`},
		{"input with data that is not a string", `{"t":"input","d":[1,2]}`},
		{"input with a size", `{"t":"input","d":"x","cols":80}`},
		{"input with invalid UTF-8", "{\"t\":\"input\",\"d\":\"\xff\"}"},
		{
			"input of one byte too many",
			`{"t":"input","d":"` + strings.Repeat("a", maxInputFrameBytes+1) + `"}`,
		},
		{"resize without rows", `{"t":"resize","cols":80}`},
		{"resize without columns", `{"t":"resize","rows":24}`},
		{"resize with data", `{"t":"resize","cols":80,"rows":24,"d":"x"}`},
		{"resize with a negative size", `{"t":"resize","cols":-1,"rows":24}`},
		{"resize with a fraction", `{"t":"resize","cols":80.5,"rows":24}`},
		{"resize with a size as text", `{"t":"resize","cols":"80","rows":24}`},
		{"resize past 16 bits", `{"t":"resize","cols":65536,"rows":24}`},
		{"resize with a null size", `{"t":"resize","cols":null,"rows":24}`},
		{"something after the object", `{"t":"input","d":"x"}{"t":"input","d":"y"}`},
		{"a comma after the object", `{"t":"input","d":"x"},`},
		{"a trailing comma inside", `{"t":"input","d":"x",}`},
		{"too long to read", `{"t":"input","d":"` + strings.Repeat("a", maxFrameBytes) + `"}`},
		{"a good frame padded past the read limit", `{"t":"input","d":"x"}` +
			strings.Repeat(" ", maxFrameBytes)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseClientFrame([]byte(tt.text))
			if !errors.Is(err, errMalformedFrame) {
				t.Fatalf("parseClientFrame = %v, want errMalformedFrame", err)
			}
		})
	}
}

// An error says what was wrong in the server's own words, never quoting the client's text: a
// frame holds keystrokes.
func TestParseClientFrameNeverQuotesTheText(t *testing.T) {
	t.Parallel()

	const marker = "hunter2-keystrokes"

	texts := []string{
		`{"t":"` + marker + `"}`,
		`{"t":"input","d":"x","` + marker + `":1}`,
		`{"t":"input","d":1,"x":"` + marker + `"}`,
		`{"t":"resize","cols":"` + marker + `","rows":1}`,
		marker,
	}

	for _, text := range texts {
		_, err := parseClientFrame([]byte(text))
		if err == nil {
			t.Fatal("a malformed frame was taken")
		}

		if strings.Contains(err.Error(), marker) {
			t.Fatal("the error quotes the frame")
		}
	}
}

func FuzzFrame(f *testing.F) {
	seeds := []string{
		`{"t":"input","d":"ls\r"}`,
		`{"t":"resize","cols":132,"rows":43}`,
		`{"t":"input","d":"\u001b[200~paste\u001b[201~"}`,
		`{"t":"input","d":"\ud800"}`,
		`{"t":"resize","cols":1e2,"rows":24}`,
		`{"t":"input","d":"x","d":"y"}`,
		`{"T":"input","d":"x"}`,
		`{"t":"exit","code":0}`,
		`{"t":"replay-end"}`,
		`[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[`,
		`{"t":"input","d":"` + strings.Repeat(`\u0000`, 100) + `"}`,
		"{\"t\":\"input\",\"d\":\"\xc3\x28\"}",
		``,
	}
	for _, seed := range seeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, text []byte) {
		got, err := parseClientFrame(text)
		if err != nil {
			if !errors.Is(err, errMalformedFrame) {
				t.Fatalf("an error that is not errMalformedFrame: %v", err)
			}

			return
		}

		if len(text) > maxFrameBytes {
			t.Fatalf("took a frame of %d bytes, more than %d", len(text), maxFrameBytes)
		}

		checkParsedFrame(t, got)
	})
}

// checkParsedFrame fails t if a frame the parser took is not one of the two shapes it promises.
func checkParsedFrame(t *testing.T, got clientFrame) {
	t.Helper()

	switch got.kind {
	case frameInput:
		if len(got.data) == 0 {
			t.Fatal("took input with no data")
		}

		if len(got.data) > maxInputFrameBytes {
			t.Fatalf("took %d bytes of input, more than %d", len(got.data), maxInputFrameBytes)
		}

		if !utf8.Valid(got.data) {
			t.Fatal("took input that is not UTF-8")
		}

		if got.columns != 0 {
			t.Fatal("input carries a size")
		}

		if got.rows != 0 {
			t.Fatal("input carries a size")
		}
	case frameResize:
		if got.data != nil {
			t.Fatal("a resize carries data")
		}
	default:
		t.Fatalf("took a frame of kind %d", got.kind)
	}
}
