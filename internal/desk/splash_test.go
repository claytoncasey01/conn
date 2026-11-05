package desk_test

import (
	"errors"
	"testing"

	"github.com/claytoncasey01/conn/internal/desk"
)

// errShellTimedOut stands in for the error shellenv.Load gives when the login shell is too slow.
var errShellTimedOut = errors.New("running the login shell \"/bin/zsh\": timed out")

func TestSplashText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		loadErr error
		want    string
	}{
		{
			"an environment read without trouble says only that Conn is opening",
			nil,
			"Opening Conn…",
		},
		{
			"an environment that could not be read says so on a line of its own",
			errShellTimedOut,
			"Opening Conn…\n" +
				"could not read your shell's environment: " +
				"running the login shell \"/bin/zsh\": timed out; using the app's own",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := desk.SplashText(tt.loadErr)
			if got != tt.want {
				t.Errorf("SplashText() = %q, want %q", got, tt.want)
			}
		})
	}
}
