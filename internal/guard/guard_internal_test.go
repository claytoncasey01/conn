// Package guard checks the repository's own tracked files, as tests: no organization names
// anywhere, and no compound conditions in the Go code.
//
// No tracked file, and no tracked file name, may hold a name of the organization a checkout is
// used at: company values are settings, chosen on first launch. The names are the person's own
// and never in the repo: a git-ignored list, .company-terms at the repo root (or the file
// CONN_GUARD_TERMS names), one term per line. Blank lines and # lines are skipped; a plain line
// matches anywhere, in any case; a line starting re: is a regular expression, matched in any case
// (for a key-like or whole-word term). With no list the scan is skipped and says how to turn it
// on; a list that cannot be read, or a bad expression in it, fails.
package guard

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// self is this file, which names the list's syntax and so is never scanned for terms.
const self = "internal/guard/guard_internal_test.go"

// trackedFilesMin is fewer files than the repository has ever had: a listing this short means git
// did not really list them.
const trackedFilesMin = 100

var (
	errEmptyExpression = errors.New("an empty re: term")
	errBadExpression   = errors.New("not a valid regular expression")
)

// binaryName matches the files whose text is not read; their names still are.
var binaryName = regexp.MustCompile(`(?i)\.(` +
	`png|jpe?g|gif|webp|ico|bmp|woff2?|ttf|otf|eot|mp4|webm|mov|pdf|zip|gz|tgz|wasm|db|sqlite` +
	`)$`)

// term is one entry of the list: the line as written (what a failure names) and what it matches.
type term struct {
	line    string
	matches *regexp.Regexp
}

type listState string

const (
	listMissing listState = "missing"
	listEmpty   listState = "empty"
	listBad     listState = "error"
	listOK      listState = "ok"
)

// termList is the state of the term list at path.
type termList struct {
	state listState
	path  string
	err   error
	terms []term
}

// parseTerms reads a list's text into its terms, or names the first bad line.
func parseTerms(text string) ([]term, error) {
	var terms []term

	for index, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#") {
			continue
		}

		parsed, err := parseTerm(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", index+1, err)
		}

		terms = append(terms, parsed)
	}

	return terms, nil
}

func parseTerm(line string) (term, error) {
	source, isExpression := strings.CutPrefix(line, "re:")
	if !isExpression {
		return term{line: line, matches: regexp.MustCompile("(?i)" + regexp.QuoteMeta(line))}, nil
	}

	source = strings.TrimSpace(source)
	if source == "" {
		return term{}, errEmptyExpression
	}

	compiled, err := regexp.Compile("(?i)" + source)
	if err != nil {
		return term{}, fmt.Errorf("%w (%w)", errBadExpression, err)
	}

	return term{line: line, matches: compiled}, nil
}

// loadTerms reads the list at path: missing, empty (nothing but blanks and comments), unreadable
// or bad (an error), or its terms.
func loadTerms(path string) termList {
	text, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return termList{state: listMissing, path: path, err: nil, terms: nil}
	}

	if err != nil {
		err = fmt.Errorf("%s could not be read: %w", path, err)

		return termList{state: listBad, path: path, err: err, terms: nil}
	}

	terms, err := parseTerms(string(text))
	if err != nil {
		err = fmt.Errorf("%s, %w", path, err)

		return termList{state: listBad, path: path, err: err, terms: nil}
	}

	if len(terms) == 0 {
		return termList{state: listEmpty, path: path, err: nil, terms: nil}
	}

	return termList{state: listOK, path: path, err: nil, terms: terms}
}

// hits is every `file:line: term "match"` in text; line 0 is the file's name.
func hits(terms []term, path, text string) []string {
	found := lineHits(terms, path, 0, path)
	for index, line := range strings.Split(text, "\n") {
		found = append(found, lineHits(terms, path, index+1, line)...)
	}

	return found
}

func lineHits(terms []term, path string, number int, line string) []string {
	var found []string

	for _, entry := range terms {
		location := entry.matches.FindStringIndex(line)
		if location == nil {
			continue
		}

		match := line[location[0]:location[1]]
		found = append(found, fmt.Sprintf(`%s:%d: %s "%s"`, path, number, entry.line, match))
	}

	return found
}

// root is the repository folder, found from this file.
func root(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot find this file")
	}

	return filepath.Join(filepath.Dir(file), "..", "..")
}

// tracked lists the tracked files. A checkout whose git cannot list them fails the test, never
// passes it; a folder that is not a git checkout at all (a source export) skips it.
func tracked(t *testing.T, dir string) []string {
	t.Helper()

	_, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil {
		t.Skip("not a git checkout (no .git): no tracked files to check")
	}

	cmd := exec.CommandContext(t.Context(), "git", "ls-files", "-z")
	cmd.Dir = dir
	cmd.Env = withoutGitVariables(os.Environ())

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}

	files := slices.DeleteFunc(strings.Split(string(out), "\x00"), func(f string) bool {
		return f == ""
	})

	if len(files) < trackedFilesMin {
		t.Fatalf("the tracked files were not listed (%d)", len(files))
	}

	if !slices.Contains(files, self) {
		t.Fatalf("the tracked files were not listed: %s is missing", self)
	}

	return files
}

