package shellenv_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/shellenv"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// shellRunsScript is a fake login shell: it prints a banner like a profile does, then runs the
// script it was given as its fourth argument (after -l -i -c).
const shellRunsScript = "#!/bin/sh\nprintf 'welcome\\n'\nexec sh -c \"$4\"\n"

// exePrintsEnvironment is a fake exe: the marker, then entries one of which holds a newline, one
// the value of a variable the app's environment sets, and one the marker variable that Load gives
// the shell (as the real exe prints it), then the end marker.
const exePrintsEnvironment = "#!/bin/sh\n" +
	"printf '\\000CONN-ENV\\000'\n" +
	"printf 'B=9\\000C=line1\\nline2\\000SEEN=%s\\000' \"$FROM_BASE\"\n" +
	"printf 'CONN_ENV_EXE=%s\\000junk\\000' \"$CONN_ENV_EXE\"\n" +
	"printf '\\000CONN-ENV-END\\000'\n"

// tracedExport is what a shell prints to its error output for an export when its profile is
// traced with set -x. No error Load gives may hold any of it.
const tracedExport = "PROFILE_VALUE=do-not-show-this"

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
				"A=1\x00B=two words\x00C=line1\nline2\x00D=x=y\x00junk\x00=novalue\x00" +
				shellenv.MarkerEnd,
			want: []string{"A=1", "B=two words", "C=line1\nline2", "D=x=y"},
		},
		{
			name:   "an empty value is kept",
			output: shellenv.Marker + "A=\x00" + shellenv.MarkerEnd,
			want:   []string{"A="},
		},
		{
			name:   "an unfinished last entry is dropped",
			output: shellenv.Marker + "A=1\x00B=2" + shellenv.MarkerEnd,
			want:   []string{"A=1"},
		},
		{
			name:   "a terminal escape after the end marker is dropped",
			output: shellenv.Marker + "A=1\x00" + shellenv.MarkerEnd + "\x1b]7;file://host/p?x=y",
			want:   []string{"A=1"},
		},
		{
			name:   "a logout message after the end marker is dropped",
			output: shellenv.Marker + "A=1\x00" + shellenv.MarkerEnd + "logout\nB=2\x00",
			want:   []string{"A=1"},
		},
		{
			name:   "text with an equals sign and no entry before it is dropped",
			output: shellenv.Marker + "x=y" + shellenv.MarkerEnd,
			want:   []string{},
		},
		{
			name: "a second marker is only text",
			output: shellenv.Marker + "A=1\x00" + shellenv.Marker + "B=2\x00" +
				shellenv.MarkerEnd,
			want: []string{"A=1", "B=2"},
		},
		{
			name:   "the markers and nothing between",
			output: shellenv.Marker + shellenv.MarkerEnd,
			want:   []string{},
		},
		{
			name:    "an environment cut short has no end marker",
			output:  shellenv.Marker + "A=1\x00B=2\x00",
			wantErr: shellenv.ErrNoEnvironment,
		},
		{
			name:    "an end marker before the marker does not end it",
			output:  shellenv.MarkerEnd + shellenv.Marker + "A=1\x00",
			wantErr: shellenv.ErrNoEnvironment,
		},
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

	// Fish, zsh, bash and sh all take these flags, so one list serves every shell.
	want := []string{"-l", "-i", "-c", `"$CONN_ENV_EXE" --print-env`}

	got := shellenv.Args()
	if !slices.Equal(got, want) {
		t.Errorf("Args() = %q, want %q", got, want)
	}

	if shellenv.Script != want[3] {
		t.Errorf("script = %q, want %q", shellenv.Script, want[3])
	}
}

