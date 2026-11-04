package sessions_test

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/run"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/testtime"
)

var errCmux = errors.New("exit status 1")

// terminal is a fake cmux and ps: screens by surface, process states by pid, and a log of every
// command. A gate, when set, holds every read-screen until it is closed.
type terminal struct {
	mutex   sync.Mutex
	screens map[string]string
	states  map[float64]string
	failing map[string]string
	log     []string
	kills   []float64
	killErr error
	gate    chan struct{}
	reading chan struct{}
}

func newTerminal() *terminal {
	return &terminal{
		mutex: sync.Mutex{}, screens: map[string]string{}, states: map[float64]string{},
		failing: map[string]string{}, log: nil, kills: nil, killErr: nil, gate: nil,
		reading: make(chan struct{}, 64),
	}
}

func (f *terminal) cmux(_ context.Context, args []string) run.Result {
	f.mutex.Lock()
	f.log = append(f.log, strings.Join(args, " "))
	gate := f.gate
	stderr, failing := f.failing[args[0]]
	screen, found := "", false

	if args[0] == "read-screen" {
		screen, found = f.screens[args[4]]
	}

	f.mutex.Unlock()

	if args[0] == "read-screen" {
		f.reading <- struct{}{}

		if gate != nil {
			<-gate
		}
	}

	if failing {
		return run.Result{
			Stdout: "", Stderr: stderr, Code: 1, Err: errCmux, Spawn: "",
			Killed: false, Signal: "",
		}
	}

	if args[0] == "read-screen" {
		if !found {
			return run.Result{
				Stdout: "", Stderr: "", Code: 1, Err: errCmux, Spawn: "",
				Killed: false, Signal: "",
			}
		}
	}

	return run.Result{
		Stdout: screen, Stderr: "", Code: 0, Err: nil, Spawn: "", Killed: false,
		Signal: "",
	}
}

func (f *terminal) stat(_ context.Context, pid float64) run.Result {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	f.log = append(f.log, "ps "+js.Number(pid))

	state, known := f.states[pid]
	if !known {
		state = "S+"
	}

	if state == "ended" {
		return run.Result{
			Stdout: "", Stderr: "", Code: 1, Err: errCmux, Spawn: "", Killed: false,
			Signal: "",
		}
	}

	return run.Result{
		Stdout: state + "\n", Stderr: "", Code: 0, Err: nil, Spawn: "",
		Killed: false, Signal: "",
	}
}

func (f *terminal) kill(pid float64) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	f.kills = append(f.kills, pid)

	return f.killErr
}

// sent is the commands that typed or pressed something.
func (f *terminal) sent() []string {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	out := []string{}

	for _, line := range f.log {
		if strings.HasPrefix(line, "send") {
			out = append(out, line)
		}

		if strings.HasPrefix(line, "workspace") {
			out = append(out, line)
		}
	}

	return out
}

func (f *terminal) count(prefix string) int {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	count := 0

	for _, line := range f.log {
		if strings.HasPrefix(line, prefix) {
			count++
		}
	}

	return count
}

const (
	rule = "────────────────────────────────────────"
	dots = "╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌"
	// idleBox is Claude's empty input box.
	idleBox = "✻ done\n\n" + rule + "\n❯ \n" + rule + "\n  ⏵⏵ auto mode on"
	draft   = "✻ done\n\n" + rule + "\n❯ half typed\n" + rule
)

// permissionScreen is a permission dialog for a command: Yes is 1, always-allow 2, No 3.
func permissionScreen(command string) string {
	return strings.Join([]string{
		" ⏺ Bash(" + command + ")", "", rule, " Bash command", " Run it", dots, " " + command,
		dots, " Do you want to proceed?", " ❯ 1. Yes",
		"   2. Yes, and always allow access to /work from this project",
		"   3. No", "", " Esc to cancel · Tab to amend",
	}, "\n")
}

func surfaceOf(pid int) string {
	return strconv.Itoa(100000+pid) + "-surface-0000-0000-000000000000"[:30]
}

