package openwork_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const secretText = "-----BEGIN OPENSSH PRIVATE KEY----- not-for-github"

// TestAPRBodyIsNeverReadThroughALink: a .conn/pr-body.md that is a link to a secret, as a
// repository can commit one, is refused at OPEN PR and nothing of the secret reaches gh; the
// body gh gets is the file's own text, from memory, never a path in the worktree.
func TestAPRBodyIsNeverReadThroughALink(t *testing.T) {
	t.Parallel()

	made, worktree := openPRWorld(t, "")
	secret := filepath.Join(t.TempDir(), "id_ed25519")

	err := os.WriteFile(secret, []byte(secretText), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	body := filepath.Join(worktree, ".conn", "pr-body.md")

	err = os.Remove(body)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(secret, body)
	if err != nil {
		t.Fatal(err)
	}

	made.work.OpenPR("PROJ-3", "")
	made.work.Settle()

	for _, line := range made.lines() {
		if strings.Contains(line, "not-for-github") {
			t.Errorf("the secret reached a call: %q", line)
		}
	}

	if made.has("sh gh pr create") {
		t.Error("a PR was opened from a linked body")
	}

	kept, _ := os.ReadFile(secret)
	if string(kept) != secretText {
		t.Error("the secret was written through the link")
	}
}

// TestAPRBodyGoesToGhAsText: the session's own body reaches gh as text, never as a path.
func TestAPRBodyGoesToGhAsText(t *testing.T) {
	t.Parallel()

	made, worktree := openPRWorld(t, "")
	same(t, "ok", made.work.OpenPR("PROJ-3", "").Code, 200)
	until(t, "the PR", func() bool { return made.state("PROJ-3") == "pr_open" })

	for _, line := range made.lines() {
		if strings.Contains(line, "--body-file") {
			t.Errorf("gh was given a path: %q", line)
		}

		if strings.Contains(line, worktree+"/.conn") {
			t.Errorf("a call names the worktree's .conn: %q", line)
		}
	}

	if !made.has("sh gh pr create --repo acme/widgets --base main --head proj-3-fix " +
		"--title PROJ-3: ") {
		t.Error(strings.Join(made.lines(), "\n"))
	}

	if !made.countPrefixAny("--body=body") {
		t.Error("the body was not sent as text")
	}
}

// countPrefixAny is whether some call holds text.
func (w *world) countPrefixAny(text string) bool {
	for _, line := range w.lines() {
		if strings.Contains(line, text) {
			return true
		}
	}

	return false
}
