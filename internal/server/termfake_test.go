package server_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"weak"

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

	// writeErr, when set, is what every Write fails with; writeGate, when set, holds every
	// Write until it is closed.
	writeErr  error
	writeGate chan struct{}
	// writeReplies, when set, answers each Write in turn: nil takes it, an error fails it.
	writeReplies chan error

	mu       sync.Mutex
	written  []byte
	sizes    []pty.Size
	exitCode int
	attempts int
}

func newFakeProgram(programs *fakePrograms) *fakeProgram {
	return &fakeProgram{
		output: make(chan []byte), closed: make(chan struct{}), exited: make(chan struct{}),
		writeErr: programs.writeErr, writeGate: programs.writeGate,
		writeReplies: programs.writeReplies,
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
	f.mu.Lock()
	f.attempts++
	f.mu.Unlock()

	select {
	case <-f.closed:
		return 0, errFakeClosed
	case <-f.exited:
		// As a real terminal does once nothing holds the program's side.
		return 0, fmt.Errorf("write: %w", syscall.EIO)
	default:
	}

	if f.writeGate != nil {
		<-f.writeGate
	}

	if f.writeErr != nil {
		return 0, f.writeErr
	}

	if f.writeReplies != nil {
		reply := <-f.writeReplies
		if reply != nil {
			return 0, reply
		}
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

// attemptsSoFar is how many Writes the program was given, taken or not.
func (f *fakeProgram) attemptsSoFar() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.attempts
}

func (f *fakeProgram) sizesSoFar() []pty.Size {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.sizes)
}

// fakePrograms is a Spawner of fakePrograms that keeps the last one for the test; each program's
// writes fail with writeErr, or wait for writeGate, when they are set.
type fakePrograms struct {
	writeErr     error
	writeGate    chan struct{}
	writeReplies chan error

	mu   sync.Mutex
	last *fakeProgram
}

func (f *fakePrograms) spawn(pty.Spec) (pty.Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.last = newFakeProgram(f)

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

// scriptedHost is a host with no programs: every Subscribe gets replay and an Output the test
// closes, saying why with end. Input and resizes are taken and forgotten.
type scriptedHost struct {
	replay []byte
	// freshReplayBytes, when set, gives each viewer a replay of its own of that many bytes,
	// which the host keeps only weakly, so a test can see when it is collected.
	freshReplayBytes int
	weakReplays      []weak.Pointer[byte]

	mu         sync.Mutex
	outputs    []chan []byte
	end        pty.ViewerEnd
	subscribes int
}

func (s *scriptedHost) Subscribe(pty.ID) (pty.Viewer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	output := make(chan []byte)
	s.outputs = append(s.outputs, output)
	s.subscribes++

	replay := s.replay
	if s.freshReplayBytes > 0 {
		replay = bytes.Repeat([]byte{'f'}, s.freshReplayBytes)
		s.weakReplays = append(s.weakReplays, weak.Make(&replay[0]))
	}

	return pty.Viewer{Replay: replay, Output: output, Ending: s.ending}, nil
}

func (s *scriptedHost) Write(pty.ID, []byte) error { return nil }

func (s *scriptedHost) Resize(pty.ID, uint16, uint16) error { return nil }

func (s *scriptedHost) ending() pty.ViewerEnd {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.end
}

// closeAll closes every viewer's Output, saying why with end.
func (s *scriptedHost) closeAll(end pty.ViewerEnd) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.end = end

	for _, output := range s.outputs {
		close(output)
	}

	s.outputs = nil
}

func (s *scriptedHost) subscribed() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.subscribes
}

// replayCollected says whether the replay of the viewer at index has been collected.
func (s *scriptedHost) replayCollected(index int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.weakReplays[index].Value() == nil
}
