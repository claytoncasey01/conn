package newsession

import (
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/pty"
)

// Request is startRequest, for the tests of the package.
type Request = startRequest

// ErrNotAClone is errNotAClone, for the tests of the package.
var ErrNotAClone = errNotAClone

// ParseRequest is parseRequest, for the tests of the package.
func ParseRequest(body *js.Object) (Request, []string) { return parseRequest(body) }

// SafeName is safeName, for the tests of the package.
func SafeName(name string) (string, error) { return safeName(name) }

// Spec is specOf, for the tests of the package.
func Spec(claude, sessionID, folder string, request Request) pty.Spec {
	return specOf(claude, sessionID, folder, request)
}
