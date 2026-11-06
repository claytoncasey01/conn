package reviews_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// The rest of the launch tests, on the rig in more_launch_test.go.

// TestDLaunchKeepsAQuotedPathWhole: a path with a space and a single quote reaches git and open
// as one intact argument.
func TestDLaunchKeepsAQuotedPathWhole(t *testing.T) {
	t.Parallel()

	repoPath := "/work/my code/it's-app"
	dataDir := "/data/ca c's"
	worktree := dataDir + "/reviews/pr-412-acme--widget-app/widget-app"

	rig := dNewRig()
	got := rig.launch(t, func(opts *reviews.LaunchOpts) {
		opts.RepoPath, opts.DataDir = repoPath, dataDir
	})

	if got != dLaunched(worktree) {
		t.Fatalf("Open = %+v", got)
	}

	dSameArgs(t, "fetch", rig.gits[0],
		[]string{"-C", repoPath, "fetch", "origin", "release/1.2"}, "")
	dSameArgs(t, "add", rig.gits[2], []string{
		"-C", repoPath, "worktree", "add", "--detach", worktree, "origin/release/1.2",
	}, "")

	if rig.opens[0].cwd != worktree {
		t.Errorf("cwd %q", rig.opens[0].cwd)
	}

	if rig.writes[0].path != worktree+"/.conn/prompt.md" {
		t.Errorf("prompt path %q", rig.writes[0].path)
	}
}

// TestDLaunchWorktreeIsThePRsOwnFolder: whatever the clone's folder is called.
func TestDLaunchWorktreeIsThePRsOwnFolder(t *testing.T) {
	t.Parallel()

	rig := dNewRig()
	got := rig.launch(t, func(opts *reviews.LaunchOpts) {
		opts.RepoPath = "/work/code/checkout-2/"
	})

	if got != dLaunched(dWorktree) {
		t.Fatalf("Open = %+v", got)
	}

	dSameArgs(t, "fetch", rig.gits[0],
		[]string{"-C", "/work/code/checkout-2/", "fetch", "origin", "release/1.2"}, "")
}

// TestDLaunchRefusesHostileInputsBeforeAnyCall: a hostile ref, number, name, repo, model, sha,
// clone or data folder makes zero calls. (The numbers 1.5, '12abc' and NaN cannot be an int64;
// the route test sends them as JSON.)
func TestDLaunchRefusesHostileInputsBeforeAnyCall(t *testing.T) {
	t.Parallel()

	badSha := "$(id)"
	tests := map[string]func(opts *reviews.LaunchOpts){
		"ref --upload-pack": func(o *reviews.LaunchOpts) { o.BaseRef = "--upload-pack=x" },
		"ref $()":           func(o *reviews.LaunchOpts) { o.BaseRef = "$(touch pwn)" },
		"ref a;b":           func(o *reviews.LaunchOpts) { o.BaseRef = "a;b" },
		"ref ../x":          func(o *reviews.LaunchOpts) { o.BaseRef = "../x" },
		"ref empty":         func(o *reviews.LaunchOpts) { o.BaseRef = "" },
		"number 0":          func(o *reviews.LaunchOpts) { o.Number = 0 },
		"number -3":         func(o *reviews.LaunchOpts) { o.Number = -3 },
		"name ; id":         func(o *reviews.LaunchOpts) { o.Name = "pr-412; id" },
		"name other":        func(o *reviews.LaunchOpts) { o.Name = "other" },
		"name pr-999":       func(o *reviews.LaunchOpts) { o.Name = "pr-999" },
		"name pr-412":       func(o *reviews.LaunchOpts) { o.Name = "pr-412" },
		"name pr-41.2":      func(o *reviews.LaunchOpts) { o.Name = "pr-41.2-acme--widget-app" },
		"name pr-4120":      func(o *reviews.LaunchOpts) { o.Name = "pr-4120-acme--widget-app" },
		"name pr-412.0":     func(o *reviews.LaunchOpts) { o.Name = "pr-412.0-acme--widget-app" },
		"name other owner":  func(o *reviews.LaunchOpts) { o.Name = "pr-412-other--widget-app" },
		"repo ../x":         func(o *reviews.LaunchOpts) { o.Repo = "../x" },
		"repo space":        func(o *reviews.LaunchOpts) { o.Repo = "acme/widget app" },
		"repo empty":        func(o *reviews.LaunchOpts) { o.Repo = "" },
		"model haiku":       func(o *reviews.LaunchOpts) { o.Model = "haiku" },
		"sha $(id)":         func(o *reviews.LaunchOpts) { o.ReReviewSha = &badSha },
		"clone empty":       func(o *reviews.LaunchOpts) { o.RepoPath = "" },
		"clone ..":          func(o *reviews.LaunchOpts) { o.RepoPath = ".." },
		"clone relative":    func(o *reviews.LaunchOpts) { o.RepoPath = "relative/clone" },
		"data empty":        func(o *reviews.LaunchOpts) { o.DataDir = "" },
	}

	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rig := dNewRig()
			got := rig.launch(t, change)

			if got.OK {
				t.Fatal("launched")
			}

			if got.Error == "" {
				t.Error("no reason given")
			}

			if len(rig.calls) != 0 {
				t.Errorf("calls %v", rig.calls)
			}
		})
	}
}

