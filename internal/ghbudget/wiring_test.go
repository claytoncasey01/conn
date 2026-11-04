package ghbudget_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTheServerMakesOneBudget: Bun's test reads index.ts for one `new GhBudget(` handed to both
// pollers. cmd/conn makes exactly one budget, in the parts both Reviews and My PRs are made from,
// and each hands that one to its poller.
func TestTheServerMakesOneBudget(t *testing.T) {
	t.Parallel()

	source := commandSource(t)

	tests := []struct {
		name    string
		pattern *regexp.Regexp
		want    int
	}{
		{"one budget is made", regexp.MustCompile(`ghbudget\.New\(`), 1},
		{"it goes into the shared parts", regexp.MustCompile(`budget:\s*ghbudget\.New\(\)`), 1},
		{"Reviews is made from the parts", regexp.MustCompile(`newReviews\(ctx, parts, live\)`), 1},
		{"My PRs is made from the parts", regexp.MustCompile(`newMyPrs\(ctx, parts, live\)`), 1},
		{"both pollers are given it", regexp.MustCompile(`Budget:\s*parts\.budget`), 2},
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
