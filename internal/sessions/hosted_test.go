package sessions_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// replyLinesRead is how many screen lines a test reads.
const replyLinesRead = 40

// settledAfterBase is longer than the host's settle window for input (250 ms).
const settledAfterBase = 400 * time.Millisecond

// settledAfter is how long a test lets pass for input to count as settled, while it holds a write
// or has a fake program wait: a lower bound, which a slow machine only makes longer.
func settledAfter() time.Duration { return testtime.Scaled(settledAfterBase) }

// waitSettled waits until a screen read of the session counts as settled (no input for the host's
// settle window), so what the test does next sees the screen the host trusts.
func waitSettled(t *testing.T, host *pty.Host, hostedID string) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(screenWaitLimit))

	for time.Now().Before(deadline) {
		_, mark, err := host.ScreenLines(pty.ID(hostedID), 1)
		if err != nil {
			t.Fatal(err)
		}

		if mark.Quiet {
			return
		}

		time.Sleep(settledPoll)
	}

	t.Fatal("the session's input never settled")
}

// notSent is the answer to a reply whose message was typed and whose Enter was not pressed.
const notSent = `{"error":"the message is in the session's input box but was not sent, ` +
	`because its terminal changed meanwhile; send it from its terminal"}`

// endShownWithin bounds the wait for an ended session's row, unscaled: it must stay below the 5 s
// a snapshot is reused while its version stands.
const endShownWithin = 4 * time.Second

// questionPicker is Claude Code asking a question, its last row selected between two rules.
func questionPicker() string {
	return strings.Join([]string{
		rule, " ☐ Color", "", "Red or blue?", "", "  1. Red", "     Pick red", "  2. Blue",
		"     Pick blue", "  3. Type something.", rule, "❯ 4. Chat about this", "",
		"Enter to select · ↑/↓ to navigate · Esc to cancel",
	}, "\n")
}

// programExitCode is what a hosted fake program exits with once it is stopped.
const programExitCode = 143

var errProgramClosed = errors.New("the fake terminal is closed")

// program is a hosted fake: a test sets what its screen shows and reads what was typed into it.
type program struct {
	pid       int
	output    chan []byte
	closed    chan struct{}
	closeOnce sync.Once
	exited    chan struct{}
	exitOnce  sync.Once

	// gate, when set (holdWrites), holds every Write until it is closed; writing is told of a
	// held one.
	gate    atomic.Pointer[chan struct{}]
	writing chan struct{}
	// echo is the screen drawn once a message is typed, echoDelay after it is typed; when it is
	// "", the box with the message's first line in it, as Claude Code draws it.
	echo      string
	echoDelay time.Duration
	// background, when set, says a program this one started has the terminal; lineMode that a
	// program reads a line on it.
	background atomic.Bool
	lineMode   atomic.Bool
	// cut, when above zero, is how many bytes the next Write takes before it fails as a
	// terminal's write deadline does.
	cut atomic.Int32

	mutex   sync.Mutex
	written []byte
	signals []string
}

func (p *program) Read(buffer []byte) (int, error) {
	select {
	case chunk := <-p.output:
		return copy(buffer, chunk), nil
	case <-p.closed:
		return 0, errProgramClosed
	}
}

func (p *program) Write(data []byte) (int, error) {
	if gate := p.gate.Load(); gate != nil {
		select {
		case p.writing <- struct{}{}:
		default:
		}

		<-*gate
	}

	if cut := int(p.cut.Swap(0)); cut > 0 {
		taken := min(cut, len(data))

		p.mutex.Lock()
		p.written = append(p.written, data[:taken]...)
		p.mutex.Unlock()

		return taken, fmt.Errorf("writing to the terminal: %w", os.ErrDeadlineExceeded)
	}

	p.mutex.Lock()
	p.written = append(p.written, data...)
	p.mutex.Unlock()

	if typedMessage(data) {
		screen := p.echo
		if screen == "" {
			screen = boxWith(string(data))
		}

		go func() {
			time.Sleep(p.echoDelay)
			p.draw(screen)
		}()
	}

	return len(data), nil
}

// boxWith is Claude Code's input box holding the first line of the message typed as data.
func boxWith(data string) string {
	message := strings.TrimSuffix(strings.TrimPrefix(data, "\x1b[200~"), "\x1b[201~")
	first, _, _ := strings.Cut(message, "\n")

	return "✻ done\n\n" + rule + "\n❯ " + first + "\n" + rule
}

// typedMessage is whether data is a message typed in, not a key, Enter or a query's answer.
func typedMessage(data []byte) bool {
	text := string(data)
	if strings.HasPrefix(text, "\x1b[200~") {
		return true
	}

	if len(text) < 2 {
		return false
	}

	return !strings.HasPrefix(text, "\x1b")
}

func (p *program) Keyboard() (pty.Keyboard, error) {
	return pty.Keyboard{Foreground: !p.background.Load(), LineMode: p.lineMode.Load()}, nil
}

func (p *program) Resize(pty.Size) error { return nil }

func (p *program) PID() int { return p.pid }

func (p *program) Terminate() error { return p.signal("terminate") }

func (p *program) Kill() error { return p.signal("kill") }

func (p *program) Group() pty.GroupState {
	select {
	case <-p.exited:
		return pty.GroupGone
	default:
		return pty.GroupRunning
	}
}

func (p *program) Wait() int {
	<-p.exited

	return programExitCode
}

func (p *program) Close() error {
	p.closeOnce.Do(func() { close(p.closed) })

	return nil
}

func (p *program) signal(name string) error {
	p.mutex.Lock()
	p.signals = append(p.signals, name)
	p.mutex.Unlock()

	p.exitOnce.Do(func() { close(p.exited) })

	return nil
}

// draw clears the screen and draws screen, unless the terminal is closed first.
func (p *program) draw(screen string) {
	select {
	case p.output <- []byte("\x1b[2J\x1b[H" + strings.ReplaceAll(screen, "\n", "\r\n")):
	case <-p.closed:
	}
}

func (p *program) typed() string {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	return string(p.written)
}

func (p *program) stops() []string {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	return slices.Clone(p.signals)
}

// holdWrites holds every Write from now on until the function it returns is called.
func (p *program) holdWrites() func() {
	gate := make(chan struct{})
	p.gate.Store(&gate)

	return func() { close(gate) }
}

// waitWriting waits until a Write is held at the gate.
func (p *program) waitWriting(t *testing.T) {
	t.Helper()

	select {
	case <-p.writing:
	case <-time.After(testtime.Scaled(5 * time.Second)):
		t.Fatal("nothing was typed into the program")
	}
}

// show clears the program's screen and draws screen on it.
func (p *program) show(t *testing.T, screen string) {
	t.Helper()

	chunk := []byte("\x1b[2J\x1b[H" + strings.ReplaceAll(screen, "\n", "\r\n"))

	select {
	case p.output <- chunk:
	case <-time.After(testtime.Scaled(5 * time.Second)):
		t.Fatal("the host did not read the program's output")
	}
}

// spawn is the host's Spawner: a fake program with the next process id.
func (w *world) spawn(pty.Spec) (pty.Process, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	started := &program{
		pid: w.spawning, output: make(chan []byte), closed: make(chan struct{}),
		closeOnce: sync.Once{}, exited: make(chan struct{}), exitOnce: sync.Once{},
		gate: atomic.Pointer[chan struct{}]{}, writing: make(chan struct{}, 1), echo: "",
		echoDelay: 0, background: atomic.Bool{}, lineMode: atomic.Bool{},
		mutex: sync.Mutex{}, written: nil, signals: nil,
	}
	w.programs[w.spawning] = started

	return started, nil
}

// hostedSession starts a hosted fake program as process pid, named name, showing screen. With a
// status it also registers as a live claude does.
func (w *world) hostedSession(pid int, name, status, waitingFor, screen string,
	stamp float64,
) (string, *program) {
	w.t.Helper()

	if w.host == nil {
		parked := w.parked
		w.host = pty.NewHost(pty.Config{
			Spawner: w.spawn, Limits: pty.DefaultLimits(), Answers: pty.Answers{},
			TurnWaited: func(sessionID pty.ID) {
				select {
				case parked <- sessionID:
				default:
				}
			},
		})
		host := w.host

		w.t.Cleanup(func() { _ = host.StopAll(context.WithoutCancel(w.t.Context())) })
	}

	folder := w.folder("hosted-"+strconv.Itoa(pid), "")

	w.mutex.Lock()
	w.spawning = pid
	w.mutex.Unlock()

	sessionID, err := w.host.Start(w.t.Context(), pty.Spec{
		Argv: []string{"/bin/claude"}, Dir: folder, Env: nil, Name: name,
	})
	if err != nil {
		w.t.Fatal(err)
	}

	started := w.programs[pid]
	// Claude Code asks for bracketed pastes. The host takes the screen on its own goroutine, so
	// it is waited for: a test acts on a screen that is drawn, however slow the machine.
	started.show(w.t, "\x1b[?2004h"+screen)
	waitDrawn(w.t, w.host, sessionID, screen)

	if status != "" {
		w.procs = append(w.procs, strconv.Itoa(pid)+" 1 claude")

		entry := map[string]any{
			"pid": pid, "sessionId": "s-" + strconv.Itoa(pid), "cwd": folder,
			"name": "n" + strconv.Itoa(pid), "status": status, "statusUpdatedAt": stamp,
		}
		if waitingFor != "" {
			entry["waitingFor"] = waitingFor
		}

		w.register(strconv.Itoa(pid)+".json", entry)
	}

	return string(sessionID), started
}

