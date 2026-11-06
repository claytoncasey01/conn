package sessions_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/ask"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/sessions"
)

// TestReviewsCanCloseAHostedSessionThroughTheHost: the input state and the close Reviews uses to
// end a finished review reach a hosted session by its issued id: the box read from the host's
// screen, and the close a stop through the host, refused while the box holds anything. Nothing
// goes through cmux.
func TestReviewsCanCloseAHostedSessionThroughTheHost(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	stamp := float64(time.Now().UnixMilli() - 5000)
	idle, idleProgram := fake.hostedSession(61, "pr-1-acme--app", "idle", "", idleBox, stamp)
	drafted, draftedProgram := fake.hostedSession(62, "pr-2-acme--app", "idle", "",
		boxWith("half a thought"), stamp)
	loop := start(t, fake)

	rowOf(t, loop, idle, anyRow)
	rowOf(t, loop, drafted, anyRow)
	time.Sleep(testSettle())

	if got := loop.InputState(t.Context(), idle); got != ask.PromptEmpty {
		t.Errorf("idle box: %q", got)
	}

	if got := loop.InputState(t.Context(), drafted); got != ask.PromptDraft {
		t.Errorf("drafted box: %q", got)
	}

	refused := loop.CloseWorkspace(t.Context(), drafted)
	if refused.Code != http.StatusConflict {
		t.Errorf("a draft was closed: %d %s", refused.Code, body(refused))
	}

	if stops := draftedProgram.stops(); len(stops) != 0 {
		t.Errorf("a refused close signalled the program: %v", stops)
	}

	closed := loop.CloseWorkspace(t.Context(), idle)
	if body(closed) != `{"ok":true}` {
		t.Errorf("close: %d %s", closed.Code, body(closed))
	}

	if !slices.Equal(idleProgram.stops(), []string{"terminate"}) {
		t.Errorf("the closed program got %v, want a polite stop", idleProgram.stops())
	}

	unknown := loop.CloseWorkspace(t.Context(), "0123456789abcdef0123456789abcdef")
	if unknown.Code != http.StatusNotFound {
		t.Errorf("unknown: %d %s", unknown.Code, body(unknown))
	}

	if got := loop.InputState(t.Context(), "0123456789abcdef0123456789abcdef"); got != "unknown" {
		t.Errorf("unknown box: %q", got)
	}

	if log := fake.term.sent(); len(log) != 0 {
		t.Errorf("a hosted close reached cmux: %v", log)
	}
}

// testSettle is longer than the host's settle window for input, so a screen read counts.
func testSettle() time.Duration { return settledAfter }

// TestAHostedCloseIsRefusedUnlessTheReadStillStands: a hosted review is not closed while a program
// it started holds its terminal, when something was typed into it just before, or while it runs
// but has not registered; none of them is signalled.
func TestAHostedCloseIsRefusedUnlessTheReadStillStands(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	stamp := float64(time.Now().UnixMilli() - 5000)
	held, heldProgram := fake.hostedSession(71, "pr-3-acme--app", "idle", "", idleBox, stamp)
	typed, typedProgram := fake.hostedSession(72, "pr-4-acme--app", "idle", "", idleBox, stamp)
	unregistered, unregisteredProgram := fake.hostedSession(73, "pr-5-acme--app", "", "",
		idleBox, stamp)
	loop := start(t, fake)

	rowOf(t, loop, held, anyRow)
	rowOf(t, loop, typed, anyRow)
	rowOf(t, loop, unregistered, anyRow)
	time.Sleep(testSettle())

	heldProgram.background.Store(true)

	foreground := loop.CloseWorkspace(t.Context(), held)
	if body(foreground) != `{"error":"a program the session started has its terminal, so `+
		`nothing was closed"}` {
		t.Errorf("foreground held: %d %s", foreground.Code, body(foreground))
	}

	err := fake.host.Write(pty.ID(typed), []byte("x"))
	if err != nil {
		t.Fatal(err)
	}

	justTyped := loop.CloseWorkspace(t.Context(), typed)
	if justTyped.Code != http.StatusConflict {
		t.Errorf("input just typed: %d %s", justTyped.Code, body(justTyped))
	}

	notRegistered := loop.CloseWorkspace(t.Context(), unregistered)
	if notRegistered.Code != http.StatusNotFound {
		t.Errorf("unregistered: %d %s", notRegistered.Code, body(notRegistered))
	}

	for _, program := range []*program{heldProgram, typedProgram, unregisteredProgram} {
		if stops := program.stops(); len(stops) != 0 {
			t.Errorf("program %d was signalled: %v", program.pid, stops)
		}
	}
}

// TestARefusedHostedCloseSaysWhyInAClosesWords: every refusal the host gives a close is a plain
// answer, never a 500 holding the host's error.
func TestARefusedHostedCloseSaysWhyInAClosesWords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		code int
		want string
	}{
		{
			"a paste open", pty.ErrPasteOpen, http.StatusConflict,
			"a paste into the session's terminal is open, so nothing was closed",
		},
		{
			"not taking input", pty.ErrInputBlocked, http.StatusConflict,
			"the session is not taking input, so nothing was closed",
		},
		{
			"other input", pty.ErrOtherInput, http.StatusConflict,
			"something was typed into the session's terminal just now, so nothing was closed",
		},
		{"ended", pty.ErrEnded, http.StatusNotFound, "no such live session"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reply := sessions.CloseFailure(tt.err)
			if reply.Code != tt.code {
				t.Errorf("code %d", reply.Code)
			}

			if body(reply) != `{"error":"`+tt.want+`"}` {
				t.Errorf("body %s", body(reply))
			}
		})
	}
}
