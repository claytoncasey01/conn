package js_test

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
)

// MarshalShown is an exported struct to embed.
type MarshalShown struct {
	A string `json:"a"`
}

// marshalInner is an unexported struct to embed.
type marshalInner struct {
	A string `json:"a"`
	B int    `json:"b"`
}

type marshalExported struct {
	MarshalShown

	C bool `json:"c"`
}

type marshalUnexported struct {
	marshalInner

	C bool `json:"c"`
}

type marshalNamed struct {
	MarshalShown `json:"shown"`
}

func TestMarshalFlattensAnExportedEmbeddedStruct(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		want  string
	}{
		{
			"exported",
			marshalExported{A: "x", C: true},
			`{"a":"x","c":true}`,
		},
		{
			"unexported is left out",
			marshalUnexported{A: "x", B: 1, C: true},
			`{"c":true}`,
		},
		{
			"a named one nests",
			marshalNamed{A: "x"},
			`{"shown":{"a":"x"}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := string(js.Marshal(test.value)); got != test.want {
				t.Errorf("Marshal = %s, want %s", got, test.want)
			}
		})
	}
}
