package server

import (
	"context"
	"net/http"
	"time"
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
)

// TerminalTimes are the terminal route's waits and its clock, which a test shortens or stops.
// A zero wait keeps the route's own.
type TerminalTimes struct {
	PingInterval      time.Duration
	PingTimeout       time.Duration
	FrameWriteTimeout time.Duration
	Now               func() time.Time
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

	if times.Now != nil {
		terminals.budgets = newBudgets(times.Now)
	}

	return terminals
}
