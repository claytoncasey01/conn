package reviews

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Placing a draft's findings against the PR's diff. GitHub rejects a whole review when one inline
// comment names a path, line or side that is not in a hunk of the diff, so each finding is checked
// first, and those GitHub would refuse go into the review body instead. This is the rule of
// skills/deep-code-review/scripts/post_review.py, as pure functions of the diff text.

const (
	// MaxDiffBytes is the most of a diff that is read. Gh.Diff's read stops gh at this
	// (gh.Capped) and refuses a diff that reaches past it; the cut in Positions keeps the
	// function bounded whatever it is handed. A cut can only lose positions, and a finding
	// without one goes in the review body, so it is safe.
	MaxDiffBytes = 8 << 20

	// outsideHeading introduces the findings the review body carries because GitHub cannot take
	// them as line comments.
	outsideHeading = "**Outside this PR's diff** (GitHub can't attach these as line comments, " +
		"so they're here instead):"

	// devNull is the path a diff gives the new side of a deleted file.
	devNull = "/dev/null"
)

// hunkHeader is a hunk's first line: the old start and count, then the new start and count. A
// count left out is one line.
var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// Position is where a line comment lands: a line of a file, on the new version of it (SideRight)
// or the old one (SideLeft).
type Position struct {
	Path string
	Line int64
	Side string
}

// diffReader is where a pass over a diff has got to: the file it is in, the next line number on
// each side, and how many lines of the current hunk are still to come on each side. The counts
// are what tell a hunk's body from the headers after it, as a removed line that starts with
// "-- " reads like a "--- " header.
type diffReader struct {
	positions map[Position]bool
	path      string // The new path of the file; empty when it has none to comment on.
	oldLine   int64
	newLine   int64
	oldLeft   int64
	newLeft   int64
}

// Positions is every position GitHub accepts a comment on in diff: an added line on the right at
// its new number, a removed line on the left at its old number, and a context line on both.
func Positions(diff string) map[Position]bool {
	if len(diff) > MaxDiffBytes {
		diff = diff[:MaxDiffBytes]
	}

	reader := diffReader{positions: map[Position]bool{}}

	for line := range strings.SplitSeq(diff, "\n") {
		reader.take(strings.TrimSuffix(line, "\r"))
	}

	return reader.positions
}

// Inline reports, for each finding in order, whether it is a position GitHub accepts a line
// comment on.
func Inline(findings []Finding, positions map[Position]bool) []bool {
	inline := make([]bool, len(findings))

	for index, finding := range findings {
		place := Position{Path: finding.Path, Line: finding.Line, Side: finding.Side}
		inline[index] = positions[place]
	}

	return inline
}

// ReviewBody is the summary and, after it, each finding that is not posted inline. With none it
// is the summary as it is.
func ReviewBody(summary string, outside []Finding) string {
	if len(outside) == 0 {
		return summary
	}

	lines := []string{
		strings.TrimRightFunc(summary, unicode.IsSpace), "", "---", "", outsideHeading, "",
	}

	for _, finding := range outside {
		lines = append(lines,
			"**"+finding.Path+":"+strconv.FormatInt(finding.Line, 10)+"**", "", finding.Body, "")
	}

	return strings.Join(lines, "\n")
}

// take reads one line of the diff.
func (reader *diffReader) take(line string) {
	if reader.oldLeft > 0 || reader.newLeft > 0 {
		reader.takeBody(line)

		return
	}

	if raw, found := strings.CutPrefix(line, "+++ "); found {
		reader.path = newPath(raw)

		return
	}

	reader.takeHunkHeader(line)
}

// takeHunkHeader starts a hunk when line is its header; any other line outside a hunk (the
// "diff --git" line, modes, renames, the old path) says nothing about positions.
func (reader *diffReader) takeHunkHeader(line string) {
	match := hunkHeader.FindStringSubmatch(line)
	if match == nil {
		return
	}

	reader.oldLine, reader.oldLeft = hunkSide(match[1], match[2])
	reader.newLine, reader.newLeft = hunkSide(match[3], match[4])
}

// takeBody reads one line inside a hunk. A "\ No newline at end of file" line belongs to the line
// before it and moves no counter.
func (reader *diffReader) takeBody(line string) {
	switch {
	case strings.HasPrefix(line, `\`):
	case strings.HasPrefix(line, "+"):
		reader.add(reader.newLine, SideRight)
		reader.newLine++
		reader.newLeft--
	case strings.HasPrefix(line, "-"):
		reader.add(reader.oldLine, SideLeft)
		reader.oldLine++
		reader.oldLeft--
	default:
		// A context line, which an editor may have left without its leading space.
		reader.add(reader.newLine, SideRight)
		reader.add(reader.oldLine, SideLeft)
		reader.newLine++
		reader.oldLine++
		reader.newLeft--
		reader.oldLeft--
	}
}

// add records a position, unless the file has no path to comment on.
func (reader *diffReader) add(line int64, side string) {
	if reader.path == "" {
		return
	}

	reader.positions[Position{Path: reader.path, Line: line, Side: side}] = true
}

// hunkSide reads one side of a hunk header: the line it starts at and how many lines it spans.
// A number too large to read makes the side empty, so the hunk after it adds nothing.
func hunkSide(start, count string) (int64, int64) {
	first, err := strconv.ParseInt(start, 10, 64)
	if err != nil {
		return 0, 0
	}

	if count == "" {
		return first, 1
	}

	span, err := strconv.ParseInt(count, 10, 64)
	if err != nil {
		return 0, 0
	}

	return first, span
}

// newPath is the path of a file's new side from a "+++ " line, without git's "b/" prefix and
// the tab it adds after a path with a space in it. A deleted file has none.
func newPath(raw string) string {
	path := strings.TrimSpace(raw)
	if path == devNull {
		return ""
	}

	return strings.TrimPrefix(path, "b/")
}