func TestPrint(t *testing.T) {
	t.Parallel()

	// Entries with no '=' or no name are not written: Parse would drop them.
	environ := []string{"A=1", "junk", "B=two words", "=novalue", "C=line1\nline2"}
	entries := []string{"A=1", "B=two words", "C=line1\nline2"}

	var output bytes.Buffer

	err := shellenv.Print(&output, environ)
	if err != nil {
		t.Fatalf("Print: %v", err)
	}

	want := shellenv.Marker + "A=1\x00B=two words\x00C=line1\nline2\x00" + shellenv.MarkerEnd
	if output.String() != want {
		t.Errorf("Print wrote %q, want %q", output.String(), want)
	}

	got, err := shellenv.Parse(output.Bytes())
	if err != nil {
		t.Fatalf("Parse of Print: %v", err)
	}

	if !slices.Equal(got, entries) {
		t.Errorf("Parse of Print = %q, want %q", got, entries)
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

	// Writes accepted before the failure: none (the marker fails), one (an entry fails), three
	// (the end marker fails).
	for _, writesMax := range []int{0, 1, 3} {
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
	name      string
	shellBody string
	base      []string
	want      []string
	wantErr   error
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

// loadCases are TestLoad's rows.
func loadCases() []loadCase {
	merged := []string{"A=1", "B=9", "FROM_BASE=kept", "C=line1\nline2", "SEEN=kept"}
	base := []string{"A=1", "B=2", "FROM_BASE=kept"}

	return []loadCase{
		{
			name:      "the shell's entries merged over base, without the app's marker variable",
			shellBody: shellRunsScript,
			base:      base, want: merged, wantErr: nil,
		},
		{
			name:      "a complete environment is used though the shell then exits 3",
			shellBody: "#!/bin/sh\nsh -c \"$4\"\nexit 3\n",
			base:      base, want: merged, wantErr: nil,
		},
		{
			name: "the shell prints no environment", shellBody: "#!/bin/sh\nprintf 'hi\\n'\n",
			base: []string{"A=1"}, want: []string{"A=1"}, wantErr: shellenv.ErrNoEnvironment,
		},
		{
			name: "the shell exits 1", shellBody: "#!/bin/sh\nprintf 'hi\\n'\nexit 1\n",
			base: []string{"A=1"}, want: []string{"A=1"}, wantErr: shellenv.ErrShellFailed,
		},
		{
			name:      "the shell's error output is never in the error",
			shellBody: "#!/bin/sh\nprintf '+ export " + tracedExport + "\\n' >&2\nexit 1\n",
			base:      []string{"A=1"}, want: []string{"A=1"}, wantErr: shellenv.ErrShellFailed,
		},
		{
			name: "the shell prints more than the cap",
			shellBody: fmt.Sprintf("#!/bin/sh\nhead -c %d /dev/zero\nexec sh -c \"$4\"\n",
				shellenv.OutputBytesMax+1),
			base: []string{"A=1"}, want: []string{"A=1"}, wantErr: shellenv.ErrOutputTooLarge,
		},
		{
			name: "the shell is missing", shellBody: "",
			base: []string{"A=1"}, want: []string{"A=1"}, wantErr: shellenv.ErrShellFailed,
		},
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	exe := writeScript(t, dir, "fake conn app", exePrintsEnvironment)
	tests := loadCases()
	shells := writeShells(t, dir, tests)

	t.Parallel()

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := shellenv.Load(context.Background(), shells[index], exe, test.base)

			if !slices.Equal(got, test.want) {
				t.Errorf("Load = %q, want %q", got, test.want)
			}

			if !errors.Is(err, test.wantErr) {
				t.Errorf("Load error = %v, want %v", err, test.wantErr)
			}

			if err != nil {
				checkShort(t, err)
			}
		})
	}
}

// checkShort fails the test when an error from Load holds what a shell printed, or is longer than
// a short reason.
func checkShort(t *testing.T, err error) {
	t.Helper()

	const reasonBytesMax = 200

	if strings.Contains(err.Error(), tracedExport) {
		t.Errorf("Load error %q holds the shell's error output", err)
	}

	if len(err.Error()) > reasonBytesMax {
		t.Errorf("Load error is %d bytes, want at most %d", len(err.Error()), reasonBytesMax)
	}
}

// TestLoadKeepsAnEnvironmentABackgroundJobHolds is a profile that leaves a job running in the
// background with the shell's output open (cmd &, a plugin's update check): the run fails once
// the output has stayed open too long, but the environment was printed whole and is used, and
// the wait for the job is bounded.
func TestLoadKeepsAnEnvironmentABackgroundJobHolds(t *testing.T) {
	dir := t.TempDir()
	shell := writeScript(t, dir, "shell with a job", "#!/bin/sh\nsleep 30 &\nexec sh -c \"$4\"\n")
	exe := writeScript(t, dir, "fake conn app", exePrintsEnvironment)
	base := []string{"A=1", "FROM_BASE=kept"}

	t.Parallel()

	started := time.Now()

	got, err := shellenv.Load(context.Background(), shell, exe, base)
	if err != nil {
		t.Fatalf("Load error = %v, want the environment", err)
	}

	want := []string{"A=1", "FROM_BASE=kept", "B=9", "C=line1\nline2", "SEEN=kept"}
	if !slices.Equal(got, want) {
		t.Errorf("Load = %q, want %q", got, want)
	}

	// The job outlasts Load's own limit, so an output wait that was not cut short would end in an
	// error above. The bound has room for a busy machine starting the scripts slowly.
	if elapsed := time.Since(started); elapsed > testtime.Scaled(8*time.Second) {
		t.Errorf("Load took %v, want it to wait for the job no longer than run's limit", elapsed)
	}
}

// TestLoadStopsAShellThatNeverAnswers is a shell that starts the environment and then hangs, so
// it never prints the end marker: it is stopped at the time limit and the environment is not used.
func TestLoadStopsAShellThatNeverAnswers(t *testing.T) {
	dir := t.TempDir()
	shell := writeScript(t, dir, "slow shell",
		"#!/bin/sh\nprintf 'welcome\\n\\000CONN-ENV\\000B=9\\000'\nexec sleep 60\n")
	exe := writeScript(t, dir, "fake conn app", exePrintsEnvironment)
	base := []string{"A=1", "PATH=" + os.Getenv("PATH")}

	t.Parallel()

	started := time.Now()

	got, err := shellenv.LoadWith(
		context.Background(), shell, exe, base, testtime.Scaled(200*time.Millisecond),
	)
	if !errors.Is(err, shellenv.ErrTimedOut) {
		t.Errorf("Load error = %v, want %v", err, shellenv.ErrTimedOut)
	}

	if !slices.Equal(got, base) {
		t.Errorf("Load = %q, want base %q", got, base)
	}

	if elapsed := time.Since(started); elapsed > testtime.Scaled(20*time.Second) {
		t.Errorf("Load took %v, want it stopped near its limit", elapsed)
	}
}
