package desk_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/claytoncasey01/conn/internal/desk"
)

// shellTimedOut stands in for the reason shellenv.Load gives when the login shell is too slow.
const shellTimedOut = "login shell /bin/zsh: timed out after 10s"

func TestSplashText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		reason string
		want   string
	}{
		{
			"an environment read without trouble says only that Conn is opening",
			"",
			"Opening Conn…",
		},
		{
			"an environment that could not be read says why on a line of its own",
			shellTimedOut,
			"Opening Conn…\n" +
				"Conn could not read your shell's environment (" + shellTimedOut +
				"), so it is using the app's own: claude, gh and git may not be found.",
		},
		{
			"only the first line of a reason is shown, never what follows it",
			"Command failed: /bin/zsh -l -i -c\n+ export PROFILE_VALUE=do-not-show-this\n",
			"Opening Conn…\n" +
				"Conn could not read your shell's environment (Command failed: /bin/zsh -l -i -c" +
				"), so it is using the app's own: claude, gh and git may not be found.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := desk.SplashText(tt.reason)
			if got != tt.want {
				t.Errorf("SplashText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNoticeCapsALongReason(t *testing.T) {
	t.Parallel()

	reason := strings.Repeat("é", 10*desk.MaxReasonRunes)

	got := desk.Notice(reason)

	want := "Conn could not read your shell's environment (" +
		strings.Repeat("é", desk.MaxReasonRunes) + "…" +
		"), so it is using the app's own: claude, gh and git may not be found."
	if got != want {
		t.Errorf("Notice of %d runes = %q, want the reason cut to %d runes and …",
			utf8.RuneCountInString(reason), got, desk.MaxReasonRunes)
	}
}
