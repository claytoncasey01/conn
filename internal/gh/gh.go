// Package gh runs the GitHub CLI for Conn: the signed-in account's own gh, with its own sign-in,
// so Conn holds no GitHub token.
package gh

import (
	"context"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/run"
)

const (
	// timeout stops one gh call.
	timeout = 30 * time.Second
	// outputBytesMax caps what one gh call may print: the largest answers are PR lists with
	// their checks.
	outputBytesMax = 8 << 20
	// errorShownMax is the most of a failure's text an error carries, in UTF-16 units.
	errorShownMax = 400
)

// Exec runs gh with args and answers what it printed. A test passes its own.
type Exec func(ctx context.Context, args []string) (string, error)

// Error is a gh call that failed: the first line gh printed on stderr, or how the run ended
// when it printed nothing there.
type Error struct {
	Message string
	Err     error
}

// Error is the message Conn shows for the failed call.
func (e *Error) Error() string { return e.Message }

// Unwrap is how the run ended (a *run.Error).
func (e *Error) Unwrap() error { return e.Err }

// Run runs gh with args, with a time limit and a cap on its output.
func Run(ctx context.Context, args []string) (string, error) {
	result := run.Cmd(ctx, "gh", args, run.Opts{
		Dir: "", Timeout: timeout, OutputBytesMax: outputBytesMax, Stdin: "", Env: nil,
	})
	if result.Err == nil {
		return result.Stdout, nil
	}

	return "", &Error{Message: Message(result), Err: result.Err}
}

// Message is what a failed gh run says: the first line of its trimmed stderr, else the run's
// own error, cut to 400 UTF-16 units.
func Message(result run.Result) string {
	message := run.FirstLine(js.Trim(result.Stderr))
	if message == "" {
		message = result.Err.Error()
	}

	return js.Slice(message, errorShownMax)
}
