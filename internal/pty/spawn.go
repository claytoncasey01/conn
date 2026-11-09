//go:build darwin || linux

package pty

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// The terminal's limits and defaults.
const (
	// writeTimeout bounds one Write to a program that has stopped reading its input.
	writeTimeout = 5 * time.Second
	// exitCodeUnknown is the exit code of a program whose end could not be read.
	exitCodeUnknown = -1
	// exitCodeSignalBase is added to the signal that ended a program, as a shell reports it.
	exitCodeSignalBase = 128
	// maxHostOpenAttempts bounds the tries at opening /dev/ptmx, and openRetryPause spaces them.
	maxHostOpenAttempts = 8
	openRetryPause      = time.Millisecond
	// childMode is the program side's permissions: read and write for its owner only.
	childMode = 0o600
)

// terminalProcess is a program on one of the system's pseudo-terminals. hostSide is the side
// Conn reads and writes; the program has the other as its stdin, stdout, stderr and controlling
// terminal.
type terminalProcess struct {
	hostSide *os.File
	command  *exec.Cmd
}

// Spawn is the real Spawner. It starts the requested program as the leader of a new session and
// process group, on a new pseudo-terminal that is its controlling terminal, with ChildEnv's
// environment. Every descriptor Conn opens is close-on-exec, so the program inherits only its
// terminal, as stdin, stdout and stderr.
func Spawn(requested Spec) (Process, error) {
	// A Host has done this already; doing it again costs little and keeps a direct call safe.
	spec, err := childSpec(requested)
	if err != nil {
		return nil, err
	}

	err = markCloseOnExec()
	if err != nil {
		return nil, err
	}

	hostSide, childSide, err := openTerminal()
	if err != nil {
		return nil, fmt.Errorf("opening a terminal: %w", err)
	}

	command, err := startOn(spec, hostSide, childSide)

	// The program has its own copies of the child side now, or never got it: Conn keeps only
	// the host side, and a close that fails here leaves nothing to undo.
	_ = childSide.Close()

	if err != nil {
		_ = hostSide.Close()

		return nil, fmt.Errorf("starting %q: %w", spec.Name, err)
	}

	return &terminalProcess{hostSide: hostSide, command: command}, nil
}

// startOn sizes the new terminal and starts spec's program on it.
func startOn(spec Spec, hostSide, childSide *os.File) (*exec.Cmd, error) {
	err := setSize(hostSide, Size{Columns: defaultColumns, Rows: defaultRows})
	if err != nil {
		return nil, err
	}

	command := newCommand(spec, childSide)

	err = command.Start()
	if err != nil {
		return nil, fmt.Errorf("running the program: %w", err)
	}

	return command, nil
}

// newCommand is the program spec names on the terminal childSide. Every field is set, so each
// is a decision made here.
func newCommand(spec Spec, childSide *os.File) *exec.Cmd {
	return &exec.Cmd{
		// An absolute path (checkSpec): no PATH lookup.
		Path: spec.Argv[0],
		Args: slices.Clone(spec.Argv),
		// ChildEnv's (childSpec), which is never nil: nil would hand the program Conn's own.
		Env:        spec.Env,
		Dir:        spec.Dir,
		Stdin:      childSide,
		Stdout:     childSide,
		Stderr:     childSide,
		ExtraFiles: nil,
		// A new session makes the program the leader of its own process group, so a stop
		// reaches everything it starts; Setctty makes the terminal (descriptor 0) its controlling
		// terminal.
		SysProcAttr: &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0},
		// No context: the Host owns the program's life, not a request.
		Cancel: nil,
		// Stdio are files, so there are no copying goroutines for Wait to wait on.
		WaitDelay:    0,
		Process:      nil,
		ProcessState: nil,
		Err:          nil,
	}
}

func (p *terminalProcess) Read(buffer []byte) (int, error) {
	count, err := p.hostSide.Read(buffer)
	if err != nil {
		return count, fmt.Errorf("reading the terminal: %w", err)
	}

	return count, nil
}

func (p *terminalProcess) Write(data []byte) (int, error) {
	err := p.hostSide.SetWriteDeadline(time.Now().Add(writeTimeout))
	if err != nil {
		return 0, fmt.Errorf("bounding a write to the terminal: %w", err)
	}

	count, err := p.hostSide.Write(data)
	if err != nil {
		return count, fmt.Errorf("writing to the terminal: %w", err)
	}

	return count, nil
}

func (p *terminalProcess) Resize(size Size) error { return setSize(p.hostSide, size) }

func (p *terminalProcess) PID() int { return p.command.Process.Pid }

func (p *terminalProcess) Terminate() error { return p.signalGroup(unix.SIGTERM) }

func (p *terminalProcess) Kill() error { return p.signalGroup(unix.SIGKILL) }

// Group asks the kernel what is left of the program's group with a signal of 0. ESRCH is
// nothing; EPERM is members none of which may be signalled (another user's or a setuid program,
// or on macOS members that are all exiting); any other answer counts as a member a signal
// reaches, so a stop goes on to the kill.
func (p *terminalProcess) Group() GroupState {
	err := unix.Kill(-p.groupID(), 0)
	if errors.Is(err, unix.ESRCH) {
		return GroupGone
	}

	if errors.Is(err, unix.EPERM) {
		return GroupUnreachable
	}

	return GroupRunning
}

// Wait waits for the program and returns its exit code: its exit status, 128 plus the signal that
// ended it, or exitCodeUnknown if its end could not be read.
func (p *terminalProcess) Wait() int {
	// A non-zero exit is an error here too; the code is read from ProcessState either way.
	_ = p.command.Wait()

	return exitCode(p.command.ProcessState)
}

