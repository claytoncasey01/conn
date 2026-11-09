package theme_test

import (
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/theme"
)

func TestParseManifestRefusesTextOverTheLimitUnread(t *testing.T) {
	t.Parallel()

	text := `{"id":"big","pad":"` + strings.Repeat("x", theme.MaxManifestBytes) + `"}`

	_, errors := theme.ParseManifest(text, theme.Context{Source: theme.SourceUser})
	if len(errors) != 1 {
		t.Fatalf("errors = %q, want one", errors)
	}

	if errors[0] != "theme.json is larger than 64 KB" {
		t.Errorf("error = %q", errors[0])
	}
}

func TestSameIgnoresKeyOrderButNotValues(t *testing.T) {
	t.Parallel()

	const (
		first = `{"id":"mine","name":"Mine","version":"1","author":"","license":"",` +
			`"description":"","modes":["dark"],"accent":"fixed","fonts":[],` +
			`"tokens":{"dark":{"bg":"#000","tx":"#fff"}},` +
			`"effects":[{"name":"grain","options":{"a":1,"b":true}}]}`
		reordered = `{"effects":[{"options":{"b":true,"a":1},"name":"grain"}],` +
			`"tokens":{"dark":{"tx":"#fff","bg":"#000"}},"id":"mine","name":"Mine",` +
			`"version":"1","author":"","license":"","description":"","modes":["dark"],` +
			`"accent":"fixed","fonts":[]}`
		negativeZero = `{"id":"mine","name":"Mine","version":"1","author":"","license":"",` +
			`"description":"","modes":["dark"],"accent":"fixed","fonts":[],` +
			`"tokens":{"dark":{"bg":"#000","tx":"#fff"}},` +
			`"effects":[{"name":"grain","options":{"a":-0,"b":true}}]}`
		zero = `{"id":"mine","name":"Mine","version":"1","author":"","license":"",` +
			`"description":"","modes":["dark"],"accent":"fixed","fonts":[],` +
			`"tokens":{"dark":{"bg":"#000","tx":"#fff"}},` +
			`"effects":[{"name":"grain","options":{"a":0,"b":true}}]}`
		otherToken = `{"id":"mine","name":"Mine","version":"1","author":"","license":"",` +
			`"description":"","modes":["dark"],"accent":"fixed","fonts":[],` +
			`"tokens":{"dark":{"bg":"#111","tx":"#fff"}},` +
			`"effects":[{"name":"grain","options":{"a":1,"b":true}}]}`
	)

	tests := []struct {
		name        string
		left, right string
		want        bool
	}{
		{name: "the same values in another order", left: first, right: reordered, want: true},
		{name: "a token's value differs", left: first, right: otherToken, want: false},
		{name: "-0 is not 0", left: negativeZero, right: zero, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			left := parse(t, tt.left)
			right := parse(t, tt.right)

			if theme.Same(&left, &right) != tt.want {
				t.Errorf("Same = %v, want %v", !tt.want, tt.want)
			}
		})
	}
}

func parse(t *testing.T, text string) theme.Manifest {
	t.Helper()

	manifest, errors := theme.ParseManifest(text, theme.Context{Source: theme.SourceGallery})
	if len(errors) > 0 {
		t.Fatalf("%q", errors)
	}

	return manifest
}
