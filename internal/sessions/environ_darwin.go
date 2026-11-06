package sessions

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// environOf is a process's environment from the kernel's record of its arguments and
// environment (sysctl kern.procargs2). The kernel bounds the record, at kern.argmax.
func environOf(pid int) ([]string, error) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, fmt.Errorf("reading the environment of process %d: %w", pid, err)
	}

	environment, valid := procArgsEnvironment(raw)
	if !valid {
		return nil, fmt.Errorf("%w: process %d", errEnvironmentUnreadable, pid)
	}

	return environment, nil
}

// errEnvironmentUnreadable is an environment that could not be read as one.
var errEnvironmentUnreadable = errors.New("the environment could not be read")
