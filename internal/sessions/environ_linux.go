package sessions

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
)

// environOf is a process's environment from /proc/<pid>/environ.
func environOf(pid int) ([]string, error) {
	file, err := os.Open("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return nil, fmt.Errorf("reading the environment of process %d: %w", pid, err)
	}

	defer func() { _ = file.Close() }()

	raw, err := io.ReadAll(io.LimitReader(file, environmentBytesMax+1))
	if err != nil {
		return nil, fmt.Errorf("reading the environment of process %d: %w", pid, err)
	}

	if len(raw) > environmentBytesMax {
		return nil, fmt.Errorf("%w: process %d", errEnvironmentTooLarge, pid)
	}

	var environment []string

	for entry := range bytes.SplitSeq(raw, []byte{0}) {
		if len(entry) > 0 {
			environment = append(environment, string(entry))
		}
	}

	return environment, nil
}
