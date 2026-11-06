package sessions_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/testtime"
)

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

	// gate, when set, holds every Write until it is closed; writing is told of each held one.
	gate    chan struct{}
	writing chan struct{}

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
	if p.gate != nil {
		p.writing <- struct{}{}

		<-p.gate
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.written = append(p.written, data...)

	return len(data), nil
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
		gate: nil, writing: make(chan struct{}, 1), mutex: sync.Mutex{}, written: nil,
		signals: nil,
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
		w.host = pty.NewHost(pty.Config{
			Spawner: w.spawn, Limits: pty.DefaultLimits(), Answers: pty.Answers{},
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
	started.show(w.t, screen)

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

	return rowWithin(t, loop, rowID, ready, 5*time.Second)
}

// rowWithin is rowOf waiting at most limit.
func rowWithin(t *testing.T, loop *sessions.Loop, rowID string, ready func(detail) bool,
	limit time.Duration,
) detail {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(limit))

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

	// Well within the 5 s a snapshot is reused while its version stands: the end moves it.
	row := rowWithin(t, loop, hostedID, func(row detail) bool { return row.ExitCode != nil },
		2*time.Second)
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

	hostedProgram.gate = make(chan struct{})
	first := make(chan sessions.Reply, 1)

	go func() { first <- loop.Reply(context.WithoutCancel(t.Context()), hostedID, "one") }()

	hostedProgram.waitWriting(t)

	second := loop.Reply(t.Context(), hostedID, "two")
	if body(second) != `{"error":"another message to this session is still being sent"}` {
		t.Errorf("the second send: %d %s", second.Code, body(second))
	}

	close(hostedProgram.gate)

	if got := <-first; body(got) != `{"ok":true}` {
		t.Errorf("the first send: %d %s", got.Code, body(got))
	}

	if typed := hostedProgram.typed(); typed != "\x1b[200~one\x1b[201~\r" {
		t.Errorf("typed %q, want the first message only", typed)
	}
}

// A reply waiting for its turn to type is refused when the screen changes before the turn
// comes: what it checked is no longer what the program shows.
func TestAReplyIsRefusedWhenTheScreenChangesBeforeItTypes(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	hostedID, hostedProgram := fake.hostedSession(62, "k", "idle", "", idleBox, 1)
	loop := start(t, fake)

	rowOf(t, loop, hostedID, anyRow)

	// A viewer's keystroke holds the program's input while the reply checks the screen.
	hostedProgram.gate = make(chan struct{})
	viewer := make(chan error, 1)

	go func() { viewer <- fake.host.Write(pty.ID(hostedID), []byte("x")) }()

	hostedProgram.waitWriting(t)

	reply := make(chan sessions.Reply, 1)

	go func() { reply <- loop.Reply(context.WithoutCancel(t.Context()), hostedID, "hello") }()

	// The reply has read the empty box by now and waits for its turn; the screen then changes.
	time.Sleep(testtime.Scaled(300 * time.Millisecond))
	hostedProgram.show(t, draft)
	waitScreen(t, fake.host, hostedID, "❯ half typed")
	close(hostedProgram.gate)

	err := <-viewer
	if err != nil {
		t.Fatal(err)
	}

	got := <-reply
	if got.Code != http.StatusConflict {
		t.Errorf("the reply: %d %s", got.Code, body(got))
	}

	if typed := hostedProgram.typed(); typed != "x" {
		t.Errorf("typed %q, want only the viewer's key", typed)
	}
}

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

// An answer waiting for its turn to press is refused when the screen changes before the turn
// comes, even though it checked the prompt shown.
func TestAnAnswerIsRefusedWhenTheScreenChangesBeforeItPresses(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	stamp := float64(time.Now().UnixMilli() - 5000)
	hostedID, hostedProgram := fake.hostedSession(67, "p", "waiting", "permission prompt",
		permissionScreen("ls"), stamp)
	loop := start(t, fake)

	askID, _ := askByID(t, loop, hostedID)["id"].(string)

	// A viewer's keystroke holds the program's input while the answer checks the screen.
	hostedProgram.gate = make(chan struct{})
	viewer := make(chan error, 1)

	go func() { viewer <- fake.host.Write(pty.ID(hostedID), []byte("x")) }()

	hostedProgram.waitWriting(t)

	answer := make(chan sessions.Reply, 1)

	go func() {
		answer <- loop.Answer(context.WithoutCancel(t.Context()), hostedID, pressOne(askID))
	}()

	// The answer has read the prompt by now and waits for its turn; the screen then changes.
	time.Sleep(testtime.Scaled(300 * time.Millisecond))
	hostedProgram.show(t, permissionScreen("rm -rf build"))
	waitScreen(t, fake.host, hostedID, " rm -rf build")
	close(hostedProgram.gate)

	err := <-viewer
	if err != nil {
		t.Fatal(err)
	}

	got := <-answer
	if got.Code != http.StatusConflict {
		t.Errorf("the answer: %d %s", got.Code, body(got))
	}

	if typed := hostedProgram.typed(); typed != "x" {
		t.Errorf("typed %q, want only the viewer's key", typed)
	}
}
