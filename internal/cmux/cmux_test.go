package cmux_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/cmux"
)

const sessionID = "0f8fad5b-d9cb-469f-a165-70867728950e"

func TestResumeCommand(t *testing.T) {
	t.Parallel()

	safe := regexp.MustCompile(
		`^claude --resume ` + sessionID + ` --name [A-Za-z0-9_][A-Za-z0-9._-]*$`)

	tests := []struct {
		name *string
		want string
	}{
		{new("PROJ-1893"), "claude --resume " + sessionID + " --name PROJ-1893"},
		{nil, "claude --resume " + sessionID},
		{new("a; rm -rf ~"), "claude --resume " + sessionID + " --name a-rm-rf"},
		{new("$(whoami)"), "claude --resume " + sessionID + " --name whoami"},
		{new(""), "claude --resume " + sessionID + " --name 0f8fad5b"},
		{new("\u65e5\u672c\u8a9e"), "claude --resume " + sessionID + " --name 0f8fad5b"},
		{new("---"), "claude --resume " + sessionID + " --name 0f8fad5b"},
	}

	for _, tt := range tests {
		got, err := cmux.ResumeCommand(sessionID, tt.name)
		if err != nil {
			t.Fatal(err)
		}

		if got != tt.want {
			t.Errorf("got %q, want %q", got, tt.want)
		}
	}

	for _, hostile := range []string{
		"`id`", "x\nrm -rf /", "it's", `say "hi"`, "-rf", "--dangerously-skip-permissions",
		"a && b", "a|b", "../../etc", ".hidden", "a b",
	} {
		got, _ := cmux.ResumeCommand(sessionID, &hostile)
		if !safe.MatchString(got) {
			t.Errorf("%q -> %q", hostile, got)
		}
	}

	long, _ := cmux.ResumeCommand(sessionID, new(strings.Repeat("a", 500)))
	if len(long) >= 150 {
		t.Errorf("a long name was not capped: %d", len(long))
	}

	for _, bad := range []string{
		"", "../x", "abc", sessionID + ";ls", sessionID + "\n", " " + sessionID,
		sessionID + "-extra",
	} {
		_, err := cmux.ResumeCommand(bad, new("n"))
		if err == nil {
			t.Errorf("%q was taken as a session id", bad)
		}
	}
}

func TestOpenHandsBackTheCommandToPaste(t *testing.T) {
	t.Parallel()

	opened := cmux.Open("/usr/bin/true")(t.Context(), "n", "/tmp", "claude --continue")
	if !opened.OK {
		t.Errorf("true failed: %+v", opened)
	}

	failed := cmux.Open("/usr/bin/false")(t.Context(), "n", "/tmp/it's here", "claude --continue")
	if failed.OK {
		t.Fatal("false succeeded")
	}

	if failed.ManualCommand != `cd '/tmp/it'\''s here' && claude --continue` {
		t.Errorf("manual command %q", failed.ManualCommand)
	}

	if failed.Error == "" {
		t.Error("no error")
	}

	missing := cmux.Open("/nonexistent/cmux")(t.Context(), "n", "/tmp", "claude --continue")
	if missing.OK {
		t.Error("a missing binary succeeded")
	}

	if !strings.Contains(missing.Error, "ENOENT") {
		if !strings.Contains(missing.Error, "nonexistent") {
			t.Errorf("error %q", missing.Error)
		}
	}
}
