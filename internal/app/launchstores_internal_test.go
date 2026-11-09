package app

import (
	"os"
	"path/filepath"
	"testing"
)

// A launch reads a module's store at most maxBytes bytes, and only from a regular file: anything
// else is no store, so no review is resumed from it.
func TestReadStoreText(t *testing.T) {
	t.Parallel()

	const maxBytes = 8

	tests := []struct {
		name     string
		arrange  func(t *testing.T, path string)
		wantText string
		wantRead bool
	}{
		{"a store", func(t *testing.T, path string) {
			t.Helper()
			writeStore(t, path, "{}")
		}, "{}", true},
		{"a store of the most bytes", func(t *testing.T, path string) {
			t.Helper()
			writeStore(t, path, "12345678")
		}, "12345678", true},
		{"no store", func(*testing.T, string) {}, "", false},
		{"a store too large", func(t *testing.T, path string) {
			t.Helper()
			writeStore(t, path, "123456789")
		}, "", false},
		{"a folder at the name", func(t *testing.T, path string) {
			t.Helper()

			err := os.Mkdir(path, 0o700)
			if err != nil {
				t.Fatal(err)
			}
		}, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "reviews.json")
			tt.arrange(t, path)

			text, read := readStoreText(path, maxBytes)
			if read != tt.wantRead {
				t.Fatalf("read %v, want %v", read, tt.wantRead)
			}

			if string(text) != tt.wantText {
				t.Errorf("text %q, want %q", text, tt.wantText)
			}
		})
	}
}

func writeStore(t *testing.T, path, text string) {
	t.Helper()

	err := os.WriteFile(path, []byte(text), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}
