package openwork_test

import (
	"os"
	"slices"
	"testing"

	"github.com/claytoncasey01/conn/internal/openwork"
	"github.com/claytoncasey01/conn/internal/pty"
)

// The check a launch makes before it resumes a build session from the record of the hosted
// sessions: the record names a run and a worktree, and the session is the run's own.

// launchSpec is the session ResumeAtLaunch gives for the worktree of PROJ-1, and whether it
// gives one.
func launchSpec(made *world, worktree string) (pty.Spec, bool) {
	return made.work.ResumeAtLaunch("PROJ-1", worktree)
}

// ownResume is the session RESUME SESSION opens for the leg-th leg of PROJ-1.
func ownResume(made *world, leg int) pty.Spec {
	record := made.record("PROJ-1")
	spec, _ := openwork.ResumeSpec("claude", &record, record.Repos[leg], "claude-code")

	return spec
}

func sameSpec(t *testing.T, got, want pty.Spec) {
	t.Helper()

	if !slices.Equal(got.Argv, want.Argv) {
		t.Errorf("argv %q, want %q", got.Argv, want.Argv)
	}

	if got.Dir != want.Dir {
		t.Errorf("folder %q, want %q", got.Dir, want.Dir)
	}

	if got.Name != want.Name {
		t.Errorf("name %q, want %q", got.Name, want.Name)
	}
}

func TestResumeAtLaunchIsTheRunsOwnResume(t *testing.T) {
	t.Parallel()

	for _, layout := range []string{"split", "shared"} {
		t.Run(layout, func(t *testing.T) {
			t.Parallel()

			made, worktrees := resumable(t, layout)

			got, ok := launchSpec(made, worktrees[0])
			if !ok {
				t.Fatal("the lead's session is not resumed")
			}

			sameSpec(t, got, ownResume(made, 0))
		})
	}
}

func TestResumeAtLaunchOfTheSecondSplitSession(t *testing.T) {
	t.Parallel()

	made, worktrees := resumable(t, "split")

	got, ok := launchSpec(made, worktrees[1])
	if !ok {
		t.Fatal("the second session is not resumed")
	}

	sameSpec(t, got, ownResume(made, 1))
}

// launchRefusal is a record ResumeAtLaunch refuses: arrange makes the world and is the
// worktree the record names.
type launchRefusal struct {
	name    string
	arrange func(t *testing.T) (*world, string)
}

// recordRefusals are records that name no session of the run's own, or no conversation.
func recordRefusals() []launchRefusal {
	return []launchRefusal{
		{"a folder that is no slot of the run", func(t *testing.T) (*world, string) {
			t.Helper()

			made, _ := resumable(t, "split")

			return made, "/etc"
		}},
		{"a leg the shared session only reaches", func(t *testing.T) (*world, string) {
			t.Helper()

			made, worktrees := resumable(t, "shared")

			return made, worktrees[1]
		}},
		{"a session id that is not known", func(t *testing.T) (*world, string) {
			t.Helper()

			made, folders := legsWorld(t, "split", "")

			return made, folders.worktrees[0]
		}},
		{"a session id that is not a UUID", func(t *testing.T) (*world, string) {
			t.Helper()

			made, folders := legsWorld(t, "split", "", `{"claudeSessionId":"--continue"}`)

			return made, folders.worktrees[0]
		}},
	}
}

// moduleRefusals are records of a resumable run while Open work would start nothing real.
func moduleRefusals() []launchRefusal {
	known := `{"claudeSessionId":"` + sessionUUIDText + `"}`

	return []launchRefusal{
		{"a dry run", func(t *testing.T) (*world, string) {
			t.Helper()

			made, folders := legsWorldOf(t, true, "split", "", known, known)

			return made, folders.worktrees[0]
		}},
		{"Open work off", func(t *testing.T) (*world, string) {
			t.Helper()

			made, worktrees := resumable(t, "split")
			made.update("atlassian", `{"enabled":false,"jira":{"projects":["PROJ"],`+
				`"buildRepo":"acme/widgets"}}`)

			return made, worktrees[0]
		}},
	}
}

// runRefusals are records of a session the run would not reopen now.
func runRefusals() []launchRefusal {
	return []launchRefusal{
		{"a worktree that is gone", goneWorktree},
		{"a repository off the allowlist", offTheList},
		{"a session that runs there", func(t *testing.T) (*world, string) {
			t.Helper()

			made, worktrees := resumable(t, "split")
			made.setSessionIn(worktrees[0], found("70"))

			return made, worktrees[0]
		}},
		{"a run that is done", func(t *testing.T) (*world, string) {
			t.Helper()

			known := `{"claudeSessionId":"` + sessionUUIDText + `"}`
			made, folders := legsWorld(t, "split", `{"state":"done"}`, known)

			return made, folders.worktrees[0]
		}},
		{"a leg that is cleaned up", func(t *testing.T) (*world, string) {
			t.Helper()

			cleaned := `{"claudeSessionId":"` + sessionUUIDText + `","cleaned":true}`
			made, folders := legsWorld(t, "split", "", cleaned)

			return made, folders.worktrees[0]
		}},
	}
}

func TestResumeAtLaunchRefuses(t *testing.T) {
	t.Parallel()

	tests := slices.Concat(recordRefusals(), moduleRefusals(), runRefusals())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			made, worktree := tt.arrange(t)

			got, ok := launchSpec(made, worktree)
			if ok {
				t.Errorf("resumed %+v", got)
			}
		})
	}
}

func TestResumeAtLaunchRefusesAnotherRun(t *testing.T) {
	t.Parallel()

	made, worktrees := resumable(t, "split")

	got, ok := made.work.ResumeAtLaunch("PROJ-2", worktrees[0])
	if ok {
		t.Errorf("resumed %+v for a run that does not exist", got)
	}
}

func goneWorktree(t *testing.T) (*world, string) {
	t.Helper()

	made, worktrees := resumable(t, "split")

	err := os.RemoveAll(worktrees[0])
	if err != nil {
		t.Fatal(err)
	}

	return made, worktrees[0]
}

func offTheList(t *testing.T) (*world, string) {
	t.Helper()

	made, worktrees := resumable(t, "split")
	made.update("repos", `{"allow":["acme/gadgets"],"clones":{},"cloneRoot":null}`)

	return made, worktrees[0]
}

// A session resumed at launch is not in the session list until it registers: a RESUME SESSION
// meanwhile opens no second one on the same conversation.
func TestAResumeRightAfterTheLaunchsIsRefused(t *testing.T) {
	t.Parallel()

	made, worktrees := resumable(t, "split")

	_, ok := launchSpec(made, worktrees[0])
	if !ok {
		t.Fatal("the session is not resumed")
	}

	reply := made.work.Resume("PROJ-1", "acme/widgets")
	same(t, "refused", []any{reply.Code, errorOf(reply)}, []any{409, "acme/widgets: " +
		"a session was just opened for this run: give it a few seconds to appear"})
}
