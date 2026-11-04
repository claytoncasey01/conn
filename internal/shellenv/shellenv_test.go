package shellenv_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/run"
	"github.com/claytoncasey01/conn/internal/shellenv"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// shellRunsScript is a fake login shell: it prints a banner like a profile does, then runs the
// script it was given as its fourth argument (after -l -i -c).
const shellRunsScript = "#!/bin/sh\nprintf 'welcome\\n'\nexec sh -c \"$4\"\n"

// exePrintsEnvironment is a fake exe: the marker, then entries one of which holds a newline and
// one the value of a variable the app's environment sets.
const exePrintsEnvironment = "#!/bin/sh\n" +
	"printf '\\000CONN-ENV\\000'\n" +
	"printf 'B=9\\000C=line1\\nline2\\000SEEN=%s\\000junk\\000' \"$FROM_BASE\"\n"

// writeScript saves an executable script. Tests write every script before they run in parallel,
// because a process forked while a script is open for writing makes running it fail with "text
// file busy".
func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()

	path := filepath.Join(dir, name)

	err := os.WriteFile(path, []byte(body), 0o700)
	if err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}

	return path
}

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		output  string
		want    []string
		wantErr error
	}{
		{
			name: "entries after a banner",
			output: "welcome\n" + shellenv.Marker +
				"A=1\x00B=two words\x00C=line1\nline2\x00D=x=y\x00junk\x00=novalue\x00",
			want: []string{"A=1", "B=two words", "C=line1\nline2", "D=x=y"},
		},
		{
			name:   "an empty value is kept",
			output: shellenv.Marker + "A=\x00",
			want:   []string{"A="},
		},
		{
			name:   "an unfinished last entry is kept",
			output: shellenv.Marker + "A=1\x00B=2",
			want:   []string{"A=1", "B=2"},
		},
		{
			name:   "a second marker is only text",
			output: shellenv.Marker + "A=1\x00" + shellenv.Marker + "B=2\x00",
			want:   []string{"A=1", "B=2"},
		},
		{name: "marker and nothing else", output: shellenv.Marker, want: []string{}},
		{name: "no marker", output: "welcome\nA=1\x00", wantErr: shellenv.ErrNoEnvironment},
		{name: "no output", output: "", wantErr: shellenv.ErrNoEnvironment},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := shellenv.Parse([]byte(test.output))
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Parse error = %v, want %v", err, test.wantErr)
			}

			if !slices.Equal(got, test.want) {
				t.Errorf("Parse = %q, want %q", got, test.want)
			}
		})
	}
}

func TestMerge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		base      []string
		fromShell []string
		want      []string
	}{
		{
			name: "replace and add", base: []string{"A=1", "B=2"},
			fromShell: []string{"B=9", "C=3"}, want: []string{"A=1", "B=9", "C=3"},
		},
		{
			name: "nothing from the shell", base: []string{"A=1"}, fromShell: nil,
			want: []string{"A=1"},
		},
		{
			name: "nothing in base", base: nil, fromShell: []string{"A=1"},
			want: []string{"A=1"},
		},
		{
			name: "the last of two shell entries wins", base: []string{"A=1"},
			fromShell: []string{"B=1", "B=2"}, want: []string{"A=1", "B=2"},
		},
		{
			name: "a value holding an equals sign", base: []string{"A=x=y"},
			fromShell: []string{"A=p=q"}, want: []string{"A=p=q"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			baseBefore := slices.Clone(test.base)

			got := shellenv.Merge(test.base, test.fromShell)
			if !slices.Equal(got, test.want) {
				t.Errorf("Merge = %q, want %q", got, test.want)
			}

			if !slices.Equal(test.base, baseBefore) {
				t.Errorf("Merge changed base to %q", test.base)
			}
		})
	}
}

func TestArgs(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, shell string }{
		{name: "fish", shell: "/opt/homebrew/bin/fish"},
		{name: "zsh", shell: "/bin/zsh"},
		{name: "bash", shell: "/bin/bash"},
		{name: "sh", shell: "/bin/sh"},
		{name: "no shell", shell: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			want := []string{"-l", "-i", "-c", `"$CONN_ENV_EXE" --print-env`}

			got := shellenv.Args(test.shell)
			if !slices.Equal(got, want) {
				t.Errorf("Args(%q) = %q, want %q", test.shell, got, want)
			}

			if shellenv.Script != want[3] {
				t.Errorf("script = %q, want %q", shellenv.Script, want[3])
			}
		})
	}
}

func TestPrint(t *testing.T) {
	t.Parallel()

	environ := []string{"A=1", "B=two words", "C=line1\nline2"}

	var output bytes.Buffer

	err := shellenv.Print(&output, environ)
	if err != nil {
		t.Fatalf("Print: %v", err)
	}

	want := shellenv.Marker + "A=1\x00B=two words\x00C=line1\nline2\x00"
	if output.String() != want {
		t.Errorf("Print wrote %q, want %q", output.String(), want)
	}

	got, err := shellenv.Parse(output.Bytes())
	if err != nil {
		t.Fatalf("Parse of Print: %v", err)
	}

	if !slices.Equal(got, environ) {
		t.Errorf("Parse of Print = %q, want %q", got, environ)
	}
}

