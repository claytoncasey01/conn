package reviews_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
)

const goodCommit = "0123456789abcdef0123456789abcdef01234567"

// quoted is text as a JSON string.
func quoted(text string) string { return js.Stringify(text) }

// finding is one entry of a draft's findings, as JSON; line is a JSON literal and an empty side
// leaves the side out.
func finding(path, line, side, severity, body string) string {
	parts := []string{
		`"path":` + quoted(path), `"line":` + line,
		`"severity":` + quoted(severity), `"body":` + quoted(body),
	}
	if side != "" {
		parts = append(parts, `"side":`+quoted(side))
	}

	return "{" + strings.Join(parts, ",") + "}"
}

// goodFinding is a finding that is kept.
func goodFinding() string { return finding("src/a.go", "10", "RIGHT", "P1", "Check this.") }

// draftFile is a draft with the commit, summary and verdict given (JSON each) and findings.
func draftFile(commit, summary, verdict string, findings ...string) string {
	return `{"commit":` + commit + `,"summary":` + summary + `,"verdict":` + verdict +
		`,"findings":[` + strings.Join(findings, ",") + `]}`
}

// readable is a draft that is whole except for its findings.
func readable(findings ...string) string {
	return draftFile(quoted(goodCommit), quoted("Looks fine."), quoted("comment"), findings...)
}

func repeated(entry string, count int) []string {
	out := make([]string, count)
	for index := range out {
		out[index] = entry
	}

	return out
}

func accept(t *testing.T, raw string) (reviews.Draft, []string) {
	t.Helper()

	parsed, err := js.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("%s: %v", raw, err)
	}

	return reviews.AcceptDraft(parsed)
}

