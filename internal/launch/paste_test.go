package launch_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// shellTimeout bounds one shell run.
const shellTimeout = 10 * time.Second

// TestThePastedLineRunsTheSameProgramInTheSameFolder: the line shown to paste, run by a real
// shell (each the person might use), lands in the session's folder and hands the program
// exactly the spec's arguments, however hostile the folder's name and the arguments are.
func TestThePastedLineRunsTheSameProgramInTheSameFolder(t *testing.T) {
	t.Parallel()

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	folder := filepath.Join(base, "-x it's $(id) `id` \"q\" \\ ;|&\n--dangerously")

	err = os.Mkdir(folder, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	spec := pty.Spec{
		Argv: []string{
			"claude", "--name", "a$(touch pwned)", "it's\n--dangerously-skip-permissions", "-x",
			"", "'", `\`, "~", "*", "{a,b}", "!!", "\u202e",
		},
		Dir: folder, Env: nil, Name: "n",
	}

	shells := map[string]string{
		"/bin/sh":   `claude() { printf '%s\0' "$PWD" "$@"; }; `,
		"/bin/bash": `claude() { printf '%s\0' "$PWD" "$@"; }; `,
		"/bin/zsh":  `claude() { printf '%s\0' "$PWD" "$@"; }; `,
		"fish":      `function claude; printf '%s\x00' $PWD $argv; end; `,
	}

	for shell, function := range shells {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			t.Parallel()

			path, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s is not on this machine", shell)
			}

			got := runPasted(t, path, function+launch.PasteLine(spec))
			want := append([]string{folder}, spec.Argv[1:]...)

			if !slices.Equal(got, want) {
				t.Errorf("got %q\nwant %q", got, want)
			}

			_, err = os.Stat(filepath.Join(folder, "pwned"))
			if err == nil {
				t.Error("a value ran")
			}
		})
	}
}

// runPasted runs script in shell from a folder of its own and answers what was printed, each
// ended by a NUL.
func runPasted(t *testing.T, shell, script string) []string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), testtime.Scaled(shellTimeout))
	defer cancel()

	// The person's own startup files stay out: fish reads config.fish and zsh reads .zshenv even
	// for -c, and a `cd` wrapped there (zoxide's, say) fails without the rest of that setup.
	noConfig := map[string][]string{"fish": {"--no-config"}, "zsh": {"-f"}}
	args := append(slices.Clone(noConfig[filepath.Base(shell)]), "-c", script)

	command := exec.CommandContext(ctx, shell, args...)
	command.Dir = t.TempDir()

	var stdout, stderr bytes.Buffer

	command.Stdout = &stdout
	command.Stderr = &stderr

	err := command.Run()
	if err != nil {
		t.Fatalf("%s: %v: %s", shell, err, stderr.String())
	}

	return strings.Split(strings.TrimSuffix(stdout.String(), "\x00"), "\x00")
}
