// Package pty hosts programs on pseudo-terminals: it starts a program on a terminal of its own,
// keeps a bounded scrollback of what the program prints, fans that output out to a few viewers,
// passes their keystrokes and window sizes in, and stops the program and everything it started.
//
// A Spec says what to run; a Spawner starts it. Spawn is the real Spawner, over the system's
// pseudo-terminals (macOS and Linux); a test or the dry run hands NewHost a scripted fake instead.
// Nothing here chooses a program: the Spec comes from Conn's own code, never from a client.
//
// Every limit is fixed: at most sessionsMax programs run at once, each keeps scrollbackBytesMax of
// scrollback, has at most viewersMax viewers, each with a queue of at most viewerQueueBytesMax,
// and takes at most writeBytesMax of input per Write. A viewer that falls behind is dropped, so
// the reader of a program's output never waits for anyone. Ended sessions are kept, for their
// exit code and scrollback, up to endedMax; the oldest is forgotten first.
//
// Terminal content is never logged and never written to disk: the scrollback is memory only, and
// no error this package returns holds input or output bytes.
//
// One platform limit: when a program on a terminal exits, macOS hangs the terminal up and throws
// away whatever it printed that had not been read yet. The reader reads all the time, so this
// only costs the last moments of a program's output on a busy machine.
//
// Locking: one mutex, Host.mu, guards the host's tables and every session's changing state (its
// exit code, scrollback and viewers). It is held only for work in memory and for channel sends that
// cannot block; every call that can block (the spawner, and the Process's Read, Write, Resize,
// signals, Wait and Close) is made without it. Each session owns two goroutines: its reader, which
// copies the program's output into the scrollback and the viewers' queues, and its waiter, which
// waits for the program to exit, lets the reader drain for at most outputDrainTimeout, closes the
// terminal (which ends the reader) and records the end. Both end when the session ends.
package pty

import (
	"errors"
	"time"
)

// The limits a Host keeps by default, which are also the most it can be given.
const (
	// sessionsMax is how many programs may run at once.
	sessionsMax = 12
	// scrollbackBytesMax is how much of a program's output is kept for a viewer that joins late.
	scrollbackBytesMax = 4 << 20
	// viewersMax is how many viewers one session may have.
	viewersMax = 4
	// stopGrace is how long a polite stop waits before the program's group is killed.
	stopGrace = 5 * time.Second
)

// The fixed limits.
const (
	// readBufferBytes is the most one read of a program's output takes, so the most one chunk a
	// viewer receives holds.
	readBufferBytes = 32 << 10
	// viewerQueueBytesMax bounds the output queued for one viewer; a viewer whose queue is full
	// is dropped.
	viewerQueueBytesMax = 1 << 20
	// viewerQueueChunks is viewerQueueBytesMax in chunks: a chunk is at most readBufferBytes.
	viewerQueueChunks = viewerQueueBytesMax / readBufferBytes
	// writeBytesMax is the most input one Write takes.
	writeBytesMax = 64 << 10
	// endedMax is how many ended sessions are kept for their exit code and scrollback.
	endedMax = sessionsMax
	// idBytes is the randomness in an ID: 128 bits.
	idBytes = 16
	// outputDrainTimeout is how long the output of a program that has exited may still be read
	// before its terminal is closed.
	outputDrainTimeout = time.Second
	// stopPoll is how often a stop looks whether the program's group is gone.
	stopPoll = 20 * time.Millisecond
	// killWait bounds how long a stop waits for the session to end after the program has gone
	// or been killed.
	killWait = 5 * time.Second
	// argumentsMax, environmentEntriesMax and nameBytesMax bound a Spec.
	argumentsMax          = 256
	environmentEntriesMax = 4096
	nameBytesMax          = 256
	// columnsMax and rowsMax bound a terminal's size.
	columnsMax = 1000
	rowsMax    = 1000
)

