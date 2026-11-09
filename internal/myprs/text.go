// Package myprs is the My PRs tab: the signed-in user's open PRs in the allowed repos, read from
// GitHub, with the Self-Review rounds Conn starts on them, the replies a round proposes for their
// review threads, the Greptile ask once a round has pushed, and the Slack draft once a PR is
// ready. What Conn itself remembers is in myprs.json; everything else comes from GitHub again.
package myprs

import (
	"regexp"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// MaxTextUnits is how much of a comment or thread text is kept, in UTF-16 units.
const MaxTextUnits = 400

// noCut is a cut that keeps the whole text.
const noCut = -1

var (
	// lineBreaks are tabs, line breaks and the Unicode line and paragraph separators.
	lineBreaks = regexp.MustCompile("[\t\n\r\u2028\u2029]+")
	// invisible are what reviews.UnsafeText leaves: C1 controls and zero-width characters
	// (that one is shared with Reviews, so it is not widened).
	invisible = regexp.MustCompile("[\u0080-\u009f\u200b-\u200d\ufeff]")
	// lineEnds are CRLF, CR and the Unicode separators, each one newline in a multiline text.
	lineEnds = regexp.MustCompile("\r\n|[\r\u2028\u2029]")
	// blankRun is more than one blank line.
	blankRun = regexp.MustCompile(`\n{3,}`)
)

// Clean makes text from GitHub safe to show on one line: tabs, line breaks and U+2028/2029
// become a space so words do not run together, every other control, bidi and zero-width
// character is removed, and it is cut to maxUnits UTF-16 units (noCut keeps it whole).
func Clean(text string, maxUnits int) string {
	spaced := lineBreaks.ReplaceAllString(text, " ")
	out := invisible.ReplaceAllString(reviews.UnsafeText.ReplaceAllString(spaced, ""), "")

	return cut(out, maxUnits)
}

// CleanMultiline is Clean for text shown with its lines (a reply): CRLF, CR and U+2028/2029
// become one newline, a tab a space, every other control, bidi and zero-width character is
// removed, a run of more than 2 newlines is cut to 2 and the ends are trimmed. maxUnits cuts
// last (noCut measures the cleaned length instead).
func CleanMultiline(text string, maxUnits int) string {
	unified := strings.ReplaceAll(lineEnds.ReplaceAllString(text, "\n"), "\t", " ")
	lines := strings.Split(unified, "\n")

	for index, line := range lines {
		lines[index] = invisible.ReplaceAllString(reviews.UnsafeText.ReplaceAllString(line, ""),
			"")
	}

	joined := blankRun.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")

	return cut(js.Trim(joined), maxUnits)
}

func cut(text string, maxUnits int) string {
	if maxUnits == noCut {
		return text
	}

	return js.Cut(text, maxUnits)
}
