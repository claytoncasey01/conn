package pty

import "testing"

func TestPasteClosingAfterFollowsWhatTheProgramRead(t *testing.T) {
	t.Parallel()

	const (
		start = "\x1b[200~"
		end   = "\x1b[201~"
	)

	tests := []struct {
		name      string
		closing   string
		attempted string
		written   int
		want      string
	}{
		{"nothing written, closed", "", "abc", 0, ""},
		{"nothing written, open", end, "abc", 0, end},
		{"keys, closed", "", "abc", 3, ""},
		{"keys, open", end, "abc", 3, end},
		{"a start read whole", "", start + "abc", 9, end},
		{"a whole paste", "", start + "abc" + end, 15, ""},
		{"an end read whole", end, "abc" + end, 9, ""},
		{"cut inside the end marker", "", start + "abc" + end, 12, "01~"},
		{"cut one byte into the end marker", "", start + "abc" + end, 10, "[201~"},
		{"cut inside the start marker", "", "ab" + start + "cd", 4, "200~" + end},
		{"cut before the start marker", "", "ab" + start + "cd", 2, ""},
		{"a paste, then another start", "", start + "a" + end + start + "b", 20, end},
		{"a paste cut in its body", "", start + "abcdef" + end, 9, end},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var closing []byte
			if tt.closing != "" {
				closing = []byte(tt.closing)
			}

			got := pasteClosingAfter(closing, []byte(tt.attempted), tt.written)
			if string(got) != tt.want {
				t.Fatalf("pasteClosingAfter = %q, want %q", got, tt.want)
			}
		})
	}
}
