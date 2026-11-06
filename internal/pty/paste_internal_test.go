package pty

import "testing"

func TestPasteOpenAfterFollowsTheNewestWholeMarker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		open    bool
		written string
		want    bool
	}{
		{"nothing, closed", false, "", false},
		{"nothing, open", true, "", true},
		{"keys, closed", false, "abc", false},
		{"keys, open", true, "abc", true},
		{"a start", false, "\x1b[200~abc", true},
		{"a whole paste", false, "\x1b[200~abc\x1b[201~", false},
		{"an end", true, "abc\x1b[201~", false},
		{"an end cut short", true, "abc\x1b[20", true},
		{"a start cut short", false, "\x1b[20", false},
		{"a paste, then another start", false, "\x1b[200~a\x1b[201~\x1b[200~b", true},
		{"an end, then keys", true, "\x1b[201~xyz", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := pasteOpenAfter(tt.open, []byte(tt.written)); got != tt.want {
				t.Fatalf("pasteOpenAfter(%v, %q) = %v, want %v", tt.open, tt.written, got, tt.want)
			}
		})
	}
}
