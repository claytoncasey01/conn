package pty_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// Exit codes the fake reports for each way it ends.
const (
	fakeTerminatedCode = 143
	fakeKilledCode     = 137
)

// Signal names the fake records, in the order it got them.
const (
	signalTerminate = "terminate"
	signalKill      = "kill"
)

// waitLimit bounds every wait in these tests, and waitPoll is how often a poll looks.
const (
	waitLimit = 5 * time.Second
	waitPoll  = 5 * time.Millisecond
)

var errFakeClosed = errors.New("the fake terminal is closed")

// killOutcome is what a fake's group looks like after it ends.
type killOutcome int

const (
	// killClears leaves no member.
	killClears killOutcome = iota
	// killLeavesUnreachable leaves members no signal can reach, for good.
	killLeavesUnreachable
	// killLeavesUnreachableBriefly leaves them for unreachableLooks looks, then none.
	killLeavesUnreachableBriefly
)

// unreachableLooks is how many looks a briefly unreachable group answers EPERM to.
const unreachableLooks = 3

// fakeProcess is a scripted program on a terminal: a test feeds its output, reads what was
// written to it, and ends it.
type fakeProcess struct {
	pid             int
	ignoreTerminate bool
	// orphans keeps the program's group alive after it exits by itself, as a child it started
	// that ignores the hang-up would.
	orphans bool
	// writeGate, when set, holds every Write until the test sends on it or closes it.
	writeGate chan struct{}
	// resizeErr, when set, is what every Resize fails with.
	resizeErr error
	// writeErr, when set, is what every Write fails with.
	writeErr error
	// cut, when above zero, is how many bytes the next Write takes before it fails as a
	// terminal's write deadline does; guarded by mu.
	cut int
	// afterKill is what the group looks like once it has ended: gone, or members no signal can
	// reach (EPERM), for good or for unreachableAnswers more looks.
	afterKill          killOutcome
	unreachableAnswers atomic.Int32
	reused             atomic.Bool
	// background, when set, says another group has the terminal; lineMode that the terminal
	// reads whole lines; keyboardErr, when set, is what looking at the terminal fails with.
	background  atomic.Bool
	lineMode    atomic.Bool
	keyboardErr error

	output    chan []byte
	closed    chan struct{}
	closeOnce sync.Once
	exited    chan struct{}
	exitOnce  sync.Once
	groupGone chan struct{}
	groupOnce sync.Once

	mu          sync.Mutex
	written     []byte
	sizes       []pty.Size
	signals     []string
	exitCode    int
	writing     int
	writingMost int
}

func (f *fakeProcess) Read(buffer []byte) (int, error) {
	select {
	case chunk, open := <-f.output:
		if !open {
			return 0, io.EOF
		}

		if len(chunk) > len(buffer) {
			panic("a fake output chunk is larger than the host's read buffer")
		}

		return copy(buffer, chunk), nil
	case <-f.closed:
		return 0, errFakeClosed
	}
}

func (f *fakeProcess) Write(data []byte) (int, error) {
	select {
	case <-f.closed:
		return 0, errFakeClosed
	default:
	}

	if f.writeErr != nil {
		return 0, f.writeErr
	}

	f.mu.Lock()
	f.writing++
	f.writingMost = max(f.writingMost, f.writing)
	f.mu.Unlock()

	if f.writeGate != nil {
		<-f.writeGate
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.writing--

	if f.cut > 0 {
		taken := min(f.cut, len(data))
		f.cut = 0
		f.written = append(f.written, data[:taken]...)

		return taken, fmt.Errorf("writing to the terminal: %w", os.ErrDeadlineExceeded)
	}

	f.written = append(f.written, data...)

	return len(data), nil
}

func (f *fakeProcess) Resize(size pty.Size) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.sizes = append(f.sizes, size)

	return f.resizeErr
}

func (f *fakeProcess) PID() int { return f.pid }

