// Package ask reads a waiting session's terminal screen: what its prompt asks (a permission, a
// question, the empty input box, or a dialog it cannot read), and whether its input box is
// empty.
package ask

import (
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/claytoncasey01/conn/internal/js"
)

// What a terminal screen shows at the bottom.
const (
	PromptEmpty = "empty"
	PromptDraft = "draft"
	PromptNone  = "none"
)

const (
	ruleMin       = 10
	ruleChar      = '─'
	dottedChar    = '╌'
	tryPrefix     = `Try "`
	tryMinLen     = len(tryPrefix) + 1
	inputChar     = '❯'
	inputCharText = ">"
)

// PromptState is what a screen's bottom shows: Claude's input box, empty or with a draft, or
// neither. Claude draws its input as a ❯ line directly under a horizontal rule. Many shells also
// use ❯, but their prompt never sits under such a rule, so a bare ❯ is not enough.
func PromptState(screen string) string {
	text, found := PromptText(screen)
	if !found {
		return PromptNone
	}

	if text == "" {
		return PromptEmpty
	}

	// A fresh session shows a greyed placeholder, `Try "how do I log an error?"`, which reads
	// as text on a plain screen. Other ghost text cannot be told from a draft and is refused.
	if placeholder(text) {
		return PromptEmpty
	}

	return PromptDraft
}

// PromptText is the text on the first line of Claude's input box at the bottom of a screen,
// trimmed, and false when the screen shows no input box (see PromptState).
func PromptText(screen string) (string, bool) {
	lines := strings.Split(screen, "\n")
	input := -1

	for index, line := range slices.Backward(lines) {
		if inputLine(line) {
			input = index

			break
		}
	}

	if input < 1 {
		return "", false
	}

	if !screenRule(lines[input-1]) {
		return "", false
	}

	return js.Trim(afterInput(lines[input])), true
}

// OwnsTerminal is whether `ps -o stat=` says a process is in the foreground of its terminal (+)
// and not stopped (T): only then does it own the keyboard.
func OwnsTerminal(stat string) bool {
	if !strings.Contains(stat, "+") {
		return false
	}

	return !strings.Contains(stat, "T")
}

// trimSpace is text without its leading JavaScript whitespace (`^\s*`).
func trimSpace(text string) string { return strings.TrimLeftFunc(text, js.IsSpace) }

// inputLine is /^\s*[❯>]/.
func inputLine(line string) bool {
	rest := trimSpace(line)
	if strings.HasPrefix(rest, inputCharText) {
		return true
	}

	first, _ := utf8.DecodeRuneInString(rest)

	return first == inputChar
}

// afterInput is line.replace(/^\s*[❯>]\s?/, ”): the line after its input mark and one space.
func afterInput(line string) string {
	rest := trimSpace(line)

	first, size := utf8.DecodeRuneInString(rest)
	if first != inputChar {
		if !strings.HasPrefix(rest, inputCharText) {
			return line
		}
	}

	return oneSpace(rest[size:])
}

// oneSpace drops one leading JavaScript whitespace character (`\s?`).
func oneSpace(text string) string {
	first, size := utf8.DecodeRuneInString(text)
	if size == 0 {
		return text
	}

	if js.IsSpace(first) {
		return text[size:]
	}

	return text
}

// screenRule is screen.ts's /^\s*─{10,}/: a rule, indented or not, with anything after it.
func screenRule(line string) bool { return leadingRun(trimSpace(line), ruleChar) >= ruleMin }

// leadingRun is how many times char starts text.
func leadingRun(text string, char rune) int {
	count := 0

	for _, current := range text {
		if current != char {
			break
		}

		count++
	}

	return count
}

// placeholder is /^Try ".*"$/: the fresh session's greyed suggestion.
func placeholder(text string) bool {
	if len(text) < tryMinLen {
		return false
	}

	if !strings.HasPrefix(text, tryPrefix) {
		return false
	}

	if !strings.HasSuffix(text, `"`) {
		return false
	}

	return !strings.ContainsFunc(text, lineTerminator)
}

// lineTerminator is what JavaScript's . does not match.
func lineTerminator(char rune) bool {
	switch char {
	case '\n', '\r', ' ', ' ':
		return true
	}

	return false
}

// unitIndex is the UTF-16 index of the first character that matches, or -1, as search does.
func unitIndex(text string, match func(rune) bool) int {
	units := 0

	for _, char := range text {
		if match(char) {
			return units
		}

		units += utf16.RuneLen(char)
	}

	return -1
}

// ScreenText is a screen's lines as the one text Parse, Unread and PromptState read, as
// `cmux read-screen` gives it: the lines joined by newlines.
func ScreenText(lines []string) string { return strings.Join(lines, "\n") }
