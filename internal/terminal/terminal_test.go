package terminal_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/claytoncasey01/conn/internal/terminal"
)

// TestIs: a pipe and a file are not a terminal, so the secret is not printed to them.
func TestIs(t *testing.T) {
	t.Parallel()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})

	file, err := os.Create(filepath.Join(t.TempDir(), "out.log"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = file.Close() })

	tests := []struct {
		name string
		file *os.File
	}{
		{"a pipe", writer},
		{"a file", file},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if terminal.Is(tt.file) {
				t.Errorf("%s is taken for a terminal", tt.name)
			}
		})
	}
}