// failingWriter accepts writes until it has accepted writesMax, then fails every write.
type failingWriter struct {
	writesMax int
	writes    int
	err       error
}

func (w *failingWriter) Write(chunk []byte) (int, error) {
	if w.writes >= w.writesMax {
		return 0, w.err
	}

	w.writes++

	return len(chunk), nil
}

var errDisk = errors.New("disk full")

func TestPrintStopsAtTheFirstWriteError(t *testing.T) {
	t.Parallel()

	// Writes accepted before the failure: none (the marker fails), one (an entry fails).
	for _, writesMax := range []int{0, 1} {
		writer := &failingWriter{writesMax: writesMax, writes: 0, err: errDisk}

		err := shellenv.Print(writer, []string{"A=1", "B=2"})
		if !errors.Is(err, errDisk) {
			t.Errorf("Print with %d writes accepted: error = %v, want %v", writesMax, err, errDisk)
		}

		if writer.writes != writesMax {
			t.Errorf("Print with %d writes accepted wrote %d times", writesMax, writer.writes)
		}
	}
}

// loadCase is one row of TestLoad: a fake shell, the environment the app has, and what Load gives.
type loadCase struct {
	name          string
	shellBody     string
	base          []string
	want          []string
	wantErr       error
	wantRunFailed bool
}

// writeShells saves the fake shell of each case and returns their paths. A case with no body gets
// the path of a shell that does not exist.
func writeShells(t *testing.T, dir string, tests []loadCase) []string {
	t.Helper()

	shells := make([]string, len(tests))

	for index, test := range tests {
		shells[index] = filepath.Join(dir, "no such shell")
		if test.shellBody != "" {
			shells[index] = writeScript(t, dir, "shell"+string(rune('a'+index)), test.shellBody)
		}
	}

	return shells
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	exe := writeScript(t, dir, "fake conn app", exePrintsEnvironment)

	tests := []loadCase{
		{
			name: "the shell's entries merged over base", shellBody: shellRunsScript,
			base: []string{"A=1", "B=2", "FROM_BASE=kept"},
			want: []string{
				"A=1", "B=9", "FROM_BASE=kept", "C=line1\nline2", "SEEN=kept",
			},
		},
		{
			name: "the shell prints no environment", shellBody: "#!/bin/sh\nprintf 'hi\\n'\n",
			base: []string{"A=1"}, want: []string{"A=1"}, wantErr: shellenv.ErrNoEnvironment,
		},
		{
			name: "the shell exits 1", shellBody: "#!/bin/sh\nprintf 'hi\\n'\nexit 1\n",
			base: []string{"A=1"}, want: []string{"A=1"}, wantRunFailed: true,
		},
		{
			name: "the shell is missing", shellBody: "",
			base: []string{"A=1"}, want: []string{"A=1"}, wantRunFailed: true,
		},
	}

	shells := writeShells(t, dir, tests)

	t.Parallel()

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := shellenv.Load(context.Background(), shells[index], exe, test.base)

			if !slices.Equal(got, test.want) {
				t.Errorf("Load = %q, want %q", got, test.want)
			}

			var runErr *run.Error

			if errors.As(err, &runErr) != test.wantRunFailed {
				t.Errorf("Load error = %v, run failure expected: %v", err, test.wantRunFailed)
			}

			if !test.wantRunFailed {
				if !errors.Is(err, test.wantErr) {
					t.Errorf("Load error = %v, want %v", err, test.wantErr)
				}
			}
		})
	}
}

func TestLoadStopsAShellThatNeverAnswers(t *testing.T) {
	dir := t.TempDir()
	shell := writeScript(t, dir, "slow shell", "#!/bin/sh\nprintf 'welcome\\n'\nexec sleep 60\n")
	exe := writeScript(t, dir, "fake conn app", exePrintsEnvironment)
	base := []string{"A=1", "PATH=" + os.Getenv("PATH")}

	t.Parallel()

	started := time.Now()

	got, err := shellenv.LoadWith(
		context.Background(), shell, exe, base, testtime.Scaled(200*time.Millisecond),
	)

	var runErr *run.Error

	if !errors.As(err, &runErr) {
		t.Fatalf("Load error = %v, want a run failure", err)
	}

	if !runErr.Killed {
		t.Errorf("Load error = %v, want the shell killed", err)
	}

	if !slices.Equal(got, base) {
		t.Errorf("Load = %q, want base %q", got, base)
	}

	if elapsed := time.Since(started); elapsed > testtime.Scaled(20*time.Second) {
		t.Errorf("Load took %v, want it stopped near its limit", elapsed)
	}
}
