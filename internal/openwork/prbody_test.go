package openwork_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/claytoncasey01/conn/internal/openwork"
	"github.com/claytoncasey01/conn/internal/testtime"
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
		if strings.Contains(line, worktree+"/.conn") {
			t.Errorf("a call names the worktree's .conn: %q", line)
		}

		if strings.Contains(line, "--body=") {
			t.Errorf("the body is an argument: %q", line)
		}
	}

	if !made.has("sh gh pr create --repo acme/widgets --base main --head proj-3-fix " +
		"--title PROJ-3: ") {
		t.Error(strings.Join(made.lines(), "\n"))
	}

	// The body is read from gh's standard input ("-"), never a path and never an argument.
	if !made.countPrefixAny("--body-file -") {
		t.Error("gh was not told to read the body from its standard input")
	}

	if !made.has("stdin body") {
		t.Error("the body was not sent on gh's standard input")
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

// TestAPRBodyFileIsCheckedBeforeGhGetsIt: a session's body that is larger than a pull request
// body holds, holds a NUL byte or is not UTF-8 is refused with each reason, rather than sent
// changed or failing in gh.
func TestAPRBodyFileIsCheckedBeforeGhGetsIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		// says are the reasons the refusal gives; none when the body is taken.
		says []string
	}{
		{"plain text", []byte("## What changed\n\ndone"), nil},
		{"exactly the most", bytes.Repeat([]byte("a"), openwork.PRBodyBytesMax), nil},
		{
			"one byte more", bytes.Repeat([]byte("a"), openwork.PRBodyBytesMax+1),
			[]string{"larger than 65536 bytes"},
		},
		{"a NUL byte", []byte("a\x00b"), []string{"a NUL byte"}},
		{"not UTF-8", []byte("caf\xe9"), []string{"not UTF-8 text"}},
		{
			"all three", append(bytes.Repeat([]byte("\xe9"), openwork.PRBodyBytesMax), 0),
			[]string{"larger than 65536 bytes", "a NUL byte", "not UTF-8 text"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := openwork.CheckPRBody("/w/.conn/pr-body.md", tt.data)
			if len(tt.says) == 0 {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}

				return
			}

			if err == nil {
				t.Fatal("taken")
			}

			for _, reason := range tt.says {
				if !strings.Contains(err.Error(), reason) {
					t.Errorf("%q does not say %q", err, reason)
				}
			}
		})
	}
}

// TestAFallbackBodyIsValidTextWithinTheLimit: the body made from commit subjects is UTF-8 with
// no NUL, and never larger than a pull request body holds, however many commits there are.
func TestAFallbackBodyIsValidTextWithinTheLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, subjects string
	}{
		{"a few", "- one\n- two"},
		{"many", strings.Repeat("- a commit subject é\n", 20000)},
		{"broken bytes", "- caf\xe9\x00 bar"},
		// Two- and three-byte characters at every offset: one of these is cut inside one,
		// wherever the limit falls.
		{"two-byte characters", strings.Repeat("é", 40000)},
		{"two-byte characters, one byte on", "a" + strings.Repeat("é", 40000)},
		{"three-byte characters", strings.Repeat("€", 30000)},
		{"three-byte characters, one byte on", "a" + strings.Repeat("€", 30000)},
		{"three-byte characters, two bytes on", "ab" + strings.Repeat("€", 30000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body := openwork.FallbackBody(tt.subjects)
			if len(body) > openwork.PRBodyBytesMax {
				t.Errorf("%d bytes", len(body))
			}

			if !utf8.ValidString(body) {
				t.Error("not UTF-8")
			}

			if strings.Contains(body, "\x00") {
				t.Error("a NUL byte")
			}

			if !strings.HasSuffix(body, "Not recorded: the session did not write "+
				".conn/pr-body.md.\n") {
				t.Errorf("the ending was cut: %q", body[max(0, len(body)-80):])
			}
		})
	}
}

// TestAPRBodyIsReadOnlyFromARegularFile: a pipe (one a session still holds open would never
// end) or a folder at .conn/pr-body.md is refused at once, never waited on.
func TestAPRBodyIsReadOnlyFromARegularFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		make func(path string) error
	}{
		{"a pipe", func(path string) error { return syscall.Mkfifo(path, 0o600) }},
		{"a folder", func(path string) error { return os.Mkdir(path, 0o700) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conn := filepath.Join(t.TempDir(), ".conn")
			path := filepath.Join(conn, "pr-body.md")

			err := os.Mkdir(conn, 0o700)
			if err != nil {
				t.Fatal(err)
			}

			err = tt.make(path)
			if err != nil {
				t.Fatal(err)
			}

			read := make(chan error, 1)

			go func() {
				_, _, readErr := openwork.ReadPRBody(path)
				read <- readErr
			}()

			select {
			case err = <-read:
			case <-time.After(testtime.Scaled(5 * time.Second)):
				t.Fatal("the read waited on it")
			}

			if err == nil {
				t.Fatal("it was read")
			}

			if !strings.Contains(err.Error(), "not a regular file") {
				t.Errorf("error %q", err)
			}
		})
	}
}
