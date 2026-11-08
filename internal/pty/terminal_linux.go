package pty

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// hostPath is the multiplexer that makes a new pseudo-terminal each time it is opened.
const hostPath = "/dev/ptmx"

// readModes is the request that reads a terminal's modes (termios); on a pty's host side it
// reads the program side's.
const readModes = unix.TCGETS

// childFolder is where devpts puts each terminal's program side, named by its number.
const childFolder = "/dev/pts/"

// peerFlags are how TIOCGPTPEER opens the program's side: read and write, not as Conn's
// controlling terminal, close-on-exec.
const peerFlags = unix.O_RDWR | unix.O_NOCTTY | unix.O_CLOEXEC

// openTerminal makes a new pseudo-terminal: its host side and its program side.
func openTerminal() (*os.File, *os.File, error) {
	hostDescriptor, err := openHostSide()
	if err != nil {
		return nil, nil, err
	}

	childDescriptor, path, err := openPeer(hostDescriptor)
	if err != nil {
		_ = unix.Close(hostDescriptor)

		return nil, nil, err
	}

	return finishTerminal(hostDescriptor, childDescriptor, path)
}

// openPeer unlocks the program's side of the terminal and opens it. TIOCGPTPEER (Linux 4.13)
// opens it from the host side itself, with no path to look up; an older kernel, which answers
// EINVAL or ENOTTY, gets the path from TIOCGPTN instead, as unlockpt and ptsname do in glibc. The
// path is also the file's name.
func openPeer(hostDescriptor int) (int, string, error) {
	err := unix.IoctlSetPointerInt(hostDescriptor, unix.TIOCSPTLCK, 0)
	if err != nil {
		return 0, "", fmt.Errorf("unlocking the terminal: %w", err)
	}

	number, err := unix.IoctlGetUint32(hostDescriptor, unix.TIOCGPTN)
	if err != nil {
		return 0, "", fmt.Errorf("naming the terminal: %w", err)
	}

	path := childFolder + strconv.FormatUint(uint64(number), 10)

	descriptor, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(hostDescriptor),
		unix.TIOCGPTPEER, peerFlags)
	if errno == 0 {
		return int(descriptor), path, nil
	}

	if !peerUnsupported(errno) {
		return 0, "", fmt.Errorf("opening the program's side of the terminal: %w", errno)
	}

	childDescriptor, err := openChildPath(path)
	if err != nil {
		return 0, "", err
	}

	return childDescriptor, path, nil
}

// peerUnsupported says whether errno is a kernel without TIOCGPTPEER.
func peerUnsupported(errno unix.Errno) bool {
	if errors.Is(errno, unix.EINVAL) {
		return true
	}

	return errors.Is(errno, unix.ENOTTY)
}
