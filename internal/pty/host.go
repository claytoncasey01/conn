package pty

import (
	"context"
	"fmt"
	"slices"
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
	// closed is set by StopAll: no Start succeeds after it.
	closed bool
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
	// ended is closed once the program has exited, its output is read and its viewers closed.
	ended chan struct{}

	exitCode   *int
	scrollback ring
	viewers    []*viewer
}

// viewer is one Viewer's queue.
type viewer struct {
	output chan []byte
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
		closed:   false,
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

// Start runs spec's program on a new terminal and returns the session's ID. ctx bounds only the
// start: the program runs until it exits or is stopped, whatever happens to ctx after.
func (h *Host) Start(ctx context.Context, spec Spec) (ID, error) {
	err := checkSpec(spec)
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

	// The spawner gets its own copy, with the environment a hosted program may have.
	spec.Argv = slices.Clone(spec.Argv)
	spec.Env = ChildEnv(spec.Env)

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

	process, err := h.runningProcess(sessionID)
	if err != nil {
		return err
	}

	if len(data) == 0 {
		return nil
	}

	_, err = process.Write(data)
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

	process, err := h.runningProcess(sessionID)
	if err != nil {
		return err
	}

	err = process.Resize(Size{Columns: columns, Rows: rows})
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

	return nil
}

// release gives back a slot whose Start failed.
func (h *Host) release() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.running--
}

// admit makes process a session and starts its goroutines. A program that started while StopAll
// ran is killed at once instead, since nothing would stop it later.
func (h *Host) admit(sessionID ID, spec Spec, process Process) error {
	started := &session{
		sessionID: sessionID, name: spec.Name, dir: spec.Dir, pid: process.PID(),
		startedAt: time.Now(),
		process:   process, ended: make(chan struct{}),
		exitCode: nil, scrollback: newRing(h.limits.ScrollbackBytesMax), viewers: nil,
	}

	h.mu.Lock()

	if h.closed {
		h.running--
		h.mu.Unlock()
		abandon(process)

		return ErrClosed
	}

	_, taken := h.sessions[sessionID]
	if taken {
		h.mu.Unlock()
		panic("pty: crypto/rand gave two sessions the same id")
	}

	h.sessions[sessionID] = started
	h.order = append(h.order, sessionID)
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

// runningProcess is the session's program while it has not ended.
func (h *Host) runningProcess(sessionID ID) (Process, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	found, ok := h.sessions[sessionID]
	if !ok {
		return nil, ErrNoSession
	}

	if found.exitCode != nil {
		return nil, ErrEnded
	}

	return found.process, nil
}