func (w *world) envRead(pid float64) bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	return slices.Contains(w.envAsked, pid)
}

// rowOf waits until the snapshot has a row with id that passes ready, and returns it.
func rowOf(t *testing.T, loop *sessions.Loop, rowID string, ready func(detail) bool) detail {
	t.Helper()

	return rowWithin(t, loop, rowID, ready, testtime.Scaled(5*time.Second))
}

// rowWithin is rowOf waiting at most limit, which is not scaled.
func rowWithin(t *testing.T, loop *sessions.Loop, rowID string, ready func(detail) bool,
	limit time.Duration,
) detail {
	t.Helper()

	deadline := time.Now().Add(limit)

	for time.Now().Before(deadline) {
		got, _ := read(t, loop)

		for _, row := range got.Sessions {
			if row.ID == rowID {
				if ready(row) {
					return row
				}
			}
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("no row %s as wanted", rowID)

	return detail{}
}

func anyRow(detail) bool { return true }

func ids(got snap) []string {
	out := make([]string, 0, len(got.Sessions))
	for _, row := range got.Sessions {
		out = append(out, row.ID)
	}

	return out
}

// A hosted session is a row under the id the host issued, never its pid: one that registers
// takes its registry entry's fields, one that has not yet is named as it was started, and one
// that has ended can no longer be replied to.
func TestAHostedSessionIsARowUnderItsIssuedID(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	registered, _ := fake.hostedSession(40, "build-1", "idle", "", idleBox, 1)
	// The host's record names the process, whatever the program's file is called.
	fake.procs[len(fake.procs)-1] = "40 1 2.1.319"
	unregistered, _ := fake.hostedSession(41, "build-2", "", "", idleBox, 1)
	ended, _ := fake.hostedSession(42, "build-3", "", "", idleBox, 1)
	offList, _ := fake.hostedSession(58, "build-5", "idle", "", idleBox, 1)
	// A repository off the allowlist hides a person's session; Conn started this one.
	fake.repos[filepath.Join(fake.home, "work", "hosted-58")] = "other/hidden"

	err := fake.host.Stop(t.Context(), pty.ID(ended))
	if err != nil {
		t.Fatal(err)
	}

	loop := start(t, fake)

	tests := []struct {
		name, id, wantName, wantStatus string
		wantCanReply                   bool
		wantExit                       bool
	}{
		{"registered", registered, "n40", "idle", true, false},
		{"not registered yet", unregistered, "build-2", "idle", true, false},
		{"ended", ended, "build-3", "idle", false, true},
		{"in a repository off the allowlist", offList, "n58", "idle", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			row := rowOf(t, loop, tt.id, anyRow)
			if !row.Hosted {
				t.Error("the row is not marked hosted")
			}

			if row.Name != tt.wantName {
				t.Errorf("name %q, want %q", row.Name, tt.wantName)
			}

			if row.Status != tt.wantStatus {
				t.Errorf("status %q, want %q", row.Status, tt.wantStatus)
			}

			if row.CanReply != tt.wantCanReply {
				t.Errorf("canReply %v, want %v", row.CanReply, tt.wantCanReply)
			}

			if (row.ExitCode != nil) != tt.wantExit {
				t.Errorf("exitCode %v, want one: %v", row.ExitCode, tt.wantExit)
			}
		})
	}

	got, _ := read(t, loop)
	if slices.Contains(ids(got), "40") {
		t.Errorf("the hosted session is also a row under its pid: %v", ids(got))
	}
}

// A hosted session that ends while it is listed stays a row, and can no longer be replied to.
func TestAHostedSessionThatEndsCanNoLongerBeRepliedTo(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	hostedID, _ := fake.hostedSession(43, "build-4", "idle", "", idleBox, 1)
	loop := start(t, fake)

	rowOf(t, loop, hostedID, func(row detail) bool { return row.CanReply })

	err := fake.host.Stop(t.Context(), pty.ID(hostedID))
	if err != nil {
		t.Fatal(err)
	}

	// Well within the 5 s a snapshot is reused while its version stands, whatever the test time
	// scale, so it is the end that moved the version.
	row := rowWithin(t, loop, hostedID, func(row detail) bool { return row.ExitCode != nil },
		endShownWithin)
	if row.CanReply {
		t.Error("an ended hosted session can still be replied to")
	}

	if row.Name != "n43" {
		t.Errorf("the ended row is named %q, not as it last showed", row.Name)
	}

	reply := loop.Reply(t.Context(), hostedID, "hello")
	if body(reply) != `{"error":"the session has ended, so nothing was sent","ask":null}` {
		t.Errorf("reply to an ended session: %d %s", reply.Code, body(reply))
	}
}

// A reply types into a hosted session's program, as a paste and Enter, only once its screen
// shows an empty input box; anything else is refused with nothing typed.
func TestReplyToAHostedSessionTypesOnlyIntoAnEmptyBox(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	empty, emptyProgram := fake.hostedSession(44, "a", "idle", "", idleBox, 1)
	drafted, draftedProgram := fake.hostedSession(45, "b", "idle", "", draft, 1)
	asking, askingProgram := fake.hostedSession(46, "c", "waiting", "permission prompt",
		idleBox, 1)
	loop := start(t, fake)

	rowOf(t, loop, asking, func(row detail) bool { return row.Status == "waiting" })

	tests := []struct {
		name, id, text, want string
		code                 int
		typedInto            *program
		wantTyped            string
	}{
		{
			"an empty box", empty, "hello\nthere", `{"ok":true}`, http.StatusOK, emptyProgram,
			"\x1b[200~hello\nthere\x1b[201~\r",
		},
		{
			"a draft", drafted, "hello", `{"error":"the prompt already has text in it (a draft ` +
				`or a suggestion); clear it in the terminal first"}`, http.StatusConflict,
			draftedProgram, "",
		},
		{
			"a permission prompt", asking, "hello", `{"error":"it is waiting on a permission ` +
				`prompt: attach to the terminal to answer it"}`, http.StatusConflict,
			askingProgram, "",
		},
		{
			"a slash command", empty, "/clear", `{"error":"a leading ! or / would run a shell ` +
				`command or slash command; type those in the terminal"}`, http.StatusBadRequest,
			nil, "",
		},
		{
			"a C1 control", empty, "a\u009b201~b", `{"error":"control characters are not sent"}`,
			http.StatusBadRequest, nil, "",
		},
		{
			"an id the host never issued", "0123456789abcdef0123456789abcdef", "hello",
			`{"error":"no such live session"}`, http.StatusNotFound, nil, "",
		},
		{
			"the pid of a hosted session", "44", "hello", `{"error":"no such live session"}`,
			http.StatusNotFound, nil, "",
		},
	}

	for _, tt := range tests {
		got := loop.Reply(t.Context(), tt.id, tt.text)
		if got.Code != tt.code {
			t.Errorf("%s: code %d, want %d", tt.name, got.Code, tt.code)
		}

		if body(got) != tt.want {
			t.Errorf("%s: %s", tt.name, body(got))
		}

		if tt.typedInto == nil {
			continue
		}

		if typed := tt.typedInto.typed(); typed != tt.wantTyped {
			t.Errorf("%s: typed %q, want %q", tt.name, typed, tt.wantTyped)
		}
	}

	if typed := emptyProgram.typed(); typed != "\x1b[200~hello\nthere\x1b[201~\r" {
		t.Errorf("a refused reply typed something: %q", typed)
	}

	if log := fake.term.sent(); len(log) != 0 {
		t.Errorf("a hosted reply reached cmux: %v", log)
	}
}

// An answer to a hosted session presses the key of the prompt its screen still shows, read
// from the host's model of its terminal; a prompt that changed since it was shown is refused
// with the ask as it is now, and a second press of the same prompt is refused.
func TestAnswerToAHostedSessionPressesTheKeyOfThePromptShown(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	stamp := float64(time.Now().UnixMilli() - 5000)
	hostedID, hostedProgram := fake.hostedSession(47, "d", "waiting", "permission prompt",
		permissionScreen("ls"), stamp)
	// A person's session in a repository off the allowlist is never shown, so never looked up
	// in cmux: a hosted session shares no workspace with it.
	fake.procs = append(fake.procs, "70 1 claude")
	fake.register("70.json", map[string]any{
		"pid": 70, "sessionId": "s-70", "cwd": fake.folder("hidden", "other/hidden"),
		"name": "n70", "status": "idle",
	})

	loop := start(t, fake)

	shown := askByID(t, loop, hostedID)
	askID, _ := shown["id"].(string)

	if shown["kind"] != "permission" {
		t.Fatalf("the ask is %v, not a permission", shown["kind"])
	}

	hostedProgram.show(t, permissionScreen("rm -rf build"))
	waitAsk(t, loop, hostedID, func(found map[string]any) bool {
		return found["text"] == "rm -rf build"
	})

	changed := loop.Answer(t.Context(), hostedID, pressOne(askID))
	if changed.Code != http.StatusConflict {
		t.Errorf("a changed prompt: %d %s", changed.Code, body(changed))
	}

	if !strings.Contains(body(changed), `"text":"rm -rf build"`) {
		t.Errorf("the refusal does not carry the ask as it is now: %s", body(changed))
	}

	if typed := hostedProgram.typed(); typed != "" {
		t.Errorf("a changed prompt was answered: %q", typed)
	}

	current, _ := askByID(t, loop, hostedID)["id"].(string)

	pressed := loop.Answer(t.Context(), hostedID, pressOne(current))
	if body(pressed) != `{"ok":true}` {
		t.Errorf("the answer: %d %s", pressed.Code, body(pressed))
	}

	again := loop.Answer(t.Context(), hostedID, pressOne(current))
	if again.Code != http.StatusConflict {
		t.Errorf("a second press: %d %s", again.Code, body(again))
	}

	if typed := hostedProgram.typed(); typed != "1" {
		t.Errorf("typed %q, want the one key 1", typed)
	}

	if log := fake.term.sent(); len(log) != 0 {
		t.Errorf("a hosted answer reached cmux: %v", log)
	}
}

// pressOne is an answer that presses 1, the first choice, on the prompt askID names.
func pressOne(askID string) sessions.AnswerBody {
	return sessions.AnswerBody{
		AskID: askID, Choice: "1", Text: nil, HasChoice: true, HasText: false,
	}
}

// Stop stops a hosted session's program through the host, by the status the page shows: a busy
// one needs force, as a cmux one does.
func TestStopOfAHostedSessionStopsItsProgram(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	busy, busyProgram := fake.hostedSession(48, "e", "busy", "", idleBox, 1)
	unregistered, unregisteredProgram := fake.hostedSession(49, "f", "", "", idleBox, 1)
	loop := start(t, fake)

	rowOf(t, loop, busy, anyRow)

	refused := loop.Stop(t.Context(), busy, false)
	if body(refused) != `{"error":"session is busy","busy":true}` {
		t.Errorf("busy stop: %d %s", refused.Code, body(refused))
	}

	if stops := busyProgram.stops(); len(stops) != 0 {
		t.Errorf("a refused stop signalled the program: %v", stops)
	}

	forced := loop.Stop(t.Context(), busy, true)
	if body(forced) != `{"ok":true,"name":"n48"}` {
		t.Errorf("forced stop: %d %s", forced.Code, body(forced))
	}

	stopped := loop.Stop(t.Context(), unregistered, false)
	if body(stopped) != `{"ok":true,"name":"f"}` {
		t.Errorf("idle stop: %d %s", stopped.Code, body(stopped))
	}

	for _, stoppedProgram := range []*program{busyProgram, unregisteredProgram} {
		if !slices.Equal(stoppedProgram.stops(), []string{"terminate"}) {
			t.Errorf("program %d got %v, want a polite stop", stoppedProgram.pid,
				stoppedProgram.stops())
		}
	}

	unknown := loop.Stop(t.Context(), "0123456789abcdef0123456789abcdef", true)
	if body(unknown) != `{"error":"no such live session"}` {
		t.Errorf("unknown: %d %s", unknown.Code, body(unknown))
	}

	if kills := fake.term.kills; len(kills) != 0 {
		t.Errorf("a hosted stop signalled a pid directly: %v", kills)
	}
}

// A hosted session never reaches cmux, even when its environment names a cmux surface and it
// runs under the cmux app: its environment is never read, and opening its terminal, replying
// and answering type nothing through cmux. A cmux session's dialog keeps its buttons beside it.
func TestAHostedSessionNeverReachesCmux(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	stamp := float64(time.Now().UnixMilli() - 5000)
	fake.cmuxSession(50, "w50", "waiting", "permission prompt", permissionScreen("pwd"), stamp)
	hostedID, _ := fake.hostedSession(51, "g", "waiting", "permission prompt",
		permissionScreen("ls"), stamp)
	// The hosted program sits under cmux's shell with cmux's variables, as one Conn started
	// from a cmux pane would without its environment cleaned.
	fake.procs[len(fake.procs)-1] = "51 600 claude"
	fake.envs[51] = "claude CMUX_WORKSPACE_ID=" + uuid("w50") + " CMUX_SURFACE_ID=" +
		uuid(surfaceOf(50))

	loop := start(t, fake)

	cmuxAsk := askOf(t, loop, 50)
	if cmuxAsk["kind"] != "permission" {
		t.Errorf("the cmux session's dialog lost its buttons: %v", cmuxAsk["kind"])
	}

	hostedAsk := askByID(t, loop, hostedID)
	askID, _ := hostedAsk["id"].(string)

	opened := loop.OpenTerminal(t.Context(), hostedID)
	if body(opened) != `{"ok":true}` {
		t.Errorf("open terminal: %d %s", opened.Code, body(opened))
	}

	replied := loop.Reply(t.Context(), hostedID, "hello")
	if replied.Code == http.StatusOK {
		t.Errorf("a reply typed over a permission prompt: %s", body(replied))
	}

	answered := loop.Answer(t.Context(), hostedID, pressOne(askID))
	if body(answered) != `{"ok":true}` {
		t.Errorf("answer: %d %s", answered.Code, body(answered))
	}

	if fake.envRead(51) {
		t.Error("the hosted session's environment was read")
	}

	if log := fake.term.sent(); len(log) != 0 {
		t.Errorf("a hosted session reached cmux: %v", log)
	}

	checkCmuxAnswered(t, fake, loop, cmuxAsk)
}

// checkCmuxAnswered answers the cmux session 50's ask: it goes through cmux, to its own surface,
// as ever, the hosted session beside it sharing no workspace with it.
func checkCmuxAnswered(t *testing.T, fake *world, loop *sessions.Loop, cmuxAsk map[string]any) {
	t.Helper()

	cmuxID, _ := cmuxAsk["id"].(string)

	cmuxAnswer := loop.Answer(t.Context(), "50", pressOne(cmuxID))
	if body(cmuxAnswer) != `{"ok":true}` {
		t.Errorf("the cmux answer: %d %s", cmuxAnswer.Code, body(cmuxAnswer))
	}

	sent := fake.term.sent()
	if len(sent) != 1 {
		t.Fatalf("sent %v, want the cmux answer only", sent)
	}

	if !strings.Contains(sent[0], uuid(surfaceOf(50))) {
		t.Errorf("the cmux answer went to %q", sent[0])
	}

	if fake.term.count("read-screen --workspace "+uuid("w50")) == 0 {
		t.Error("the cmux session's screen was never read")
	}
}

// A person-started session stays on the cmux path whatever its environment and screen hold: a
// hosted session's id, Conn's or cmux's variables are text, never what makes a session hosted.
func TestAPersonStartedSessionStaysOnTheCmuxPath(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	fake.cmuxSession(53, "w53", "idle", "", idleBox, 1)
	hostedID, _ := fake.hostedSession(52, "h", "idle", "", idleBox, 1)
	forged := "hosted " + hostedID + " CONN_PORT=7777 CMUX_WORKSPACE_ID=" + uuid("w99")
	fake.term.screens[uuid(surfaceOf(53))] = forged + "\n" + idleBox
	fake.envs[53] += " CONN_HOSTED_ID=" + hostedID + " " + forged

	loop := start(t, fake)

	row := rowOf(t, loop, "53", anyRow)
	if row.Hosted {
		t.Error("a person-started session is marked hosted")
	}

	if !row.CanReply {
		t.Error("a person-started session lost its cmux terminal")
	}

	replied := loop.Reply(t.Context(), "53", "hello")
	if body(replied) != `{"ok":true}` {
		t.Errorf("reply: %d %s", replied.Code, body(replied))
	}

	sent := fake.term.sent()
	if len(sent) != 2 {
		t.Fatalf("the reply did not go through cmux: %v", sent)
	}

	if !strings.Contains(sent[0], uuid("w53")) {
		t.Errorf("the reply went to %q, not the session's own workspace", sent[0])
	}

	hostedRow := rowOf(t, loop, hostedID, anyRow)
	if !hostedRow.Hosted {
		t.Error("the hosted session is not marked hosted")
	}
}

// A hosted session's agent pages are found under its issued id, never its pid.
func TestAHostedSessionsAgentPagesAreUnderItsIssuedID(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	hostedID, _ := fake.hostedSession(54, "i", "idle", "", idleBox, 1)
	loop := start(t, fake)

	rowOf(t, loop, hostedID, anyRow)

	_, found, err := loop.AgentDetail(t.Context(), hostedID, "lead")
	if err != nil {
		t.Fatal(err)
	}

	if !found {
		t.Error("the lead's page is not found under the issued id")
	}

	_, found, err = loop.AgentDetail(t.Context(), "54", "lead")
	if err != nil {
		t.Fatal(err)
	}

	if found {
		t.Error("the lead's page is found under the hosted session's pid")
	}
}

// askByID waits until the snapshot shows the row's ask, read by the loop.
func askByID(t *testing.T, loop *sessions.Loop, id string) map[string]any {
	t.Helper()

	return waitAsk(t, loop, id, func(map[string]any) bool { return true })
}

// waitAsk waits until the row's ask passes ready.
func waitAsk(t *testing.T, loop *sessions.Loop, rowID string,
	ready func(map[string]any) bool,
) map[string]any {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(5 * time.Second))
	for time.Now().Before(deadline) {
		loop.ReadAsks(t.Context())

		found := shownAsk(t, loop, rowID)
		if found != nil {
			if ready(found) {
				return found
			}
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("session %s never showed the ask wanted", rowID)

	return nil
}

// shownAsk is the ask the snapshot shows for the row, or nil.
func shownAsk(t *testing.T, loop *sessions.Loop, rowID string) map[string]any {
	t.Helper()

	data, err := loop.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	parsed, _ := js.Parse(data)
	list, _ := get(parsed, "sessions").([]any)

	for _, session := range list {
		if get(session, "id") != rowID {
			continue
		}

		found, isObject := get(session, "ask").(*js.Object)
		if !isObject {
			return nil
		}

		out := map[string]any{}
		for _, key := range found.Keys() {
			out[key], _ = found.Get(key)
		}

		return out
	}

	return nil
}

// hostedGolden is the contract of a hosted row, shared with the page's tests: the fields of each
// kind of hosted row the page reads to show it and its ask panel.
const hostedGolden = "../../testdata/answers/sessions/hosted.json"

// hostedStamp is the waiting session's status time, fixed so its ask's id is too.
const hostedStamp = 1_700_000_000_000

// TestHostedRowsKeepTheirContract holds the hosted rows in testdata/answers, which the page's
// tests read too. CONN_UPDATE_GOLDEN=1 rewrites it.
func TestHostedRowsKeepTheirContract(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	waiting, _ := fake.hostedSession(55, "waiting", "waiting", "permission prompt",
		permissionScreen("ls"), hostedStamp)
	running, _ := fake.hostedSession(56, "running", "", "", idleBox, 1)
	ended, _ := fake.hostedSession(57, "ended", "", "", idleBox, 1)

	err := fake.host.Stop(t.Context(), pty.ID(ended))
	if err != nil {
		t.Fatal(err)
	}

	loop := start(t, fake)
	askByID(t, loop, waiting)

	rowIDs := []string{waiting, running, ended}
	rows := make([]any, 0, len(rowIDs))

	for _, rowID := range rowIDs {
		rows = append(rows, contractOf(t, loop, rowID))
	}

	got := string(js.Marshal(map[string]any{"rows": rows})) + "\n"

	if os.Getenv("CONN_UPDATE_GOLDEN") == "1" {
		err := os.WriteFile(hostedGolden, []byte(got), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(hostedGolden)
	if err != nil {
		t.Fatal(err)
	}

	if got != string(want) {
		t.Errorf("the hosted rows changed:\n got  %s\n want %s", got, want)
	}
}

// contractOf is a row's contract fields, its id replaced by its name: the host's ids are random.
func contractOf(t *testing.T, loop *sessions.Loop, rowID string) *js.Object {
	t.Helper()

	data, err := loop.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	parsed, _ := js.Parse(data)
	list, _ := get(parsed, "sessions").([]any)

	for _, row := range list {
		if get(row, "id") != rowID {
			continue
		}

		out := js.NewObject()
		out.Set("id", "hosted-"+js.String(get(row, "name")))

		for _, key := range []string{
			"name", "status", "waitingFor", "canReply", "hosted", "exitCode", "ask",
		} {
			object, _ := row.(*js.Object)

			value, present := object.Get(key)
			if present {
				out.Set(key, value)
			}
		}

		return out
	}

	t.Fatalf("no row %s", rowID)

	return nil
}

// A registry entry an ended hosted session left behind is never taken for a new hosted session
// the system gave the same process id: the new one is named as it was started.
func TestAReusedPidIsNotTakenForTheEndedSession(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	first, _ := fake.hostedSession(60, "first", "idle", "", idleBox, 1)
	loop := start(t, fake)

	rowOf(t, loop, first, func(row detail) bool { return row.Name == "n60" })

	err := fake.host.Stop(t.Context(), pty.ID(first))
	if err != nil {
		t.Fatal(err)
	}

	second, _ := fake.hostedSession(60, "second", "", "", idleBox, 1)

	row := rowOf(t, loop, second, anyRow)
	if row.Name != "second" {
		t.Errorf("the new session is named %q: the old entry was taken for it", row.Name)
	}
}

// Two sends to one hosted session never both pass the screen check: the second is refused while
// the first is typing.
func TestOneSendToAHostedSessionAtATime(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	hostedID, hostedProgram := fake.hostedSession(61, "j", "idle", "", idleBox, 1)
	loop := start(t, fake)

	rowOf(t, loop, hostedID, anyRow)

	release := hostedProgram.holdWrites()
	first := make(chan sessions.Reply, 1)

	go func() { first <- loop.Reply(context.WithoutCancel(t.Context()), hostedID, "one") }()

	hostedProgram.waitWriting(t)

	second := loop.Reply(t.Context(), hostedID, "two")
	if body(second) != `{"error":"another message to this session is still being sent"}` {
		t.Errorf("the second send: %d %s", second.Code, body(second))
	}

	release()

	if got := <-first; body(got) != `{"ok":true}` {
		t.Errorf("the first send: %d %s", got.Code, body(got))
	}

	if typed := hostedProgram.typed(); typed != "\x1b[200~one\x1b[201~\r" {
		t.Errorf("typed %q, want the first message only", typed)
	}
}

// waitDrawn waits until the session's screen shows the last line of screen that is not blank.
func waitDrawn(t *testing.T, host *pty.Host, sessionID pty.ID, screen string) {
	t.Helper()

	last := ""

	for line := range strings.SplitSeq(screen, "\n") {
		if strings.TrimSpace(line) != "" {
			last = strings.TrimSpace(line)
		}
	}

	if last == "" {
		return
	}

	ctx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(screenWaitLimit))
	defer cancel()

	_, err := host.WaitScreen(ctx, sessionID, drawnLinesRead, func(lines []string) bool {
		return slices.ContainsFunc(lines, func(line string) bool {
			return strings.TrimSpace(line) == last
		})
	})
	if err != nil {
		t.Fatalf("the screen never showed %q: %v", last, err)
	}
}

const (
	// drawnLinesRead is how many lines waitDrawn looks at: every line of a test's screen.
	drawnLinesRead = 40
	// screenWaitLimit bounds waitDrawn's and waitSettled's waits for the host.
	screenWaitLimit = 5 * time.Second
	// settledPoll is how often waitSettled looks.
	settledPoll = 10 * time.Millisecond
)

// waitScreen waits until the session's screen has a line want.
func waitScreen(t *testing.T, host *pty.Host, hostedID, want string) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(5 * time.Second))

	for time.Now().Before(deadline) {
		lines, _, err := host.ScreenLines(pty.ID(hostedID), 40)
		if err != nil {
			t.Fatal(err)
		}

		if slices.Contains(lines, want) {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("the screen never showed %q", want)
}

// An answer is refused before the screen is read when there is nothing to answer: a hosted
// session that has not registered, or has ended; and a text answer is never a control.
func TestAnswerToAHostedSessionWithNothingToAnswer(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	unregistered, unregisteredProgram := fake.hostedSession(63, "l", "", "", idleBox, 1)
	waiting, waitingProgram := fake.hostedSession(64, "m", "waiting", "input needed", idleBox,
		1)
	ended, _ := fake.hostedSession(65, "n", "", "", idleBox, 1)
	idle, idleProgram := fake.hostedSession(66, "o", "idle", "", permissionScreen("ls"), 1)

	err := fake.host.Stop(t.Context(), pty.ID(ended))
	if err != nil {
		t.Fatal(err)
	}

	loop := start(t, fake)

	rowOf(t, loop, waiting, func(row detail) bool { return row.Status == "waiting" })

	text := sessions.AnswerBody{
		AskID: "a1", Choice: nil, Text: "a\u009bb", HasChoice: false, HasText: true,
	}
	tests := []struct {
		name, id string
		body     sessions.AnswerBody
		want     string
	}{
		{
			"not registered", unregistered, pressOne("a1"),
			`{"error":"the session is not waiting on a prompt, so nothing was sent","ask":null}`,
		},
		{
			"ended", ended, pressOne("a1"),
			`{"error":"the session has ended, so nothing was sent","ask":null}`,
		},
		{
			"not waiting", idle, pressOne("a1"),
			`{"error":"the session is not waiting on a prompt, so nothing was sent","ask":null}`,
		},
		{"a C1 control", waiting, text, `{"error":"control characters are not sent"}`},
	}

	for _, tt := range tests {
		got := loop.Answer(t.Context(), tt.id, tt.body)
		if body(got) != tt.want {
			t.Errorf("%s: %d %s", tt.name, got.Code, body(got))
		}
	}

	for _, untouched := range []*program{unregisteredProgram, waitingProgram, idleProgram} {
		if typed := untouched.typed(); typed != "" {
			t.Errorf("program %d was typed into: %q", untouched.pid, typed)
		}
	}
}

// deviceAttributes is what the host answers a program that asks for its terminal's device
// attributes.
const deviceAttributes = "\x1b[?62;22c"

// whileTurnHeld holds the program's input with the host's answer to its query for the
// terminal's device attributes (an answer, not input, so what act reads stays good), starts
// act, waits until act is parked behind the answer, then draws screen, waits until line shows,
// and lets the input go. It returns act's reply.
func whileTurnHeld(t *testing.T, fake *world, hostedID string, held *program,
	act func(context.Context) sessions.Reply, screen, line string,
) sessions.Reply {
	t.Helper()

	release := held.holdWrites()

	select {
	case held.output <- []byte("\x1b[c"):
	case <-time.After(testtime.Scaled(5 * time.Second)):
		t.Fatal("the host did not read the program's query")
	}

	held.waitWriting(t)

	acted := make(chan sessions.Reply, 1)

	go func() { acted <- act(context.WithoutCancel(t.Context())) }()

	select {
	case <-fake.parked:
	case <-time.After(testtime.Scaled(5 * time.Second)):
		t.Fatal("the action never waited for its turn")
	}

	held.show(t, screen)
	waitScreen(t, fake.host, hostedID, line)
	release()

	return <-acted
}

// A reply or an answer waiting for its turn checks the screen again when its turn comes: a
// screen that no longer shows what it checked (a draft, another prompt) refuses it with nothing
// typed, and one that only redrew something else (Claude Code blinks the mark above a dialog
// while it waits) does not.
func TestASendChecksTheScreenAgainWhenItsTurnComes(t *testing.T) {
	t.Parallel()

	stamp := float64(time.Now().UnixMilli() - 5000)
	blinked := strings.Replace(permissionScreen("ls"), "⏺", " ", 1)
	tests := []struct {
		name, status, waitingFor, before, after, line string
		answer                                        bool
		wantCode                                      int
		wantTyped                                     string
	}{
		{
			"a reply into a box now drafted", "idle", "", idleBox, draft, "❯ half typed", false,
			http.StatusConflict, deviceAttributes,
		},
		{
			"a reply past a status line redrawn", "idle", "", idleBox,
			strings.Replace(idleBox, "✻ done", "✻ done.", 1), "✻ done.", false, http.StatusOK,
			deviceAttributes + "\x1b[200~hello\x1b[201~\r",
		},
		{
			"an answer to a prompt now replaced", "waiting", "permission prompt",
			permissionScreen("ls"), permissionScreen("rm -rf build"), " rm -rf build", true,
			http.StatusConflict, deviceAttributes,
		},
		{
			"an answer past a blink", "waiting", "permission prompt", permissionScreen("ls"),
			blinked, "   Bash(ls)", true, http.StatusOK, deviceAttributes + "1",
		},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := newWorld(t)
			hostedID, held := fake.hostedSession(70+index, "q", tt.status, tt.waitingFor,
				tt.before, stamp)
			loop := start(t, fake)

			act := func(ctx context.Context) sessions.Reply {
				return loop.Reply(ctx, hostedID, "hello")
			}

			rowOf(t, loop, hostedID, func(row detail) bool { return row.Status == tt.status })

			if tt.answer {
				askID, _ := askByID(t, loop, hostedID)["id"].(string)
				act = func(ctx context.Context) sessions.Reply {
					return loop.Answer(ctx, hostedID, pressOne(askID))
				}
			}

			got := whileTurnHeld(t, fake, hostedID, held, act, tt.after, tt.line)
			if got.Code != tt.wantCode {
				t.Errorf("%d %s, want %d", got.Code, body(got), tt.wantCode)
			}

			if typed := held.typed(); typed != tt.wantTyped {
				t.Errorf("typed %q, want %q", typed, tt.wantTyped)
			}
		})
	}
}

// A hosted session that has registered is a live session for the modules, carrying the id its
// host issued, so an action a module takes on it names it as the page does.
func TestAHostedSessionIsLiveUnderItsIssuedID(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	hostedID, _ := fake.hostedSession(80, "r", "idle", "", idleBox, 1)
	fake.cmuxSession(81, "w81", "idle", "", idleBox, 1)
	loop := start(t, fake)

	live := waitLive(t, loop, 2)
	issued := map[float64]string{}

	for _, session := range live {
		issued[session.PID] = session.HostedID
	}

	if issued[80] != hostedID {
		t.Errorf("the hosted session carries %q, want %q", issued[80], hostedID)
	}

	if issued[81] != "" {
		t.Errorf("the cmux session carries a hosted id %q", issued[81])
	}
}

// How a viewer's key comes before a page's action, in TestOtherInputRefusesASendOnTheScreenBefore.
const (
	// keyJustBefore is typed and taken just before the action.
	keyJustBefore = iota
	// keySettledThenRedrawn is typed, then the same screen drawn again, past the settle window.
	keySettledThenRedrawn
	// keyHeldByTheTerminal is still being written (a full input queue) past the settle window
	// when the action reads, and taken while the action waits for its turn.
	keyHeldByTheTerminal
	// keyTakenLateJustBefore is held by the terminal past the settle window, then taken, just
	// before the action reads: the window counts from when it was taken.
	keyTakenLateJustBefore
)

// Input another viewer typed refuses a click or a reply made on the screen as it looked before
// it: an Allow pressed after a viewer's 3, a reply after a viewer's ! (Claude Code's shell
// mode), a click on a prompt a viewer answered while an identical one is drawn again, and either
// after a viewer's key the terminal took only after the settle window.
func TestOtherInputRefusesASendOnTheScreenBeforeIt(t *testing.T) {
	t.Parallel()

	stamp := float64(time.Now().UnixMilli() - 5000)
	otherInput := `{"error":"something else was typed into the session's terminal just now, ` +
		`so nothing was sent","ask":null}`
	tests := []struct {
		name, status, waitingFor, screen, key string
		answer                                bool
		how                                   int
		want                                  string
	}{
		{
			"an answer after a viewer's key", "waiting", "permission prompt",
			permissionScreen("ls"), "3", true, keyJustBefore, "",
		},
		{"a reply after a viewer's !", "idle", "", idleBox, "!", false, keyJustBefore, otherInput},
		{
			"an answer after a viewer answered and the same prompt came again", "waiting",
			"permission prompt", permissionScreen("ls"), "1", true, keySettledThenRedrawn, "",
		},
		{
			"a reply after a key the terminal took late", "idle", "", idleBox, "x", false,
			keyHeldByTheTerminal, otherInput,
		},
		{
			"an answer after a key the terminal took late", "waiting", "permission prompt",
			permissionScreen("ls"), "3", true, keyHeldByTheTerminal, "",
		},
		{
			"a reply just after a key the terminal took late", "idle", "", idleBox, "x", false,
			keyTakenLateJustBefore, otherInput,
		},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := newWorld(t)
			hostedID, held := fake.hostedSession(100+index, "s", tt.status, tt.waitingFor,
				tt.screen, stamp)
			loop := start(t, fake)

			rowOf(t, loop, hostedID, func(row detail) bool { return row.Status == tt.status })

			act := func(ctx context.Context) sessions.Reply {
				return loop.Reply(ctx, hostedID, "rm -rf build")
			}

			if tt.answer {
				askID, _ := askByID(t, loop, hostedID)["id"].(string)
				act = func(ctx context.Context) sessions.Reply {
					return loop.Answer(ctx, hostedID, pressOne(askID))
				}
			}

			got := afterViewerKey(t, fake, hostedID, held, tt.key, tt.how, tt.screen, act)
			if got.Code != http.StatusConflict {
				t.Errorf("%d %s", got.Code, body(got))
			}

			if tt.want != "" {
				if body(got) != tt.want {
					t.Errorf("%s, want %s", body(got), tt.want)
				}
			}

			if typed := held.typed(); typed != tt.key {
				t.Errorf("typed %q, want only the viewer's %q", typed, tt.key)
			}
		})
	}
}

