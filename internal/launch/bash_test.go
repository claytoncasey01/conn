package launch_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// interactiveTimeout bounds one interactive shell run.
const interactiveTimeout = 15 * time.Second

// TestThePastedLineIsInertInAnInteractiveBash: the line to paste, typed into an interactive
// bash on a terminal (the readline that acts on Ctrl-U and Ctrl-J before any quote is read),
// lands in the folder and hands the program exactly the arguments; and every spec whose line a
// line editor could turn into another command is refused before any line is made.
func TestThePastedLineIsInertInAnInteractiveBash(t *testing.T) {
	t.Parallel()

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	folder := filepath.Join(base, "it's $(touch pwned) `id` \"q\" \\ ;|& !! ~ *")

	err = os.Mkdir(folder, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	spec := pty.Spec{
		Argv: []string{"claude", "--name", "a$(touch pwned)", "'", `\`, "", "-x"},
		Dir:  folder, Env: nil, Name: "n",
	}

	err = launch.Check(spec)
	if err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(base, "out")
	typed := "claude() { printf '%s\\0' \"$PWD\" \"$@\" > " + out + "; }\r" +
		launch.PasteLine(spec) + "\rexit\r"

	runInteractiveBash(t, typed)

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}

	words := strings.Split(strings.TrimSuffix(string(got), "\x00"), "\x00")
	if !slices.Equal(words, append([]string{folder}, spec.Argv[1:]...)) {
		t.Errorf("got %q", words)
	}

	for _, dir := range []string{folder, base} {
		_, err = os.Stat(filepath.Join(dir, "pwned"))
		if err == nil {
			t.Error("a value ran")
		}
	}

	for _, hostile := range []string{"/w/a\x15touch pwned\nb", "/w/a\x1b[Ab", "/w/a\u202eb"} {
		refused := spec
		refused.Dir = hostile

		if launch.Check(refused) == nil {
			t.Errorf("%q was taken: its line would reach a line editor", hostile)
		}
	}
}

// runInteractiveBash types typed into an interactive bash, with no profile, on a terminal of its
// own, and waits for it to exit.
func runInteractiveBash(t *testing.T, typed string) {
	t.Helper()

	process, err := pty.Spawn(pty.Spec{
		Argv: []string{"/bin/bash", "--norc", "--noprofile", "-i"}, Dir: t.TempDir(),
		Env: []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "PS1=$ "}, Name: "bash",
	})
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = process.Close() }()

	go drain(process)

	_, err = process.Write([]byte(typed))
	if err != nil {
		t.Fatal(err)
	}

	exited := make(chan int, 1)

	go func() { exited <- process.Wait() }()

	select {
	case <-exited:
	case <-time.After(testtime.Scaled(interactiveTimeout)):
		_ = process.Kill()

		t.Fatal("bash did not exit")
	}
}

// drain reads the terminal until it closes, so the shell never blocks writing.
func drain(process pty.Process) {
	var sink bytes.Buffer

	buffer := make([]byte, 4096)

	for {
		count, err := process.Read(buffer)
		if err != nil {
			return
		}

		sink.Reset()
		sink.Write(buffer[:count])
	}
}
