package server

import (
	"sync"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
)

// The input a session takes from its viewers, all of them together. Each rate refills a bucket
// that holds at most its burst: a paste of a megabyte goes through at once, typing never comes
// near either cap, and mouse movement (Claude Code asks for every motion) stays well inside the
// frames. A frame past either bucket closes the viewer that sent it.
const (
	inputBytesPerSecond  = 1 << 20
	inputBytesBurst      = 1 << 20
	inputFramesPerSecond = 250
	inputFramesBurst     = 500
)

// inputBudget is what is left of one session's input buckets at a moment.
type inputBudget struct {
	bytes  float64
	frames float64
	at     time.Time
}

// fullBudget is both buckets full at now.
func fullBudget(now time.Time) inputBudget {
	return inputBudget{bytes: inputBytesBurst, frames: inputFramesBurst, at: now}
}

// spendInput refills budget for the time since it was last looked at, up to the bursts, and
// spends one frame of size bytes from it if both buckets hold enough. A refused frame spends
// nothing. A clock that went back refills nothing.
func spendInput(budget inputBudget, now time.Time, size int) (inputBudget, bool) {
	elapsed := max(now.Sub(budget.at), 0).Seconds()

	refilled := inputBudget{
		bytes:  min(inputBytesBurst, budget.bytes+elapsed*inputBytesPerSecond),
		frames: min(inputFramesBurst, budget.frames+elapsed*inputFramesPerSecond),
		at:     budget.at,
	}
	if now.After(budget.at) {
		refilled.at = now
	}

	if refilled.frames < 1 {
		return refilled, false
	}

	if refilled.bytes < float64(size) {
		return refilled, false
	}

	refilled.frames--
	refilled.bytes -= float64(size)

	return refilled, true
}

// budgets holds each session's input budget while it has a viewer, shared by its viewers, so
// the table is never larger than the terminals open.
type budgets struct {
	now func() time.Time

	mu   sync.Mutex
	held map[pty.ID]*heldBudget
}

// heldBudget is a session's budget and how many of its viewers hold it.
type heldBudget struct {
	budget  inputBudget
	holders int
}

func newBudgets(now func() time.Time) *budgets {
	return &budgets{now: now, mu: sync.Mutex{}, held: make(map[pty.ID]*heldBudget)}
}

// join is a viewer of the session coming; a session's first viewer starts it full.
func (b *budgets) join(sessionID pty.ID) {
	b.mu.Lock()
	defer b.mu.Unlock()

	held, found := b.held[sessionID]
	if !found {
		held = &heldBudget{budget: fullBudget(b.now()), holders: 0}
		b.held[sessionID] = held
	}

	held.holders++
}

// leave is a viewer of the session going; the session's budget is forgotten with its last.
func (b *budgets) leave(sessionID pty.ID) {
	b.mu.Lock()
	defer b.mu.Unlock()

	held := b.mustHold(sessionID)

	held.holders--
	if held.holders == 0 {
		delete(b.held, sessionID)
	}
}

// spend spends a frame of size bytes from the session's budget, and says whether it held enough.
func (b *budgets) spend(sessionID pty.ID, size int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	held := b.mustHold(sessionID)

	var allowed bool

	held.budget, allowed = spendInput(held.budget, b.now(), size)

	return allowed
}

// count is how many sessions' budgets are held.
func (b *budgets) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return len(b.held)
}

// mustHold is the session's budget; only a viewer that joined spends or leaves, so a session
// with none is a mistake in the server's code. The caller holds b.mu.
func (b *budgets) mustHold(sessionID pty.ID) *heldBudget {
	held, found := b.held[sessionID]
	if !found {
		panic("server: an input budget was used by a viewer that never joined")
	}

	return held
}
