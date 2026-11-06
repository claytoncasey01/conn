package sessions_test

import (
	"bufio"
	"encoding/binary"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/sessions"
)

// The cmux ids the environment helper is started with: the real one in its environment, the
// decoy only in its arguments.
const (
	realWorkspace  = "CMUX_WORKSPACE_ID=11111111-1111-1111-1111-111111111111"
	decoyWorkspace = "CMUX_WORKSPACE_ID=22222222-2222-2222-2222-222222222222"
	helperSwitch   = "CONN_ENV_HELPER=1"
)

// TestEnvironmentHelper is not a test: started by TestEnvReadsOnlyTheEnvironment, it waits for a
// line on its input, so its environment can be read meanwhile.
func TestEnvironmentHelper(t *testing.T) {
	t.Parallel()

	if os.Getenv("CONN_ENV_HELPER") != "1" {
		return
	}

	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')

	os.Exit(0)
}

// Env reads a process's environment, never its arguments: a cmux id written into a command
// line (a prompt, a ticket's text) is not an entry of it.
func TestEnvReadsOnlyTheEnvironment(t *testing.T) {
	t.Parallel()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	helper := exec.CommandContext(t.Context(), executable,
		"-test.run=^TestEnvironmentHelper$", "--", decoyWorkspace, "x "+decoyWorkspace)
	helper.Env = []string{helperSwitch, realWorkspace}

	input, err := helper.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}

	err = helper.Start()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = input.Close()
		_ = helper.Wait()
	})

	environment, err := sessions.Env(float64(helper.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(environment, realWorkspace) {
		t.Errorf("the environment %q lacks its own entry", environment)
	}

	for _, entry := range environment {
		if strings.Contains(entry, "2222") {
			t.Errorf("an argument was read as the environment: %q", entry)
		}
	}
}

// procArgs is a kern.procargs2 record: the argument count, the path with its padding, the
// arguments, then the environment.
func procArgs(path string, padding int, arguments, environment []string) []byte {
	var count uint32
	for range arguments {
		count++
	}

	raw := binary.LittleEndian.AppendUint32(nil, count)
	raw = append(raw, path...)
	raw = append(raw, make([]byte, 1+padding)...)

	for _, text := range append(slices.Clone(arguments), environment...) {
		raw = append(append(raw, text...), 0)
	}

	return append(raw, 0, 0)
}

// The environment of a kern.procargs2 record is what follows its arguments, entry by entry.
func TestProcArgsEnvironmentIsWhatFollowsTheArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		raw       []byte
		want      []string
		wantValid bool
	}{
		{
			"arguments holding an id", procArgs("/bin/claude", 3,
				[]string{"claude", decoyWorkspace}, []string{"HOME=/h", realWorkspace}),
			[]string{"HOME=/h", realWorkspace},
			true,
		},
		{"no environment", procArgs("/bin/claude", 0, []string{"claude"}, nil), nil, true},
		{
			"more arguments than the record holds", binary.LittleEndian.AppendUint32(nil, 9),
			nil, false,
		},
		{"too short for its count", []byte{1, 0}, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, valid := sessions.ProcArgsEnvironment(tt.raw)
			if valid != tt.wantValid {
				t.Fatalf("valid = %v, want %v", valid, tt.wantValid)
			}

			if !slices.Equal(got, tt.want) {
				t.Errorf("environment = %q, want %q", got, tt.want)
			}
		})
	}
}

// A cmux id is an entry of its own name, whole: one written into another entry's value, or
// with more after it, is not.
func TestACmuxIDIsAWholeEntry(t *testing.T) {
	t.Parallel()

	wantID := strings.TrimPrefix(realWorkspace, "CMUX_WORKSPACE_ID=")
	tests := []struct {
		name        string
		environment []string
		want        string
		wantFound   bool
	}{
		{"its own entry", []string{"HOME=/h", realWorkspace}, wantID, true},
		{
			"inside another value first",
			[]string{"NOTE=x " + decoyWorkspace, realWorkspace},
			wantID,
			true,
		},
		{"more after the id", []string{realWorkspace + " NOTE=x"}, "", false},
		{"none", []string{"HOME=/h"}, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, found := sessions.CmuxIDOf(tt.environment, "CMUX_WORKSPACE_ID")
			if found != tt.wantFound {
				t.Fatalf("found = %v, want %v", found, tt.wantFound)
			}

			if got != tt.want {
				t.Errorf("id = %q, want %q", got, tt.want)
			}
		})
	}
}
