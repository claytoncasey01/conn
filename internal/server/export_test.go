package server

import (
	"context"
	"net/http"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
)

// MarshalJSON lets the tests check the JSON text without a route that returns user text.
func MarshalJSON(v any) []byte { return marshalJSON(v) }

// ServeWith is ServeHTTP with next in place of the API's routes.
func ServeWith(s *Server, next func(http.ResponseWriter, *http.Request, string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serve(w, r, next) })
}

// The terminal route's limits a test goes up to or past.
const (
	InputFrameBytesMax = inputFrameBytesMax
	FrameBytesMax      = frameBytesMax
	InputBytesBurst    = inputBytesBurst
	InputFramesBurst   = inputFramesBurst
	ConnectionsMax     = connectionsMax
	// The bounds of fix round 1.
	InputQueueFramesMax    = inputQueueFramesMax
	InputResizesBurst      = inputResizesBurst
	ReplayBytesInFlightMax = replayBytesInFlightMax
)

// TerminalTimes are the terminal route's waits and its clock, which a test shortens or stops.
// A zero wait keeps the route's own.
type TerminalTimes struct {
	PingInterval      time.Duration
	PingTimeout       time.Duration
	FrameWriteTimeout time.Duration
	// ReplayWriteTimeout bounds the writing of a whole replay.
	ReplayWriteTimeout time.Duration
	Now                func() time.Time
}

// NewTerminalsTimed is NewTerminals with the waits and clock times gives.
func NewTerminalsTimed(ctx context.Context, host TerminalHost, times TerminalTimes) *Terminals {
	terminals := NewTerminals(ctx, host)

	if times.PingInterval > 0 {
		terminals.times.pingInterval = times.PingInterval
	}

	if times.PingTimeout > 0 {
		terminals.times.pingTimeout = times.PingTimeout
	}

	if times.FrameWriteTimeout > 0 {
		terminals.times.frameWriteTimeout = times.FrameWriteTimeout
	}

	if times.ReplayWriteTimeout > 0 {
		terminals.times.replayWriteTimeout = times.ReplayWriteTimeout
	}

	if times.Now != nil {
		terminals.budgets = newBudgets(times.Now)
		terminals.interrupts = newInterrupts(times.Now)
	}

	return terminals
}

// InterruptRepeatWindow is interruptRepeatWindow.
const InterruptRepeatWindow = interruptRepeatWindow

// BudgetsKept is how many sessions' input budgets terminals keeps.
func BudgetsKept(terminals *Terminals) int { return terminals.budgets.count() }

// SetTerminalEnded has terminals tell ended each session whose terminal is told to end. Set it
// before any terminal is opened.
func SetTerminalEnded(terminals *Terminals, ended func(pty.ID)) { terminals.ended = ended }

// ActiveTerminals is how many terminals are being served.
func ActiveTerminals(terminals *Terminals) int {
	terminals.activeMu.Lock()
	defer terminals.activeMu.Unlock()

	return terminals.active
}
