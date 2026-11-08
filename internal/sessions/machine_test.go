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
// line (a prompt, a ticket's text) is not an entry of it, nor does an empty first argument move
// where the environment starts.
func TestEnvReadsOnlyTheEnvironment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, first string
	}{
		{"a named program", "helper"},
		{"an empty first argument", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pid := startEnvironmentHelper(t, tt.first)

			environment, err := sessions.Env(float64(pid))
			if err != nil {
				t.Fatal(err)
			}

			if !slices.Equal(environment, []string{helperSwitch, realWorkspace}) {
				t.Errorf("environment = %q, want only its own two entries", environment)
			}
		})
	}
}

// startEnvironmentHelper starts the environment helper, its first argument first, its others
// holding a decoy cmux id, and its environment the real one, and returns its pid.
func startEnvironmentHelper(t *testing.T, first string) int {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	helper := exec.CommandContext(t.Context(), executable)
	helper.Args = []string{
		first, "-test.run=^TestEnvironmentHelper$", "--", decoyWorkspace, "x " + decoyWorkspace,
	}
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

	return helper.Process.Pid
}

// procArgs is a kern.procargs2 record: the argument count, the path padded to the kernel's
// alignment, the arguments, then the environment.
func procArgs(path string, arguments, environment []string) []byte {
	var count uint32
	for range arguments {
		count++
	}

	raw := binary.LittleEndian.AppendUint32(nil, count)
	raw = append(raw, path...)
	raw = append(raw, 0)

	for (len(raw)-4)%8 != 0 {
		raw = append(raw, 0)
	}

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
			"arguments holding an id", procArgs("/bin/claude",
				[]string{"claude", decoyWorkspace}, []string{"HOME=/h", realWorkspace}),
			[]string{"HOME=/h", realWorkspace},
			true,
		},
		{
			"an empty first argument", procArgs("/bin/x",
				[]string{"", decoyWorkspace, "5"}, []string{"A=1", realWorkspace}),
			[]string{"A=1", realWorkspace},
			true,
		},
		{
			"a path that ends on the alignment", procArgs("/bin/cl",
				[]string{"claude"}, []string{"A=1"}),
			[]string{"A=1"},
			true,
		},
		{"no environment", procArgs("/bin/claude", []string{"claude"}, nil), nil, true},
		{
			"padding that is not NULs",
			append(binary.LittleEndian.AppendUint32(nil, 0), "/bin/x\x00zz"...), nil, false,
		},
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
