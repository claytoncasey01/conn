package desk_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/app"
	"github.com/claytoncasey01/conn/internal/desk"
	"github.com/claytoncasey01/conn/internal/hosted"
	"github.com/claytoncasey01/conn/internal/instance"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/testtime"
)

var errHeldProgramClosed = errors.New("the fake terminal is closed")

// heldProgram is a hosted fake that runs until it is stopped, and says whether it was.
type heldProgram struct {
	output  chan []byte
	closed  chan struct{}
	exited  chan struct{}
	stop    sync.Once
	shut    sync.Once
	stopped chan struct{}
}

func newHeldProgram() *heldProgram {
	return &heldProgram{
		output: make(chan []byte), closed: make(chan struct{}), exited: make(chan struct{}),
		stop: sync.Once{}, shut: sync.Once{}, stopped: make(chan struct{}),
	}
}

func (p *heldProgram) Read(buffer []byte) (int, error) {
	select {
	case chunk := <-p.output:
		return copy(buffer, chunk), nil
	case <-p.closed:
		return 0, errHeldProgramClosed
	}
}

func (p *heldProgram) Write(data []byte) (int, error) { return len(data), nil }
func (p *heldProgram) Resize(pty.Size) error          { return nil }
func (p *heldProgram) PID() int                       { return 4343 }
func (p *heldProgram) Keyboard() (pty.Keyboard, error) {
	return pty.Keyboard{Foreground: true, LineMode: false}, nil
}

func (p *heldProgram) Terminate() error {
	p.stop.Do(func() {
		close(p.stopped)
		close(p.exited)
	})

	return nil
}

func (p *heldProgram) Kill() error { return p.Terminate() }

func (p *heldProgram) Group() pty.GroupState {
	select {
	case <-p.exited:
		return pty.GroupGone
	default:
		return pty.GroupRunning
	}
}

func (p *heldProgram) Wait() int {
	<-p.exited

	return 0
}

func (p *heldProgram) Close() error {
	p.shut.Do(func() { close(p.closed) })

	return nil
}

func (p *heldProgram) wasStopped() bool {
	select {
	case <-p.stopped:
		return true
	default:
		return false
	}
}

// startOwner starts the Conn that owns the data folder, with one hosted session running.
func startOwner(t *testing.T, env map[string]string) (*app.Server, *heldProgram) {
	t.Helper()

	program := newHeldProgram()
	host := pty.NewHost(pty.Config{
		Spawner: func(pty.Spec) (pty.Process, error) { return program, nil },
		Limits:  pty.DefaultLimits(), Answers: pty.Answers{}, TurnWaited: nil,
	})

	_, err := host.Start(t.Context(), pty.Spec{
		Argv: []string{"/bin/claude"}, Dir: t.TempDir(), Env: nil, Name: "hosted",
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, stop := context.WithCancel(t.Context())

	owner, err := app.StartWith(ctx, lookupFrom(env), io.Discard, host)
	if err != nil {
		stop()
		t.Fatal(err)
	}

	t.Cleanup(func() {
		stop()
		<-owner.Done()
	})

	// The owner serves at once and resumes while it serves: its first record is written then.
	select {
	case <-owner.Resumed():
	case <-time.After(testtime.Scaled(runWait)):
		t.Fatal("the owner never finished its resume")
	}

	return owner, program
}

// The window of a second app attached to a running Conn does not own its sessions: its quit
// stops none of them and leaves the record as it was.
func TestAnAttachedWindowsQuitStopsNoSession(t *testing.T) {
	t.Parallel()

	env := environment(t)
	_, program := startOwner(t, env)
	recordPath := filepath.Join(env["CONN_DATA"], hosted.FileName)

	before, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}

	opened := make(chan string, 1)
	ctx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(runWait))

	defer cancel()

	result := make(chan error, 1)

	go func() {
		result <- desk.Run(ctx, desk.Deps{
			Start: func(context.Context) (*app.Server, error) {
				return nil, errStartedOnAHeldFolder
			},
			Holder: func() (instance.Holder, bool, error) {
				return instance.Running(env["CONN_DATA"])
			},
			Probe: func(context.Context, string, secret.Secret) error { return nil },
			Open:  func(url string) { opened <- url },
			Fail:  func(message string) { t.Errorf("a window was told: %s", message) },
			After: func(time.Duration) <-chan time.Time { return nil },
		})
	}()

	<-opened
	cancel()

	err = <-result
	if err != nil {
		t.Errorf("Run error = %v after its context ended, want nil", err)
	}

	if program.wasStopped() {
		t.Error("the attached window's quit stopped the owner's session")
	}

	after, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(after, before) {
		t.Errorf("the record changed from %s to %s", before, after)
	}
}