func (p *terminalProcess) Close() error {
	err := p.hostSide.Close()
	if err != nil {
		return fmt.Errorf("closing the terminal: %w", err)
	}

	return nil
}

// Keyboard says who the terminal's input goes to: whether its foreground process group is the
// program's own, and whether it reads whole lines (ICANON), as a password or line prompt does.
func (p *terminalProcess) Keyboard() (Keyboard, error) {
	raw, err := p.hostSide.SyscallConn()
	if err != nil {
		return Keyboard{}, fmt.Errorf("reaching the terminal: %w", err)
	}

	var (
		group    int
		modes    *unix.Termios
		ioctlErr error
	)

	err = raw.Control(func(descriptor uintptr) {
		group, ioctlErr = unix.IoctlGetInt(int(descriptor), unix.TIOCGPGRP)
		if ioctlErr != nil {
			return
		}

		modes, ioctlErr = unix.IoctlGetTermios(int(descriptor), readModes)
	})
	if err != nil {
		return Keyboard{}, fmt.Errorf("reaching the terminal: %w", err)
	}

	if ioctlErr != nil {
		return Keyboard{}, fmt.Errorf("reading the terminal's owner: %w", ioctlErr)
	}

	return Keyboard{
		Foreground: group == p.groupID(), LineMode: modes.Lflag&unix.ICANON != 0,
	}, nil
}

// signalGroup sends signal to every process in the program's group. A group that is already
// gone is not an error.
func (p *terminalProcess) signalGroup(signal unix.Signal) error {
	err := unix.Kill(-p.groupID(), signal)
	if beyondReach(err) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("signalling the program's group: %w", err)
	}

	return nil
}

// beyondReach says whether a signal to a group failed because no member can get one: there is
// none (ESRCH), or none may be signalled (EPERM). Neither is an error to the sender; the wait that
// follows every signal tells the two apart, and never takes EPERM for gone.
func beyondReach(err error) bool {
	if errors.Is(err, unix.ESRCH) {
		return true
	}

	return errors.Is(err, unix.EPERM)
}

// groupID is the program's process group id, which Setsid made its pid. An id of 1 or less is a
// mistake in the code: kill(-1) reaches every process the user may signal, kill(0) Conn's own
// group.
func (p *terminalProcess) groupID() int {
	pid := p.PID()
	if pid <= 1 {
		panic(fmt.Sprintf("pty: a process group id of %d", pid))
	}

	return pid
}

// exitCode is a finished program's exit code, as a shell reports it.
func exitCode(state *os.ProcessState) int {
	if state == nil {
		return exitCodeUnknown
	}

	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok {
		return state.ExitCode()
	}

	if status.Signaled() {
		return exitCodeSignalBase + int(status.Signal())
	}

	return status.ExitStatus()
}

// setSize sets the terminal's size; the kernel sends the program SIGWINCH.
func setSize(hostSide *os.File, size Size) error {
	raw, err := hostSide.SyscallConn()
	if err != nil {
		return fmt.Errorf("reaching the terminal: %w", err)
	}

	var ioctlErr error

	err = raw.Control(func(descriptor uintptr) {
		ioctlErr = unix.IoctlSetWinsize(int(descriptor), unix.TIOCSWINSZ,
			&unix.Winsize{Row: size.Rows, Col: size.Columns, Xpixel: 0, Ypixel: 0})
	})
	if err != nil {
		return fmt.Errorf("reaching the terminal: %w", err)
	}

	if ioctlErr != nil {
		return fmt.Errorf("sizing the terminal: %w", ioctlErr)
	}

	return nil
}

// openChildPath opens the program's side of a terminal by the path the kernel gave it,
// close-on-exec and not as Conn's controlling terminal.
func openChildPath(path string) (int, error) {
	descriptor, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, fmt.Errorf("opening the program's side of the terminal: %w", err)
	}

	return descriptor, nil
}

// finishTerminal makes the two sides of a new terminal into files. The program's side becomes
// its owner's alone (mode 0600, as after mesg n), so no other user can write to the program's
// screen; the host side becomes non-blocking, so the runtime's poller can bound its writes and
// Close can end a pending read. On failure it closes both.
func finishTerminal(hostDescriptor, childDescriptor int, path string) (*os.File, *os.File, error) {
	err := unix.Fchmod(childDescriptor, childMode)
	if err == nil {
		err = unix.SetNonblock(hostDescriptor, true)
	}

	if err != nil {
		_ = unix.Close(childDescriptor)
		_ = unix.Close(hostDescriptor)

		return nil, nil, fmt.Errorf("preparing the terminal: %w", err)
	}

	hostSide := os.NewFile(uintptr(hostDescriptor), hostPath)

	return hostSide, os.NewFile(uintptr(childDescriptor), path), nil
}

// openHostSide opens a new pseudo-terminal's host side, close-on-exec and not as a controlling
// terminal for Conn. macOS sometimes refuses to open /dev/ptmx while another goroutine of the same
// process forks (an odd errno, -6); a second attempt succeeds, so it tries up to
// maxHostOpenAttempts times, openRetryPause apart. Measured: 40 of 300 concurrent spawns needed a
// second attempt, none a third; separate processes never collide.
func openHostSide() (int, error) {
	var err error

	for attempt := range maxHostOpenAttempts {
		if attempt > 0 {
			time.Sleep(openRetryPause)
		}

		var descriptor int

		descriptor, err = unix.Open(hostPath, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
		if err == nil {
			return descriptor, nil
		}
	}

	return 0, fmt.Errorf("opening %s: %w", hostPath, err)
}
