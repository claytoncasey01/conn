package server

import (
	"sync"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
)

// The input a session takes from its viewers, all of them together. Each rate refills a bucket
// that holds at most its burst: a paste of a megabyte goes through at once, typing never comes
// near either cap, and mouse movement (Claude Code asks for every motion) stays well inside the
// frames. Resizes have a small bucket of their own: each makes the host resize its screen and
// the program redraw, and a page sends one only when its window changes size. A frame past any
// bucket closes the viewer that sent it.
const (
	inputBytesPerSecond   = 1 << 20
	inputBytesBurst       = 1 << 20
	inputFramesPerSecond  = 250
	inputFramesBurst      = 500
	inputResizesPerSecond = 10
	inputResizesBurst     = 20
)

// inputCost is what one frame spends: its bytes of input, and whether it is a resize. Every
// frame also spends one of the frames.
type inputCost struct {
	bytes  int
	resize bool
}

// inputBudget is what is left of one session's input buckets at a moment.
type inputBudget struct {
	bytes   float64
	frames  float64
	resizes float64
	at      time.Time
}

// fullBudget is every bucket full at now.
func fullBudget(now time.Time) inputBudget {
	return inputBudget{
		bytes: inputBytesBurst, frames: inputFramesBurst, resizes: inputResizesBurst, at: now,
	}
}

// refillInput refills budget for the time since it was last looked at, up to the bursts. A clock
// that went back refills nothing.
func refillInput(budget inputBudget, now time.Time) inputBudget {
	elapsed := max(now.Sub(budget.at), 0).Seconds()

	refilled := inputBudget{
		bytes:   min(inputBytesBurst, budget.bytes+elapsed*inputBytesPerSecond),
		frames:  min(inputFramesBurst, budget.frames+elapsed*inputFramesPerSecond),
		resizes: min(inputResizesBurst, budget.resizes+elapsed*inputResizesPerSecond),
		at:      budget.at,
	}
	if now.After(budget.at) {
		refilled.at = now
	}

	return refilled
}

// spendInput refills budget, then spends one frame of cost from it if every bucket it draws on
// holds enough. A refused frame spends nothing.
func spendInput(budget inputBudget, now time.Time, cost inputCost) (inputBudget, bool) {
	refilled := refillInput(budget, now)

	if refilled.frames < 1 {
		return refilled, false
	}

	if refilled.bytes < float64(cost.bytes) {
		return refilled, false
	}

	if cost.resize {
		if refilled.resizes < 1 {
			return refilled, false
		}

		refilled.resizes--
	}

	refilled.frames--
	refilled.bytes -= float64(cost.bytes)

	return refilled, true
}

// isFull says whether every bucket of budget is at its burst.
func isFull(budget inputBudget) bool {
	if budget.bytes < inputBytesBurst {
		return false
	}

	if budget.frames < inputFramesBurst {
		return false
	}

	return budget.resizes >= inputResizesBurst
}

// budgets holds each session's input budget, shared by its viewers. A budget outlives its last
// viewer until it has refilled, so a viewer closed for overspending cannot come back to a full
// one; a full budget nobody holds is forgotten, so the table holds at most the sessions viewed
// in the last few seconds, each of which the host issued.
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

// join is a viewer of the session coming; a session with no budget kept starts full.
func (b *budgets) join(sessionID pty.ID) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.forgetFull()

	held, found := b.held[sessionID]
	if !found {
		held = &heldBudget{budget: fullBudget(b.now()), holders: 0}
		b.held[sessionID] = held
	}

	held.holders++
}

// leave is a viewer of the session going; its budget is kept until it has refilled.
func (b *budgets) leave(sessionID pty.ID) {
	b.mu.Lock()
	defer b.mu.Unlock()

	held := b.mustHold(sessionID)
	held.holders--

	b.forgetFull()
}

// spend spends a frame of cost from the session's budget, and says whether it held enough.
func (b *budgets) spend(sessionID pty.ID, cost inputCost) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	held := b.mustHold(sessionID)

	var allowed bool

	held.budget, allowed = spendInput(held.budget, b.now(), cost)

	return allowed
}

// forgetFull forgets every budget nobody holds that has refilled. The caller holds b.mu.
func (b *budgets) forgetFull() {
	now := b.now()

	for sessionID, held := range b.held {
		if held.holders > 0 {
			continue
		}

		if isFull(refillInput(held.budget, now)) {
			delete(b.held, sessionID)
		}
	}
}

// count is how many sessions' budgets are kept.
func (b *budgets) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return len(b.held)
}

// mustHold is the session's budget for a viewer that joined; a viewer that spends or leaves
// without having joined is a mistake in the server's code. The caller holds b.mu.
func (b *budgets) mustHold(sessionID pty.ID) *heldBudget {
	held, found := b.held[sessionID]
	if !found {
		panic("server: an input budget was used by a viewer that never joined")
	}

	if held.holders <= 0 {
		panic("server: an input budget was used by a viewer that never joined")
	}

	return held
}
