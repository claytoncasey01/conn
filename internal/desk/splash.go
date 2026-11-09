package desk

import (
	"strings"
	"unicode/utf8"
)

const (
	// splashOpening is the splash's first line, there on every start.
	splashOpening = "Opening Conn…"

	// MaxReasonRunes caps the reason a notice gives: a short reason fits well inside it, and
	// anything longer is not a reason but output that has no place on screen.
	MaxReasonRunes = 160

	// reasonCut ends a reason that was cut at MaxReasonRunes.
	reasonCut = "…"
)

// Notice is what the app says when the login shell's environment could not be read: why, and
// what that means for the person. reason is a short reason (shellenv's errors are): only its
// first line is used, at most MaxReasonRunes runes of it, so text a shell printed after a reason
// never reaches the screen. The text is plain: what shows it escapes it.
func Notice(reason string) string {
	return "Conn could not read your shell's environment (" + shortReason(reason) +
		"), so it is using the app's own: claude, gh and git may not be found."
}

// SplashText is what the window shows while Conn starts: that it is opening and, when reason is
// not empty, the Notice on a line of its own. Lines are separated by "\n". The text is plain: the
// page that shows it escapes it.
func SplashText(reason string) string {
	if reason == "" {
		return splashOpening
	}

	return splashOpening + "\n" + Notice(reason)
}

// shortReason is the first line of reason, cut to MaxReasonRunes runes.
func shortReason(reason string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(reason), "\n")
	line = strings.TrimSpace(line)

	if utf8.RuneCountInString(line) <= MaxReasonRunes {
		return line
	}

	return string([]rune(line)[:MaxReasonRunes]) + reasonCut
}
