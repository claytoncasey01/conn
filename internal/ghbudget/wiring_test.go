package ghbudget_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTheServerMakesOneBudget: Bun's test reads index.ts for one `new GhBudget(` handed to both
// pollers. Go has no My PRs poller yet, so the half that can be checked is that cmd/conn makes
// exactly one budget and hands it to Reviews (My PRs, PR 9, gets the same one).
func TestTheServerMakesOneBudget(t *testing.T) {
	t.Parallel()

	source := commandSource(t)

	tests := []struct {
		name    string
		pattern *regexp.Regexp
		want    int
	}{
		{"one budget is made", regexp.MustCompile(`ghbudget\.New\(`), 1},
		{"Reviews is given it", regexp.MustCompile(`Budget:\s*parts\.budget`), 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := len(tt.pattern.FindAllString(source, -1))
			if got != tt.want {
				t.Errorf("%s appears %d times in cmd/conn, want %d", tt.pattern, got, tt.want)
			}
		})
	}
}

// commandSource is every non-test Go file of cmd/conn, joined.
func commandSource(t *testing.T) string {
	t.Helper()

	paths, err := filepath.Glob("../../cmd/conn/*.go")
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder

	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		out.Write(data)
	}

	return out.String()
}
