package myprs

// The GitHub writes of My PRs, each its own small function that takes nothing a caller could use
// to say something else: the first asks Greptile for a review, with a constant text; the other
// two reply to a review thread and resolve it, and only ever run on a click. The repo, the
// number and the thread id are checked here again, whoever calls, and the arguments go to gh as
// a list (no shell). The two GraphQL writes have a constant query: the id and the text are
// variables (`-f`, which never reads a file or a number out of its value, as `-F` does), so
// nothing a comment says can change what is run. Anything gh says is only ever the error.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/repos"
)

// ReplyQuery and ResolveQuery are the two thread writes.
const (
	ReplyQuery = "mutation($threadId: ID!, $body: String!) { " +
		"addPullRequestReviewThreadReply(input: { pullRequestReviewThreadId: $threadId, " +
		"body: $body }) { comment { id } } }"
	ResolveQuery = "mutation($threadId: ID!) { resolveReviewThread(input: { threadId: " +
		"$threadId }) { thread { id isResolved } } }"
)

var (
	errNotRepoName     = errors.New("not a repo name")
	errNotNumber       = errors.New("not a PR number")
	errNotReplyText    = errors.New("not a reply text")
	errNotConfirmed    = errors.New("gh did not confirm the write")
	errRefusedTheWrite = errors.New("GitHub refused the write")
)

// maxSafeInteger is Number.MAX_SAFE_INTEGER.
const maxSafeInteger = 1<<53 - 1

// isCount is Number.isSafeInteger(n) and n > 0.
func isCount(number float64) bool {
	if !wholePositive(number) {
		return false
	}

	return number <= maxSafeInteger
}

// Writes are My PRs' GitHub writes over gh (gh.Run, or a fake).
type Writes struct {
	Exec Exec
	// Trigger is the comment that asks Greptile for a review.
	Trigger string
}

// TriggerReview comments the trigger on the PR. It fails before gh is touched when the repo or
// the number is not what GitHub uses, and with gh's error when gh fails.
func (w Writes) TriggerReview(ctx context.Context, repo string, number float64) error {
	if !repos.IsName(repo) {
		return errNotRepoName
	}

	if !isCount(number) {
		return errNotNumber
	}

	_, err := w.Exec(ctx, []string{
		"pr", "comment", js.Number(number), "-R", repo, "--body", w.Trigger,
	})
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	return nil
}

// ReplyToThread posts text as a reply on the review thread. It fails before gh is touched for an
// id or a text that is not what GitHub takes (1 to 2000 units, no NUL).
func (w Writes) ReplyToThread(ctx context.Context, threadID, body string) error {
	if !ThreadID.MatchString(threadID) {
		return errNotThreadID
	}

	if !replyText(body) {
		return errNotReplyText
	}

	return w.mutate(ctx, []string{
		"-f", "query=" + ReplyQuery, "-f", "threadId=" + threadID, "-f", "body=" + body,
	}, "addPullRequestReviewThreadReply")
}

func replyText(body string) bool {
	units := js.Len(body)
	if units < 1 {
		return false
	}

	if units > MaxReplyUnits {
		return false
	}

	return !strings.Contains(body, "\x00")
}

// ResolveThread marks the review thread resolved. It fails before gh is touched for an id that
// is not a node id.
func (w Writes) ResolveThread(ctx context.Context, threadID string) error {
	if !ThreadID.MatchString(threadID) {
		return errNotThreadID
	}

	return w.mutate(ctx, []string{"-f", "query=" + ResolveQuery, "-f", "threadId=" + threadID},
		"resolveReviewThread")
}

// mutate runs one mutation and fails unless GitHub answered with its result: gh exits 0 for
// some GraphQL errors, and an answer that is not the mutation's is not a confirmation.
func (w Writes) mutate(ctx context.Context, args []string, name string) error {
	out, err := w.Exec(ctx, append([]string{ghAPI, ghGraphQL}, args...))
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	answer, err := js.Parse([]byte(out))
	if err != nil {
		return errNotConfirmed
	}

	if errs, _ := field(answer, "errors").([]any); len(errs) > 0 {
		message, _ := text(field(errs[0], "message"))
		if message == "" {
			return errRefusedTheWrite
		}

		return &FailureError{Message: js.Cut(message, maxErrorUnits)}
	}

	switch field(field(answer, "data"), name).(type) {
	case *js.Object, []any:
		return nil
	}

	return errNotConfirmed
}
