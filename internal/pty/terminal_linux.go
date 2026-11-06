package pty

import (
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// hostPath is the multiplexer that makes a new pseudo-terminal each time it is opened.
const hostPath = "/dev/ptmx"

// childFolder is where devpts puts each terminal's program side, named by its number.
const childFolder = "/dev/pts/"

// openTerminal makes a new pseudo-terminal: its host side and its program side.
func openTerminal() (*os.File, *os.File, error) {
	hostDescriptor, err := openHostSide()
	if err != nil {
		return nil, nil, err
	}

	path, err := unlockTerminal(hostDescriptor)
	if err != nil {
		_ = unix.Close(hostDescriptor)

		return nil, nil, err
	}

	return openChildSide(hostDescriptor, path)
}

// unlockTerminal unlocks the program's side of the terminal and returns its path, as unlockpt
// and ptsname do in glibc.
func unlockTerminal(hostDescriptor int) (string, error) {
	err := unix.IoctlSetPointerInt(hostDescriptor, unix.TIOCSPTLCK, 0)
	if err != nil {
		return "", fmt.Errorf("unlocking the terminal: %w", err)
	}

	number, err := unix.IoctlGetUint32(hostDescriptor, unix.TIOCGPTN)
	if err != nil {
		return "", fmt.Errorf("naming the terminal: %w", err)
	}

	return childFolder + strconv.FormatUint(uint64(number), 10), nil
}
