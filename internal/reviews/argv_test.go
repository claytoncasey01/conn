package reviews_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// goldenSpec checks spec against testdata/name: the argv one argument a line as JSON, the
// folder and the name. CONN_UPDATE_GOLDEN=1 rewrites it.
func goldenSpec(t *testing.T, name string, spec pty.Spec) {
	t.Helper()

	got, err := json.MarshalIndent(map[string]any{
		"argv": spec.Argv, "dir": spec.Dir, "name": spec.Name,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join("testdata", name)

	if os.Getenv("CONN_UPDATE_GOLDEN") == "1" {
		err = os.WriteFile(path, append(got, '\n'), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(got)+"\n" != string(want) {
		t.Errorf("%s changed:\n%s\nwant:\n%s", name, got, want)
	}
}

// TestTheReviewSessionsArgvIsGolden: a review launch hands the opener claude, its arguments
// (the prompt stays in its file), the worktree and the review's name.
func TestTheReviewSessionsArgvIsGolden(t *testing.T) {
	t.Parallel()

	rig := dNewRig()
	deps := rig.deps()
	deps.Claude = "/opt/claude/bin/claude"

	var specs []pty.Spec

	deps.Open = func(_ context.Context, spec pty.Spec) launch.Opened {
		specs = append(specs, spec)

		return launch.Opened{
			OK: true, Where: "inside", HostedID: "", Place: "", Tried: "",
			Error: "", ManualCommand: "",
		}
	}

	got := reviews.Open(t.Context(), deps, reviews.LaunchOpts{
		Repo: dRepo, Number: 412, BaseRef: "release/1.2", Model: "sonnet",
		Advisor: "claude-code", Name: dName, RepoPath: dClone, DataDir: "/data/conn",
		ReReviewSha: nil,
	})
	if !got.OK {
		t.Fatalf("Open = %+v", got)
	}

	if len(specs) != 1 {
		t.Fatalf("opened %d", len(specs))
	}

	goldenSpec(t, "argv-review.json", specs[0])

	rules := reviews.ReviewDisallowed()
	if len(specs[0].Argv) < len(rules)+2 {
		t.Fatalf("argv %q is shorter than the rules", specs[0].Argv)
	}

	if specs[0].Argv[1] != "--disallowedTools" {
		t.Errorf("argv starts %q, want --disallowedTools", specs[0].Argv[:2])
	}

	if !slices.Equal(specs[0].Argv[2:2+len(rules)], rules) {
		t.Errorf("the rules after --disallowedTools are %q, want %q",
			specs[0].Argv[2:2+len(rules)], rules)
	}

	for _, argument := range specs[0].Argv {
		if strings.Contains(argument, "pull request #412") {
			t.Errorf("the prompt is in an argument: %q", argument)
		}
	}
}

// TestReviewDisallowedBlocksEveryGitHubWrite: a review session cannot post, merge or write to
// GitHub through gh, nor send anything to Slack, whatever its settings allow.
func TestReviewDisallowedBlocksEveryGitHubWrite(t *testing.T) {
	t.Parallel()

	rules := reviews.ReviewDisallowed()

	tests := slices.Concat([]string{
		"Bash(gh pr review:*)", "Bash(gh pr comment:*)", "Bash(gh pr merge:*)",
		"Bash(gh api --method POST:*)", "Bash(gh api -X POST:*)",
		"Bash(gh api --method PATCH:*)", "Bash(gh api -X PATCH:*)",
		"Bash(*post_review.py*)", "Bash(gh api *-X*)", "Bash(gh api *--method*)",
		"Bash(gh api *-f *)", "Bash(gh api *-F *)", "Bash(gh api *--field*)",
		"Bash(gh api *--raw-field*)", "Bash(gh api *--input*)", "Bash(gh api graphql*)",
		"Bash(gh api * -f*)", "Bash(gh api * -F*)", "Bash(gh api -f*)", "Bash(gh api -F*)",
		"Bash(gh issue comment:*)", "Bash(gh issue close:*)", "Bash(gh issue edit:*)",
		"Bash(gh pr edit:*)", "Bash(gh pr close:*)", "Bash(gh pr ready:*)",
		"Bash(gh pr reopen:*)",
	}, claude.SlackWrite())

	for _, rule := range tests {
		t.Run(rule, func(t *testing.T) {
			t.Parallel()

			if !slices.Contains(rules, rule) {
				t.Errorf("ReviewDisallowed() lacks %q", rule)
			}
		})
	}
}

// ruleMatches is whether a Bash permission rule refuses command, read as Claude Code reads one:
// a trailing ":*" matches any rest of the command and each other "*" any text.
func ruleMatches(rule, command string) bool {
	inner, isBash := strings.CutPrefix(rule, "Bash(")
	if !isBash {
		return false
	}

	inner = strings.TrimSuffix(inner, ")")
	inner = strings.Replace(inner, ":*", "*", 1)

	parts := strings.Split(inner, "*")
	for index, part := range parts {
		parts[index] = regexp.QuoteMeta(part)
	}

	return regexp.MustCompile("^" + strings.Join(parts, ".*") + "$").MatchString(command)
}

// TestReviewDisallowedRefusesWritesAndKeepsReads: each GitHub write a review session might
// try is refused by some rule, with a short flag glued to its value too, and a read of a repo
// whose name holds a dash and a letter stays allowed.
func TestReviewDisallowedRefusesWritesAndKeepsReads(t *testing.T) {
	t.Parallel()

	rules := reviews.ReviewDisallowed()

	tests := []struct {
		command     string
		wantRefused bool
	}{
		{"gh api repos/acme/web-frontend/pulls/1/comments -fbody=hi", true},
		{"gh api repos/acme/web-frontend/pulls/1/comments -Fline=3", true},
		{"gh api -fbody=hi repos/acme/app/issues/1/comments", true},
		{"gh api -Fbody=@file repos/acme/app/issues/1/comments", true},
		{"gh issue comment 7 --body hi", true},
		{"gh issue close 7", true},
		{"gh issue edit 7 --title x", true},
		{"gh pr edit 7 --title x", true},
		{"gh pr close 7", true},
		{"gh pr ready 7", true},
		{"gh pr reopen 7", true},
		{"gh pr review 7 --approve", true},
		{"gh api repos/acme/web-frontend/pulls/1/comments", false},
		{"gh api repos/acme/fast-Forward/pulls/1", false},
		{"gh pr view 7 --comments", false},
		{"gh pr diff 7", false},
		{"gh issue view 7", false},
	}

	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			t.Parallel()

			refused := slices.ContainsFunc(rules, func(rule string) bool {
				return ruleMatches(rule, test.command)
			})
			if refused != test.wantRefused {
				t.Errorf("refused = %v, want %v", refused, test.wantRefused)
			}
		})
	}
}
