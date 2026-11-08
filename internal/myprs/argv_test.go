package myprs_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
)

// TestTheSelfReviewSessionsArgvIsGolden: a self-review hands the opener claude, the rules it
// can never use (closed by --name), its name and model, the instruction to read the prompt from
// its file, the worktree and the round's name. CONN_UPDATE_GOLDEN=1 rewrites the file.
func TestTheSelfReviewSessionsArgvIsGolden(t *testing.T) {
	t.Parallel()

	rig := eNewRig(nil)
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

	got := selfOf().Launch(t.Context(), deps, eOpts())
	if !got.OK {
		t.Fatalf("Launch = %+v", got)
	}

	if len(specs) != 1 {
		t.Fatalf("opened %d", len(specs))
	}

	text, err := json.MarshalIndent(map[string]any{
		"argv": specs[0].Argv, "dir": specs[0].Dir, "name": specs[0].Name,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join("testdata", "argv-self-review.json")

	if os.Getenv("CONN_UPDATE_GOLDEN") == "1" {
		err = os.WriteFile(path, append(text, '\n'), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(text)+"\n" != string(want) {
		t.Errorf("the self-review's argv changed:\n%s\nwant:\n%s", text, want)
	}

	for _, argument := range specs[0].Argv {
		if strings.Contains(argument, specs[0].Dir) {
			t.Errorf("the folder is in an argument: %q", argument)
		}
	}
}
