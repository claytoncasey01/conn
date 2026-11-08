//go:build !darwin && !linux

package sessions

import (
	"errors"
	"fmt"
)

// environOf reads no environment on a system Conn does not run cmux on.
func environOf(pid int) ([]string, error) {
	return nil, fmt.Errorf("%w: process %d", errEnvironmentUnreadable, pid)
}

// errEnvironmentUnreadable is an environment that could not be read as one.
var errEnvironmentUnreadable = errors.New("the environment could not be read")
