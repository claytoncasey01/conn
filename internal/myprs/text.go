// Package myprs is the My PRs tab: the signed-in user's open PRs in the allowed repos, read from
// GitHub, with the Self-Review rounds Conn starts on them, the replies a round proposes for their
// review threads, the Greptile ask once a round has pushed, and the Slack draft once a PR is
// ready. What Conn itself remembers is in myprs.json; everything else comes from GitHub again.
package myprs

import (
	"regexp"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// MaxTextUnits is how much of a comment or thread text is kept, in UTF-16 units.
const MaxTextUnits = 400

// noCut is a cut that keeps the whole text.
const noCut = -1

// lineBreaks are tabs, line breaks and the Unicode line and paragraph separators.
var lineBreaks = regexp.MustCompile("[\t\n\r\u2028\u2029]+")

// Clean makes text from GitHub safe to show on one line: tabs, line breaks and U+2028/2029
// become a space so words do not run together, every other control, bidi and zero-width
// character is removed, and it is cut to maxUnits UTF-16 units (noCut keeps it whole).
func Clean(text string, maxUnits int) string {
	spaced := lineBreaks.ReplaceAllString(text, " ")
	unsafe := reviews.UnsafeText.ReplaceAllString(spaced, "")

	return cut(reviews.InvisibleText.ReplaceAllString(unsafe, ""), maxUnits)
}

// CleanMultiline is Clean for text shown with its lines (a reply): see reviews.CleanMultiline.
// maxUnits cuts last (noCut measures the cleaned length instead).
func CleanMultiline(text string, maxUnits int) string {
	return cut(reviews.CleanMultiline(text), maxUnits)
}

func cut(text string, maxUnits int) string {
	if maxUnits == noCut {
		return text
	}

	return js.Cut(text, maxUnits)
}
