package pty

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// hostPath is the multiplexer that makes a new pseudo-terminal each time it is opened.
const hostPath = "/dev/ptmx"

// readModes is the request that reads a terminal's modes (termios); on a pty's host side it
// reads the program side's.
const readModes = unix.TIOCGETA

// terminalNameBytes is the size of the buffer TIOCPTYGNAME fills (sys/ttycom.h).
const terminalNameBytes = 128

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

	childDescriptor, err := openChildPath(path)
	if err != nil {
		_ = unix.Close(hostDescriptor)

		return nil, nil, err
	}

	return finishTerminal(hostDescriptor, childDescriptor, path)
}

// unlockTerminal grants and unlocks the program's side of the terminal and returns its path, as
// grantpt, unlockpt and ptsname do in macOS's libc.
func unlockTerminal(hostDescriptor int) (string, error) {
	err := unix.IoctlSetInt(hostDescriptor, unix.TIOCPTYGRANT, 0)
	if err != nil {
		return "", fmt.Errorf("granting the terminal: %w", err)
	}

	err = unix.IoctlSetInt(hostDescriptor, unix.TIOCPTYUNLK, 0)
	if err != nil {
		return "", fmt.Errorf("unlocking the terminal: %w", err)
	}

	var name [terminalNameBytes]byte

	// x/sys has no wrapper for TIOCPTYGNAME, which fills a buffer: the one raw ioctl here.
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(hostDescriptor),
		uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&name[0])))
	if errno != 0 {
		return "", fmt.Errorf("naming the terminal: %w", errno)
	}

	return unix.ByteSliceToString(name[:]), nil
}
