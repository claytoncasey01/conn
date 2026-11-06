package server_test

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// Exit codes the fake program reports when it is stopped.
const (
	fakeTerminatedCode = 143
	fakeKilledCode     = 137
	// fakePID is every fake program's process id: above 1, as the host requires.
	fakePID = 4242
)

// waitLimit bounds every wait in the terminal tests, and waitPoll is how often a poll looks.
const (
	waitLimit = 5 * time.Second
	waitPoll  = 5 * time.Millisecond
)

var errFakeClosed = errors.New("the fake terminal is closed")

// fakeProgram is a scripted program on a terminal for a real pty.Host: a test feeds what it
// prints, reads what was typed into it and the sizes it was given, and ends it.
type fakeProgram struct {
	output    chan []byte
	closed    chan struct{}
	closeOnce sync.Once
	exited    chan struct{}
	exitOnce  sync.Once

	mu       sync.Mutex
	written  []byte
	sizes    []pty.Size
	exitCode int
}

func newFakeProgram() *fakeProgram {
	return &fakeProgram{
		output: make(chan []byte), closed: make(chan struct{}), exited: make(chan struct{}),
	}
}

func (f *fakeProgram) Read(buffer []byte) (int, error) {
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

func (f *fakeProgram) Write(data []byte) (int, error) {
	select {
	case <-f.closed:
		return 0, errFakeClosed
	default:
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.written = append(f.written, data...)

	return len(data), nil
}

func (f *fakeProgram) Resize(size pty.Size) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.sizes = append(f.sizes, size)

	return nil
}

func (f *fakeProgram) PID() int { return fakePID }

func (f *fakeProgram) Terminate() error {
	f.exit(fakeTerminatedCode)

	return nil
}

func (f *fakeProgram) Kill() error {
	f.exit(fakeKilledCode)

	return nil
}

func (f *fakeProgram) Group() pty.GroupState {
	select {
	case <-f.exited:
		return pty.GroupGone
	default:
		return pty.GroupRunning
	}
}

func (f *fakeProgram) Wait() int {
	<-f.exited

	f.mu.Lock()
	defer f.mu.Unlock()

	return f.exitCode
}

func (f *fakeProgram) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })

	return nil
}

// exit ends the program with code; the first call wins.
func (f *fakeProgram) exit(code int) {
	f.exitOnce.Do(func() {
		f.mu.Lock()
		f.exitCode = code
		f.mu.Unlock()
		close(f.exited)
	})
}

// finish ends the program with code after what it has printed so far, as a program that exits
// by itself does. Call it once.
func (f *fakeProgram) finish(code int) {
	f.exit(code)
	close(f.output)
}

// feed hands the host one chunk of output, failing the test if the host's reader does not take
// it in time: the reader must never block, whatever a viewer does.
func (f *fakeProgram) feed(t *testing.T, chunk []byte) {
	t.Helper()

	timer := time.NewTimer(testtime.Scaled(waitLimit))
	defer timer.Stop()

	select {
	case f.output <- chunk:
	case <-timer.C:
		t.Fatal("the host stopped reading the program's output")
	}
}

func (f *fakeProgram) writtenSoFar() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.written)
}

func (f *fakeProgram) sizesSoFar() []pty.Size {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.sizes)
}

// fakePrograms is a Spawner of fakePrograms that keeps the last one for the test.
type fakePrograms struct {
	mu   sync.Mutex
	last *fakeProgram
}

func (f *fakePrograms) spawn(pty.Spec) (pty.Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.last = newFakeProgram()

	return f.last, nil
}

func (f *fakePrograms) lastStarted() *fakeProgram {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.last
}

// countedHost is the host behind the terminal route, counting every call the route makes to
// it: a refused handshake must make none.
type countedHost struct {
	host  *pty.Host
	calls atomic.Int64
}

func (c *countedHost) Subscribe(sessionID pty.ID) (pty.Viewer, error) {
	c.calls.Add(1)

	viewer, err := c.host.Subscribe(sessionID)
	if err != nil {
		return pty.Viewer{}, fmt.Errorf("subscribing: %w", err)
	}

	return viewer, nil
}

func (c *countedHost) Write(sessionID pty.ID, data []byte) error {
	c.calls.Add(1)

	err := c.host.Write(sessionID, data)
	if err != nil {
		return fmt.Errorf("writing: %w", err)
	}

	return nil
}

func (c *countedHost) Resize(sessionID pty.ID, columns, rows uint16) error {
	c.calls.Add(1)

	err := c.host.Resize(sessionID, columns, rows)
	if err != nil {
		return fmt.Errorf("resizing: %w", err)
	}

	return nil
}

func (c *countedHost) Live() []pty.Info {
	c.calls.Add(1)

	return c.host.Live()
}
