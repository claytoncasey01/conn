package reviews

import (
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
)

// The draft file a review session writes (`.conn/review.json`): what of it Conn believes. The
// file is written by a model and read from a folder Conn does not control, so every field is
// checked and the file can never grow a finding list without limit.

// Verdict is what a draft proposes the review does: one of the verdicts eventOf knows.
type Verdict string

// Severity is how much a finding matters: one of the severities severityOf knows.
type Severity string

const (
	// VerdictApprove is a draft that would approve the PR.
	VerdictApprove Verdict = "approve"
	// VerdictComment is a draft that would only comment on it.
	VerdictComment Verdict = "comment"
	// SideRight is a finding on the new version of a file.
	SideRight = "RIGHT"
	// SideLeft is a finding on the old version of a file.
	SideLeft = "LEFT"
	// MaxDraftFileBytes is the largest draft file that is read at all.
	MaxDraftFileBytes = 512 * 1024

	// maxDraftFindings is the most findings kept from one read of the file.
	maxDraftFindings = 100
	// maxSummaryUnits is the longest summary after cleaning, in UTF-16 units; a longer one makes
	// the whole file unreadable, it is never cut.
	maxSummaryUnits = 8000
	// MaxCommentUnits is the longest comment Conn writes to GitHub after cleaning, in UTF-16
	// units: a review's finding here, and a reply in My PRs. A longer one is dropped whole, never
	// cut. It is defined here, as My PRs imports this package and not the other way.
	MaxCommentUnits = 2000
)

// commitShape is a full commit sha, as git prints it.
var commitShape = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Finding is one comment of a draft: a line of a file in the PR's diff and what to say of it.
type Finding struct {
	Path     string   `json:"path"`
	Line     int64    `json:"line"`
	Side     string   `json:"side"`     // SideRight or SideLeft.
	Severity Severity `json:"severity"` // P0 to P3.
	Body     string   `json:"body"`
}

// Draft is the review a session wrote, once accepted: the head commit it read, the summary, the
// verdict it proposes, the findings kept and how many entries were not.
type Draft struct {
	Commit   string    `json:"commit"`
	Summary  string    `json:"summary"`
	Verdict  Verdict   `json:"verdict"` // VerdictApprove or VerdictComment.
	Findings []Finding `json:"findings"`
	Dropped  int       `json:"dropped"`
}

// AcceptDraft is the draft in raw, a parsed `{"commit","summary","verdict","findings":[...]}`.
// The problems are what make the whole file unreadable (a file that is not an object, a commit
// that is not 40 lowercase hex digits, a summary that is empty or over maxSummaryUnits once
// cleaned, a verdict that is not approve or comment), all of them at once; with any, the draft
// is empty. A finding is dropped whole, and counted, when its path is not a clean relative path,
// its line is not a positive whole number, its side (RIGHT when absent) or severity is not in
// its table, or its body is empty or over MaxCommentUnits once cleaned. No more than
// maxDraftFindings are kept; the rest count as dropped.
func AcceptDraft(raw any) (Draft, []string) {
	empty := emptyDraft()

	file := object(raw)
	if file == nil {
		return empty, []string{"the file is not a JSON object"}
	}

	problems := []string{}
	draft := empty

	draft.Commit = draftCommit(memberOf(file, "commit"), &problems)
	draft.Summary = draftSummary(memberOf(file, "summary"), &problems)
	draft.Verdict = draftVerdict(memberOf(file, "verdict"), &problems)

	if len(problems) > 0 {
		return empty, problems
	}

	draft.Findings, draft.Dropped = acceptFindings(memberOf(file, "findings"))

	return draft, nil
}

// emptyDraft is a draft with nothing in it: what an unreadable file, or a posted marker, holds.
func emptyDraft() Draft {
	return Draft{Commit: "", Summary: "", Verdict: "", Findings: []Finding{}, Dropped: 0}
}

func draftCommit(value any, problems *[]string) string {
	commit, isText := text(value)
	if !isText || !commitShape.MatchString(commit) {
		*problems = append(*problems, "commit: not a 40-character commit sha")

		return ""
	}

	return commit
}

