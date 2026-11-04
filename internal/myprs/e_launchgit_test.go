package myprs_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// The port of myPrsLaunch.git.test.ts: Launch against real git, a bare origin and its clones in
// a temp folder, so nothing reaches the network; cmux is a fake that only records. The fakes of
// e_launch_test.go answer whatever sha a test hands them: only real git can put a reused
// worktree ahead of the PR's head.

// eGitEnv is git with no global or system config, so a signing or hook setting of this machine
// never reaches a test.
func eGitEnv() []string {
	return append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@t")
}

// eRunGit runs git in dir; a failure's error is git's stderr, as the launch's git gives it.
func eRunGit(ctx context.Context, dir string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer

	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = dir
	command.Env = eGitEnv()
	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()
	if err != nil {
		return stdout.String(), &myprs.FailureError{Message: stderr.String() + err.Error()}
	}

	return stdout.String(), nil
}

// eMustGit runs git in dir and answers what it printed, trimmed.
func eMustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	out, err := eRunGit(t.Context(), dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}

	return strings.TrimSpace(out)
}

func eCommit(t *testing.T, dir, file string) {
	t.Helper()

	err := os.WriteFile(filepath.Join(dir, file), []byte(file), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	eMustGit(t, dir, "add", file)
	eMustGit(t, dir, "commit", "-qm", file)
}

// eGitWorld is a bare origin with a branch feat, a seed clone that pushes to it, the clone web
// the launches use, and the names of the workspaces opened.
type eGitWorld struct {
	seed, clone, data string
	mu                sync.Mutex
	opens             []string
}

// eNewGitWorld sets the world up. narrow clones once main and feat are on origin, then narrows
// the clone's fetch refspec to main: its origin/feat is the clone's, and neither a plain fetch
// of feat nor a push to it moves it.
func eNewGitWorld(t *testing.T, narrow bool) *eGitWorld {
	t.Helper()

	_, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}

	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")

	err = os.MkdirAll(bare, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	eMustGit(t, bare, "init", "--bare", "-q")
	eMustGit(t, root, "clone", "-q", bare, "seed")
	seed := filepath.Join(root, "seed")
	eCommit(t, seed, "a")
	eMustGit(t, seed, "push", "-q", "origin", "HEAD:feat")

	if narrow {
		eMustGit(t, seed, "push", "-q", "origin", "HEAD:main")
	}

	eMustGit(t, root, "clone", "-q", bare, "web")
	clone := filepath.Join(root, "web")

	if narrow {
		eMustGit(t, clone, "config", "remote.origin.fetch",
			"+refs/heads/main:refs/remotes/origin/main")
	}

	return &eGitWorld{
		seed: seed, clone: clone, data: filepath.Join(root, "data"), mu: sync.Mutex{},
		opens: nil,
	}
}

func (w *eGitWorld) deps() reviews.LaunchDeps {
	return reviews.LaunchDeps{
		Git: func(ctx context.Context, args []string, dir string) (string, error) {
			if dir == "" {
				dir = w.clone
			}

			return eRunGit(ctx, dir, args...)
		},
		Open: func(_ context.Context, name, _, _ string) cmux.Opened {
			w.mu.Lock()
			defer w.mu.Unlock()

			w.opens = append(w.opens, name)

			return cmux.Opened{OK: true, Error: "", ManualCommand: ""}
		},
		Write: reviews.WriteLaunchFile,
		Exists: func(path string) bool {
			_, err := os.Stat(path)

			return err == nil
		},
		Read: func(path string) (string, error) {
			data, err := os.ReadFile(path)

			return string(data), err
		},
		Origin: nil, Names: reviews.Names{Pattern: nil},
		Prompts: reviews.Prompts{Review: "", ReReview: ""},
	}
}

// round launches round of acme/web#12 from branch feat.
func (w *eGitWorld) round(t *testing.T, round float64) myprs.LaunchResult {
	t.Helper()

	name, err := selfOf().Name("acme/web", 12, round)
	if err != nil {
		t.Fatal(err)
	}

	return selfOf().Launch(t.Context(), w.deps(), myprs.LaunchOpts{
		Repo: "acme/web", Number: 12, HeadRef: "feat", Model: "sonnet", Name: name,
		RepoPath: w.clone, DataDir: w.data, Round: round,
	})
}

// originHead is origin/feat as the clone has it.
func (w *eGitWorld) originHead(t *testing.T) string {
	t.Helper()

	return eMustGit(t, w.clone, "rev-parse", "origin/feat")
}

// prHead is the PR's head as origin has it, not as the clone remembers it.
func (w *eGitWorld) prHead(t *testing.T) string {
	t.Helper()

	sha, _, _ := strings.Cut(eMustGit(t, w.seed, "ls-remote", "origin", "refs/heads/feat"), "\t")

	return sha
}

// firstRound launches round 1 and checks it went.
func (w *eGitWorld) firstRound(t *testing.T) myprs.LaunchResult {
	t.Helper()

	first := w.round(t, 1)
	if !first.OK {
		t.Fatalf("round 1: %+v", first)
	}

	return first
}

func TestELaunchGitRefusesAWorktreeAheadOfOrigin(t *testing.T) {
	t.Parallel()

	world := eNewGitWorld(t, false)
	first := world.firstRound(t)

	if first.HeadSha != world.originHead(t) {
		t.Errorf("round 1 at %s", first.HeadSha)
	}

	// Round 1's session commits a fix and is closed before it pushes.
	eCommit(t, first.Worktree, "b")
	local := eMustGit(t, first.Worktree, "rev-parse", "HEAD")

	second := world.round(t, 2)
	want := "remove the worktree and branch conn-self-12 (or push only if it belongs on the " +
		"PR), then press Re-review again: " + first.Worktree + " has work not on origin/feat"

	if second.OK {
		t.Fatal("round 2 went")
	}

	if second.Error != want {
		t.Errorf("error %q", second.Error)
	}

	if !slices.Equal(world.opens, []string{"self-web-12"}) {
		t.Errorf("opens %v", world.opens)
	}

	if eMustGit(t, first.Worktree, "rev-parse", "HEAD") != local {
		t.Error("the worktree was moved")
	}

	if eMustGit(t, first.Worktree, "status", "--porcelain") != "" {
		t.Error("the worktree is not clean")
	}

	prompt, err := os.ReadFile(filepath.Join(first.Worktree, ".conn", "prompt.md"))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(prompt), "round 1") {
		t.Error("the prompt was rewritten")
	}
}