// cmuxSession puts a live session under the cmux app, in workspace, with a screen.
func (w *world) cmuxSession(pid int, workspace, status, waitingFor, screen string, stamp float64) {
	w.t.Helper()

	folder := w.folder("cmux-"+strconv.Itoa(pid), "")
	surface := surfaceOf(pid)

	if len(w.procs) == 1 {
		w.procs = append(w.procs, "500 1 /Applications/cmux.app/Contents/MacOS/cmux",
			"600 500 /bin/zsh")
	}

	w.procs = append(w.procs, strconv.Itoa(pid)+" 600 claude")
	w.envs[float64(pid)] = "claude CMUX_WORKSPACE_ID=" + uuid(workspace) + " CMUX_SURFACE_ID=" +
		uuid(surface)
	w.term.screens[uuid(surface)] = screen

	entry := map[string]any{
		"pid": pid, "sessionId": "s-" + strconv.Itoa(pid), "cwd": folder,
		"name": "n" + strconv.Itoa(pid), "status": status, "statusUpdatedAt": stamp,
	}
	if waitingFor != "" {
		entry["waitingFor"] = waitingFor
	}

	w.register(strconv.Itoa(pid)+".json", entry)
}

// uuid makes a 36-character id from a short name, as cmux's ids are.
func uuid(name string) string {
	padded := (name + strings.Repeat("0", 32))[:32]
	hex := strings.Map(func(char rune) rune {
		if strings.ContainsRune("0123456789abcdef", char) {
			return char
		}

		return 'a'
	}, strings.ToLower(padded))

	return hex[:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:]
}

