// Package guard keeps organization names out of the repository. No tracked file, and no tracked file name, may hold a name of the
// organization a checkout is used at: company values are settings, chosen on first launch. The names are the person's own and never
// in the repo: a git-ignored list, .company-terms at the repo root (or the file CONN_GUARD_TERMS names), one term per line. Blank
// lines and # lines are skipped; a plain line matches anywhere, in any case; a line starting re: is a regular expression, matched in
// any case (for a key-like or whole-word term). With no list the scan is skipped and says how to turn it on; a list that cannot be
// read, or a bad expression in it, fails.
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
	"strings"
	"testing"
)

// Term is one entry of the list: the line as written (what a failure names) and what it matches.
type Term struct {
	Term string
	Re   *regexp.Regexp
}

// ParseTerms reads a list's text into its terms, or names the first bad line.
func ParseTerms(text string) ([]Term, error) {
	var terms []Term
	for i, raw := range regexp.MustCompile(`\r?\n`).Split(text, -1) {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "re:") {
			source := strings.TrimSpace(line[3:])
			if source == "" {
				return nil, fmt.Errorf("line %d: an empty re: term", i+1)
			}
			re, err := regexp.Compile("(?i)" + source)
			if err != nil {
				return nil, fmt.Errorf("line %d: not a valid regular expression (%v)", i+1, err)
			}
			terms = append(terms, Term{line, re})
			continue
		}
		terms = append(terms, Term{line, regexp.MustCompile("(?i)" + regexp.QuoteMeta(line))})
	}
	return terms, nil
}

// List is the state of the term list at Path.
type List struct {
	State string // missing, empty, error or ok
	Path  string
	Err   error
	Terms []Term
}

// LoadTerms reads the list at path: missing, empty (nothing but blanks and comments), unreadable or bad (an error), or its terms.
func LoadTerms(path string) List {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return List{State: "missing", Path: path}
	}
	if err != nil {
		return List{State: "error", Path: path, Err: fmt.Errorf("%s could not be read: %v", path, err)}
	}
	terms, err := ParseTerms(string(b))
	if err != nil {
		return List{State: "error", Path: path, Err: fmt.Errorf("%s, %v", path, err)}
	}
	if len(terms) == 0 {
		return List{State: "empty", Path: path}
	}
	return List{State: "ok", Path: path, Terms: terms}
}

// Hits is every `file:line: term "match"` in text; line 0 is the file's name.
func Hits(terms []Term, path, text string) []string {
	var out []string
	scan := func(line string, n int) {
		for _, t := range terms {
			if loc := t.Re.FindStringIndex(line); loc != nil {
				out = append(out, fmt.Sprintf(`%s:%d: %s "%s"`, path, n, t.Term, line[loc[0]:loc[1]]))
			}
		}
	}
	scan(path, 0)
	for i, line := range strings.Split(text, "\n") {
		scan(line, i+1)
	}
	return out
}