// withoutGitVariables drops GIT_DIR and the like, which a git hook running the tests sets and
// which would point git ls-files at another repository.
func withoutGitVariables(environ []string) []string {
	return slices.DeleteFunc(slices.Clone(environ), func(variable string) bool {
		return strings.HasPrefix(variable, "GIT_")
	})
}

// fileText is a tracked file's text, or "" for a binary, a deleted file or a folder: their names
// are still checked.
func fileText(dir, name string) string {
	if binaryName.MatchString(name) {
		return ""
	}

	text, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}

	if bytes.Contains(text, []byte{0}) {
		return ""
	}

	return string(text)
}

func TestNoTrackedFileHoldsACompanyTerm(t *testing.T) {
	t.Parallel()

	dir := root(t)

	path, _ := os.LookupEnv("CONN_GUARD_TERMS")
	if path == "" {
		path = filepath.Join(dir, ".company-terms")
	}

	list := loadTerms(path)
	switch list.state {
	case listMissing, listEmpty:
		t.Skipf("no company terms to look for (%s is %s; that is expected in a fresh clone). "+
			"To turn the scan on, list your organization's names in .company-terms at the repo "+
			"root (git-ignored), one per line (`re:` for a regular expression), or point "+
			"CONN_GUARD_TERMS at such a file", list.path, list.state)
	case listBad:
		t.Fatal(list.err)
	case listOK:
	}

	var found []string

	for _, name := range tracked(t, dir) {
		if name == self {
			continue
		}

		found = append(found, hits(list.terms, name, fileText(dir, name))...)
	}

	if len(found) > 0 {
		t.Fatalf("company-specific text in the repo:\n%s", strings.Join(found, "\n"))
	}
}

// The matching, with synthetic terms passed in as data.

func mustParse(t *testing.T, text string) []term {
	t.Helper()

	terms, err := parseTerms(text)
	if err != nil {
		t.Fatal(err)
	}

	return terms
}

// matchedTerms is the term line of each hit in s.
func matchedTerms(terms []term, s string) []string {
	found := hits(terms, "x", s)
	matched := make([]string, 0, len(found))

	for _, hit := range found {
		_, rest, _ := strings.Cut(hit, ": ")
		line, _, _ := strings.Cut(rest, ` "`)
		matched = append(matched, line)
	}

	return matched
}

func TestTermsMatch(t *testing.T) {
	t.Parallel()

	const (
		plain      = "# a comment\n\nacme-secret\n  widgets.corp  \n"
		expression = `re:\bqwx-?\d*\b`
	)

	tests := []struct {
		name string
		list string
		text string
		want []string
	}{
		{"plain term in other case", plain, "see Acme-Secret here", []string{"acme-secret"}},
		{"plain term inside a word", plain, "xACME-SECRETx", []string{"acme-secret"}},
		{"plain term with a dot", plain, "widgets.corp", []string{"widgets.corp"}},
		{"a dot is a dot", plain, "widgetsXcorp", []string{}},
		{"no term", plain, "nothing here", []string{}},
		{"expression in other case", expression, "ticket QWX-12 done", []string{expression}},
		{"expression bare", expression, "qwx", []string{expression}},
		{"expression word bounds", expression, "aqwx-12 and qwxy", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := matchedTerms(mustParse(t, tt.list), tt.text)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTermLinesAreKeptAsWritten(t *testing.T) {
	t.Parallel()

	terms := mustParse(t, "# a comment\n\nacme-secret\r\n  widgets.corp  \n")

	got := []string{terms[0].line, terms[1].line}
	if !slices.Equal(got, []string{"acme-secret", "widgets.corp"}) {
		t.Fatalf("got %q", got)
	}
}

func TestAHitNamesFileLineTermAndMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, path, text string
		want             []string
	}{
		{
			"a file name is line 0", "docs/acme-secret-notes.md", "clean",
			[]string{`docs/acme-secret-notes.md:0: acme-secret "acme-secret"`},
		},
		{
			"a line of text", "README.md", "a\nb ACME-secret c",
			[]string{`README.md:2: acme-secret "ACME-secret"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := hits(mustParse(t, "acme-secret"), tt.path, tt.text)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestAListThatIsMissingOrEmptySkipsABadOneFails(t *testing.T) {
	t.Parallel()

	dir := root(t)

	tests := []struct {
		name string
		path string
		want listState
	}{
		{"missing", filepath.Join(dir, "no-such-terms-file"), listMissing},
		// A folder cannot be read as a file: an error, never a pass.
		{"a folder", filepath.Join(dir, "internal"), listBad},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := loadTerms(tt.path).state; got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestBadListLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		list    string
		want    int
		wantErr error
	}{
		{"comments only", "# only\n\n#comments\n", 0, nil},
		{"bad expression", "acme-secret\n# fine\nre:(unclosed", 0, errBadExpression},
		{"empty expression", "re:", 0, errEmptyExpression},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			terms, err := parseTerms(tt.list)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error %v, want %v", err, tt.wantErr)
			}

			if len(terms) != tt.want {
				t.Fatalf("%d terms", len(terms))
			}
		})
	}
}

func TestABadLineIsNamedByNumber(t *testing.T) {
	t.Parallel()

	_, err := parseTerms("acme-secret\n# fine\nre:(unclosed")
	if !strings.HasPrefix(err.Error(), "line 3: not a valid regular expression (") {
		t.Fatalf("got %v", err)
	}
}
