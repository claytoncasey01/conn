package myprs_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/myprs"
)

// The gh writes. The recorded answers (section writes) cover the argv of the
// ask and of a resolve, gh's failures and the answers that are not a confirmation; here are the
// refusal lists, the argv of a reply with shell syntax in it and the queries' shape. Rows that
// are not a string or a number (42 as a repo, null, undefined, '7' as a number) cannot be
// written: the types refuse them.

const eThreadID = "PRRT_kwDOabc123"

// eExec is a fake gh that records each call and answers out.
type eExec struct {
	calls [][]string
	out   string
	err   error
}

func (e *eExec) run(_ context.Context, args []string) (string, error) {
	e.calls = append(e.calls, args)

	return e.out, e.err
}

func eWrites(exec *eExec) myprs.Writes {
	return myprs.Writes{Exec: exec.run, Trigger: trigger}
}

func TestEWritesAreOnlyTheThree(t *testing.T) {
	t.Parallel()

	if reflect.TypeFor[myprs.Writes]().NumMethod() != 3 {
		t.Errorf("Writes has %d methods", reflect.TypeFor[myprs.Writes]().NumMethod())
	}

	exec := &eExec{calls: nil, out: "", err: nil}
	writes := eWrites(exec)

	for _, err := range []error{
		writes.TriggerReview(context.Background(), "org/repo", 7),
		writes.TriggerReview(context.Background(), "Org.x/re-po_1", 12345),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	want := []string{"pr", "comment", "12345", "-R", "Org.x/re-po_1", "--body", trigger}
	if !slices.Equal(exec.calls[1], want) {
		t.Errorf("argv %q", exec.calls[1])
	}

	for _, call := range exec.calls {
		for _, banned := range []string{
			"api", "graphql", "merge", "close", "edit", "review", "delete", "--force", "-f",
		} {
			if slices.Contains(call, banned) {
				t.Errorf("%q in %q", banned, call)
			}
		}
	}
}

func TestETriggerReviewRefusesBeforeGh(t *testing.T) {
	t.Parallel()

	tests := []struct {
		repo    string
		number  float64
		wantErr error
	}{
		{"", 7, myprs.ErrENotRepoName},
		{"org", 7, myprs.ErrENotRepoName},
		{"org/", 7, myprs.ErrENotRepoName},
		{"/repo", 7, myprs.ErrENotRepoName},
		{"-x/repo", 7, myprs.ErrENotRepoName},
		{"org/repo extra", 7, myprs.ErrENotRepoName},
		{"org/repo;ls", 7, myprs.ErrENotRepoName},
		{"a/b/c", 7, myprs.ErrENotRepoName},
		{"org/re\npo", 7, myprs.ErrENotRepoName},
		{"--help/x", 7, myprs.ErrENotRepoName},
		{"org/repo", 0, myprs.ErrENotNumber},
		{"org/repo", -1, myprs.ErrENotNumber},
		{"org/repo", 1.5, myprs.ErrENotNumber},
		{"org/repo", math.NaN(), myprs.ErrENotNumber},
		{"org/repo", math.Inf(1), myprs.ErrENotNumber},
		{"org/repo", 1 << 53, myprs.ErrENotNumber},
	}

	for _, tt := range tests {
		t.Run(tt.repo, func(t *testing.T) {
			t.Parallel()

			exec := &eExec{calls: nil, out: "", err: nil}

			err := eWrites(exec).TriggerReview(context.Background(), tt.repo, tt.number)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("%q %v: got %v", tt.repo, tt.number, err)
			}

			if len(exec.calls) != 0 {
				t.Errorf("gh ran: %q", exec.calls)
			}
		})
	}
}

func TestEReplyArgvCarriesTheTextAsOneVariable(t *testing.T) {
	t.Parallel()

	body := "Fixed in abc.\n\nThanks $(rm -rf ~) \"quoted\" `x`"
	exec := &eExec{calls: nil, out: `{"data":{"addPullRequestReviewThreadReply":` +
		`{"comment":{"id":"PRRC_1"}}}}`, err: nil}

	err := eWrites(exec).ReplyToThread(context.Background(), eThreadID, body)
	if err != nil {
		t.Fatal(err)
	}

	if len(exec.calls) != 1 {
		t.Fatalf("calls %q", exec.calls)
	}

	want := []string{
		"api", "graphql", "-f", "query=" + myprs.ReplyQuery, "-f", "threadId=" + eThreadID,
		"-f", "body=" + body,
	}
	if !slices.Equal(exec.calls[0], want) {
		t.Errorf("argv %q", exec.calls[0])
	}
}

