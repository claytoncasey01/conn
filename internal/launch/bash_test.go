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
	"github.com/claytoncasey01/conn/internal/settings"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// interactiveTimeout bounds one interactive shell run.
const interactiveTimeout = 15 * time.Second

// TestThePastedLineIsInertInAnInteractiveBash: the line to paste, typed into an interactive
// bash on a terminal (the readline that acts on Ctrl-U and Ctrl-J, and in a C locale on every byte
// of 0x80 or more as a Meta key, before any quote is read), with no locale and with LANG=C, lands
// in the folder and hands the program exactly the arguments. A spec whose line a line editor could
// turn into another command gives no line: a control character starts nothing at all, and a
// character beyond printable ASCII starts inside Conn but offers nothing to paste.
func TestThePastedLineIsInertInAnInteractiveBash(t *testing.T) {
	t.Parallel()

	for _, locale := range [][]string{nil, {"LANG=C"}} {
		t.Run(strings.Join(append([]string{"locale"}, locale...), " "), func(t *testing.T) {
			t.Parallel()

			pasteIntoBash(t, locale)
		})
	}

	spec := pty.Spec{Argv: []string{"claude", "--name", "n"}, Dir: "/w", Env: nil, Name: "n"}

	for _, hostile := range []string{"/w/a\x15touch pwned\nb", "/w/a\x1b[Ab", "/w/a\u202eb"} {
		refused := spec
		refused.Dir = hostile

		if launch.Check(refused) == nil {
			t.Errorf("%q was taken: its line would reach a line editor", hostile)
		}
	}

	for _, beyond := range []string{"/w/x\u0112touch pwned;#", "/w/a\u2028b"} {
		shown := spec
		shown.Dir = beyond

		opened := launch.New(commandDeps())(t.Context(), shown)
		if opened.ManualCommand != "" {
			t.Errorf("%q offered a line to paste: %q", beyond, opened.ManualCommand)
		}
	}
}

// commandDeps are an opener's deps with sessions set to open as a command: nothing is called.
func commandDeps() launch.Deps {
	return launch.Deps{
		Sessions: func() settings.Sessions {
			return settings.Sessions{Where: settings.WhereCommand, ResumeOnLaunch: true}
		},
		Start: nil, Cmux: nil, LookPath: nil, Environ: nil,
	}
}

// pasteIntoBash types the line to paste for a hostile but printable folder and arguments into an
// interactive bash with locale, and checks it ran as the arguments say and nothing else ran.
func pasteIntoBash(t *testing.T, locale []string) {
	t.Helper()

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

	runInteractiveBash(t, typed, locale)

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
}

// runInteractiveBash types typed into an interactive bash, with no profile and the locale
// variables locale, on a terminal of its own, and waits for it to exit.
func runInteractiveBash(t *testing.T, typed string, locale []string) {
	t.Helper()

	process, err := pty.Spawn(pty.Spec{
		Argv: []string{"/bin/bash", "--norc", "--noprofile", "-i"}, Dir: t.TempDir(),
		Env:  append([]string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "PS1=$ "}, locale...),
		Name: "bash",
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
