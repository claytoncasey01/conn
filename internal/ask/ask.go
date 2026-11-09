package ask

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/claytoncasey01/conn/internal/js"
)

// What the defence against a command that draws a fake frame relies on about the terminal
// renderer:
//
// VERIFIED (captured from real sessions):
//   - every row of a multi-line command is drawn with the ` │ ` prefix, even rows that look like
//     rules, dotted rules or `Do you want to proceed?`;
//   - the frame's rules and dotted rules start at column 0 (title, description, question and
//     options at one space);
//   - the real permission dialog ends with its footer followed by one full-width rule (and blank
//     rows); in a named session that border carries the name near its right end;
//   - in the transcript view a wrapped, over-long command row continues indented, not at
//     column 0.
//
// ASSUMED (not captured from a permission dialog):
//   - the question picker's footer is followed by the same one bottom rule;
//   - wrapped rows inside a permission dialog's command are also indented, not at column 0;
//   - a \r or an escape sequence in a command cannot put a rule at column 0;
//   - an Edit or Write permission draws its content inside its own frame (such a screen is
//     unknown here, since it has no dotted pair);
//   - a digit picks the numbered option whatever row the picker's cursor is on. The id ignores
//     the ❯ by design; only the numbered options are ever offered, never `Type something.`.
//
// The caller should read at least 80 lines of screen, so the real top rule stays in view for a
// long command; a screen whose top rule has scrolled off is unknown.

// Ask kinds.
const (
	KindQuestion   = "question"
	KindPermission = "permission"
	KindText       = "text"
	KindUnknown    = "unknown"
)

// Option roles.
const (
	RolePick  = "pick"
	RoleAllow = "allow"
	RoleDeny  = "deny"
)

const (
	maxLines     = 20
	maxChars     = 1500
	idHexChars   = 16
	maxBorder    = 80
	footerText   = "Esc to cancel"
	wantText     = "Do you want"
	commandBar   = " │ "
	commandEmpty = " │"
	ellipsisRows = 2
	pickerTail   = 2
	minPicker    = 3
)

// Option is one choice: Key is the digit printed beside it, the key that picks it.
type Option struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Role  string `json:"role"`
}

// Ask is what a waiting session's screen asks. ID names this exact prompt (a click is honoured
// only while the screen still shows it); Since is when the store first saw it, 0 from the parser.
type Ask struct {
	Kind    string   `json:"kind"`
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Text    string   `json:"text"`
	Options []Option `json:"options"`
	Since   float64  `json:"since"`
}

// squash is the text with each run of whitespace one space, trimmed.
func squash(text string) string { return js.Trim(js.CollapseSpace(text)) }

// ID names one prompt. Every wrapped or padded variant of the same screen squashes to the same
// words, so it hashes alike; a different command, question, option or stamp does not. The ❯ is
// never part of the input, so moving it does not change the id.
func ID(kind, title, text string, options []Option, statusStamp float64) string {
	rows := make([]any, 0, len(options))
	for _, option := range options {
		rows = append(rows, []any{option.Key, option.Role, squash(option.Label)})
	}

	content := js.Stringify([]any{kind, squash(title), squash(text), rows, statusStamp})
	sum := sha256.Sum256([]byte(content))

	return hex.EncodeToString(sum[:])[:idHexChars]
}

func build(kind, title, text string, options []Option, statusStamp float64) *Ask {
	return &Ask{
		Kind: kind, ID: ID(kind, title, text, options, statusStamp), Title: title, Text: text,
		Options: options, Since: 0,
	}
}

// isRule is /^─{10,}\s*$/: a whole-line rule from column 0.
func isRule(line string) bool { return wholeRun(line, ruleChar) }

// isDotted is /^╌{10,}\s*$/.
func isDotted(line string) bool { return wholeRun(line, dottedChar) }

func wholeRun(line string, char rune) bool {
	count := leadingRun(line, char)
	if count < minRule {
		return false
	}

	return js.Trim(line[count*utf8.RuneLen(char):]) == ""
}

// isBorder is /^─{10,}(?: [^─\r\n]{1,80} ─+)?\s*$/: the rule under a footer, which may carry
// the session's name near its right end.
func isBorder(line string) bool {
	count := leadingRun(line, ruleChar)
	if count < minRule {
		return false
	}

	rest := line[count*utf8.RuneLen(ruleChar):]
	if js.Trim(rest) == "" {
		return true
	}

	label, labelled := strings.CutPrefix(strings.TrimRightFunc(rest, js.IsSpace), " ")
	if !labelled {
		return false
	}

	trailing := strings.TrimRight(label, string(ruleChar))
	if trailing == label {
		return false
	}

	label, spaced := strings.CutSuffix(trailing, " ")
	if !spaced {
		return false
	}

	units := len(utf16.Encode([]rune(label)))
	if units < 1 {
		return false
	}

	if units > maxBorder {
		return false
	}

	return !strings.ContainsAny(label, string(ruleChar)+"\r\n")
}

