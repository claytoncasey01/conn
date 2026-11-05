package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

var (
	errStdoutClosed = errors.New("stdout is closed")
	errNotAsked     = errors.New("the executable was not asked for")
)

// brokenWriter is an output that fails on every write.
type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errStdoutClosed }

// processWith is a process whose environment is environ and whose output goes to stdout and
// stderr, and that has no other input: a test of realMain touches nothing of the real process.
func processWith(environ []string, stdout, stderr *bytes.Buffer) process {
	return process{
		lookup:     func(string) (string, bool) { return "", false },
		environ:    func() []string { return environ },
		executable: func() (string, error) { return "", errNotAsked },
		stdout:     stdout,
		stderr:     stderr,
		splash:     "",
	}
}

func TestRealMainPrintsTheEnvironment(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	proc := processWith([]string{"A=1", "B=two words"}, &stdout, &stderr)

	code := realMain([]string{"conn-app", printEnvFlag}, proc)
	if code != exitOK {
		t.Errorf("realMain exit code = %d, want %d", code, exitOK)
	}

	for _, entry := range []string{"A=1\x00", "B=two words\x00"} {
		if !strings.Contains(stdout.String(), entry) {
			t.Errorf("the printed environment %q does not hold %q", stdout.String(), entry)
		}
	}

	if stderr.Len() != 0 {
		t.Errorf("realMain wrote %q to stderr, want nothing", stderr.String())
	}
}

func TestRealMainSaysWhenTheEnvironmentCannotBeWritten(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer

	proc := processWith([]string{"A=1"}, &bytes.Buffer{}, &stderr)
	proc.stdout = brokenWriter{}

	code := realMain([]string{"conn-app", printEnvFlag}, proc)
	if code != exitError {
		t.Errorf("realMain exit code = %d, want %d", code, exitError)
	}

	if !strings.Contains(stderr.String(), errStdoutClosed.Error()) {
		t.Errorf("realMain wrote %q to stderr, want %q in it", stderr.String(), errStdoutClosed)
	}
}
