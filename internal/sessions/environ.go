package sessions

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// argcBytes is the size of the argument count kern.procargs2 starts with.
const argcBytes = 4

// procArgsEnvironment is the environment in a kern.procargs2 record: the argument count, the
// executable's path ended by a NUL, NULs to the next pointerBytes boundary of the strings, the
// arguments, then the environment's entries, each ended by a NUL, until an empty one. Only the
// alignment is skipped as padding, so an empty argument is counted as one. False for a record
// that does not have that shape.
func procArgsEnvironment(raw []byte) ([]string, bool) {
	if len(raw) < argcBytes {
		return nil, false
	}

	argc := int(binary.LittleEndian.Uint32(raw[:argcBytes]))
	area := raw[argcBytes:]

	pathEnd := bytes.IndexByte(area, 0)
	if pathEnd < 0 {
		return nil, false
	}

	start := alignedPast(pathEnd + 1)
	if start > len(area) {
		return nil, false
	}

	if bytes.ContainsFunc(area[pathEnd:start], func(char rune) bool { return char != 0 }) {
		return nil, false
	}

	fields := bytes.Split(area[start:], []byte{0})
	if argc > len(fields) {
		return nil, false
	}

	var environment []string

	for _, entry := range fields[argc:] {
		if len(entry) == 0 {
			break
		}

		environment = append(environment, string(entry))
	}

	return environment, true
}

// pointerBytes is the alignment the kernel pads the executable's path to.
const pointerBytes = 8

// alignedPast is offset rounded up to a multiple of pointerBytes.
func alignedPast(offset int) int {
	return (offset + pointerBytes - 1) / pointerBytes * pointerBytes
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