func TestEThreadQueriesAreConstants(t *testing.T) {
	t.Parallel()

	reply := regexp.MustCompile(`^mutation\(\$threadId: ID!, \$body: String!\) \{ ` +
		`addPullRequestReviewThreadReply\(input: \{ pullRequestReviewThreadId: \$threadId, ` +
		`body: \$body \}\)`)
	resolve := regexp.MustCompile(`^mutation\(\$threadId: ID!\) \{ resolveReviewThread\(` +
		`input: \{ threadId: \$threadId \}\)`)

	if !reply.MatchString(myprs.ReplyQuery) {
		t.Errorf("reply query %q", myprs.ReplyQuery)
	}

	if !resolve.MatchString(myprs.ResolveQuery) {
		t.Errorf("resolve query %q", myprs.ResolveQuery)
	}

	if strings.Contains(myprs.ReplyQuery, "resolveReviewThread") {
		t.Error("the reply query resolves")
	}

	if strings.Contains(myprs.ResolveQuery, "addPullRequestReviewThreadReply") {
		t.Error("the resolve query replies")
	}

	exec := &eExec{calls: nil, out: `{"data":{"addPullRequestReviewThreadReply":{},` +
		`"resolveReviewThread":{}}}`, err: nil}
	writes := eWrites(exec)
	ctx := context.Background()

	for _, err := range []error{
		writes.ReplyToThread(ctx, eThreadID, "one"),
		writes.ReplyToThread(ctx, "PRRT_other_id_9", "two $threadId body"),
		writes.ResolveThread(ctx, "PRRT_other_id_9"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, call := range exec.calls {
		eCheckThreadCall(t, call)
	}
}

func eCheckThreadCall(t *testing.T, call []string) {
	t.Helper()

	query := call[3]
	if query != "query="+myprs.ReplyQuery {
		if query != "query="+myprs.ResolveQuery {
			t.Errorf("query %q", query)
		}
	}

	for _, value := range []string{"one", "two", "PRRT_"} {
		if strings.Contains(query, value) {
			t.Errorf("the query holds %q", value)
		}
	}

	for _, banned := range []string{"-F", "--method", "--input"} {
		if slices.Contains(call, banned) {
			t.Errorf("%q in %q", banned, call)
		}
	}
}

func TestEThreadWritesRefuseBeforeGh(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, id, text string
		wantErr        error
	}{
		{"empty id", "", "ok", myprs.ErrENotThreadID},
		{"short id", "short", "ok", myprs.ErrENotThreadID},
		{"id with a space", "PRRT_kwDO abc", "ok", myprs.ErrENotThreadID},
		{"id with a newline", "PRRT_kwDO\nabc", "ok", myprs.ErrENotThreadID},
		{"id with a substitution", "PRRT_$(x)xx", "ok", myprs.ErrENotThreadID},
		{"long id", strings.Repeat("a", 201), "ok", myprs.ErrENotThreadID},
		{"id that is a flag", "-f threadId=x", "ok", myprs.ErrENotThreadID},
		{"id with a quote", "PRRT_kwDO\"abc", "ok", myprs.ErrENotThreadID},
		{"empty text", eThreadID, "", myprs.ErrENotReplyText},
		{"long text", eThreadID, strings.Repeat("a", 2001), myprs.ErrENotReplyText},
		{"NUL in the text", eThreadID, "a\x00b", myprs.ErrENotReplyText},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exec := &eExec{calls: nil, out: "", err: nil}
			writes := eWrites(exec)

			err := writes.ReplyToThread(context.Background(), tt.id, tt.text)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("reply: got %v", err)
			}

			if errors.Is(tt.wantErr, myprs.ErrENotThreadID) {
				err = writes.ResolveThread(context.Background(), tt.id)
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("resolve: got %v", err)
				}
			}

			if len(exec.calls) != 0 {
				t.Errorf("gh ran: %q", exec.calls)
			}
		})
	}

	t.Run("the longest id and text are taken", func(t *testing.T) {
		t.Parallel()

		exec := &eExec{
			calls: nil, out: `{"data":{"addPullRequestReviewThreadReply":{}}}`,
			err: nil,
		}

		err := eWrites(exec).ReplyToThread(context.Background(), strings.Repeat("a", 200),
			strings.Repeat("x", 2000))
		if err != nil {
			t.Fatal(err)
		}

		if len(exec.calls) != 1 {
			t.Errorf("calls %d", len(exec.calls))
		}
	})
}

func TestEThreadWriteFailures(t *testing.T) {
	t.Parallel()

	errorsAnswer := `{"data":null,"errors":[{"message":"Could not resolve to a node"}]}`
	tests := []struct {
		name, out string
		err       error
		reply     bool
		want      string
	}{
		{"gh fails on a reply", "", &myprs.FailureError{Message: "gh: HTTP 403"}, true, "HTTP 403"},
		{
			"gh fails on a resolve", "", &myprs.FailureError{Message: "gh: HTTP 502"}, false,
			"HTTP 502",
		},
		{"errors on a reply", errorsAnswer, nil, true, "Could not resolve"},
		{"errors on a resolve", errorsAnswer, nil, false, "Could not resolve"},
		{
			"no result", `{"data":{"addPullRequestReviewThreadReply":null}}`, nil, true,
			"did not confirm",
		},
		{"not JSON", "not json", nil, false, "did not confirm"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			writes := eWrites(&eExec{calls: nil, out: tt.out, err: tt.err})

			var err error
			if tt.reply {
				err = writes.ReplyToThread(context.Background(), eThreadID, "x")
			} else {
				err = writes.ResolveThread(context.Background(), eThreadID)
			}

			if err == nil {
				t.Fatal("no error")
			}

			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v", err)
			}
		})
	}
}
