// Package pty hosts programs on pseudo-terminals: it starts a program on a terminal of its own,
// keeps a bounded scrollback of what the program prints, fans that output out to a few viewers,
// passes their keystrokes and window sizes in, and stops the program and everything it started.
//
// A Spec says what to run; a Spawner starts it. Spawn is the real Spawner, over the system's
// pseudo-terminals (macOS and Linux); a test or the dry run hands NewHost a scripted fake instead.
// Nothing here chooses a program: the Spec comes from Conn's own code, never from a client.
//
// Every limit is fixed: at most sessionsMax programs run at once, each keeps scrollbackBytesMax of
// scrollback, has at most viewersMax viewers, each with a queue of at most viewerQueueBytesMax
// (counted in bytes, so a burst of small writes is one batch; with the batch being handed over, a
// viewer holds at most twice that), and takes at most writeBytesMax of
// input per Write. Writes to one session take turns, each waiting at most writeWaitMax for its
// turn. A viewer that falls behind is dropped, so the reader of a program's output never waits
// for anyone. A viewer holds one of its session's viewersMax places until it is closed: close it
// when its client disconnects. Ended sessions are kept, for their exit code and scrollback, up to
// endedMax; the oldest is forgotten first.
//
// Stopping: a stop signals the program's whole process group (the program is a session leader,
// so the group's id is its pid): a polite SIGTERM, then SIGKILL if anything in the group is left
// after the grace, or at once when the caller stops waiting, then a wait of at most killWait for
// the group to empty. The same happens when a program exits by itself, before its session is
// recorded as ended, so nothing it started outlives it. A group whose members no signal may reach
// (EPERM) is never taken for gone: if it is still there at the bound, the stop fails with
// ErrGroupUnreachable, Info.StopError says so, and the session keeps its running slot and is
// never forgotten while that is so. Once a group is known to be empty its number is never
// signalled again, since a new program may have been given it. A descendant that calls setsid or
// setpgid itself has left the group and is out of reach; Task 9 checks this against a real
// claude running a background command. StopAll (which returns an error, unlike the plan's
// signature, so quitting can say what did not stop) refuses new Starts and waits for those in
// flight.
//
// Descriptors: every descriptor this package opens is close-on-exec, and so is every file and
// socket Go's os and net packages open, so a program inherits only its terminal. A descriptor
// made by cgo or by a raw syscall without O_CLOEXEC is not covered; Conn makes none.
//
// Terminal content is never logged and never written to disk: the scrollback is memory only, and
// no error this package returns holds input or output bytes. The program's side of each terminal
// is mode 0600, so no other user can write to it.
//
// The screen: each session keeps a Screen, a model of its terminal fed with the same output the
// viewers get, so what a session shows (ScreenLines) and what it asks can be read with no terminal
// app and no viewer. It keeps the live screen only, never the scrollback, and is bounded by the
// terminal's size, at most cellsMax cells a screen.
//
// Terminal queries: the host, not a viewer, answers the few queries in its fixed table (device
// attributes, the cursor position and the background colour, the last from Config.Answers), once
// each, as the output is read; answers.go says why. Each reply is fixed bytes and numbers, never
// text from the output. The reader queues replies, at most answersQueuedMax, dropping the rest,
// and the session's answerer types them in turn with the viewers' input, so the reader never
// waits for a program to read its input.
//
// One platform limit: when a program on a terminal exits, macOS hangs the terminal up and throws
// away whatever it printed that had not been read yet. The reader reads all the time, so this
// only costs the last moments of a program's output on a busy machine.
//
// Locking: one mutex, Host.mu, guards the host's tables and every session's changing state (its
// exit code, scrollback and viewers), except its screen, which its own screenMu guards, so
// feeding a screen a large chunk never holds up the host. Each is held only for work in memory
// and for channel sends that cannot block; every call that can block (the spawner, and the
// Process's Read, Write, Resize, signals, Wait and Close) is made without them. Each session owns
// three goroutines: its reader, which copies the program's output into the scrollback, the
// viewers' queues and its screen; its answerer, which types the replies to the program's queries
// and ends once the reader has ended and every queued reply is typed; and its waiter, which waits
// for the program to exit, stops what is left of its group, lets the reader drain for at most
// outputDrainTimeout, closes the terminal (which ends the reader), waits for the answerer and
// records the end. All three end when the session ends. Each viewer owns one more, its pump,
// which hands its queue to Output and ends when the viewer is closed or dropped, or once the
// session has ended and the queue is handed over.
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
	// viewerQueueBytesMax bounds the output queued for one viewer, counted in bytes; a viewer
	// that output would take past it is dropped.
	viewerQueueBytesMax = 1 << 20
	// writeBytesMax is the most input one Write takes.
	writeBytesMax = 64 << 10
	// writeWaitMax bounds how long a Write waits for an earlier Write to the same session.
	writeWaitMax = 5 * time.Second
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
	// columnsMax and rowsMax bound a terminal's size, and cellsMax its area: the most cells one
	// screen of a session's model holds (a session has two, the main and the alternate).
	columnsMax = 1000
	rowsMax    = 1000
	cellsMax   = 1 << 18
	// columnsDefault and rowsDefault are a new terminal's size, until the first Resize.
	columnsDefault = 80
	rowsDefault    = 24
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
	// ErrInputBlocked is a Write that waited writeWaitMax behind another: the program is not
	// reading its input.
	ErrInputBlocked = errors.New("the program is not taking input")
	// ErrInvalidSize is a Resize to a size out of range.
	ErrInvalidSize = errors.New("terminal size out of range")
	// ErrUnsupported is Spawn on a system without the pseudo-terminals it knows.
	ErrUnsupported = errors.New("terminals are not supported on this system")
	// ErrGroupUnreachable is a group that still had members no signal may reach killWait after
	// the kill.
	ErrGroupUnreachable = errors.New("the program's group is unreachable by signals")
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
	// StopError is set when the program's group could not be stopped: what failed, in Conn's own
	// words, never the program's output. The session keeps its running slot while it is set.
	StopError string
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
	Answers Answers
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
	// Group says whether anything is left in the program's group, and whether a signal reaches
	// it.
	Group() GroupState
	// Wait waits for the program to exit and returns its exit code.
	Wait() int
	// Close closes the terminal.
	Close() error
}

// GroupState is what is left of a program's process group.
type GroupState int

const (
	// GroupGone is no member left (kill answers ESRCH).
	GroupGone GroupState = iota
	// GroupRunning is a member a signal reaches.
	GroupRunning
	// GroupUnreachable is members none of which a signal may reach (kill answers EPERM): another
	// user's or a setuid program, or, on macOS only, members that are all exiting until init
	// reaps them. It is never taken for gone.
	GroupUnreachable
)

// Viewer is one viewer of a session: the scrollback when it joined, then the output after it, in
// batches. Output closes once the session has ended and everything queued is handed over, when
// Close is called, or when the viewer falls viewerQueueBytesMax behind. Each batch is the
// viewer's own. Close every Viewer: until then it holds one of the session's viewersMax places
// (an ended session's viewer holds none) and its goroutine.
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