func TestAcceptDraft(t *testing.T) {
	t.Parallel()

	const (
		badCommit  = "commit: not a 40-character commit sha"
		badSummary = "summary: empty"
		badVerdict = "verdict: not approve or comment"
	)

	tests := []struct {
		name         string
		raw          string
		wantProblems []string
		wantFindings int
		wantDropped  int
	}{
		{"two findings", readable(goodFinding(), goodFinding()), nil, 2, 0},
		{"no findings", readable(), nil, 0, 0},
		{"a left side", readable(finding("a.go", "3", "LEFT", "P0", "Old.")), nil, 1, 0},
		{"side missing", readable(finding("a.go", "3", "", "P2", "Fine.")), nil, 1, 0},
		{"side unknown", readable(finding("a.go", "3", "BOTH", "P2", "Fine.")), nil, 0, 1},
		{"side lower case", readable(finding("a.go", "3", "right", "P2", "Fine.")), nil, 0, 1},
		{"clean relative path", readable(finding("a/b/c.go", "1", "", "P3", "Ok.")), nil, 1, 0},
		{
			"unsafe paths", readable(
				finding("../x", "1", "", "P1", "b"), finding("/etc/passwd", "1", "", "P1", "b"),
				finding(`a\b`, "1", "", "P1", "b"), finding("", "1", "", "P1", "b"),
				finding("./a", "1", "", "P1", "b"),
			), nil, 0, 5,
		},
		{
			"more unsafe paths", readable(
				finding("a/../b", "1", "", "P1", "b"), finding("a//b", "1", "", "P1", "b"),
				finding("a/", "1", "", "P1", "b"), finding("a\x00b", "1", "", "P1", "b"),
				finding("..", "1", "", "P1", "b"), finding(".", "1", "", "P1", "b"),
			), nil, 0, 6,
		},
		{
			"bad lines", readable(
				finding("a", "0", "", "P1", "b"), finding("a", "-1", "", "P1", "b"),
				finding("a", "1.5", "", "P1", "b"), finding("a", `"3"`, "", "P1", "b"),
				finding("a", "null", "", "P1", "b"), finding("a", "1e400", "", "P1", "b"),
			), nil, 0, 6,
		},
		{
			"a missing line", readable(`{"path":"a","severity":"P1","body":"b"}`), nil, 0, 1,
		},
		{
			"bad severities", readable(
				finding("a", "1", "", "P4", "b"), finding("a", "1", "", "p1", "b"),
				finding("a", "1", "", "", "b"),
			), nil, 0, 3,
		},
		{
			"bad bodies", readable(
				finding("a", "1", "", "P1", ""), finding("a", "1", "", "P1", " \n\t "),
				finding("a", "1", "", "P1", strings.Repeat("x", 2001)),
			), nil, 0, 3,
		},
		{"a body at the limit", readable(
			finding("a", "1", "", "P1", strings.Repeat("x", 2000)),
		), nil, 1, 0},
		{"a body over the limit in UTF-16 units", readable(
			finding("a", "1", "", "P1", strings.Repeat("\U0001F600", 1001)),
		), nil, 0, 1},
		{"entries that are not objects", readable(`"a"`, `7`, `null`, `[]`), nil, 0, 4},
		{
			"a hundred findings kept", readable(repeated(goodFinding(), 100)...), nil, 100, 0,
		},
		{
			"a hundred and one findings", readable(repeated(goodFinding(), 101)...), nil, 100, 1,
		},
		{"findings that are not a list", `{"commit":"` + goodCommit +
			`","summary":"s","verdict":"approve","findings":{}}`, nil, 0, 0},
		{"findings missing", `{"commit":"` + goodCommit +
			`","summary":"s","verdict":"approve"}`, nil, 0, 0},
		{"an array", `[]`, []string{"the file is not a JSON object"}, 0, 0},
		{"a string", `"x"`, []string{"the file is not a JSON object"}, 0, 0},
		{"null", `null`, []string{"the file is not a JSON object"}, 0, 0},
		{
			"commit too short", draftFile(quoted("abc123"), quoted("s"), quoted("approve")),
			[]string{badCommit},
			0, 0,
		},
		{
			"commit upper case", draftFile(
				quoted(strings.ToUpper(goodCommit)), quoted("s"), quoted("approve"),
			), []string{badCommit}, 0, 0,
		},
		{
			"commit not text", draftFile("7", quoted("s"), quoted("approve")),
			[]string{badCommit},
			0, 0,
		},
		{
			"summary empty", draftFile(quoted(goodCommit), quoted(""), quoted("approve")),
			[]string{badSummary},
			0, 0,
		},
		{
			"summary blank", draftFile(quoted(goodCommit), quoted(" \n "), quoted("approve")),
			[]string{badSummary},
			0, 0,
		},
		{
			"summary over the limit", draftFile(
				quoted(goodCommit), quoted(strings.Repeat("x", 8001)), quoted("approve"),
			), []string{"summary: over 8000 characters"}, 0, 0,
		},
		{
			"summary at the limit", draftFile(
				quoted(goodCommit), quoted(strings.Repeat("x", 8000)), quoted("approve"),
			), nil, 0, 0,
		},
		{
			"verdict request_changes",
			draftFile(quoted(goodCommit), quoted("s"), quoted("request_changes")),
			[]string{badVerdict},
			0, 0,
		},
		{
			"verdict upper case", draftFile(quoted(goodCommit), quoted("s"), quoted("APPROVE")),
			[]string{badVerdict},
			0, 0,
		},
		{
			"every field bad, every problem listed",
			draftFile(quoted("x"), quoted(""), quoted("nope"), goodFinding()),
			[]string{badCommit, badSummary, badVerdict},
			0, 0,
		},
		{"no fields at all", `{}`, []string{badCommit, badSummary, badVerdict}, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			draft, problems := accept(t, tt.raw)

			if !slices.Equal(problems, tt.wantProblems) {
				t.Errorf("problems = %q, want %q", problems, tt.wantProblems)
			}

			if len(draft.Findings) != tt.wantFindings {
				t.Errorf("kept %d findings, want %d", len(draft.Findings), tt.wantFindings)
			}

			if draft.Dropped != tt.wantDropped {
				t.Errorf("dropped = %d, want %d", draft.Dropped, tt.wantDropped)
			}
		})
	}
}

func TestAcceptDraftKeepsWhatItChecked(t *testing.T) {
	t.Parallel()

	raw := draftFile(
		quoted(goodCommit), quoted("  Two issues.\r\n\r\n\r\n\r\nSee below.\u200b "),
		quoted("approve"),
		finding("src/a.go", "42", "", "P2", "Line one.\r\nLine\u202e two.\t"),
	)

	draft, problems := accept(t, raw)
	if len(problems) != 0 {
		t.Fatalf("problems = %q", problems)
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"commit", draft.Commit, goodCommit},
		{"summary", draft.Summary, "Two issues.\n\nSee below."},
		{"verdict", string(draft.Verdict), string(reviews.VerdictApprove)},
		{"path", draft.Findings[0].Path, "src/a.go"},
		{"side", draft.Findings[0].Side, reviews.SideRight},
		{"severity", string(draft.Findings[0].Severity), "P2"},
		{"line", strconv.FormatInt(draft.Findings[0].Line, 10), "42"},
		{"body", draft.Findings[0].Body, "Line one.\nLine two."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}
