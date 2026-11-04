package ask

import (
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
)

// Parse reads what a session's screen is asking (`cmux read-screen --lines 80`). A dialog must
// be the bottom frame (its `Esc to cancel` footer is the last line). A permission dialog and a
// question picker are recognized by their frame (`Do you want ...?`, or a ☐ tab under a rule)
// followed by numbered options; one that does not read as either, or where both read, is
// unknown. Otherwise the empty input box is a free text prompt, and anything else (a draft, a
// shell prompt, plain output) is not an ask (nil).
func Parse(screen string, statusStamp float64) *Ask {
	// A real dialog's footer is followed by one full-width rule, with the session's name in it
	// when it has one. It is dropped when it directly follows a footer line.
	lines := dropBorder(trimTail(strings.Split(screen, "\n")))

	// A dialog replaces the input box, so it is the bottom frame: its footer is the last line.
	if len(lines) > 0 {
		if isFooter(lines[len(lines)-1]) {
			found := dialog(lines, statusStamp)
			if found != nil {
				return found
			}
		}
	}

	if PromptState(screen) == PromptEmpty {
		return build(KindText, "", "", []Option{}, statusStamp)
	}

	return nil
}

// dialog reads the frame above a footer. One that parses cleanly wins; a frame that both ways
// of reading accept is not trusted.
func dialog(lines []string, statusStamp float64) *Ask {
	header, want := anchors(lines)

	var picked, permitted *Ask

	if header >= 0 {
		picked = question(lines, header, statusStamp)
	}

	if want >= 0 {
		permitted = permission(lines, want, statusStamp)
	}

	if clean(picked) != clean(permitted) {
		if clean(picked) {
			return picked
		}

		return permitted
	}

	if picked == nil {
		return permitted
	}

	if permitted == nil {
		return picked
	}

	return unknown(lines, statusStamp, max(header-1, lastRuleBefore(lines, want)))
}

// anchors are the last question tab line under a rule and the last `Do you want` line, -1 for
// none.
func anchors(lines []string) (int, int) {
	header, want := -1, -1

	for index, line := range lines {
		if tabUnderRule(lines, index) {
			header = index

			continue
		}

		if isWant(line) {
			want = index
		}
	}

	return header, want
}

func tabUnderRule(lines []string, index int) bool {
	if !isHeader(lines[index]) {
		return false
	}

	if index == 0 {
		return false
	}

	return isRule(lines[index-1])
}

func clean(found *Ask) bool {
	if found == nil {
		return false
	}

	return found.Kind != KindUnknown
}

// permission is `Do you want to ...?` over numbered options whose choices are a plain Yes and a
// No. The frame must match exactly: a full-width rule on top (under a blank line), the title, one
// dotted pair around the command, any note, the question, the options and the footer. Every body
// line starts with the gutter space, and no dotted rule sits above the top rule.
func permission(lines []string, want int, statusStamp float64) *Ask {
	top := lastRuleBefore(lines, want)
	bad := func() *Ask { return unknown(lines, statusStamp, top) }

	yes, deny, framed := yesNo(lines, want, top)
	if !framed {
		return bad()
	}

	// The real top rule has a blank line (or the screen's edge) above it, and no command rule
	// sits above it.
	if top > 0 {
		if js.Trim(lines[top-1]) != "" {
			return bad()
		}
	}

	if slices.ContainsFunc(lines[:top], isDotted) {
		return bad()
	}

	title, text, read := permissionBody(lines[top+1 : want])
	if !read {
		return bad()
	}

	options := []Option{
		{Key: yes.Key, Label: yes.Label, Role: RoleAllow},
		{Key: deny.Key, Label: deny.Label, Role: RoleDeny},
	}

	return build(KindPermission, title, text, options, statusStamp)
}

// yesNo is the one Yes and the one No of a permission's options.
func yesNo(lines []string, want, top int) (Option, Option, bool) {
	options, end, found := optionBlock(lines, want+1, false)
	if !found {
		return Option{}, Option{}, false
	}

	if !consecutive(options) {
		return Option{}, Option{}, false
	}

	if !onlyFooter(lines, end) {
		return Option{}, Option{}, false
	}

	if top < 0 {
		return Option{}, Option{}, false
	}

	var yes, deny []Option

	for _, option := range options {
		label := strings.Join(option.lines, " ")
		if label == "Yes" {
			yes = append(yes, Option{Key: option.key, Label: label, Role: ""})
		}

		if startsWord(label, "No") {
			deny = append(deny, Option{Key: option.key, Label: label, Role: ""})
		}
	}

	if len(yes) != 1 {
		return Option{}, Option{}, false
	}

	if len(deny) != 1 {
		return Option{}, Option{}, false
	}

	return yes[0], deny[0], true
}

