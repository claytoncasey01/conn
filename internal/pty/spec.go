package pty

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
)

// checkSpec returns every problem with spec, each wrapping ErrInvalidSpec, or nil. No problem
// quotes the spec's values: an argument or a variable may hold something private.
func checkSpec(spec Spec) error {
	var problems []error

	problems = append(problems, checkArgv(spec.Argv)...)

	if !filepath.IsAbs(spec.Dir) {
		problems = append(problems, fmt.Errorf("%w: the folder must be an absolute path",
			ErrInvalidSpec))
	}

	if spec.Name == "" {
		problems = append(problems, fmt.Errorf("%w: the name is empty; give the session a name",
			ErrInvalidSpec))
	}

	if len(spec.Name) > nameBytesMax {
		problems = append(problems, fmt.Errorf("%w: the name is %d bytes, more than %d",
			ErrInvalidSpec, len(spec.Name), nameBytesMax))
	}

	if len(spec.Env) > environmentEntriesMax {
		problems = append(problems, fmt.Errorf("%w: the environment has %d entries, more than %d",
			ErrInvalidSpec, len(spec.Env), environmentEntriesMax))
	}

	return errors.Join(problems...)
}

// checkArgv returns the problems with a Spec's Argv.
func checkArgv(argv []string) []error {
	if len(argv) == 0 {
		return []error{fmt.Errorf("%w: argv is empty; give the program's absolute path first",
			ErrInvalidSpec)}
	}

	var problems []error

	if len(argv) > argumentsMax {
		problems = append(problems, fmt.Errorf("%w: argv has %d entries, more than %d",
			ErrInvalidSpec, len(argv), argumentsMax))
	}

	// An absolute path is run as it is: no PATH lookup can swap in another program.
	if !filepath.IsAbs(argv[0]) {
		problems = append(problems, fmt.Errorf("%w: the program must be an absolute path",
			ErrInvalidSpec))
	}

	return problems
}

// newID is a fresh ID: idBytes from crypto/rand, in hex.
func newID() (ID, error) {
	raw := make([]byte, idBytes)

	_, err := rand.Read(raw)
	if err != nil {
		return "", fmt.Errorf("making a session id: %w", err)
	}

	return ID(hex.EncodeToString(raw)), nil
}