// afterViewerKey types a viewer's key into the session as how says, and runs act after it.
func afterViewerKey(t *testing.T, fake *world, hostedID string, held *program, key string,
	how int, screen string, act func(context.Context) sessions.Reply,
) sessions.Reply {
	t.Helper()

	if how == keyHeldByTheTerminal {
		return afterHeldKey(t, fake, hostedID, held, key, act)
	}

	if how == keyTakenLateJustBefore {
		release := held.holdWrites()
		viewer := make(chan error, 1)

		go func() { viewer <- fake.host.Write(pty.ID(hostedID), []byte(key)) }()

		held.waitWriting(t)
		time.Sleep(settledAfter())
		release()

		err := <-viewer
		if err != nil {
			t.Fatal(err)
		}

		return act(t.Context())
	}

	err := fake.host.Write(pty.ID(hostedID), []byte(key))
	if err != nil {
		t.Fatal(err)
	}

	if how == keySettledThenRedrawn {
		held.show(t, screen)
		waitDrawn(t, fake.host, pty.ID(hostedID), screen)
		waitSettled(t, fake.host, hostedID)
	}

	return act(t.Context())
}

// afterHeldKey has the terminal hold a viewer's key past the settle window, starts act, and
// lets the key through once act waits for its turn.
func afterHeldKey(t *testing.T, fake *world, hostedID string, held *program, key string,
	act func(context.Context) sessions.Reply,
) sessions.Reply {
	t.Helper()

	release := held.holdWrites()
	viewer := make(chan error, 1)

	go func() { viewer <- fake.host.Write(pty.ID(hostedID), []byte(key)) }()

	held.waitWriting(t)
	time.Sleep(settledAfter())

	acted := make(chan sessions.Reply, 1)

	go func() { acted <- act(context.WithoutCancel(t.Context())) }()

	// An action is refused before its turn when what it read already counts the key (an
	// answer's prompt id holds the count); else it waits for its turn behind the key.
	var got *sessions.Reply

	select {
	case <-fake.parked:
	case reply := <-acted:
		got = &reply
	case <-time.After(testtime.Scaled(5 * time.Second)):
		t.Fatal("the action neither ended nor waited for its turn")
	}

	release()

	err := <-viewer
	if err != nil {
		t.Fatal(err)
	}

	if got != nil {
		return *got
	}

	return <-acted
}

