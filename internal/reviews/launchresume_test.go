package reviews_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

const (
	resumeID   = "0b6c4f7e-2d1a-4c3b-9e8f-112233445566"
	resumeName = "pr-12"
	claudeBin  = "/opt/claude"
	folderPerm = 0o700
)

// resumeWorld is Conn's reviews folder with one review worktree in it.
type resumeWorld struct {
	root, worktree string
}

func newResumeWorld(t *testing.T) resumeWorld {
	t.Helper()

	root := filepath.Join(t.TempDir(), "reviews")
	worktree := filepath.Join(root, resumeName, "widgets")

	err := os.MkdirAll(worktree, folderPerm)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: x\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return resumeWorld{root: root, worktree: worktree}
}

func (w resumeWorld) rule(disallowed []string) reviews.ResumeRule {
	return reviews.ResumeRule{
		Claude: claudeBin, Root: w.root, Pattern: regexp.MustCompile(`^pr-[0-9]+$`),
		Disallowed: disallowed, On: func() bool { return true },
		Allowed: func(string) bool { return true },
	}
}

func TestResumeAtLaunchResumesTheConversationUnderItsName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		disallowed []string
		wantArgv   []string
	}{
		{
			"a review", nil,
			[]string{claudeBin, "--resume", resumeID, "--name", resumeName},
		},
		{
			"a self-review keeps its rules",
			[]string{"Bash(git push:*)", "Bash(gh pr merge:*)"},
			[]string{
				claudeBin, "--disallowedTools", "Bash(git push:*)", "Bash(gh pr merge:*)",
				"--resume", resumeID, "--name", resumeName,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := newResumeWorld(t)

			got, ok := reviews.ResumeAtLaunch(world.rule(tt.disallowed), resumeName,
				world.worktree, resumeID)
			if !ok {
				t.Fatal("not resumed")
			}

			if !slices.Equal(got.Argv, tt.wantArgv) {
				t.Errorf("argv %q, want %q", got.Argv, tt.wantArgv)
			}

			if got.Dir != world.worktree {
				t.Errorf("folder %q, want %q", got.Dir, world.worktree)
			}

			if got.Name != resumeName {
				t.Errorf("name %q, want %q", got.Name, resumeName)
			}
		})
	}
}

// refusal is one record ResumeAtLaunch refuses: what is changed from a good one.
type refusal struct {
	name string
	// arrange changes the world and the rule, and is the name, worktree and id to check.
	arrange func(t *testing.T, world resumeWorld, rule *reviews.ResumeRule) (string, string, string)
}

func linkInPlace(t *testing.T, folder string) {
	t.Helper()

	moved := folder + "-real"

	err := os.Rename(folder, moved)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(moved, folder)
	if err != nil {
		t.Fatal(err)
	}
}

// good is the record of world's review session, which ResumeAtLaunch resumes.
func good(world resumeWorld) (string, string, string) {
	return resumeName, world.worktree, resumeID
}

// ruleRefusals are records refused for the module, the allowlist, the id or the name.
func ruleRefusals() []refusal {
	return []refusal{
		{"the module is off", func(_ *testing.T, world resumeWorld, rule *reviews.ResumeRule,
		) (string, string, string) {
			rule.On = func() bool { return false }

			return good(world)
		}},
		{"a repository off the allowlist", func(_ *testing.T, world resumeWorld,
			rule *reviews.ResumeRule,
		) (string, string, string) {
			rule.Allowed = func(string) bool { return false }

			return good(world)
		}},
		{"a session id that is not a UUID", func(_ *testing.T, world resumeWorld,
			_ *reviews.ResumeRule,
		) (string, string, string) {
			return resumeName, world.worktree, "--continue"
		}},
		{"a name of another kind", func(_ *testing.T, world resumeWorld, _ *reviews.ResumeRule,
		) (string, string, string) {
			return "--dangerously-skip-permissions", world.worktree, resumeID
		}},
	}
}

// folderRefusals are records refused for their folder.
func folderRefusals() []refusal {
	return []refusal{
		{"a folder outside Conn's", func(_ *testing.T, _ resumeWorld, _ *reviews.ResumeRule,
		) (string, string, string) {
			return resumeName, "/etc", resumeID
		}},
		{"a worktree of the person's own outside Conn's folder", func(t *testing.T,
			_ resumeWorld, _ *reviews.ResumeRule,
		) (string, string, string) {
			t.Helper()

			elsewhere := newResumeWorld(t)

			return resumeName, elsewhere.worktree, resumeID
		}},
		{"the reviews folder itself", func(_ *testing.T, world resumeWorld,
			_ *reviews.ResumeRule,
		) (string, string, string) {
			return resumeName, filepath.Join(world.root, resumeName), resumeID
		}},
		{"a path that climbs out", func(_ *testing.T, world resumeWorld, _ *reviews.ResumeRule,
		) (string, string, string) {
			return resumeName, world.root + "/" + resumeName + "/../../../etc", resumeID
		}},
		{"a worktree that is a link", func(t *testing.T, world resumeWorld,
			_ *reviews.ResumeRule,
		) (string, string, string) {
			t.Helper()
			linkInPlace(t, world.worktree)

			return good(world)
		}},
		{"a folder that is a link", func(t *testing.T, world resumeWorld, _ *reviews.ResumeRule,
		) (string, string, string) {
			t.Helper()
			linkInPlace(t, filepath.Dir(world.worktree))

			return good(world)
		}},
		{"no git worktree", func(t *testing.T, world resumeWorld, _ *reviews.ResumeRule,
		) (string, string, string) {
			t.Helper()

			err := os.Remove(filepath.Join(world.worktree, ".git"))
			if err != nil {
				t.Fatal(err)
			}

			return good(world)
		}},
	}
}

func TestResumeAtLaunchRefuses(t *testing.T) {
	t.Parallel()

	for _, tt := range slices.Concat(ruleRefusals(), folderRefusals()) {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := newResumeWorld(t)
			rule := world.rule(nil)
			name, worktree, sessionID := tt.arrange(t, world, &rule)

			got, ok := reviews.ResumeAtLaunch(rule, name, worktree, sessionID)
			if ok {
				t.Errorf("resumed %+v", got)
			}
		})
	}
}

func TestResumeAtLaunchRefusesARootThatIsALink(t *testing.T) {
	t.Parallel()

	world := newResumeWorld(t)
	linkInPlace(t, world.root)

	got, ok := reviews.ResumeAtLaunch(world.rule(nil), resumeName, world.worktree, resumeID)
	if ok {
		t.Errorf("resumed %+v", got)
	}
}
