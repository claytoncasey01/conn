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
	// columnsDefault and rowsDefault are a new terminal's size, until the first Resize.
	columnsDefault = 80
	rowsDefault    = 24
	// exitCodeUnknown is the exit code of a program whose end could not be read.
	exitCodeUnknown = -1
	// exitCodeSignalBase is added to the signal that ended a program, as a shell reports it.
	exitCodeSignalBase = 128
	// hostOpenAttemptsMax bounds the tries at opening /dev/ptmx, and openRetryPause spaces them.
	hostOpenAttemptsMax = 8
	openRetryPause      = time.Millisecond
)

// terminalProcess is a program on one of the system's pseudo-terminals. hostSide is the side
// Conn reads and writes; the program has the other as its stdin, stdout, stderr and controlling
// terminal.
type terminalProcess struct {
	hostSide *os.File
	command  *exec.Cmd
}

// Spawn is the real Spawner. It starts spec's program as the leader of a new session and process
// group, on a new pseudo-terminal that is its controlling terminal, with ChildEnv's environment.
// Every descriptor Conn opens is close-on-exec, so the program inherits only its terminal, as
// stdin, stdout and stderr.
func Spawn(spec Spec) (Process, error) {
	err := checkSpec(spec)
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
	err := setSize(hostSide, Size{Columns: columnsDefault, Rows: rowsDefault})
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
		// Never nil, which would hand the program Conn's own environment.
		Env:        ChildEnv(spec.Env),
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

// GroupAlive asks the kernel whether the program's group has a member left. Only "no such
// process" means it has none; any other answer counts as alive, so a stop goes on to the kill.
func (p *terminalProcess) GroupAlive() bool {
	err := unix.Kill(-p.PID(), 0)

	return !errors.Is(err, unix.ESRCH)
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

// signalGroup sends signal to every process in the program's group. A group that is already
// gone is not an error.
func (p *terminalProcess) signalGroup(signal unix.Signal) error {
	err := unix.Kill(-p.PID(), signal)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("signalling the program's group: %w", err)
	}

	return nil
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

// openChildSide opens the program's side of the terminal whose host side is hostDescriptor and
// whose program side the kernel named path, and makes both files. Both are close-on-exec; the
// host side is non-blocking, so the runtime's poller can bound its writes and Close can end a
// pending read.
func openChildSide(hostDescriptor int, path string) (*os.File, *os.File, error) {
	childDescriptor, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = unix.Close(hostDescriptor)

		return nil, nil, fmt.Errorf("opening the program's side of the terminal: %w", err)
	}

	err = unix.SetNonblock(hostDescriptor, true)
	if err != nil {
		_ = unix.Close(childDescriptor)
		_ = unix.Close(hostDescriptor)

		return nil, nil, fmt.Errorf("making the terminal non-blocking: %w", err)
	}

	hostSide := os.NewFile(uintptr(hostDescriptor), hostPath)

	return hostSide, os.NewFile(uintptr(childDescriptor), path), nil
}

// openHostSide opens a new pseudo-terminal's host side, close-on-exec and not as a controlling
// terminal for Conn. macOS sometimes refuses to open /dev/ptmx while another goroutine of the same
// process forks (an odd errno, -6); a second attempt succeeds, so it tries up to
// hostOpenAttemptsMax times, openRetryPause apart. Measured: 40 of 300 concurrent spawns needed a
// second attempt, none a third; separate processes never collide.
func openHostSide() (int, error) {
	var err error

	for attempt := range hostOpenAttemptsMax {
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
