package sessions_test

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/sessions"
)

// The environment part of a process's `ps eww` line is what follows its command line: a cmux
// id written into the command line (a prompt, a ticket's text) is never read as the
// environment's.
func TestTheEnvironmentIsWhatFollowsTheCommandLine(t *testing.T) {
	t.Parallel()

	decoy := "claude -p CMUX_WORKSPACE_ID=decoy CMUX_SURFACE_ID=decoy"
	tests := []struct {
		name, withEnvironment, command, want string
	}{
		{
			"an environment", decoy + " HOME=/h CMUX_WORKSPACE_ID=real", decoy + "\n",
			" HOME=/h CMUX_WORKSPACE_ID=real",
		},
		{"no environment", decoy, decoy + "\n", ""},
		{"the process changed between the two reads", "other HOME=/h", decoy + "\n", ""},
		{"no command line", "HOME=/h", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := sessions.EnvironmentPart(tt.withEnvironment, tt.command)
			if got != tt.want {
				t.Errorf("EnvironmentPart = %q, want %q", got, tt.want)
			}
		})
	}
}
