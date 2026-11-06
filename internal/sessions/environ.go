package sessions

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
)

// argcBytes is the size of the argument count kern.procargs2 starts with.
const argcBytes = 4

// errEnvironmentTooLarge is an environment past environmentBytesMax.
var errEnvironmentTooLarge = errors.New("the environment is too large to read")

// procArgsEnvironment is the environment in a kern.procargs2 record: the argument count, the
// executable's path, padding, the arguments, then the environment's entries, each ended by a
// NUL, until an empty one. False for a record that does not have that shape.
func procArgsEnvironment(raw []byte) ([]string, bool) {
	if len(raw) < argcBytes {
		return nil, false
	}

	argc := int(binary.LittleEndian.Uint32(raw[:argcBytes]))
	fields := bytes.Split(raw[argcBytes:], []byte{0})

	// The executable's path, then the NULs that pad it, then the arguments.
	index := 1
	for index < len(fields) {
		if len(fields[index]) > 0 {
			break
		}

		index++
	}

	index += argc
	if index > len(fields) {
		return nil, false
	}

	var environment []string

	for _, entry := range fields[index:] {
		if len(entry) == 0 {
			break
		}

		environment = append(environment, string(entry))
	}

	return environment, true
}

// environmentValue is the value of the entry named name in environment, and false when there
// is none. The first entry of that name counts, as getenv reads it.
func environmentValue(environment []string, name string) (string, bool) {
	for _, entry := range environment {
		value, found := strings.CutPrefix(entry, name+"=")
		if found {
			return value, true
		}
	}

	return "", false
}