func (f *fakeProcess) Terminate() error {
	f.record(signalTerminate)

	if !f.ignoreTerminate {
		f.endGroup()
		f.exit(fakeTerminatedCode)
	}

	return nil
}

func (f *fakeProcess) Kill() error {
	f.record(signalKill)
	f.endGroup()
	f.exit(fakeKilledCode)

	return nil
}

func (f *fakeProcess) Group() pty.GroupState {
	select {
	case <-f.groupGone:
	default:
		return pty.GroupRunning
	}

	if f.reused.Load() {
		return pty.GroupRunning
	}

	switch f.afterKill {
	case killLeavesUnreachable:
		return pty.GroupUnreachable
	case killLeavesUnreachableBriefly:
		if f.unreachableAnswers.Add(-1) >= 0 {
			return pty.GroupUnreachable
		}
	case killClears:
	}

	return pty.GroupGone
}

func (f *fakeProcess) Wait() int {
	<-f.exited

	f.mu.Lock()
	defer f.mu.Unlock()

	return f.exitCode
}

func (f *fakeProcess) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })

	return nil
}

func (f *fakeProcess) Keyboard() (pty.Keyboard, error) {
	if f.keyboardErr != nil {
		return pty.Keyboard{}, f.keyboardErr
	}

	return pty.Keyboard{Foreground: !f.background.Load(), LineMode: f.lineMode.Load()}, nil
}

// cutNextWrite makes the next Write take only count bytes, then fail as a write deadline does.
func (f *fakeProcess) cutNextWrite(count int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.cut = count
}

// reuse makes the group's number look taken again, as a new program that a reused pid made a
// group leader would.
func (f *fakeProcess) reuse() { f.reused.Store(true) }

func (f *fakeProcess) record(signal string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.signals = append(f.signals, signal)
}

// exit ends the program with code; the first call wins.
func (f *fakeProcess) exit(code int) {
	f.exitOnce.Do(func() {
		f.mu.Lock()
		f.exitCode = code
		f.mu.Unlock()
		close(f.exited)
	})
}

// endGroup ends every process left in the program's group.
func (f *fakeProcess) endGroup() {
	f.groupOnce.Do(func() { close(f.groupGone) })
}

// finish ends the program with code after its output so far. Its group ends with it, unless the
// fake orphans. As in a real group, the group is gone before the program's exit can be seen.
func (f *fakeProcess) finish(code int) {
	if !f.orphans {
		f.endGroup()
	}

	f.exit(code)
	close(f.output)
}

// mostWritersAtOnce is how many Writes were ever in the program at the same time.
func (f *fakeProcess) mostWritersAtOnce() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.writingMost
}

func (f *fakeProcess) writtenSoFar() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.written)
}

func (f *fakeProcess) signalsSoFar() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.signals)
}

func (f *fakeProcess) sizesSoFar() []pty.Size {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.sizes)
}

// feed hands the host one chunk of output, failing the test if the host's reader does not take it
// in time: the reader must never block.
func (f *fakeProcess) feed(t *testing.T, chunk []byte) {
	t.Helper()

	timer := time.NewTimer(testtime.Scaled(waitLimit))
	defer timer.Stop()

	select {
	case f.output <- chunk:
	case <-timer.C:
		t.Fatal("the host stopped reading the program's output")
	}
}

// fakes is a Spawner that starts fakeProcesses and keeps them for the test.
type fakes struct {
	ignoreTerminate bool
	orphans         bool
	writeGate       chan struct{}
	resizeErr       error
	writeErr        error
	afterKill       killOutcome
	// pid, when set, is every process's id.
	pid int
	// spawning, when set, gets a value once a spawn has begun, and the spawn then waits for
	// release to be closed.
	spawning chan struct{}
	release  chan struct{}
	err      error

	mu      sync.Mutex
	started []*fakeProcess
	specs   []pty.Spec
}

