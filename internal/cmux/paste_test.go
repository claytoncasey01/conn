package cmux_test

import (
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/cmux"
)

// TestNoLineToPasteHoldsAControlCharacter: when cmux fails for a folder or a command holding a
// control character, there is no line to paste: a line editor would act on it before any quote
// is read.
func TestNoLineToPasteHoldsAControlCharacter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, cwd, command string
	}{
		{"Ctrl-U in the folder", "/tmp/a\x15touch pwned", "claude"},
		{"a newline in the folder", "/tmp/a\nb", "claude"},
		{"an escape in the command", "/tmp", "claude \x1b[A"},
		{"a bidi control in the folder", "/tmp/a\u202eb", "claude"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			failed := cmux.Open("/usr/bin/false")(t.Context(), "n", tt.cwd, tt.command)
			if failed.OK {
				t.Fatal("false succeeded")
			}

			if failed.ManualCommand != "" {
				t.Errorf("a line to paste: %q", failed.ManualCommand)
			}

			if !strings.Contains(failed.Error, "control character") {
				t.Errorf("error %q", failed.Error)
			}
		})
	}
}
