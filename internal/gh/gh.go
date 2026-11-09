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
	// maxOutputBytes caps what one gh call may print: the largest answers are PR lists with
	// their checks.
	maxOutputBytes = 8 << 20
	// maxErrorShown is the most of a failure's text an error carries, in UTF-16 units.
	maxErrorShown = 400
	// graphqlTimeout stops one GraphQL call: a search of several patterns takes a while.
	graphqlTimeout = 60 * time.Second
	// maxGraphQLBytes caps what one GraphQL call may print: a page of 100 PRs with their checks.
	maxGraphQLBytes = 32 << 20
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
		Dir: "", Timeout: timeout, MaxOutputBytes: maxOutputBytes, Stdin: "", Env: nil,
	})
	if result.Err == nil {
		return result.Stdout, nil
	}

	return "", &Error{Message: Message(result), Err: result.Err}
}

// GraphQL runs gh as Run does, with the longer limits a GraphQL query needs, and keeps what gh
// printed when it fails: gh exits non-zero when an answer carries errors (a PR that is gone, one
// organization's SAML rule) and still prints the answer, the parts that did resolve included.
// On a failure the output is returned beside the error.
func GraphQL(ctx context.Context, args []string) (string, error) {
	result := run.Cmd(ctx, "gh", args, run.Opts{
		Dir: "", Timeout: graphqlTimeout, MaxOutputBytes: maxGraphQLBytes, Stdin: "", Env: nil,
	})
	if result.Err == nil {
		return result.Stdout, nil
	}

	return result.Stdout, &Error{Message: Message(result), Err: result.Err}
}

// Large runs gh with Run's time limit and GraphQL's room for output, and keeps what gh printed
// when it fails, as GraphQL does: My PRs' searches carry every comment body, and a search that
// carries errors (one organization's SAML rule) still prints the PRs that did resolve.
func Large(ctx context.Context, args []string) (string, error) {
	result := run.Cmd(ctx, "gh", args, run.Opts{
		Dir: "", Timeout: timeout, MaxOutputBytes: maxGraphQLBytes, Stdin: "", Env: nil,
	})
	if result.Err == nil {
		return result.Stdout, nil
	}

	return result.Stdout, &Error{Message: Message(result), Err: result.Err}
}

// Message is what a failed gh run says: the first line of its trimmed stderr, else the run's
// own error, cut to 400 UTF-16 units.
func Message(result run.Result) string {
	message := run.FirstLine(js.Trim(result.Stderr))
	if message == "" {
		message = result.Err.Error()
	}

	return js.Slice(message, maxErrorShown)
}