func (f *fakes) spawn(spec pty.Spec) (pty.Process, error) {
	if f.err != nil {
		return nil, f.err
	}

	if f.spawning != nil {
		f.spawning <- struct{}{}

		<-f.release
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	pid := f.pid
	if pid == 0 {
		pid = len(f.started) + 1000
	}

	process := &fakeProcess{
		pid:             pid,
		ignoreTerminate: f.ignoreTerminate,
		orphans:         f.orphans,
		writeGate:       f.writeGate,
		resizeErr:       f.resizeErr,
		writeErr:        f.writeErr,
		afterKill:       f.afterKill,
		output:          make(chan []byte),
		closed:          make(chan struct{}),
		exited:          make(chan struct{}),
		groupGone:       make(chan struct{}),
	}
	f.started = append(f.started, process)
	f.specs = append(f.specs, spec)

	process.unreachableAnswers.Store(unreachableLooks)

	return process, nil
}

// last is the process the latest Start made.
func (f *fakes) last(t *testing.T) *fakeProcess {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.started) == 0 {
		t.Fatal("no process was started")
	}

	return f.started[len(f.started)-1]
}

func (f *fakes) lastSpec(t *testing.T) pty.Spec {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.specs) == 0 {
		t.Fatal("no process was started")
	}

	return f.specs[len(f.specs)-1]
}

// testSpec is a valid Spec for the fake.
func testSpec(t *testing.T) pty.Spec {
	t.Helper()

	return pty.Spec{
		Argv: []string{"/bin/sh", "-c", "true"},
		Dir:  t.TempDir(),
		Env:  []string{"PATH=/usr/bin:/bin"},
		Name: "test",
	}
}

// newFakeHost is a Host over fakes with limits.
func newFakeHost(t *testing.T, limits pty.Limits) (*pty.Host, *fakes) {
	t.Helper()

	spawner := &fakes{}
	host := pty.NewHost(pty.Config{Spawner: spawner.spawn, Limits: limits})

	t.Cleanup(func() { _ = host.StopAll(context.WithoutCancel(t.Context())) })

	return host, spawner
}

// info is the session's entry in Live.
func info(t *testing.T, host *pty.Host, sessionID pty.ID) (pty.Info, bool) {
	t.Helper()

	for _, entry := range host.Live() {
		if entry.ID == sessionID {
			return entry, true
		}
	}

	return pty.Info{}, false
}

// waitEnded waits until the session has ended and returns its entry.
func waitEnded(t *testing.T, host *pty.Host, sessionID pty.ID) pty.Info {
	t.Helper()

	return waitEndedWithin(t, host, sessionID, waitLimit)
}

// waitEndedWithin is waitEnded for at most limit.
func waitEndedWithin(
	t *testing.T, host *pty.Host, sessionID pty.ID, limit time.Duration,
) pty.Info {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(limit))

	for time.Now().Before(deadline) {
		entry, found := info(t, host, sessionID)
		if !found {
			t.Fatalf("session %s is not in Live", sessionID)
		}

		if entry.ExitCode != nil {
			return entry
		}

		time.Sleep(waitPoll)
	}

	t.Fatalf("session %s did not end", sessionID)

	return pty.Info{}
}

// receive is the next chunk on output, or fails the test.
func receive(t *testing.T, output <-chan []byte) []byte {
	t.Helper()

	timer := time.NewTimer(testtime.Scaled(waitLimit))
	defer timer.Stop()

	select {
	case chunk, open := <-output:
		if !open {
			t.Fatal("the viewer's output closed")
		}

		return chunk
	case <-timer.C:
		t.Fatal("no output arrived")
	}

	return nil
}

// drainUntilClosed reads output until it closes, and returns everything read.
func drainUntilClosed(t *testing.T, output <-chan []byte) []byte {
	t.Helper()

	timer := time.NewTimer(testtime.Scaled(waitLimit))
	defer timer.Stop()

	var all []byte

	for {
		select {
		case chunk, open := <-output:
			if !open {
				return all
			}

			all = append(all, chunk...)
		case <-timer.C:
			t.Fatal("the viewer's output never closed")
		}
	}
}
