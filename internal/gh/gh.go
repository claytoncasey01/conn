// Package gh runs the GitHub CLI for Conn: the signed-in account's own gh, with its own sign-in,
// so Conn holds no GitHub token.
package gh

import (
	"context"
	"regexp"
	"strconv"
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
	// httpClientFirst and httpClientLast bound the HTTP statuses that are client errors.
	httpClientFirst = 400
	httpClientLast  = 499
)

// httpStatus is how gh ends the message of a call GitHub answered with an HTTP error: the status
// in brackets, "(HTTP 422)".
var httpStatus = regexp.MustCompile(`\(HTTP (\d{3})\)`)

// Exec runs gh with args and answers what it printed. A test passes its own.
type Exec func(ctx context.Context, args []string) (string, error)

// Error is a gh call that failed: the first line gh printed on stderr, or how the run ended
// when it printed nothing there. What that text says of the failure is read here, once, so a
// caller asks the fields rather than matching the text again.
type Error struct {
	Message string
	// Status is the HTTP status gh said GitHub answered with; 0 when it named none (a timeout, a
	// killed gh, a dropped connection).
	Status int
	// Overflowed is that gh printed more than the call's cap: the answer was cut off and gh was
	// stopped.
	Overflowed bool
	Err        error
}

// Error is the message Conn shows for the failed call.
func (e *Error) Error() string { return e.Message }

// Unwrap is how the run ended (a *run.Error).
func (e *Error) Unwrap() error { return e.Err }

// ClientError is whether GitHub answered with a client error (HTTP 4xx): it refused the call, so
// a write surely did not happen. Any other failure may have reached GitHub.
func (e *Error) ClientError() bool {
	return e.Status >= httpClientFirst && e.Status <= httpClientLast
}

// Run runs gh with args, with a time limit and a cap on its output.
func Run(ctx context.Context, args []string) (string, error) {
	result := run.Cmd(ctx, "gh", args, run.Opts{
		Dir: "", Timeout: timeout, MaxOutputBytes: maxOutputBytes, Stdin: "", Env: nil,
	})
	if result.Err == nil {
		return result.Stdout, nil
	}

	return "", errorOf(result)
}

// RunWithInput runs gh with args as Run does, with Run's limits, and feeds it input on stdin: a
// body too large or too free-form to pass as arguments, such as a review sent with --input -. On
// a failure what gh printed is returned beside the error: gh api prints GitHub's answer, whose
// errors say why it refused, while the first line of stderr (all Message keeps) may say only
// the status.
func RunWithInput(ctx context.Context, args []string, input string) (string, error) {
	result := run.Cmd(ctx, "gh", args, run.Opts{
		Dir: "", Timeout: timeout, MaxOutputBytes: maxOutputBytes, Stdin: input, Env: nil,
	})
	if result.Err == nil {
		return result.Stdout, nil
	}

	return result.Stdout, errorOf(result)
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

	return result.Stdout, errorOf(result)
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

	return result.Stdout, errorOf(result)
}

// Capped is Run with its time limit, but with maxOutputBytes as the cap on what gh prints: for a
// read whose answer is refused past a size, so no more than that is ever held. An answer past
// the cap fails with an *Error whose Overflowed is set.
func Capped(maxOutputBytes int) func(ctx context.Context, args []string) (string, error) {
	if maxOutputBytes <= 0 {
		panic("gh: Capped needs a cap above zero")
	}

	return func(ctx context.Context, args []string) (string, error) {
		result := run.Cmd(ctx, "gh", args, run.Opts{
			Dir: "", Timeout: timeout, MaxOutputBytes: maxOutputBytes, Stdin: "", Env: nil,
		})
		if result.Err == nil {
			return result.Stdout, nil
		}

		return "", errorOf(result)
	}
}

// errorOf is the *Error of a failed run: its message, the HTTP status the message names, and
// whether the output passed its cap.
func errorOf(result run.Result) *Error {
	message := Message(result)
	status := 0

	match := httpStatus.FindStringSubmatch(message)
	if match != nil {
		// The pattern takes three digits, which always parse.
		status, _ = strconv.Atoi(match[1])
	}

	return &Error{
		Message: message, Status: status, Overflowed: result.Overflowed != "", Err: result.Err,
	}
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
