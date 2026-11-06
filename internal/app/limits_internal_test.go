package app

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/server"
)

// A WebSocket handshake for a terminal is an HTTP request, so the server's bound on reading a
// request's headers is the handshake's deadline: one fact, the terminal route's.
func TestTheServerReadsHeadersWithinTheHandshakeDeadline(t *testing.T) {
	t.Parallel()

	got := newHTTPServer(nil).ReadHeaderTimeout
	if got != server.HandshakeTimeout {
		t.Fatalf("ReadHeaderTimeout %v, want the handshake deadline %v", got,
			server.HandshakeTimeout)
	}
}