// TestDLaunchGitFailureIsTheFirstStderrLine: and cmux is never opened, nothing written.
func TestDLaunchGitFailureIsTheFirstStderrLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, fail string
		existing   []string
		failErr    error
		want       string
	}{
		{"fetch", "-C " + dClone + " fetch", nil, errDBoom, "fatal: boom"},
		{"worktree add", "-C " + dClone + " worktree add", nil, errDBoom, "fatal: boom"},
		{
			"checkout", "-C " + dWorktree + " checkout",
			[]string{dWorktree, dWorktree + "/.git"},
			errDPathspec, "error: pathspec nope",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := dNewRig(tt.existing...)
			rig.fail, rig.failErr = tt.fail, tt.failErr

			got := rig.launch(t, nil)
			if got != dRefused(tt.want) {
				t.Errorf("Open = %+v", got)
			}

			if slices.Contains(rig.calls, "open") {
				t.Error("cmux was opened")
			}

			if slices.Contains(rig.calls, "write") {
				t.Error("a file was written")
			}
		})
	}
}

// TestDLaunchGoesOnWhenTheExcludeLookupFails: the exclude file is best effort.
func TestDLaunchGoesOnWhenTheExcludeLookupFails(t *testing.T) {
	t.Parallel()

	rig := dNewRig()
	rig.fail = "rev-parse"

	got := rig.launch(t, nil)
	if got != dLaunched(dWorktree) {
		t.Errorf("Open = %+v", got)
	}

	if len(rig.opens) != 1 {
		t.Errorf("opens %+v", rig.opens)
	}
}

// TestDLaunchCmuxFailureGivesTheManualCommand: the worktree is left, nothing is removed.
func TestDLaunchCmuxFailureGivesTheManualCommand(t *testing.T) {
	t.Parallel()

	manual := "cd " + dWorktree + " && claude --name pr-412-acme--widget-app --model sonnet " +
		"'Read .conn/prompt.md and follow it exactly.'"
	rig := dNewRig()
	rig.opened = cmux.Opened{OK: false, Error: "cmux not found", ManualCommand: manual}

	got := rig.launch(t, nil)
	want := reviews.LaunchResult{
		OK: false, Worktree: dWorktree, Error: "cmux not found", ManualCommand: manual,
	}

	if got != want {
		t.Errorf("Open = %+v", got)
	}

	for _, call := range rig.gits {
		if slices.Contains(call.args, "remove") {
			t.Errorf("removed: %q", call.args)
		}
	}

	if rig.writes[0].path != dWorktree+"/.conn/prompt.md" {
		t.Errorf("prompt path %q", rig.writes[0].path)
	}
}

// TestDLaunchPromptIsInTheFileOnly: the sha and the prompt's text never reach the command.
func TestDLaunchPromptIsInTheFileOnly(t *testing.T) {
	t.Parallel()

	sha := "abc1234"
	rig := dNewRig()
	rig.launch(t, func(opts *reviews.LaunchOpts) { opts.ReReviewSha = &sha })

	if !strings.Contains(rig.writes[0].content, sha) {
		t.Errorf("the prompt has no sha: %s", rig.writes[0].content)
	}

	for _, part := range []string{sha, "deep-code-review"} {
		if strings.Contains(rig.opens[0].command, part) {
			t.Errorf("the command holds %q: %s", part, rig.opens[0].command)
		}
	}
}