var (
	// ErrSessionsFull is a Start while sessionsMax programs are running.
	ErrSessionsFull = errors.New("too many sessions are running")
	// ErrViewersFull is a Subscribe to a session that has viewersMax viewers.
	ErrViewersFull = errors.New("the session has too many viewers")
	// ErrNoSession is an ID the host does not hold.
	ErrNoSession = errors.New("no such session")
	// ErrEnded is input or a resize for a session whose program has ended.
	ErrEnded = errors.New("the session has ended")
	// ErrClosed is a Start after StopAll.
	ErrClosed = errors.New("the host is stopping its sessions")
	// ErrInvalidSpec is a Spec that cannot be started; each problem wraps it.
	ErrInvalidSpec = errors.New("invalid session spec")
	// ErrInputTooLarge is a Write of more than writeBytesMax.
	ErrInputTooLarge = errors.New("too much input at once")
	// ErrInvalidSize is a Resize to a size out of range.
	ErrInvalidSize = errors.New("terminal size out of range")
	// ErrUnsupported is Spawn on a system without the pseudo-terminals it knows.
	ErrUnsupported = errors.New("terminals are not supported on this system")
	// errTimedOut is a stop whose program did not end within killWait of being killed.
	errTimedOut = errors.New("the program did not end after it was killed")
)

// ID names a session. It is random and unguessable: a client can only use one the server gave it.
type ID string

// Spec is what to run: the program's absolute path and its arguments, the folder it runs in, its
// environment (ChildEnv is applied to it before it starts), and a name for people to read.
type Spec struct {
	Argv []string
	Dir  string
	Env  []string
	Name string
}

// Size is a terminal's size in character cells.
type Size struct {
	Columns uint16
	Rows    uint16
}

// Info is what the host says about one session. ExitCode is nil while the program runs; once the
// session has ended it is the program's exit status, or 128 plus the signal that ended it.
type Info struct {
	ID        ID
	Name      string
	Dir       string
	PID       int
	StartedAt time.Time
	ExitCode  *int
}

// Limits are a Host's limits. Each must be above zero and at most its default.
type Limits struct {
	SessionsMax        int
	ScrollbackBytesMax int
	ViewersMax         int
	StopGrace          time.Duration
}

// DefaultLimits are the limits Conn runs with.
func DefaultLimits() Limits {
	return Limits{
		SessionsMax:        sessionsMax,
		ScrollbackBytesMax: scrollbackBytesMax,
		ViewersMax:         viewersMax,
		StopGrace:          stopGrace,
	}
}

// Config is what a Host is made with.
type Config struct {
	Spawner Spawner
	Limits  Limits
}

// Spawner starts a Spec's program on a terminal of its own. Spawn is the real one.
type Spawner func(spec Spec) (Process, error)

// Process is a running program on its terminal, as a Host drives it. Read and Write are the
// terminal; the signals reach the program's whole process group.
type Process interface {
	// Read reads the program's output. Once Close is called it returns an error.
	Read(buffer []byte) (int, error)
	// Write types data into the terminal, in bounded time.
	Write(data []byte) (int, error)
	// Resize sets the terminal's size; the program gets SIGWINCH.
	Resize(size Size) error
	// PID is the program's process id, which is also its process group's.
	PID() int
	// Terminate asks the program's group to stop.
	Terminate() error
	// Kill kills the program's group.
	Kill() error
	// GroupAlive says whether any process is left in the program's group.
	GroupAlive() bool
	// Wait waits for the program to exit and returns its exit code.
	Wait() int
	// Close closes the terminal.
	Close() error
}

// Viewer is one viewer of a session: the scrollback when it joined, then the output after it.
// Output closes when the session ends, when Close is called, or when the viewer falls so far
// behind that its queue is full. A chunk is shared by every viewer: read it, never change it.
type Viewer struct {
	Replay []byte
	Output <-chan []byte

	close func()
}

// Close stops the viewer and closes its Output. Calling it again does nothing.
func (v Viewer) Close() {
	if v.close != nil {
		v.close()
	}
}