var binary = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp|ico|bmp|woff2?|ttf|otf|eot|mp4|webm|mov|pdf|zip|gz|tgz|wasm|db|sqlite)$`)

// root is the repository folder, found from this file.
func root(t *testing.T) string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot find this file")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

const self = "internal/guard/guard_test.go"

// tracked lists the tracked files. A checkout whose git cannot list them fails the test, never passes it.
func tracked(t *testing.T, dir string) []string {
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files
}

func TestNoTrackedFileHoldsACompanyTerm(t *testing.T) {
	dir := root(t)
	path := os.Getenv("CONN_GUARD_TERMS")
	if path == "" {
		path = filepath.Join(dir, ".company-terms")
	}
	list := LoadTerms(path)
	// Only a folder that is not a git checkout at all (a source export, with no .git) or a checkout with no list skips the scan
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Skip("not a git checkout (no .git): no tracked files to scan for company-specific text")
	}
	if list.State == "missing" || list.State == "empty" {
		t.Skipf("no company terms to look for (%s is %s; that is expected in a fresh clone). To turn the scan on, list your organization's names in .company-terms at the repo root (git-ignored), one per line (`re:` for a regular expression), or point CONN_GUARD_TERMS at such a file", list.Path, list.State)
	}
	if list.State == "error" {
		t.Fatal(list.Err)
	}
	files := tracked(t, dir)
	has := func(name string) bool {
		for _, f := range files {
			if f == name {
				return true
			}
		}
		return false
	}
	if len(files) < 100 || !has("README.md") || !has(self) {
		t.Fatalf("the tracked files were not listed (%d)", len(files))
	}
	var found []string
	for _, f := range files {
		if f == self {
			continue
		}
		text := ""
		if !binary.MatchString(f) {
			// a file deleted in the working tree (or not a file) still has its name checked
			if b, err := os.ReadFile(filepath.Join(dir, f)); err == nil && !bytes.Contains(b, []byte{0}) {
				text = string(b)
			}
		}
		found = append(found, Hits(list.Terms, f, text)...)
	}
	if len(found) > 0 {
		t.Fatalf("company-specific text in the repo:\n%s", strings.Join(found, "\n"))
	}
}

// The matching, with synthetic terms passed in as data

func terms(t *testing.T, text string) []Term {
	t.Helper()
	ts, err := ParseTerms(text)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func matched(ts []Term, s string) []string {
	out := []string{}
	for _, h := range Hits(ts, "x", s) {
		rest := strings.SplitN(h, ": ", 2)[1]
		out = append(out, strings.SplitN(rest, ` "`, 2)[0])
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestAPlainTermMatchesAnywhereInAnyCaseAsWritten(t *testing.T) {
	ts := terms(t, "# a comment\n\nacme-secret\n  widgets.corp  \n")
	eq(t, []string{ts[0].Term, ts[1].Term}, []string{"acme-secret", "widgets.corp"})
	eq(t, matched(ts, "see Acme-Secret here"), []string{"acme-secret"})
	eq(t, matched(ts, "xACME-SECRETx"), []string{"acme-secret"})
	eq(t, matched(ts, "widgets.corp"), []string{"widgets.corp"})
	// the dot is a dot, not any character
	eq(t, matched(ts, "widgetsXcorp"), []string{})
	eq(t, matched(ts, "nothing here"), []string{})
}

func TestAReTermIsAnExpressionInAnyCase(t *testing.T) {
	ts := terms(t, `re:\bqwx-?\d*\b`)
	eq(t, matched(ts, "ticket QWX-12 done"), []string{`re:\bqwx-?\d*\b`})
	eq(t, matched(ts, "qwx"), []string{`re:\bqwx-?\d*\b`})
	eq(t, matched(ts, "aqwx-12 and qwxy"), []string{})
}

func TestAFileNameIsLineZeroAndAFailureNamesFileLineTermAndMatch(t *testing.T) {
	ts := terms(t, "acme-secret")
	eq(t, Hits(ts, "docs/acme-secret-notes.md", "clean"), []string{`docs/acme-secret-notes.md:0: acme-secret "acme-secret"`})
	eq(t, Hits(ts, "README.md", "a\nb ACME-secret c"), []string{`README.md:2: acme-secret "ACME-secret"`})
}

func TestTheListMissingAndEmptySkipABadOneFails(t *testing.T) {
	dir := root(t)
	if s := LoadTerms(filepath.Join(dir, "no-such-terms-file")).State; s != "missing" {
		t.Fatalf("missing: %s", s)
	}
	// a folder cannot be read as a file: an error, never a pass
	if s := LoadTerms(filepath.Join(dir, "internal")).State; s != "error" {
		t.Fatalf("folder: %s", s)
	}
	if ts, err := ParseTerms("# only\n\n#comments\n"); err != nil || len(ts) != 0 {
		t.Fatalf("comments only: %v %v", ts, err)
	}
	if _, err := ParseTerms("acme-secret\n# fine\nre:(unclosed"); err == nil || !strings.HasPrefix(err.Error(), "line 3: not a valid regular expression") {
		t.Fatalf("bad expression: %v", err)
	}
	if _, err := ParseTerms("re:"); err == nil || !strings.HasPrefix(err.Error(), "line 1: an empty re: term") {
		t.Fatalf("empty re: %v", err)
	}
}
