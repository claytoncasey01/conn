// Package drysession is the dry run's stand-in for claude (CONN_WORK_DRY): a session hosted
// like any other, whose program is a script in this process. It prints a banner and a prompt,
// echoes what is typed and answers Enter with a line of its own, so the terminal panel, typing
// and stopping can be tried end to end with nothing run and nothing sent anywhere.
//
// A dry session is asked for by its program, Program: Spawner hands that one to the script and
// every other Spec to the real spawner. Program is a path no program can be at (a folder under
// /dev/null, which is a file), so a real spawner given it fails to start anything: a dry spec
// that missed the script still runs nothing, and no other spec is ever scripted.
package drysession

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/claytoncasey01/conn/internal/argv"
	"github.com/claytoncasey01/conn/internal/pty"
)

// Program is the program of a dry session.
const Program = "/dev/null/conn-dry-session"

const (
	// pidFirst is the first process id a dry session reports: above any process id macOS
	// (99,999) or Linux (4,194,304) gives, so the session list never takes a real claude's
	// registry entry for a dry session. The host signals only through the Process, never by
	// the number.
	pidFirst = 1 << 30
	// outputChunksMax bounds the output a dry session holds for the host's reader; more is
	// dropped, so a write never waits for the reader.
	outputChunksMax = 64
	// lineBytesMax bounds the line a dry session keeps of what is typed; more is not echoed.
	lineBytesMax = 4 << 10
	// terminatedCode and killedCode are the exit codes a stop leaves, as a signal would.
	terminatedCode = 143
	killedCode     = 137
)

// The bytes a dry session answers keys with.
const (
	prompt     = "\x1b[1m>\x1b[0m "
	newline    = "\r\n"
	erase      = "\b \b"
	interrupt  = "^C"
	keyEnter   = '\r'
	keyErase   = 0x7f
	keyCancel  = 0x03
	keyEscape  = 0x1b
	printFirst = 0x20
)

// errClosed is a read or write after the session's terminal was closed.
var errClosed = errors.New("the dry session's terminal is closed")

// Spec is a dry session for a run: the script, in folder, named name.
func Spec(folder, name string) pty.Spec {
	return pty.Spec{Argv: []string{Program, "--name", name}, Dir: folder, Env: nil, Name: name}
}

// Spawner is spawn for every Spec but a dry one, whose program is Program: that one starts a
// scripted session in this process.
func Spawner(spawn pty.Spawner) pty.Spawner {
	var started atomic.Int64

	return func(spec pty.Spec) (pty.Process, error) {
		if spec.Argv[0] != Program {
			return spawn(spec)
		}

		return newScript(pidFirst+int(started.Add(1)), spec.Name), nil
	}
}

// script is one dry session: its output for the host to read, the line typed so far, and how
// it ended.
type script struct {
	pid    int
	output chan []byte
	closed chan struct{}
	exited chan struct{}

	closeOnce sync.Once
	exitOnce  sync.Once

	mu       sync.Mutex
	line     []byte
	escaping bool
	exitCode int
}

func newScript(pid int, name string) *script {
	started := &script{
		pid: pid, output: make(chan []byte, outputChunksMax), closed: make(chan struct{}),
		exited: make(chan struct{}), closeOnce: sync.Once{}, exitOnce: sync.Once{},
		mu: sync.Mutex{}, line: nil, escaping: false, exitCode: 0,
	}
	started.say("Dry run: a stand-in for claude, " + quoted(name) + ". Nothing runs here." +
		newline + "Type something and press Enter." + newline + prompt)

	return started
}

// quoted is name in quotes, with every control character left out (C0, DEL, C1 and the bidi
// controls, as argv.Control finds them): the name is Conn's own, but it is drawn on a terminal.
func quoted(name string) string {
	kept := make([]rune, 0, len(name))

	for _, char := range name {
		if !argv.Control(char) {
			kept = append(kept, char)
		}
	}

	return `"` + string(kept) + `"`
}

func (s *script) Read(buffer []byte) (int, error) {
	select {
	case chunk := <-s.output:
		return copy(buffer, chunk), nil
	case <-s.closed:
		return 0, errClosed
	case <-s.exited:
		return 0, errClosed
	}
}

// Write takes keys: printable ones are echoed, Backspace erases, Enter answers, Ctrl-C starts
// the line again. Escape sequences (arrows, a bracketed paste's marks) are dropped.
func (s *script) Write(data []byte) (int, error) {
	select {
	case <-s.closed:
		return 0, errClosed
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, key := range data {
		s.key(key)
	}

	return len(data), nil
}

// endsSequence is whether key ends an escape sequence.
func endsSequence(key byte) bool {
	if key == '~' {
		return true
	}

	if key >= 'A' {
		if key <= 'Z' {
			return true
		}
	}

	if key >= 'a' {
		return key <= 'z'
	}

	return false
}

func (s *script) Resize(pty.Size) error { return nil }

func (s *script) PID() int { return s.pid }

func (s *script) Terminate() error {
	s.exit(terminatedCode)

	return nil
}

func (s *script) Kill() error {
	s.exit(killedCode)

	return nil
}

// Group is gone once the script has ended: it never starts anything.
func (s *script) Group() pty.GroupState {
	select {
	case <-s.exited:
		return pty.GroupGone
	default:
		return pty.GroupRunning
	}
}

func (s *script) Keyboard() (pty.Keyboard, error) {
	return pty.Keyboard{Foreground: true, LineMode: false}, nil
}

func (s *script) Wait() int {
	<-s.exited

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.exitCode
}

func (s *script) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })

	return nil
}

// exit ends the script with code; the first call wins.
func (s *script) exit(code int) {
	s.exitOnce.Do(func() {
		s.mu.Lock()
		s.exitCode = code
		s.mu.Unlock()
		close(s.exited)
	})
}

// say queues text for the host to read, dropping it when the queue is full.
func (s *script) say(text string) {
	select {
	case s.output <- []byte(text):
	default:
	}
}

// key answers one key. The caller holds mu.
func (s *script) key(key byte) {
	switch {
	case s.escaping:
		// A sequence ends with a letter or a tilde: arrows, function keys and paste marks.
		s.escaping = !endsSequence(key)
	case key == keyEscape:
		s.escaping = true
	case key == keyEnter:
		s.say(newline + "(dry run) Noted: nothing was sent." + newline + prompt)
		s.line = s.line[:0]
	case key == keyErase:
		if len(s.line) > 0 {
			s.line = s.line[:len(s.line)-1]
			s.say(erase)
		}
	case key == keyCancel:
		s.say(interrupt + newline + prompt)
		s.line = s.line[:0]
	case key < printFirst:
	default:
		if len(s.line) < lineBytesMax {
			s.line = append(s.line, key)
			s.say(string([]byte{key}))
		}
	}
}