// askOf waits until the snapshot shows the session's ask, read by the loop in the background.
func askOf(t *testing.T, loop *sessions.Loop, pid int) map[string]any {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(5 * time.Second))
	for time.Now().Before(deadline) {
		loop.ReadAsks(t.Context())

		data, err := loop.Snapshot(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		parsed, _ := js.Parse(data)
		sessionsList, _ := get(parsed, "sessions").([]any)

		for _, session := range sessionsList {
			if get(session, "id") != strconv.Itoa(pid) {
				continue
			}

			if found, ok := get(session, "ask").(*js.Object); ok {
				out := map[string]any{}
				for _, key := range found.Keys() {
					out[key], _ = found.Get(key)
				}

				return out
			}
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("session %d never showed an ask", pid)

	return nil
}

func get(value any, key string) any {
	object, _ := value.(*js.Object)
	found, _ := object.Get(key)

	return found
}

func body(reply sessions.Reply) string { return string(js.Marshal(reply.Body)) }

func TestStopGoesByTheShownStatus(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	fake.cmuxSession(10, "w10", "busy", "", idleBox, 1)
	fake.cmuxSession(11, "w11", "idle", "", idleBox, 1)
	fake.cmuxSession(12, "w12", "idle", "", idleBox, 1)
	fake.term.killErr = syscall.ESRCH

	loop := start(t, fake)
	read(t, loop)

	// The registry says busy, but nothing was ever written: the page shows it idle... only once
	// it has a line; with none at all it stays busy.
	busy := loop.Stop(t.Context(), "10", false)
	if busy.Code != http.StatusConflict {
		t.Errorf("busy stop: %d %s", busy.Code, body(busy))
	}

	if body(busy) != `{"error":"session is busy","busy":true}` {
		t.Errorf("busy body %s", body(busy))
	}

	fake.term.mutex.Lock()
	fake.term.killErr = nil
	fake.term.mutex.Unlock()

	stopped := loop.Stop(t.Context(), "11", false)
	if body(stopped) != `{"ok":true,"name":"n11"}` {
		t.Errorf("idle stop: %d %s", stopped.Code, body(stopped))
	}

	fake.term.mutex.Lock()
	fake.term.killErr = syscall.ESRCH
	fake.term.mutex.Unlock()

	failed := loop.Stop(t.Context(), "12", true)
	if body(failed) != `{"error":"kill() failed: ESRCH: No such process"}` {
		t.Errorf("failed kill: %d %s", failed.Code, body(failed))
	}

	missing := loop.Stop(t.Context(), "99", true)
	if body(missing) != `{"error":"no such live session"}` {
		t.Errorf("missing: %s", body(missing))
	}
}

func TestReplyTypesOnlyIntoAnEmptyBox(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	fake.cmuxSession(20, "w20", "idle", "", idleBox, 1)
	fake.cmuxSession(21, "w21", "idle", "", draft, 1)
	fake.cmuxSession(22, "w22", "idle", "", idleBox, 1)
	fake.cmuxSession(23, "w23", "waiting", "permission prompt", idleBox, 1)
	fake.term.states[22] = "T+"

	loop := start(t, fake)
	read(t, loop)

	tests := []struct {
		pid, text, want string
	}{
		{"20", "hello", `{"ok":true}`},
		{"21", "hello", `{"error":"the prompt already has text in it (a draft or a suggestion); ` +
			`clear it in the terminal first"}`},
		{"22", "hello", `{"error":"the session is not in the foreground of its terminal ` +
			`(suspended or exiting), so nothing was sent"}`},
		{"23", "hello", `{"error":"it is waiting on a permission prompt: attach to the ` +
			`terminal to answer it"}`},
		{"20", "  ", `{"error":"nothing to send"}`},
		{"20", "/clear", `{"error":"a leading ! or / would run a shell command or slash ` +
			`command; type those in the terminal"}`},
		{"99", "hello", `{"error":"no such live session"}`},
	}

	for _, tt := range tests {
		got := loop.Reply(t.Context(), tt.pid, tt.text)
		if body(got) != tt.want {
			t.Errorf("reply %s %q: %s", tt.pid, tt.text, body(got))
		}
	}

	sent := fake.term.sent()
	if len(sent) != 2 {
		t.Fatalf("sent %v", sent)
	}

	if !strings.HasSuffix(sent[0], "--paste -- hello") {
		t.Errorf("paste %q", sent[0])
	}

	if !strings.HasSuffix(sent[1], " enter") {
		t.Errorf("enter %q", sent[1])
	}

	fake.term.mutex.Lock()
	fake.term.failing["send"] = "Error: surface gone\nmore detail"
	fake.term.mutex.Unlock()

	failed := loop.Reply(t.Context(), "20", "again")
	if body(failed) != `{"error":"cmux would not send it: Error: surface gone"}` {
		t.Errorf("failed send: %s", body(failed))
	}

	fake.term.mutex.Lock()
	delete(fake.term.failing, "send")
	fake.term.mutex.Unlock()

	if body(loop.Reply(t.Context(), "20", "after")) != `{"ok":true}` {
		t.Error("a failed send blocked the next reply")
	}
}

func TestAnswerPressesTheKeyOnTheScreenReadAgain(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	stamp := float64(time.Now().UnixMilli() - 5000)
	fake.cmuxSession(30, "w30", "waiting", "permission prompt", permissionScreen("ls"), stamp)

	loop := start(t, fake)
	read(t, loop)

	shown := askOf(t, loop, 30)
	askID, _ := shown["id"].(string)

	if shown["since"] != stamp {
		t.Errorf("since %v, want the status time %v", shown["since"], stamp)
	}

	wrongKey := loop.Answer(t.Context(), "30", sessions.AnswerBody{
		AskID: askID, Choice: "2", Text: nil, HasChoice: true, HasText: false,
	})
	if body(wrongKey) != `{"error":"that is not one of the choices on the prompt"}` {
		t.Errorf("always-allow row: %s", body(wrongKey))
	}

	// The screen changes between display and click: the ask comes back as it is now.
	fake.term.mutex.Lock()
	fake.term.screens[uuid(surfaceOf(30))] = permissionScreen("rm -rf build")
	fake.term.mutex.Unlock()

	changed := loop.Answer(t.Context(), "30", sessions.AnswerBody{
		AskID: askID, Choice: "3", Text: nil, HasChoice: true, HasText: false,
	})
	if changed.Code != http.StatusConflict {
		t.Errorf("changed screen: %d %s", changed.Code, body(changed))
	}

	if !strings.Contains(body(changed), `"text":"rm -rf build"`) {
		t.Errorf("the 409 does not carry the ask as it is now: %s", body(changed))
	}

	current := askOf(t, loop, 30)
	newID, _ := current["id"].(string)

	denied := loop.Answer(t.Context(), "30", sessions.AnswerBody{
		AskID: newID, Choice: "3", Text: nil, HasChoice: true, HasText: false,
	})
	if body(denied) != `{"ok":true}` {
		t.Fatalf("deny: %s", body(denied))
	}

	sent := fake.term.sent()
	if len(sent) != 1 {
		t.Fatalf("sent %v", sent)
	}

	if !strings.HasSuffix(sent[0], " -- 3") {
		t.Errorf("pressed %q", sent[0])
	}

	// A double click before the terminal redraws: the prompt just answered is not answered
	// again.
	again := loop.Answer(t.Context(), "30", sessions.AnswerBody{
		AskID: newID, Choice: "3", Text: nil, HasChoice: true, HasText: false,
	})
	if again.Code != http.StatusConflict {
		t.Errorf("double click: %d %s", again.Code, body(again))
	}

	if len(fake.term.sent()) != 1 {
		t.Errorf("a double click pressed again: %v", fake.term.sent())
	}
}

func TestTwoAnswersAtOnceSendOnce(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	fake.cmuxSession(40, "w40", "waiting", "permission prompt", permissionScreen("ls"), 1)

	loop := start(t, fake)
	read(t, loop)

	askID, _ := askOf(t, loop, 40)["id"].(string)

	fake.term.mutex.Lock()
	fake.term.gate = make(chan struct{})
	gate := fake.term.gate
	fake.term.mutex.Unlock()

	for len(fake.term.reading) > 0 {
		<-fake.term.reading
	}

	results := make(chan sessions.Reply, 2)
	answer := func() {
		results <- loop.Answer(context.Background(), "40", sessions.AnswerBody{
			AskID: askID, Choice: "1", Text: nil, HasChoice: true, HasText: false,
		})
	}

	go answer()

	<-fake.term.reading

	go answer()

	second := <-results

	close(gate)

	first := <-results

	if body(second) != `{"error":"another message to this session is still being sent"}` {
		t.Errorf("second answer: %s", body(second))
	}

	if body(first) != `{"ok":true}` {
		t.Errorf("first answer: %s", body(first))
	}

	if len(fake.term.sent()) != 1 {
		t.Errorf("sent %v", fake.term.sent())
	}
}

func TestSharedWorkspacesAndMisnamedDialogsHaveNoButtons(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	screen := permissionScreen("ls")
	fake.cmuxSession(50, "shared", "waiting", "permission prompt", screen, 1)
	fake.cmuxSession(51, "shared", "idle", "", idleBox, 1)
	fake.cmuxSession(52, "own", "waiting", "dialog open", screen, 1)
	fake.cmuxSession(53, "own2", "waiting", "permission prompt", screen, 1)

	loop := start(t, fake)
	read(t, loop)

	for pid, want := range map[int]string{50: "unknown", 52: "unknown", 53: "permission"} {
		found := askOf(t, loop, pid)
		if found["kind"] != want {
			t.Errorf("session %d: kind %v, want %s", pid, found["kind"], want)
		}

		options, _ := found["options"].([]any)
		if want == "unknown" {
			if len(options) != 0 {
				t.Errorf("session %d has buttons", pid)
			}
		}
	}
}

func TestAtMostFourScreensAreReadAtOnce(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	for pid := 60; pid < 66; pid++ {
		fake.cmuxSession(pid, "w"+strconv.Itoa(pid), "waiting", "permission prompt",
			permissionScreen("ls"), 1)
	}

	fake.term.gate = make(chan struct{})

	loop := start(t, fake)
	read(t, loop)
	loop.ReadAsks(t.Context())

	for range 4 {
		select {
		case <-fake.term.reading:
		case <-time.After(testtime.Scaled(5 * time.Second)):
			t.Fatal("four reads did not start")
		}
	}

	loop.ReadAsks(t.Context())

	select {
	case <-fake.term.reading:
		t.Error("a fifth read started while four were running")
	case <-time.After(testtime.Scaled(200 * time.Millisecond)):
	}

	close(fake.term.gate)
}

func TestCloseWorkspaceReadsTheScreenItself(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	fake.cmuxSession(70, "w70", "idle", "", idleBox, 1)
	fake.cmuxSession(71, "w71", "idle", "", draft, 1)
	fake.cmuxSession(72, "shared", "idle", "", idleBox, 1)
	fake.cmuxSession(73, "shared", "idle", "", idleBox, 1)

	loop := start(t, fake)
	read(t, loop)

	if loop.InputState(t.Context(), "71") != "draft" {
		t.Errorf("input state %s", loop.InputState(t.Context(), "71"))
	}

	if loop.InputState(t.Context(), "99") != "unknown" {
		t.Error("an unknown session's input state")
	}

	tests := []struct {
		pid, want string
	}{
		{"71", `{"error":"the session's input box is not visibly empty, so nothing was closed"}`},
		{"72", `{"error":"another session is running in this workspace"}`},
		{"99", `{"error":"no such live session"}`},
		{"70", `{"ok":true}`},
	}

	for _, tt := range tests {
		got := loop.CloseWorkspace(t.Context(), tt.pid)
		if body(got) != tt.want {
			t.Errorf("close %s: %s", tt.pid, body(got))
		}
	}

	closes := fake.term.count("workspace close")
	if closes != 1 {
		t.Errorf("%d closes", closes)
	}
}

func TestOpenTerminalSelectsTheWorkspace(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	fake.cmuxSession(80, "w80", "idle", "", idleBox, 1)

	loop := start(t, fake)
	read(t, loop)

	if body(loop.OpenTerminal(t.Context(), "80")) != `{"ok":true}` {
		t.Error("open terminal")
	}

	if strings.Join(fake.term.sent(), ";") != "workspace select "+uuid("w80") {
		t.Errorf("sent %v", fake.term.sent())
	}

	fake.term.mutex.Lock()
	fake.term.failing["workspace"] = "Error: gone"
	fake.term.mutex.Unlock()

	failed := loop.OpenTerminal(t.Context(), "80")
	if body(failed) != `{"error":"cmux would not open it: Error: gone"}` {
		t.Error("a failed select")
	}
}

// TestAReadThatComesBackAfterTheSessionMovedOnIsDropped: the session answered and asked again
// while its screen was being read, so the old screen never stands for the new prompt.
func TestAReadThatComesBackAfterTheSessionMovedOnIsDropped(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	fake.cmuxSession(90, "w90", "waiting", "permission prompt", permissionScreen("old-command"), 1)
	fake.term.gate = make(chan struct{})

	loop := start(t, fake)
	read(t, loop)
	loop.ReadAsks(t.Context())

	select {
	case <-fake.term.reading:
	case <-time.After(testtime.Scaled(5 * time.Second)):
		t.Fatal("the read never started")
	}

	// Answered and asking again while the read was out: a new status time, a new screen.
	fake.cmuxSession(90, "w90", "waiting", "permission prompt", permissionScreen("new-command"),
		2)
	time.Sleep(testtime.Scaled(2500 * time.Millisecond))

	fake.term.mutex.Lock()
	gate := fake.term.gate
	fake.term.gate = nil
	fake.term.mutex.Unlock()
	close(gate)

	text, _ := askOf(t, loop, 90)["text"].(string)
	if !strings.Contains(text, "new-command") {
		t.Errorf("the ask is the old screen's: %v", text)
	}
}
