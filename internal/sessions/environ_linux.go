package sessions

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
)

// maxEnvironmentBytes bounds a process's environment as read: past it, it is not read.
const maxEnvironmentBytes = 4 << 20

// errEnvironmentTooLarge is an environment past maxEnvironmentBytes.
var errEnvironmentTooLarge = errors.New("the environment is too large to read")

// environOf is a process's environment from /proc/<pid>/environ.
func environOf(pid int) ([]string, error) {
	file, err := os.Open("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return nil, fmt.Errorf("reading the environment of process %d: %w", pid, err)
	}

	defer func() { _ = file.Close() }()

	raw, err := io.ReadAll(io.LimitReader(file, maxEnvironmentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading the environment of process %d: %w", pid, err)
	}

	if len(raw) > maxEnvironmentBytes {
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