// A reply is typed as the terminal takes it: as keys where the program has not asked for
// bracketed pastes (a message of several lines is then refused), never while a program the
// session started has the terminal, and Enter only once the message shows in the box with no
// dialog.
func TestAReplyIsTypedAsTheTerminalTakesIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, text, echo string
		pasteOff         bool
		background       bool
		want, wantTyped  string
	}{
		{
			"one line, no bracketed paste", "hello", "", true, false, `{"ok":true}`,
			"hello\r",
		},
		{
			"several lines, no bracketed paste", "hello\nthere", "", true, false,
			`{"error":"the session's terminal does not take pastes, so a message of several ` +
				`lines was not sent"}`, "",
		},
		{
			"another program has the terminal", "hello", "", false, true,
			`{"error":"the session is not in the foreground of its terminal (suspended or ` +
				`exiting), so nothing was sent","ask":null}`, "",
		},
		{
			// A picker's last row between rules reads like a box with text in it: Enter on it
			// would pick it.
			"a question picker that looks like a box", "hello", questionPicker(), false, false,
			notSent, "\x1b[200~hello\x1b[201~",
		},
		{
			"a dialog instead of the message", "hello", permissionScreen("ls"), false, false,
			notSent, "\x1b[200~hello\x1b[201~",
		},
		{
			"the box holding other text", "hello", boxWith("goodbye"), false, false, notSent,
			"\x1b[200~hello\x1b[201~",
		},
		{
			"a long paste shown as such", "hello\nthere", boxWith("[Pasted text #1 +2 lines]"),
			false, false, `{"ok":true}`, "\x1b[200~hello\nthere\x1b[201~\r",
		},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := newWorld(t)
			hostedID, held := fake.hostedSession(110+index, "u", "idle", "", idleBox, 1)
			held.echo = tt.echo
			held.background.Store(tt.background)

			if tt.pasteOff {
				held.show(t, "\x1b[?2004l"+idleBox)
				waitPastesUnbracketed(t, fake.host, hostedID)
			}

			loop := start(t, fake)
			rowOf(t, loop, hostedID, anyRow)

			got := loop.Reply(t.Context(), hostedID, tt.text)
			if body(got) != tt.want {
				t.Errorf("%d %s", got.Code, body(got))
			}

			if typed := held.typed(); typed != tt.wantTyped {
				t.Errorf("typed %q, want %q", typed, tt.wantTyped)
			}
		})
	}
}