// isHeader is /^\s*(←\s*)?[☐☒]\s/: a question's tab line.
func isHeader(line string) bool {
	rest := trimSpace(line)
	if after, found := strings.CutPrefix(rest, "←"); found {
		rest = trimSpace(after)
	}

	first, size := utf8.DecodeRuneInString(rest)
	if first != '☐' {
		if first != '☒' {
			return false
		}
	}

	next, _ := utf8.DecodeRuneInString(rest[size:])

	return js.IsSpace(next)
}

// isWant is /^ ?Do you want\b/.
func isWant(line string) bool {
	rest := strings.TrimPrefix(line, " ")
	if !strings.HasPrefix(rest, wantText) {
		return false
	}

	next := rest[len(wantText):]
	if next == "" {
		return true
	}

	return !wordByte(next[0])
}

func wordByte(char byte) bool {
	if char == '_' {
		return true
	}

	if char >= 'a' {
		return char <= 'z'
	}

	if char >= 'A' {
		return char <= 'Z'
	}

	if char >= '0' {
		return char <= '9'
	}

	return false
}

// option reads /^\s*(❯\s*)?(\d+)\.\s/: an option's key and the line after the match.
func option(line string) (string, string, bool) {
	rest := trimSpace(line)
	if after, found := strings.CutPrefix(rest, string(inputChar)); found {
		rest = trimSpace(after)
	}

	digits := 0
	for digits < len(rest) {
		if rest[digits] < '0' {
			break
		}

		if rest[digits] > '9' {
			break
		}

		digits++
	}

	if digits == 0 {
		return "", "", false
	}

	key := rest[:digits]

	after, dotted := strings.CutPrefix(rest[digits:], ".")
	if !dotted {
		return "", "", false
	}

	space, size := utf8.DecodeRuneInString(after)
	if !js.IsSpace(space) {
		return "", "", false
	}

	return key, after[size:], true
}

func isOption(line string) bool {
	_, _, found := option(line)

	return found
}

func isFooter(line string) bool { return strings.Contains(line, footerText) }

// parsed is one option as read: its key, its lines, and whether a full-width rule sits right
// above its row.
type parsed struct {
	key   string
	lines []string
	ruled bool
}

// optionBlock is ONE contiguous block of numbered options starting at start: numbered rows plus
// the indented lines that continue them. It ends at the first line that is neither. With
// allowRule a single full-width rule may sit between two rows (the picker draws one above
// `Chat about this`). end is the first line after the block.
func optionBlock(lines []string, start int, allowRule bool) ([]parsed, int, bool) {
	if !isOption(at(lines, start)) {
		return nil, 0, false
	}

	options := []parsed{}
	digitAt := -1
	ruled := false
	justRuled := false
	index := start

	for ; index < len(lines); index++ {
		line := lines[index]

		key, rest, isRow := option(line)
		if isRow {
			digitAt = unitIndex(line, isASCIIDigit)

			options = append(options,
				parsed{key: key, lines: []string{js.Trim(rest)}, ruled: justRuled})
			justRuled = false

			continue
		}

		if continues(line, digitAt) {
			last := &options[len(options)-1]
			last.lines = append(last.lines, js.Trim(line))

			continue
		}

		if !allowRule {
			break
		}

		if ruled {
			break
		}

		if !isRule(line) {
			break
		}

		if !isOption(at(lines, index+1)) {
			break
		}

		ruled = true
		justRuled = true
	}

	return options, index, true
}

// continues is whether a line carries on the option above: text, not a rule, indented past the
// option's digit.
func continues(line string, digitAt int) bool {
	if js.Trim(line) == "" {
		return false
	}

	if isRule(line) {
		return false
	}

	if isDotted(line) {
		return false
	}

	return unitIndex(line, notSpace) > digitAt
}

func isASCIIDigit(char rune) bool {
	if char < '0' {
		return false
	}

	return char <= '9'
}

func notSpace(char rune) bool { return !js.IsSpace(char) }

// at is lines[index], or "" past either end.
func at(lines []string, index int) string {
	if index < 0 {
		return ""
	}

	if index >= len(lines) {
		return ""
	}

	return lines[index]
}

// consecutive is whether the keys run 1, 2, 3 ... with no gap and no repeat.
func consecutive(options []parsed) bool {
	for index, option := range options {
		number, err := strconv.ParseFloat(option.key, 64)
		if err != nil {
			return false
		}

		if number != float64(index+1) {
			return false
		}
	}

	return true
}