func TestELaunchGitReusesAWorktree(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		narrow bool
		// pushed is the round's own push of a fix; else a push from elsewhere.
		pushed bool
	}{
		{"pushed", false, true},
		{"behind", false, false},
		{"narrow, pushed", true, true},
		{"narrow, pushed elsewhere", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := eNewGitWorld(t, tt.narrow)
			first := world.firstRound(t)

			if tt.pushed {
				eCommit(t, first.Worktree, "b")
				eMustGit(t, first.Worktree, "push", "-q", "origin", "conn-self-12:feat")
			} else {
				eCommit(t, world.seed, "c")
				eMustGit(t, world.seed, "push", "-q", "origin", "HEAD:feat")
			}

			second := world.round(t, 2)
			if !second.OK {
				t.Fatalf("round 2: %+v", second)
			}

			eCheckReused(t, world, first, second)
		})
	}
}

func eCheckReused(t *testing.T, world *eGitWorld, first, second myprs.LaunchResult) {
	t.Helper()

	head := world.prHead(t)

	if second.Worktree != first.Worktree {
		t.Errorf("worktree %s", second.Worktree)
	}

	if second.HeadSha != head {
		t.Errorf("round 2 at %s, the PR at %s", second.HeadSha, head)
	}

	if world.originHead(t) != head {
		t.Error("origin/feat is not the PR's head")
	}

	if eMustGit(t, second.Worktree, "rev-parse", "HEAD") != head {
		t.Error("the worktree is not at the PR's head")
	}

	if !slices.Equal(world.opens, []string{"self-web-12", "self-web-12-2"}) {
		t.Errorf("opens %v", world.opens)
	}
}