// A person's claude started by a hosted session (a nested run, or a wrapper's child) is never a
// cmux session, even when Conn runs under cmux and its environment names a cmux surface: its
// environment is not read, and it cannot be replied to through cmux.
func TestWhatAHostedSessionStartedIsNeverACmuxSession(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	fake.cmuxSession(50, "w50", "idle", "", idleBox, 1)
	fake.hostedSession(120, "v", "idle", "", idleBox, 1)
	fake.procs[len(fake.procs)-1] = "120 600 claude"
	fake.procs = append(fake.procs, "121 120 claude")
	fake.envs[121] = "CMUX_WORKSPACE_ID=" + uuid("w50") + " CMUX_SURFACE_ID=" + uuid(surfaceOf(50))
	fake.register("121.json", map[string]any{
		"pid": 121, "sessionId": "s-121", "cwd": fake.folder("nested", ""), "name": "n121",
		"status": "idle",
	})

	loop := start(t, fake)

	row := rowOf(t, loop, "121", anyRow)
	if row.CanReply {
		t.Error("a claude a hosted session started has a cmux terminal")
	}

	if fake.envRead(121) {
		t.Error("its environment was read")
	}

	if !rowOf(t, loop, "50", anyRow).CanReply {
		t.Error("the cmux session lost its terminal")
	}
}

