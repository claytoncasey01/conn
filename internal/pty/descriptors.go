//go:build darwin || linux

package pty

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

const (
	// descriptorsFolder lists the descriptors this process holds, by number.
	descriptorsFolder = "/dev/fd"
	// descriptorsSweptMax bounds the descriptors a spawn looks at: far above what Conn's process
	// holds. Past it the rest are left as they are.
	descriptorsSweptMax = 1 << 16
	// firstInherited is the first descriptor past stdin, stdout and stderr.
	firstInherited = 3
)

// markCloseOnExec sets close-on-exec on every descriptor this process holds past stderr, so a
// program spawned next inherits only what the spawn hands it. Go's os and net packages, and this
// package, open every descriptor so already; a framework the desktop app links (WebKit, Metal)
// or a raw syscall may not. A descriptor opened by another thread after the sweep is not covered.
// A descriptor that closes during the sweep is passed over. An error says the sweep could not be
// made, and the spawn is refused.
func markCloseOnExec() error {
	folder, err := os.Open(descriptorsFolder)
	if err != nil {
		return fmt.Errorf("listing the open descriptors: %w", err)
	}

	// A listing only read: nothing is lost if it fails to close.
	defer func() { _ = folder.Close() }()

	names, err := folder.Readdirnames(descriptorsSweptMax)
	if err != nil {
		if !errors.Is(err, io.EOF) {
			return fmt.Errorf("listing the open descriptors: %w", err)
		}
	}

	for _, name := range names {
		err = markOneCloseOnExec(name)
		if err != nil {
			return err
		}
	}

	return nil
}

// markOneCloseOnExec sets close-on-exec on the descriptor named name in descriptorsFolder, unless
// it is stdin, stdout or stderr, already has it, or is closed by now.
func markOneCloseOnExec(name string) error {
	descriptor, inherited := inheritedNumber(name)
	if !inherited {
		return nil
	}

	flags, open := descriptorFlags(descriptor)
	if !open {
		return nil
	}

	if flags&unix.FD_CLOEXEC != 0 {
		return nil
	}

	_, err := unix.FcntlInt(uintptr(descriptor), unix.F_SETFD, flags|unix.FD_CLOEXEC)
	if err != nil {
		return fmt.Errorf("marking descriptor %d close-on-exec: %w", descriptor, err)
	}

	return nil
}

// inheritedNumber is the descriptor name names, and whether it is one a program would inherit:
// a number past stderr.
func inheritedNumber(name string) (int, bool) {
	descriptor, err := strconv.Atoi(name)
	if err != nil {
		return 0, false
	}

	return descriptor, descriptor >= firstInherited
}

// descriptorFlags are the descriptor's flags, and whether it is still open: one listed may have
// closed since (the listing's own among them).
func descriptorFlags(descriptor int) (int, bool) {
	flags, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0)
	if err != nil {
		return 0, false
	}

	return flags, true
}
