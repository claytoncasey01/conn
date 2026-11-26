package reviews

import (
	"regexp"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
)

var (
	// InvisibleText are what UnsafeText leaves: C1 controls and zero-width characters (UnsafeText
	// is shared with Reviews, so it is not widened). My PRs removes both from what it shows.
	InvisibleText = regexp.MustCompile(`[\x{0080}-\x{009f}\x{200b}-\x{200d}\x{feff}]`)
	// lineEnds are CRLF, CR and the Unicode separators, each one newline in a multiline text.
	lineEnds = regexp.MustCompile("\r\n|[\r\u2028\u2029]")
	// blankRun is more than one blank line.
	blankRun = regexp.MustCompile(`\n{3,}`)
)

// CleanMultiline makes text written by a model safe to show with its lines: CRLF, CR and
// U+2028/2029 become one newline, a tab a space, every other control, bidi and zero-width
// character is removed, a run of more than 2 newlines is cut to 2 and the ends are trimmed. It
// is not cut to a length; the caller measures it with js.Len.
func CleanMultiline(text string) string {
	unified := strings.ReplaceAll(lineEnds.ReplaceAllString(text, "\n"), "\t", " ")
	lines := strings.Split(unified, "\n")

	for index, line := range lines {
		lines[index] = InvisibleText.ReplaceAllString(UnsafeText.ReplaceAllString(line, ""), "")
	}

	return js.Trim(blankRun.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}