// startsWord is /^word\b/.
func startsWord(text, word string) bool {
	rest, found := strings.CutPrefix(text, word)
	if !found {
		return false
	}

	if rest == "" {
		return true
	}

	return !wordByte(rest[0])
}

// permissionBody reads the frame between the top rule and the question: the title, the command
// between the dotted pair, and any note after it.
func permissionBody(body []string) (string, string, bool) {
	dots, first, gutters := frameRows(body)
	if !gutters {
		return "", "", false
	}

	// The title is a line of its own above the opening dotted rule: a frame that opens on the
	// dotted rule has none.
	if len(dots) != ellipsisRows {
		return "", "", false
	}

	if first < 0 {
		return "", "", false
	}

	if first >= dots[0] {
		return "", "", false
	}

	command := commandLines(body[dots[0]+1 : dots[1]])
	if len(command) == 0 {
		return "", "", false
	}

	note := commandLines(body[dots[1]+1:])
	text := command

	if len(note) > 0 {
		text = append(append(slices.Clone(command), ""), note...)
	}

	return js.Trim(body[first]), strings.Join(text, "\n"), true
}

// frameRows are the dotted rules and the first text row of a permission's body, and false when
// a text row lacks the gutter space.
func frameRows(body []string) ([]int, int, bool) {
	dots := []int{}
	first := -1

	for index, line := range body {
		if js.Trim(line) == "" {
			continue
		}

		if isDotted(line) {
			dots = append(dots, index)
		} else if !strings.HasPrefix(line, " ") {
			return nil, -1, false
		}

		if first < 0 {
			first = index
		}
	}

	return dots, first, true
}

// question is an AskUserQuestion picker with one single-select question: its last two rows are
// `Type something.` and `Chat about this`. The header line holds the one tab and is followed by a
// blank line, then the question. A second tab anywhere is several questions, which this does not
// answer.
func question(lines []string, header int, statusStamp float64) *Ask {
	bad := func() *Ask { return unknown(lines, statusStamp, header-1) }

	if strings.Count(lines[header], "☐")+strings.Count(lines[header], "☒") > 1 {
		return bad()
	}

	if strings.Contains(lines[header], "✔") {
		return bad()
	}

	firstOption := header + 1
	for firstOption < len(lines) {
		if isOption(lines[firstOption]) {
			break
		}

		if isRule(lines[firstOption]) {
			break
		}

		firstOption++
	}

	title, titled := questionTitle(lines[header+1 : firstOption])
	if !titled {
		return bad()
	}

	picks, read := pickerOptions(lines, firstOption)
	if !read {
		return bad()
	}

	return build(KindQuestion, title, "", picks, statusStamp)
}

// questionTitle is the question's text: a blank line, then its lines, with no rule or tab.
func questionTitle(titleLines []string) (string, bool) {
	for _, line := range titleLines {
		if isDotted(line) {
			return "", false
		}

		if isRule(line) {
			return "", false
		}

		if strings.ContainsAny(line, "☐☒✔") {
			return "", false
		}
	}

	if len(titleLines) == 0 {
		return "", false
	}

	if js.Trim(titleLines[0]) != "" {
		return "", false
	}

	title := squash(strings.Join(titleLines, " "))

	return title, title != ""
}

// pickerOptions are the picker's choices, its last two rows (Type something, Chat about this)
// left out.
func pickerOptions(lines []string, firstOption int) ([]Option, bool) {
	options, end, found := optionBlock(lines, firstOption, true)
	if !found {
		return nil, false
	}

	if !consecutive(options) {
		return nil, false
	}

	if !onlyFooter(lines, end) {
		return nil, false
	}

	if !pickerTailRows(options) {
		return nil, false
	}

	count := len(options)
	picks := []Option{}

	for index, option := range options {
		if checkbox(option.lines[0]) {
			return nil, false
		}

		if index < count-pickerTail {
			picks = append(picks, Option{Key: option.key, Label: option.lines[0], Role: RolePick})
		}
	}

	return picks, true
}

// pickerTailRows is whether a picker ends in its Type something and Chat about this rows, the
// last one under the picker's rule.
func pickerTailRows(options []parsed) bool {
	count := len(options)
	if count < pickerMin {
		return false
	}

	// The description under an option is its second line; only the first is the label.
	if !strings.HasPrefix(options[count-pickerTail].lines[0], "Type something") {
		return false
	}

	if !strings.HasPrefix(options[count-1].lines[0], "Chat about this") {
		return false
	}

	// optionBlock takes one rule at most, so with Chat about this ruled no other row is.
	return options[count-1].ruled
}

// checkbox is /^\[[ xX✔✓]\]/: a multi-select row.
func checkbox(label string) bool {
	for _, mark := range []string{"[ ]", "[x]", "[X]", "[✔]", "[✓]"} {
		if strings.HasPrefix(label, mark) {
			return true
		}
	}

	return false
}
