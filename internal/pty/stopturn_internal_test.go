package pty

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/testtime"
)

var errTurnFakeClosed = errors.New("closed")

// turnFake is a program whose Terminate notes whether its session's write turn was held then.
type turnFake struct {
	output    chan []byte
	closed    chan struct{}
	closeOnce sync.Once
	exited    chan struct{}
	exitOnce  sync.Once
	gone      atomic.Bool
	// heldAtTerminate is set by Terminate: whether held said the turn was taken.
	held            func() bool
	terminates      atomic.Int32
	heldAtTerminate atomic.Bool
}

func (f *turnFake) Read(buffer []byte) (int, error) {
	select {
	case chunk, open := <-f.output:
		if !open {
			return 0, io.EOF
		}

		return copy(buffer, chunk), nil
	case <-f.closed:
		return 0, errTurnFakeClosed
	}
}

func (f *turnFake) Write(data []byte) (int, error) { return len(data), nil }
func (f *turnFake) Resize(Size) error              { return nil }
func (f *turnFake) PID() int                       { return 4242 }

func (f *turnFake) Terminate() error {
	f.terminates.Add(1)
	f.heldAtTerminate.Store(f.held())
	f.gone.Store(true)
	f.exitOnce.Do(func() { close(f.exited) })

	return nil
}

func (f *turnFake) Kill() error { return f.Terminate() }

func (f *turnFake) Group() GroupState {
	if f.gone.Load() {
		return GroupGone
	}

	return GroupRunning
}

func (f *turnFake) Keyboard() (Keyboard, error) {
	return Keyboard{Foreground: true, LineMode: false}, nil
}

func (f *turnFake) Wait() int {
	<-f.exited

	return terminatedExit
}

func (f *turnFake) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })

	return nil
}

// terminatedExit is the exit code the fake reports.
const terminatedExit = 143

// turnHost is a host of one turnFake session, and the session.
func turnHost(t *testing.T) (*Host, *turnFake, ID) {
	t.Helper()

	fake := &turnFake{
		output: make(chan []byte), closed: make(chan struct{}), exited: make(chan struct{}),
	}
	host := NewHost(Config{
		Spawner: func(Spec) (Process, error) { return fake, nil }, Limits: DefaultLimits(),
		Answers: Answers{}, TurnWaited: nil,
	})

	t.Cleanup(func() { _ = host.StopAll(context.WithoutCancel(t.Context())) })

	sessionID, err := host.Start(t.Context(), Spec{
		Argv: []string{"/bin/true"}, Dir: "/", Env: nil, Name: "turn",
	})
	if err != nil {
		t.Fatal(err)
	}

	held, err := host.find(sessionID)
	if err != nil {
		t.Fatal(err)
	}

	fake.held = func() bool { return len(held.writeTurn) == 1 }

	return host, fake, sessionID
}

// TestStopIfStillSendsThePoliteStopInsideTheWriteTurn: the polite stop is sent while the write
// turn is held, so no input can reach the program between the check and the stop.
func TestStopIfStillSendsThePoliteStopInsideTheWriteTurn(t *testing.T) {
	t.Parallel()

	host, fake, sessionID := turnHost(t)

	mark := quietMark(t, host, sessionID)

	err := host.StopIfStill(t.Context(), sessionID, mark, 1, func([]string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}

	if fake.terminates.Load() < 1 {
		t.Fatal("no polite stop")
	}

	if !fake.heldAtTerminate.Load() {
		t.Error("the polite stop was sent outside the write turn")
	}
}

// TestStopIfStillSignalsNoGroupThatIsGone: a group that emptied just before is not signalled in
// the turn: its number may belong to another program by now.
func TestStopIfStillSignalsNoGroupThatIsGone(t *testing.T) {
	t.Parallel()

	host, fake, sessionID := turnHost(t)

	mark := quietMark(t, host, sessionID)

	fake.gone.Store(true)

	ctx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(stopWait))
	defer cancel()

	_ = host.StopIfStill(ctx, sessionID, mark, 1, func([]string) bool { return true })

	if fake.terminates.Load() != 0 {
		t.Errorf("a gone group was signalled %d times", fake.terminates.Load())
	}
}

// TestStopIfStillSignalsNoGroupKnownEmpty: a group the host knows emptied is never signalled
// again, even when its number answers as if taken: a new program may have been given it.
func TestStopIfStillSignalsNoGroupKnownEmpty(t *testing.T) {
	t.Parallel()

	host, fake, sessionID := turnHost(t)

	mark := quietMark(t, host, sessionID)

	held, err := host.find(sessionID)
	if err != nil {
		t.Fatal(err)
	}

	host.groupEmptied(held)

	ctx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(stopWait))
	defer cancel()

	_ = host.StopIfStill(ctx, sessionID, mark, 1, func([]string) bool { return true })

	if fake.terminates.Load() != 0 {
		t.Errorf("a group known empty was signalled %d times", fake.terminates.Load())
	}
}

const (
	// turnWaitLimit bounds how long a test waits for the session's input to settle.
	turnWaitLimit = 5 * time.Second
	// stopWait bounds a stop whose answer the test does not look at.
	stopWait = 200 * time.Millisecond
	// quietPoll is how often quietMark looks.
	quietPoll = 10 * time.Millisecond
)

// quietMark is a screen read taken once the session's input has settled, so StopIfStill reaches
// its check in the turn rather than refusing the read as one other input may have moved.
func quietMark(t *testing.T, host *Host, sessionID ID) ScreenMark {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(turnWaitLimit))

	for time.Now().Before(deadline) {
		_, mark, err := host.ScreenLines(sessionID, 1)
		if err != nil {
			t.Fatal(err)
		}

		if mark.Quiet {
			return mark
		}

		time.Sleep(quietPoll)
	}

	t.Fatal("the session's input never settled")

	return ScreenMark{}
}
