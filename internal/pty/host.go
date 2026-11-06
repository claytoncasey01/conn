package pty

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Host runs sessions: programs on terminals of their own. Its methods are safe to call from many
// goroutines. The package comment says how it locks.
type Host struct {
	spawner Spawner
	limits  Limits

	mu       sync.Mutex
	sessions map[ID]*session
	// order is every held session, oldest Start first; ended is the ended ones, oldest end first.
	order []ID
	ended []ID
	// running counts the programs started and not ended, and the Starts still spawning.
	running int
	// starting counts the Starts still spawning or being admitted.
	starting int
	// closed is set by StopAll: no Start succeeds after it.
	closed bool
	// startsDone is closed once closed is set and starting is zero: StopAll waits on it.
	startsDone       chan struct{}
	startsDoneClosed bool
}

// session is one program on its terminal. The fields above mu's are set once, before the session
// is shared; the rest are guarded by Host.mu.
type session struct {
	sessionID ID
	name      string
	dir       string
	pid       int
	startedAt time.Time
	process   Process
	// ended is closed once the program has exited, its group is stopped, its output is read and
	// its viewers told.
	ended chan struct{}
	// writeTurn holds one value while a Write is in the program, so Writes take turns.
	writeTurn chan struct{}

	exitCode   *int
	scrollback ring
	viewers    []*viewer
}

// NewHost is a Host over config. A Config without a Spawner, or with a limit out of range, is a
// mistake in the code that builds it, so it panics.
func NewHost(config Config) *Host {
	if config.Spawner == nil {
		panic("pty: NewHost needs a Spawner")
	}

	checkLimits(config.Limits)

	return &Host{
		spawner:  config.Spawner,
		limits:   config.Limits,
		mu:       sync.Mutex{},
		sessions: make(map[ID]*session, sessionsMax+endedMax),
		order:    nil,
		ended:    nil,
		running:  0,
		starting: 0,
		closed:   false,

		startsDone:       make(chan struct{}),
		startsDoneClosed: false,
	}
}

// checkLimits panics on a limit at or below zero or above its default.
func checkLimits(limits Limits) {
	checkLimit("SessionsMax", int64(limits.SessionsMax), sessionsMax)
	checkLimit("ScrollbackBytesMax", int64(limits.ScrollbackBytesMax), scrollbackBytesMax)
	checkLimit("ViewersMax", int64(limits.ViewersMax), viewersMax)
	checkLimit("StopGrace", int64(limits.StopGrace), int64(stopGrace))
}

func checkLimit(name string, value, most int64) {
	if value <= 0 {
		panic(fmt.Sprintf("pty: limit %s is %d, want above zero", name, value))
	}

	if value > most {
		panic(fmt.Sprintf("pty: limit %s is %d, want at most %d", name, value, most))
	}
}

// Start runs the requested program on a new terminal and returns the session's ID. ctx bounds
// only the start: the program runs until it exits or is stopped, whatever happens to ctx after.
func (h *Host) Start(ctx context.Context, requested Spec) (ID, error) {
	// The spawner gets its own copy, with the environment a hosted program may have.
	spec, err := childSpec(requested)
	if err != nil {
		return "", err
	}

	err = ctx.Err()
	if err != nil {
		return "", fmt.Errorf("starting session %q: %w", spec.Name, err)
	}

	sessionID, err := newID()
	if err != nil {
		return "", err
	}

	err = h.reserve()
	if err != nil {
		return "", err
	}

	process, err := h.spawner(spec)
	if err != nil {
		h.release()

		return "", fmt.Errorf("starting session %q: %w", spec.Name, err)
	}

	return sessionID, h.admit(sessionID, spec, process)
}

// Write types data into the session's terminal. It never logs or quotes data.
func (h *Host) Write(sessionID ID, data []byte) error {
	if len(data) > writeBytesMax {
		return fmt.Errorf("%w: %d bytes, at most %d", ErrInputTooLarge, len(data), writeBytesMax)
	}

	writing, err := h.runningSession(sessionID)
	if err != nil {
		return err
	}

	if len(data) == 0 {
		return nil
	}

	// Writes take turns, and one waits at most writeWaitMax for its turn; the program's own
	// Write is bounded too (writeTimeout for a terminal), so a Write ends within both.
	turnWait := time.NewTimer(writeWaitMax)
	defer turnWait.Stop()

	select {
	case writing.writeTurn <- struct{}{}:
	case <-turnWait.C:
		return ErrInputBlocked
	}

	defer func() { <-writing.writeTurn }()

	_, err = writing.process.Write(data)
	if err != nil {
		return fmt.Errorf("typing into a session: %w", err)
	}

	return nil
}