// onlyFooter is whether, after the options, only the dialog footer follows (blank lines aside).
func onlyFooter(lines []string, end int) bool {
	rest := []string{}

	for _, line := range lines[min(end, len(lines)):] {
		if js.Trim(line) != "" {
			rest = append(rest, line)
		}
	}

	if len(rest) != 1 {
		return false
	}

	return isFooter(rest[0])
}

func lastRuleBefore(lines []string, end int) int {
	for index := end - 1; index >= 0; index-- {
		if isRule(lines[index]) {
			return index
		}
	}

	return -1
}

// gutter is l.replace(/^ /, ”).trimEnd().
func gutter(line string) string {
	return strings.TrimRightFunc(strings.TrimPrefix(line, " "), js.IsSpace)
}

func trimBlank(lines []string) []string {
	first, end := 0, len(lines)

	for first < end {
		if js.Trim(lines[first]) != "" {
			break
		}

		first++
	}

	for end > first {
		if js.Trim(lines[end-1]) != "" {
			break
		}

		end--
	}

	return lines[first:end]
}

// withoutCursor is l.replace(/^(\s*)❯\s*/, '$1'): the ❯ and the spaces after it dropped, the
// indentation kept.
func withoutCursor(line string) string {
	rest := trimSpace(line)

	after, found := strings.CutPrefix(rest, string(inputChar))
	if !found {
		return line
	}

	return line[:len(line)-len(rest)] + trimSpace(after)
}

// shown is the lines as the page shows an unread dialog: no cursor, no gutter, no blank edges,
// at most 20 lines and 1500 UTF-16 units.
func shown(lines []string) string {
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		cleaned = append(cleaned, gutter(withoutCursor(line)))
	}

	kept := trimBlank(cleaned)

	return js.Cut(strings.Join(kept[:min(len(kept), maxLines)], "\n"), maxChars)
}

// unknownText is what a dialog this does not read says: the area under the rule above its anchor
// (from), or without one the area under the last full-width rule, never the whole screen.
func unknownText(lines []string, from int) string {
	last := from
	if last < 0 {
		for index, line := range lines {
			if isRule(line) {
				last = index
			}
		}
	}

	if last >= 0 {
		return shown(lines[last+1:])
	}

	return shown(lines[max(0, len(lines)-maxLines):])
}

func unknown(lines []string, statusStamp float64, from int) *Ask {
	return build(KindUnknown, "", unknownText(lines, from), []Option{}, statusStamp)
}

// Unread is what a waiting session's screen says when it is not a dialog this package reads
// (Parse gave nothing): the bottom of the screen as plain text, with no options, so the page can
// show it and offer the terminal rather than say nothing.
func Unread(screen string, statusStamp float64) *Ask {
	lines := strings.Split(screen, "\n")
	lines = dropBorder(trimTail(lines))

	found := unknown(lines, statusStamp, -1)
	if found.Text != "" {
		return found
	}

	// Nothing under the last rule: show the bottom lines instead, never an empty text.
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		cleaned = append(cleaned, gutter(withoutCursor(line)))
	}

	bottom := trimBlank(cleaned[max(0, len(cleaned)-maxLines):])

	return build(KindUnknown, "", js.Cut(strings.Join(bottom, "\n"), maxChars), []Option{},
		statusStamp)
}

func trimTail(lines []string) []string {
	for len(lines) > 0 {
		if js.Trim(lines[len(lines)-1]) != "" {
			break
		}

		lines = lines[:len(lines)-1]
	}

	return lines
}

// dropBorder drops the bottom border that directly follows a footer line (blank lines may sit
// around it). Anything else is left.
func dropBorder(lines []string) []string {
	if len(lines) <= 1 {
		return lines
	}

	if !isBorder(lines[len(lines)-1]) {
		return lines
	}

	rest := trimTail(lines[:len(lines)-1])
	if len(rest) == 0 {
		return lines
	}

	if !isFooter(rest[len(rest)-1]) {
		return lines
	}

	return rest
}

// commandLine is a command's own line: exactly the 3-character ` │ ` prefix of a multi-line
// command dropped, or the one-space gutter of a single line; its own indentation stays.
func commandLine(line string) string {
	switch {
	case strings.HasPrefix(line, commandBar):
		line = line[len(commandBar):]
	case line == commandEmpty:
		line = ""
	default:
		line = strings.TrimPrefix(line, " ")
	}

	return strings.TrimRightFunc(line, js.IsSpace)
}

func commandLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, commandLine(line))
	}

	return trimBlank(out)
}
