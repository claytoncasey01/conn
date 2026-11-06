// Package terminal says whether an output stream is a terminal, the one place Conn prints its
// secret.
package terminal

import "os"

// Is says whether file is a character device: a terminal, or a sink such as /dev/null that keeps
// nothing. A pipe, a file, a socket or a log is not, so what is printed only to a terminal is
// never written where it would be kept or read by another program.
func Is(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}
