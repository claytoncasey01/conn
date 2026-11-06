package pty

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

// Host runs sessions: programs on terminals of their own. Its methods are safe to call from many
// goroutines. The package comment says how it locks.
type Host struct {
	spawner Spawner
	limits  Limits
	answers Answers

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
	// answers queues the replies to the program's terminal queries, from its reader to its
	// answerer, which closes answered once the reader has closed answers and all are typed.
	answers  chan []byte
	answered chan struct{}

	// screenMu guards screen, the model of the program's terminal, apart from Host.mu: feeding
	// it a large chunk never holds up the rest of the host.
	screenMu sync.Mutex
	screen   *Screen

	exitCode   *int
	scrollback ring
	viewers    []*viewer
	// waited is set once the program has exited, before the session's end is recorded: input
	// and resizes are refused as ended from then on.
	waited bool
	// groupStopped is set once the program's group is known to be empty; from then on its
	// number is never signalled, since a new program may have been given it.
	groupStopped bool
	// stopError is why the group could not be stopped, while that is so.
	stopError string
	// retired is set once the session has given back its running slot and joined h.ended:
	// when its program has ended and its group is known to be empty.
	retired bool
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
		answers:  config.Answers,
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
	return h.write(sessionID, data, nil)
}

// WriteIfStill types data into the session's terminal only while its screen still shows what
// the caller checked: once this write's turn comes, the bottom count lines of the screen (as
// ScreenLines gives them) are handed to still, and if it answers false, the write is
// ErrScreenChanged with nothing typed. still is the caller's own test of what matters (an empty
// input box, the same prompt), not "nothing changed": a program redraws parts of its screen
// that do not matter, such as a blinking mark, all the time. It runs inside the write's turn, so
// no other input reaches the program between the check and the write. Otherwise it is Write.
func (h *Host) WriteIfStill(
	sessionID ID, count int, still func(lines []string) bool, data []byte,
) error {
	if still == nil {
		panic("pty: WriteIfStill needs a check")
	}

	return h.write(sessionID, data, &screenCheck{count: count, still: still})
}

// Resize sets the session's terminal to columns by rows.
func (h *Host) Resize(sessionID ID, columns, rows uint16) error {
	size := Size{Columns: columns, Rows: rows}

	err := checkSize(size)
	if err != nil {
		return err
	}

	resizing, err := h.runningSession(sessionID)
	if err != nil {
		return err
	}

	// As a terminal does, the screen takes the new size before the program hears of it, so
	// what the program draws for the new size is read at that size.
	previous := resizing.resizeScreen(size)

	err = resizing.process.Resize(size)
	if err != nil {
		resizing.resizeScreen(previous)

		return fmt.Errorf("resizing a session: %w", err)
	}

	return nil
}

// ScreenLines is the bottom count lines of the session's screen, as Screen.Lines gives them,
// and the screen's generation when they were read: a later read with the same generation would
// read the same lines, so a caller that checked a prompt can tell whether the screen has changed
// since. An ended session's last screen can still be read.
func (h *Host) ScreenLines(sessionID ID, count int) ([]string, uint64, error) {
	held, err := h.find(sessionID)
	if err != nil {
		return nil, 0, err
	}

	held.screenMu.Lock()
	defer held.screenMu.Unlock()

	return held.screen.Lines(count), held.screen.Generation(), nil
}

// lines is the bottom count lines of the session's screen now.
func (held *session) lines(count int) []string {
	held.screenMu.Lock()
	defer held.screenMu.Unlock()

	return held.screen.Lines(count)
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
			StartedAt: held.startedAt, ExitCode: exitCode, StopError: held.stopError,
		})
	}

	return infos
}

// screenCheck is what WriteIfStill checks the screen with.
type screenCheck struct {
	count int
	still func(lines []string) bool
}

// write is Write, and WriteIfStill when check is set.
func (h *Host) write(sessionID ID, data []byte, check *screenCheck) error {
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

	return h.typeInto(writing, data, check)
}

// typeInto writes data to the session's program in its turn. With check set, data is written
// only if the screen passes it once the turn is taken.
func (h *Host) typeInto(writing *session, data []byte, check *screenCheck) error {
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

	if check != nil {
		if !check.still(writing.lines(check.count)) {
			return ErrScreenChanged
		}
	}

	_, err := writing.process.Write(data)
	if err != nil {
		return writeFailure(err)
	}

	return nil
}

// writeFailure is what a failed Write to a program means. EIO is the terminal saying nothing
// holds its program's side any more, and a closed terminal is a session ending: either way the
// program has gone, ErrEnded. The write deadline passing is a program not taking its input,
// ErrInputBlocked. Anything else is the host's own failure.
func writeFailure(err error) error {
	switch {
	case errors.Is(err, syscall.EIO):
		return fmt.Errorf("%w: %w", ErrEnded, err)
	case errors.Is(err, os.ErrClosed):
		return fmt.Errorf("%w: %w", ErrEnded, err)
	case errors.Is(err, os.ErrDeadlineExceeded):
		return fmt.Errorf("%w: %w", ErrInputBlocked, err)
	default:
		return fmt.Errorf("typing into a session: %w", err)
	}
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

	screen, err := NewScreen(Size{Columns: columnsDefault, Rows: rowsDefault})
	if err != nil {
		panic("pty: the default terminal size is out of range: " + err.Error())
	}

	started := &session{
		sessionID: sessionID, name: spec.Name, dir: spec.Dir, pid: pid, startedAt: time.Now(),
		process: process, ended: make(chan struct{}), writeTurn: make(chan struct{}, 1),
		answers: make(chan []byte, answersQueuedMax), answered: make(chan struct{}),
		screenMu: sync.Mutex{}, screen: screen,
		exitCode: nil, scrollback: newRing(h.limits.ScrollbackBytesMax), viewers: nil,
		groupStopped: false, stopError: "", retired: false,
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
	go h.answerQueries(started)
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

	if found.waited {
		return nil, ErrEnded
	}

	return found, nil
}