func draftSummary(value any, problems *[]string) string {
	summary, _ := text(value)
	cleaned := CleanMultiline(summary)

	if cleaned == "" {
		*problems = append(*problems, "summary: empty")

		return ""
	}

	if js.Len(cleaned) > maxSummaryUnits {
		*problems = append(*problems,
			"summary: over "+strconv.Itoa(maxSummaryUnits)+" characters")

		return ""
	}

	return cleaned
}

func draftVerdict(value any, problems *[]string) Verdict {
	verdict, known := verdictOf(value)
	if !known {
		*problems = append(*problems, errBadVerdict.Error())

		return ""
	}

	return verdict
}

// verdictOf is the verdict value names, and false for anything not in eventOf's table.
func verdictOf(value any) (Verdict, bool) {
	given, _ := text(value)
	verdict := Verdict(given)
	_, known := eventOf(verdict)

	return verdict, known
}

// eventOf is the review event a verdict is posted as, and false for one not in the table: the
// one list of the verdicts Conn knows.
func eventOf(verdict Verdict) (string, bool) {
	switch verdict {
	case VerdictApprove:
		return eventApprove, true
	case VerdictComment:
		return eventComment, true
	}

	return "", false
}

// severityOf is whether a severity holds a review back from an approval (a blocker), and false
// for one not in the table: the one list of the severities a finding may have, P0 to P3.
func severityOf(severity Severity) (bool, bool) {
	switch severity {
	case "P0", "P1":
		return true, true
	case "P2", "P3":
		return false, true
	}

	return false, false
}

// acceptFindings are the findings of the list that pass, up to maxDraftFindings, and how many of
// its entries did not. A value that is not a list has none.
func acceptFindings(value any) ([]Finding, int) {
	entries, isList := value.([]any)
	kept := []Finding{}

	if !isList {
		return kept, 0
	}

	for _, entry := range entries {
		if len(kept) >= maxDraftFindings {
			break
		}

		accepted, ok := acceptFinding(entry)
		if ok {
			kept = append(kept, accepted)
		}
	}

	return kept, len(entries) - len(kept)
}

func acceptFinding(entry any) (Finding, bool) {
	var none Finding

	fields := object(entry)
	if fields == nil {
		return none, false
	}

	filePath, isPath := text(memberOf(fields, "path"))
	line, isLine := wholeLine(memberOf(fields, "line"))
	side, isSide := findingSide(memberOf(fields, "side"))
	severity, isSeverity := findingSeverity(memberOf(fields, "severity"))
	body, isBody := findingBody(memberOf(fields, "body"))

	valid := isPath && repoPath(filePath) && isLine && isSide && isSeverity && isBody
	if !valid {
		return none, false
	}

	return Finding{Path: filePath, Line: line, Side: side, Severity: severity, Body: body}, true
}

// wholeLine is a line number: a JSON number that is a whole number from 1 to the largest safe
// integer.
func wholeLine(value any) (int64, bool) {
	line, ok := number(value)
	if !ok || line != float64(int64(line)) {
		return 0, false
	}

	if line < 1 || line > maxSafeInteger {
		return 0, false
	}

	return int64(line), true
}

// findingSeverity is the severity given, when it is one in severityOf's table.
func findingSeverity(value any) (Severity, bool) {
	given, _ := text(value)
	severity := Severity(given)
	_, known := severityOf(severity)

	return severity, known
}

// findingSide is the side given, RIGHT when there is none.
func findingSide(value any) (string, bool) {
	if value == nil {
		return SideRight, true
	}

	side, isText := text(value)

	return side, isText && slices.Contains([]string{SideRight, SideLeft}, side)
}

// findingBody is a body once cleaned, when it is not empty and not over MaxCommentUnits.
func findingBody(value any) (string, bool) {
	body, isText := text(value)
	if !isText {
		return "", false
	}

	cleaned := CleanMultiline(body)
	if cleaned == "" || js.Len(cleaned) > MaxCommentUnits {
		return "", false
	}

	return cleaned, true
}

// repoPath is a path inside the repo: not empty, relative, with no backslash or NUL, already
// clean (no ".", ".." or doubled and trailing slashes).
func repoPath(candidate string) bool {
	if candidate == "" || strings.ContainsAny(candidate, "\\\x00") {
		return false
	}

	if strings.HasPrefix(candidate, "/") || path.Clean(candidate) != candidate {
		return false
	}

	return candidate != "." && candidate != ".." && !strings.HasPrefix(candidate, "../")
}
