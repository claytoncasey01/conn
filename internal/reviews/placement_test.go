package reviews_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

// readDiff is the fixture testdata/diffs/name.
func readDiff(t *testing.T, name string) string {
	t.Helper()

	diff, err := os.ReadFile(filepath.Join("testdata", "diffs", name))
	if err != nil {
		t.Fatal(err)
	}

	return string(diff)
}

// at is a position in the shortest spelling a table row can use.
func at(path string, line int64, side string) reviews.Position {
	return reviews.Position{Path: path, Line: line, Side: side}
}

func TestPositions(t *testing.T) {
	t.Parallel()

	const (
		right = reviews.SideRight
		left  = reviews.SideLeft
	)

	tests := []struct {
		name    string
		fixture string
		in      []reviews.Position
		out     []reviews.Position
	}{
		{
			name:    "an added line is on the right at its new number, not on the left",
			fixture: "added.diff",
			in:      []reviews.Position{at("a.go", 12, right)},
			out:     []reviews.Position{at("a.go", 13, left), at("a.go", 14, right)},
		},
		{
			name:    "context lines are on both sides at their own numbers",
			fixture: "added.diff",
			in: []reviews.Position{
				at("a.go", 10, right), at("a.go", 10, left),
				at("a.go", 13, right), at("a.go", 12, left),
			},
			out: []reviews.Position{at("a.go", 9, right), at("a.go", 9, left)},
		},
		{
			name:    "a removed line is on the left at its old number, not on the right",
			fixture: "removed.diff",
			in:      []reviews.Position{at("a.go", 21, left), at("a.go", 23, left)},
			out:     []reviews.Position{at("a.go", 22, right), at("a.go", 24, left)},
		},
		{
			name:    "a removed line that starts with dashes is a removed line, not a header",
			fixture: "removed.diff",
			in:      []reviews.Position{at("a.go", 22, left), at("a.go", 21, right)},
		},
		{
			name:    "a no-newline marker moves no counter",
			fixture: "nonewline.diff",
			in: []reviews.Position{
				at("a.go", 1, left), at("a.go", 1, right),
				at("a.go", 2, left), at("a.go", 2, right),
			},
			out: []reviews.Position{at("a.go", 3, left), at("a.go", 3, right)},
		},
		{
			name:    "a deleted file has no path to comment on",
			fixture: "deleted.diff",
			out: []reviews.Position{
				at("/dev/null", 1, left), at("/dev/null", 0, right), at("gone.go", 1, left),
			},
		},
		{
			name:    "a renamed file is keyed by its new path",
			fixture: "renamed.diff",
			in: []reviews.Position{
				at("new.go", 5, right), at("new.go", 6, left), at("new.go", 6, right),
			},
			out: []reviews.Position{at("old.go", 5, right), at("old.go", 6, left)},
		},
		{
			name:    "each file of a diff keeps its own hunks",
			fixture: "two-files.diff",
			in: []reviews.Position{
				at("a.go", 2, right), at("b.go", 8, right), at("b.go", 30, left),
				at("b.go", 31, right), at("b.go", 32, right),
			},
			out: []reviews.Position{
				at("a.go", 8, right), at("b.go", 2, right), at("b.go", 9, right),
				at("b.go", 30, right),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := reviews.Positions(readDiff(t, test.fixture))

			for _, position := range test.in {
				if !got[position] {
					t.Errorf("%+v is missing from %v", position, got)
				}
			}

			for _, position := range test.out {
				if got[position] {
					t.Errorf("%+v is there but GitHub would refuse it", position)
				}
			}
		})
	}
}

func TestPositionsOfNothing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		diff string
	}{
		{name: "an empty diff", diff: ""},
		{name: "text with no hunk", diff: "Binary files a/x.png and b/x.png differ\n"},
		{name: "a deleted file", diff: readDiff(t, "deleted.diff")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := reviews.Positions(test.diff)
			if len(got) != 0 {
				t.Errorf("positions = %v, want none", got)
			}
		})
	}
}

// TestPositionsStopAtTheDiffLimit: a diff past MaxDiffBytes is read only up to it, so the lines
// after the limit cannot be commented on.
func TestPositionsStopAtTheDiffLimit(t *testing.T) {
	t.Parallel()

	line := "+" + strings.Repeat("a", 1023) + "\n"
	count := reviews.MaxDiffBytes/len(line) + 10
	header := "+++ b/big.go\n@@ -0,0 +1," + strconv.Itoa(count) + " @@\n"
	diff := header + strings.Repeat(line, count)

	got := reviews.Positions(diff)

	if !got[at("big.go", 1, reviews.SideRight)] {
		t.Error("the first line of a big diff is missing")
	}

	if got[at("big.go", int64(count), reviews.SideRight)] {
		t.Error("a line past the limit is there")
	}
}

func TestInline(t *testing.T) {
	t.Parallel()

	positions := reviews.Positions(readDiff(t, "two-files.diff"))
	findings := []reviews.Finding{
		{Path: "a.go", Line: 2, Side: reviews.SideRight},
		{Path: "a.go", Line: 3, Side: reviews.SideLeft},
		{Path: "b.go", Line: 30, Side: reviews.SideLeft},
		{Path: "README.md", Line: 4, Side: reviews.SideRight},
	}
	want := []bool{true, false, true, false}

	got := reviews.Inline(findings, positions)

	if len(got) != len(want) {
		t.Fatalf("Inline gave %d answers for %d findings", len(got), len(want))
	}

	for index := range want {
		if got[index] != want[index] {
			t.Errorf("finding %d (%+v): inline = %v, want %v",
				index, findings[index], got[index], want[index])
		}
	}
}

func TestInlineOfNothing(t *testing.T) {
	t.Parallel()

	got := reviews.Inline(nil, reviews.Positions(readDiff(t, "added.diff")))
	if len(got) != 0 {
		t.Errorf("Inline of no findings = %v, want none", got)
	}
}

// goldenText checks got against testdata/name; CONN_UPDATE_GOLDEN=1 rewrites it.
func goldenText(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name)

	if os.Getenv("CONN_UPDATE_GOLDEN") == "1" {
		err := os.WriteFile(path, []byte(got), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if got != string(want) {
		t.Errorf("%s changed:\n%s\nwant:\n%s", name, got, want)
	}
}

func TestReviewBody(t *testing.T) {
	t.Parallel()

	outside := []reviews.Finding{
		{Path: "docs/README.md", Line: 4, Side: reviews.SideRight, Body: "P3: Say what it is."},
		{Path: "src/main.go", Line: 120, Side: reviews.SideLeft, Body: "P1: This\nspans two."},
	}

	t.Run("with nothing outside it is the summary unchanged", func(t *testing.T) {
		t.Parallel()

		got := reviews.ReviewBody("Looks good.\n\n", nil)
		if got != "Looks good.\n\n" {
			t.Errorf("body = %q, want the summary unchanged", got)
		}
	})

	t.Run("with findings outside they follow the summary", func(t *testing.T) {
		t.Parallel()

		goldenText(t, "review-body.txt", reviews.ReviewBody("Looks good.\n\n", outside))
	})
}
