package main

import (
	"bytes"
	"testing"

	"github.com/claytoncasey01/conn"
)

func TestVersionCommand(t *testing.T) {
	t.Parallel()

	for _, arg := range []string{"version", "--version", "-v"} {
		t.Run(arg, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			code := realMain([]string{arg}, func(string) (string, bool) { return "", false },
				&stdout, &stderr)
			if code != exitOK {
				t.Errorf("exit = %d, want %d", code, exitOK)
			}

			if stdout.String() != "conn "+conn.Version()+"\n" {
				t.Errorf("stdout = %q, want conn %s", stdout.String(), conn.Version())
			}

			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want nothing", stderr.String())
			}
		})
	}
}
