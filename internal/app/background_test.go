package app_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/app"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// askingProgram is a hosted fake that prints what a test feeds it and keeps what is typed.
type askingProgram struct {
	output chan []byte
	closed chan struct{}
	exited chan struct{}
	close  sync.Once
	exit   sync.Once

	mutex   sync.Mutex
	written []byte
}

func (p *askingProgram) Read(buffer []byte) (int, error) {
	select {
	case chunk := <-p.output:
		return copy(buffer, chunk), nil
	case <-p.closed:
		return 0, errProgramClosed
	}
}

func (p *askingProgram) Write(data []byte) (int, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.written = append(p.written, data...)

	return len(data), nil
}

func (p *askingProgram) Resize(pty.Size) error { return nil }
func (p *askingProgram) PID() int              { return 4343 }
func (p *askingProgram) Keyboard() (pty.Keyboard, error) {
	return pty.Keyboard{Foreground: true, LineMode: false}, nil
}
func (p *askingProgram) Terminate() error { return p.stop() }
func (p *askingProgram) Kill() error      { return p.stop() }

func (p *askingProgram) Group() pty.GroupState {
	select {
	case <-p.exited:
		return pty.GroupGone
	default:
		return pty.GroupRunning
	}
}

func (p *askingProgram) Wait() int {
	<-p.exited

	return 0
}

func (p *askingProgram) Close() error {
	p.close.Do(func() { close(p.closed) })

	return nil
}

func (p *askingProgram) stop() error {
	p.exit.Do(func() { close(p.exited) })

	return nil
}

func (p *askingProgram) typed() []byte {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	return bytes.Clone(p.written)
}

// TestAPagesLightThemeReachesTheHostsAnswer: the page says its terminal is drawn on a light
// background, and a program that then asks for the background (OSC 11) is answered with it.
func TestAPagesLightThemeReachesTheHostsAnswer(t *testing.T) {
	t.Parallel()

	program := &askingProgram{
		output: make(chan []byte), closed: make(chan struct{}), exited: make(chan struct{}),
		close: sync.Once{}, exit: sync.Once{}, mutex: sync.Mutex{}, written: nil,
	}
	host := pty.NewHost(pty.Config{
		Spawner: func(pty.Spec) (pty.Process, error) { return program, nil },
		Limits:  pty.DefaultLimits(), Answers: pty.Answers{}, TurnWaited: nil,
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	server, err := app.StartWith(ctx, lookupFrom(environment(t)), io.Discard, host)
	if err != nil {
		t.Fatal(err)
	}

	putBackground(t, server, `{"background":"#f3f5f9"}`)

	_, err = host.Start(t.Context(), pty.Spec{
		Argv: []string{"/bin/claude"}, Dir: t.TempDir(), Env: nil, Name: "hosted",
	})
	if err != nil {
		t.Fatal(err)
	}

	program.output <- []byte("\x1b]11;?\x1b\\")

	want := []byte("\x1b]11;rgb:f3f3/f5f5/f9f9\x1b\\")
	deadline := time.Now().Add(testtime.Scaled(quitWait))

	for !bytes.Contains(program.typed(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("the program was answered %q", program.typed())
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// putBackground sends the page's terminal background as the page does.
func putBackground(t *testing.T, server *app.Server, body string) {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPut,
		server.URL+"/api/term/background", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	request.Header.Set(secret.Header, server.Secret.Authorization())
	request.Header.Set("Origin", server.URL)
	request.Header.Set("X-Conn", "1")
	request.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	_ = response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/term/background = %d", response.StatusCode)
	}
}