// An ended hosted session's last row is kept only while the host keeps the session: once the
// host forgets it, so does the session list.
func TestAForgottenHostedSessionsLastRowIsDropped(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	first, _ := fake.hostedSession(130, "first", "idle", "", idleBox, 1)
	loop := start(t, fake)

	rowOf(t, loop, first, func(row detail) bool { return row.Name == "n130" })

	err := fake.host.Stop(t.Context(), pty.ID(first))
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(sessions.KeptRows(t.Context(), loop), first) {
		t.Fatal("the ended session's row was not kept")
	}

	// The host keeps 12 ended sessions: 12 more ending make it forget the first.
	var group sync.WaitGroup

	for index := range 12 {
		later, _ := fake.hostedSession(131+index, "later", "", "", idleBox, 1)

		group.Go(func() { _ = fake.host.Stop(context.WithoutCancel(t.Context()), pty.ID(later)) })
	}

	group.Wait()

	deadline := time.Now().Add(testtime.Scaled(5 * time.Second))
	for time.Now().Before(deadline) {
		if !slices.Contains(sessions.KeptRows(t.Context(), loop), first) {
			return
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Error("the row of a session the host forgot is still kept")
}

// An Enter waiting for its turn is not pressed when the screen turns, before its turn, into a
// question picker whose selected row reads like a box with text in it: Enter would pick it.
func TestAnEnterIsCheckedAgainWhenItsTurnComes(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	hostedID, held := fake.hostedSession(140, "w", "idle", "", idleBox, 1)
	// The message is drawn only once the program's input is held below.
	held.echoDelay = settledAfter()
	loop := start(t, fake)

	rowOf(t, loop, hostedID, anyRow)

	replied := make(chan sessions.Reply, 1)

	go func() { replied <- loop.Reply(context.WithoutCancel(t.Context()), hostedID, "hello") }()

	waitTyped(t, held, "\x1b[200~hello\x1b[201~")

	// The host's answer to a query holds the program's input before the reply's Enter.
	release := held.holdWrites()

	select {
	case held.output <- []byte("\x1b[c"):
	case <-time.After(testtime.Scaled(5 * time.Second)):
		t.Fatal("the host did not read the program's query")
	}

	held.waitWriting(t)

	select {
	case <-fake.parked:
	case <-time.After(testtime.Scaled(5 * time.Second)):
		t.Fatal("the Enter never waited for its turn")
	}

	held.show(t, questionPicker())
	waitScreen(t, fake.host, hostedID, "❯ 4. Chat about this")
	release()

	got := <-replied
	if body(got) != notSent {
		t.Errorf("%d %s", got.Code, body(got))
	}

	if typed := held.typed(); strings.HasSuffix(typed, "\r") {
		t.Errorf("Enter was pressed: %q", typed)
	}
}

// Text a viewer types after the message, while the reply waits to press Enter, is never sent
// with it: Enter is pressed only if nothing was typed after the message.
func TestTextTypedAfterTheMessageIsNotSent(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	hostedID, held := fake.hostedSession(142, "y", "idle", "", idleBox, 1)
	// The box shows the message whatever else is typed, so only what was typed refuses it.
	held.echo = boxWith("hello")
	held.echoDelay = settledAfter()
	loop := start(t, fake)

	rowOf(t, loop, hostedID, anyRow)

	replied := make(chan sessions.Reply, 1)

	go func() { replied <- loop.Reply(context.WithoutCancel(t.Context()), hostedID, "hello") }()

	waitTyped(t, held, "\x1b[200~hello\x1b[201~")

	err := fake.host.Write(pty.ID(hostedID), []byte(" and also delete the tests"))
	if err != nil {
		t.Fatal(err)
	}

	got := <-replied
	if body(got) != notSent {
		t.Errorf("%d %s", got.Code, body(got))
	}

	if typed := held.typed(); strings.HasSuffix(typed, "\r") {
		t.Errorf("the viewer's text was sent with the message: %q", typed)
	}
}

// A reply is refused while a program the session started reads a line on its terminal (a
// password prompt in Claude Code's own process group): the message would be its answer.
func TestAReplyIsRefusedWhileAProgramReadsALine(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	hostedID, held := fake.hostedSession(143, "z", "idle", "", idleBox, 1)

	held.lineMode.Store(true)

	loop := start(t, fake)

	rowOf(t, loop, hostedID, anyRow)

	got := loop.Reply(t.Context(), hostedID, "hello")

	want := `{"error":"a program the session started is reading a line on its terminal (a ` +
		`password, say), so nothing was sent","ask":null}`
	if body(got) != want {
		t.Errorf("%d %s", got.Code, body(got))
	}

	if typed := held.typed(); typed != "" {
		t.Errorf("typed %q", typed)
	}
}

// waitTyped waits until the program has been sent want.
func waitTyped(t *testing.T, held *program, want string) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(5 * time.Second))
	for time.Now().Before(deadline) {
		if held.typed() == want {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("the program was sent %q, want %q", held.typed(), want)
}

// A message the program draws in its box only after the settle window is still sent: Enter
// waits for the box to show it.
func TestAMessageDrawnLateIsStillSent(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	hostedID, held := fake.hostedSession(141, "x", "idle", "", idleBox, 1)
	held.echoDelay = 600 * time.Millisecond
	loop := start(t, fake)

	rowOf(t, loop, hostedID, anyRow)

	got := loop.Reply(t.Context(), hostedID, "hello")
	if body(got) != `{"ok":true}` {
		t.Errorf("%d %s", got.Code, body(got))
	}

	if typed := held.typed(); typed != "\x1b[200~hello\x1b[201~\r" {
		t.Errorf("typed %q", typed)
	}
}

// An answer to a prompt read after earlier input is pressed: the prompt's id holds the input
// count it was read at, and the click is checked against the same count.
func TestAnAnswerAfterEarlierInputIsPressed(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	stamp := float64(time.Now().UnixMilli() - 5000)
	hostedID, held := fake.hostedSession(144, "a", "waiting", "permission prompt",
		permissionScreen("ls"), stamp)

	err := fake.host.Write(pty.ID(hostedID), []byte("x"))
	if err != nil {
		t.Fatal(err)
	}

	loop := start(t, fake)

	shown := waitAsk(t, loop, hostedID, func(found map[string]any) bool {
		askID, _ := found["id"].(string)

		return strings.HasSuffix(askID, ".1")
	})
	askID, _ := shown["id"].(string)

	waitSettled(t, fake.host, hostedID)

	got := loop.Answer(t.Context(), hostedID, pressOne(askID))
	if body(got) != `{"ok":true}` {
		t.Errorf("%d %s", got.Code, body(got))
	}

	if typed := held.typed(); typed != "x1" {
		t.Errorf("typed %q, want the viewer's x, then 1", typed)
	}
}

// waitPastesUnbracketed waits until the session's screen has taken the program's turning
// bracketed paste off.
func waitPastesUnbracketed(t *testing.T, host *pty.Host, hostedID string) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(5 * time.Second))
	for time.Now().Before(deadline) {
		_, mark, err := host.ScreenLines(pty.ID(hostedID), replyLinesRead)
		if err != nil {
			t.Fatal(err)
		}

		if !mark.BracketedPaste {
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatal("the screen never took bracketed paste off")
}

// A prompt the page has just answered stays the answered prompt while it is still on screen,
// whatever the answer's own key did to the input count: the page is shown it under the id it
// answered, and a second press, under that id or under the count after it, is refused, so one
// press reaches the program, never a second that would land on the next dialog.
func TestASecondPressOfAJustAnsweredPromptIsRefused(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	stamp := float64(time.Now().UnixMilli() - 5000)
	hostedID, held := fake.hostedSession(145, "b", "waiting", "permission prompt",
		permissionScreen("ls"), stamp)
	loop := start(t, fake)

	answered, _ := askByID(t, loop, hostedID)["id"].(string)

	first := loop.Answer(t.Context(), hostedID, pressOne(answered))
	if body(first) != `{"ok":true}` {
		t.Fatalf("the answer: %d %s", first.Code, body(first))
	}

	// The program has not redrawn: the prompt is still on screen, read again past the settle
	// window.
	waitSettled(t, fake.host, hostedID)

	shown, _ := askByID(t, loop, hostedID)["id"].(string)
	if shown != answered {
		t.Errorf("the prompt just answered is shown as %q, not as %q", shown, answered)
	}

	justAnswered := `"error":"this prompt was just answered and the terminal has not moved on ` +
		`yet, so nothing was sent"`

	for _, askID := range []string{shown, strings.TrimSuffix(answered, ".0") + ".1"} {
		again := loop.Answer(t.Context(), hostedID, pressOne(askID))
		if again.Code != http.StatusConflict {
			t.Errorf("a second press on %s: %d %s", askID, again.Code, body(again))
		}

		if askID == shown {
			if !strings.Contains(body(again), justAnswered) {
				t.Errorf("a second press on %s: %s", askID, body(again))
			}
		}
	}

	if typed := held.typed(); typed != "1" {
		t.Errorf("typed %q, want one 1", typed)
	}

	// A viewer's key while the prompt still stands does not show it again as a new prompt.
	err := fake.host.Write(pty.ID(hostedID), []byte("x"))
	if err != nil {
		t.Fatal(err)
	}

	waitSettled(t, fake.host, hostedID)

	if again, _ := askByID(t, loop, hostedID)["id"].(string); again != answered {
		t.Errorf("after a viewer's key the prompt just answered is shown as %q", again)
	}
}

// Two identical dialogs back to back on one status stamp: the second, drawn after a frame with
// no dialog, is shown locked under the first's answered id, a click on it is refused, and the
// program gets one key. It is answered in the terminal (see keepAnswered).
func TestAnIdenticalDialogOnTheSameStampStaysAnswered(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	stamp := float64(time.Now().UnixMilli() - 5000)
	hostedID, held := fake.hostedSession(146, "c", "waiting", "permission prompt",
		permissionScreen("ls"), stamp)
	loop := start(t, fake)

	answered, _ := askByID(t, loop, hostedID)["id"].(string)

	first := loop.Answer(t.Context(), hostedID, pressOne(answered))
	if body(first) != `{"ok":true}` {
		t.Fatalf("the answer: %d %s", first.Code, body(first))
	}

	// The loop reads a frame with no dialog, then the same dialog drawn again: each is on the
	// screen before it is read, however slow the machine.
	held.show(t, "✻ working")
	waitScreen(t, fake.host, hostedID, "✻ working")
	loop.ReadAsks(t.Context())

	if between := shownAsk(t, loop, hostedID); between != nil {
		t.Fatalf("with no dialog on the screen, an ask is shown: %v", between)
	}

	held.show(t, permissionScreen("ls"))
	waitScreen(t, fake.host, hostedID, " Do you want to proceed?")

	shown, _ := askByID(t, loop, hostedID)["id"].(string)
	if shown != answered {
		t.Errorf("the second dialog is shown as %q, not locked as %q", shown, answered)
	}

	again := loop.Answer(t.Context(), hostedID, pressOne(shown))
	if !strings.Contains(body(again), "just answered") {
		t.Errorf("a click on the second dialog: %d %s", again.Code, body(again))
	}

	if typed := held.typed(); typed != "1" {
		t.Errorf("typed %q, want one 1", typed)
	}
}

// While the page's answer is being typed, a read of the prompt already shows it locked under
// the id it is answered as, never under the count its own key moved.
func TestAPromptBeingAnsweredIsShownLocked(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	stamp := float64(time.Now().UnixMilli() - 5000)
	hostedID, held := fake.hostedSession(147, "d", "waiting", "permission prompt",
		permissionScreen("ls"), stamp)
	loop := start(t, fake)

	answered, _ := askByID(t, loop, hostedID)["id"].(string)
	release := held.holdWrites()
	replied := make(chan sessions.Reply, 1)

	go func() {
		replied <- loop.Answer(context.WithoutCancel(t.Context()), hostedID, pressOne(answered))
	}()

	held.waitWriting(t)
	loop.ReadAsks(t.Context())

	shown := shownAsk(t, loop, hostedID)
	if shown == nil {
		t.Fatal("mid-answer the prompt is not shown")
	}

	if shown["id"] != answered {
		t.Errorf("mid-answer the prompt is shown as %v, not locked as %q", shown["id"], answered)
	}

	release()

	if got := <-replied; body(got) != `{"ok":true}` {
		t.Errorf("the answer: %d %s", got.Code, body(got))
	}
}

// An answer refused before anything was typed leaves the prompt unanswered: once what refused
// it is over, the prompt can be answered.
func TestAnAnswerThatTypedNothingLeavesThePromptOpen(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	stamp := float64(time.Now().UnixMilli() - 5000)
	hostedID, held := fake.hostedSession(148, "e", "waiting", "permission prompt",
		permissionScreen("ls"), stamp)
	loop := start(t, fake)

	shown, _ := askByID(t, loop, hostedID)["id"].(string)

	held.background.Store(true)

	refused := loop.Answer(t.Context(), hostedID, pressOne(shown))
	if !strings.Contains(body(refused), "not in the foreground") {
		t.Fatalf("the answer with another program in front: %d %s", refused.Code, body(refused))
	}

	held.background.Store(false)

	again, _ := askByID(t, loop, hostedID)["id"].(string)

	answered := loop.Answer(t.Context(), hostedID, pressOne(again))
	if body(answered) != `{"ok":true}` {
		t.Errorf("the answer once the program was in front: %d %s", answered.Code,
			body(answered))
	}

	if typed := held.typed(); typed != "1" {
		t.Errorf("typed %q, want one 1", typed)
	}
}

// A text answer is locked like a choice: with the empty box drawn again on the same stamp, the
// prompt is shown under the answered id, and a second text answer is refused as just answered.
func TestASecondTextAnswerIsRefused(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	stamp := float64(time.Now().UnixMilli() - 5000)
	hostedID, held := fake.hostedSession(149, "f", "waiting", "input needed", idleBox, stamp)
	loop := start(t, fake)

	shown := askByID(t, loop, hostedID)
	if shown["kind"] != "text" {
		t.Fatalf("the empty box asks %v, not text", shown["kind"])
	}

	answered, _ := shown["id"].(string)
	text := func(askID string) sessions.AnswerBody {
		return sessions.AnswerBody{
			AskID: askID, Choice: nil, Text: "hi", HasChoice: false, HasText: true,
		}
	}

	first := loop.Answer(t.Context(), hostedID, text(answered))
	if body(first) != `{"ok":true}` {
		t.Fatalf("the text answer: %d %s", first.Code, body(first))
	}

	held.show(t, idleBox)
	waitDrawn(t, fake.host, pty.ID(hostedID), idleBox)
	waitSettled(t, fake.host, hostedID)

	if again, _ := askByID(t, loop, hostedID)["id"].(string); again != answered {
		t.Errorf("the box drawn again is shown as %q, not locked as %q", again, answered)
	}

	second := loop.Answer(t.Context(), hostedID, text(answered))
	if !strings.Contains(body(second), "just answered") {
		t.Errorf("a second text answer: %d %s", second.Code, body(second))
	}

	if typed := held.typed(); typed != "\x1b[200~hi\x1b[201~\r" {
		t.Errorf("typed %q, want the text and Enter once", typed)
	}
}

// A text answer whose text was typed but whose Enter was not pressed keeps the prompt answered:
// the text is in the box, so a click on the prompt as shown is refused, and nothing more is
// typed.
func TestATextAnswerLeftInTheBoxKeepsThePromptAnswered(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	stamp := float64(time.Now().UnixMilli() - 5000)
	hostedID, held := fake.hostedSession(150, "g", "waiting", "input needed", idleBox, stamp)
	// The program never shows the text: the reply's Enter waits, and is not pressed.
	held.echo = idleBox
	loop := start(t, fake)

	answered, _ := askByID(t, loop, hostedID)["id"].(string)
	text := sessions.AnswerBody{
		AskID: answered, Choice: nil, Text: "hi", HasChoice: false, HasText: true,
	}

	first := loop.Answer(t.Context(), hostedID, text)
	if body(first) != notSent {
		t.Fatalf("the text answer: %d %s", first.Code, body(first))
	}

	shown, _ := askByID(t, loop, hostedID)["id"].(string)
	if shown != answered {
		t.Errorf("the prompt is shown as %q, not kept as %q", shown, answered)
	}

	text.AskID = shown

	second := loop.Answer(t.Context(), hostedID, text)
	if second.Code != http.StatusConflict {
		t.Errorf("a second text answer: %d %s", second.Code, body(second))
	}

	if typed := held.typed(); typed != "\x1b[200~hi\x1b[201~" {
		t.Errorf("typed %q, want the text once and no Enter", typed)
	}
}

// A reply whose message was cut short by the terminal's write deadline leaves a paste open in the
// program. Every later reply is refused, and says why and what to do: not "something else was
// typed", which would never go away by trying again.
func TestARepliesAfterACutPasteSayThePasteIsOpen(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	hostedID, held := fake.hostedSession(144, "z", "idle", "", idleBox, 1)

	loop := start(t, fake)

	rowOf(t, loop, hostedID, anyRow)

	held.cut.Store(9)

	got := loop.Reply(t.Context(), hostedID, "hello there")
	if got.Code != http.StatusBadGateway {
		t.Fatalf("a cut reply: %d %s", got.Code, body(got))
	}

	typed := held.typed()
	if typed != "\x1b[200~hel" {
		t.Fatalf("typed %q", typed)
	}

	got = loop.Reply(t.Context(), hostedID, "again")

	want := `{"error":"a paste into the session's terminal was cut short and is still open; ` +
		`open its terminal and press a key to close it, then check the screen","ask":null}`
	if body(got) != want {
		t.Errorf("%d %s", got.Code, body(got))
	}

	// The page shows a 409 with no ask as the server's own reason.
	if got.Code != http.StatusConflict {
		t.Errorf("status %d, want %d", got.Code, http.StatusConflict)
	}

	if held.typed() != typed {
		t.Errorf("typed %q after the refusal", held.typed())
	}
}
