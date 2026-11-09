package reviews_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

	for _, argument := range specs[0].Argv {
		if strings.Contains(argument, "pull request #412") {
			t.Errorf("the prompt is in an argument: %q", argument)
		}
	}
}
