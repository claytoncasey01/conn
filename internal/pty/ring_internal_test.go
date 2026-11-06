package pty

import (
	"bytes"
	"testing"
)

func TestRingKeepsTheNewestBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		limit  int
		writes []string
		want   string
	}{
		{"empty", 4, nil, ""},
		{"under the limit", 4, []string{"ab"}, "ab"},
		{"exactly the limit", 4, []string{"ab", "cd"}, "abcd"},
		{"one past the limit", 4, []string{"abcd", "e"}, "bcde"},
		{"wrapping more than once", 4, []string{"abc", "def", "ghi"}, "fghi"},
		{"one write larger than the limit", 4, []string{"abcdefg"}, "defg"},
		{"a large write after a wrap", 4, []string{"abcde", "123456"}, "3456"},
		{"an empty write", 4, []string{"ab", "", "c"}, "abc"},
		{"a write of exactly the limit after others", 4, []string{"xy", "1234"}, "1234"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			scrollback := newRing(tt.limit)
			for _, write := range tt.writes {
				scrollback.write([]byte(write))
			}

			got := scrollback.contents()
			if !bytes.Equal(got, []byte(tt.want)) {
				t.Fatalf("contents = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRingContentsIsACopy(t *testing.T) {
	t.Parallel()

	scrollback := newRing(4)
	scrollback.write([]byte("ab"))

	got := scrollback.contents()
	got[0] = 'z'

	if string(scrollback.contents()) != "ab" {
		t.Fatal("changing the contents changed the ring")
	}
}
