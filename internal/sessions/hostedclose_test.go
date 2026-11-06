package sessions_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/ask"
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