// Resize sets the session's terminal to columns by rows.
func (h *Host) Resize(sessionID ID, columns, rows uint16) error {
	if columns == 0 {
		return fmt.Errorf("%w: no columns", ErrInvalidSize)
	}

	if rows == 0 {
		return fmt.Errorf("%w: no rows", ErrInvalidSize)
	}

	if columns > columnsMax {
		return fmt.Errorf("%w: %d columns, at most %d", ErrInvalidSize, columns, columnsMax)
	}

	if rows > rowsMax {
		return fmt.Errorf("%w: %d rows, at most %d", ErrInvalidSize, rows, rowsMax)
	}

	resizing, err := h.runningSession(sessionID)
	if err != nil {
		return err
	}

	err = resizing.process.Resize(Size{Columns: columns, Rows: rows})
	if err != nil {
		return fmt.Errorf("resizing a session: %w", err)
	}

	return nil
}

// Live is every session the host holds, running or ended, oldest Start first.
func (h *Host) Live() []Info {
	h.mu.Lock()
	defer h.mu.Unlock()

	infos := make([]Info, 0, len(h.order))

	for _, sessionID := range h.order {
		held := h.sessions[sessionID]

		var exitCode *int

		if held.exitCode != nil {
			code := *held.exitCode
			exitCode = &code
		}

		infos = append(infos, Info{
			ID: held.sessionID, Name: held.name, Dir: held.dir, PID: held.pid,
			StartedAt: held.startedAt, ExitCode: exitCode,
		})
	}

	return infos
}

// reserve takes a running slot for a Start.
func (h *Host) reserve() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.closed {
		return ErrClosed
	}

	if h.running >= h.limits.SessionsMax {
		return fmt.Errorf("%w: at most %d", ErrSessionsFull, h.limits.SessionsMax)
	}

	h.running++
	h.starting++

	return nil
}

// release gives back a slot whose Start failed.
func (h *Host) release() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.running--
	h.starting--
	h.signalStartsDone()
}

// signalStartsDone closes startsDone once StopAll has begun and no Start is in flight. The
// caller holds h.mu.
func (h *Host) signalStartsDone() {
	if !h.closed {
		return
	}

	if h.starting > 0 {
		return
	}

	if h.startsDoneClosed {
		return
	}

	h.startsDoneClosed = true
	close(h.startsDone)
}

// admit makes process a session and starts its goroutines. A program that started while StopAll
// ran is killed at once instead, since nothing would stop it later; StopAll waits for that.
func (h *Host) admit(sessionID ID, spec Spec, process Process) error {
	pid := process.PID()
	if pid <= 1 {
		// A signal to the group -pid would reach every process the user may signal.
		panic(fmt.Sprintf("pty: a spawner returned process id %d", pid))
	}

	started := &session{
		sessionID: sessionID, name: spec.Name, dir: spec.Dir, pid: pid, startedAt: time.Now(),
		process: process, ended: make(chan struct{}), writeTurn: make(chan struct{}, 1),
		exitCode: nil, scrollback: newRing(h.limits.ScrollbackBytesMax), viewers: nil,
	}

	h.mu.Lock()

	if h.closed {
		h.mu.Unlock()
		abandon(process)
		h.release()

		return ErrClosed
	}

	_, taken := h.sessions[sessionID]
	if taken {
		h.mu.Unlock()
		panic("pty: crypto/rand gave two sessions the same id")
	}

	h.sessions[sessionID] = started
	h.order = append(h.order, sessionID)
	h.starting--
	h.mu.Unlock()

	readDone := make(chan struct{})

	go h.readOutput(started, readDone)
	go h.awaitEnd(started, readDone)

	return nil
}

// abandon kills a program no session holds and waits for it.
func abandon(process Process) {
	// The program is being thrown away: if the kill fails, Close below still hangs it up.
	_ = process.Kill()
	_ = process.Close()
	process.Wait()
}

// find is the session sessionID names.
func (h *Host) find(sessionID ID) (*session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	found, ok := h.sessions[sessionID]
	if !ok {
		return nil, ErrNoSession
	}

	return found, nil
}

// runningSession is the session while it has not ended.
func (h *Host) runningSession(sessionID ID) (*session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	found, ok := h.sessions[sessionID]
	if !ok {
		return nil, ErrNoSession
	}

	if found.exitCode != nil {
		return nil, ErrEnded
	}

	return found, nil
}
