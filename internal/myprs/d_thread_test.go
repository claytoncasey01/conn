package myprs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// The port of myPrsThread.test.ts: the thread click (reply, then resolve) through the real
// myprs.Gh and myprs.Writes over a fake gh that records every call, so what is checked is the
// exact command line. Nothing here reaches the real gh, cmux, git or ~/.conn.

const (
	dTID   = "PRRT_kwDOabc123"
	dOther = "PRRT_kwDOother99"
	dKey   = "org/repo#7"
	// dGuardPastMs moves the clock past the 5 s click guard.
	dGuardPastMs = 6000
	dMinuteMs    = 60_000
	dWaitLimit   = 10 * time.Second
	dNoProposal  = "<none>"
)

// dVariable is what every argument after the query is: a name=value variable.
var dVariable = regexp.MustCompile(`^(id|threadId|body)=`)

// dForbidden is the 403 with its bidi control dropped (one or two spaces left where it was).
var dForbidden = regexp.MustCompile(`HTTP 403 {1,2}forbidden`)

// dThreadOpts are what a thread rig starts from.
type dThreadOpts struct {
	file      string
	prs       []*myprs.PR
	proposals []*myprs.Proposal
	me        string
	dry       bool
}

// dThreadRig is Bun's rig(): My PRs over the world, with the real gh client and writes over a
// fake gh. Its fields after mu are guarded by mu: the fake runs on My PRs' goroutines.
type dThreadRig struct {
	t     *testing.T
	world *World
	m     *myprs.MyPrs
	file  string

	mu    sync.Mutex
	calls [][]string
	prs   []*myprs.PR
	// info is the thread as the fake GitHub has it.
	info        map[string]any
	failInfo    error
	failReply   error
	landOnFail  bool
	failResolve error
	// hold holds the reply back until it is closed; entered says the reply is held.
	hold    chan struct{}
	entered chan struct{}
	// atResolve is what the file said of the proposal each time the resolve was asked.
	atResolve []string
}

// dThreadGh is the real thread read, with the login and the list a fake.
type dThreadGh struct {
	gh  myprs.Gh
	rig *dThreadRig
}

func (g dThreadGh) ThreadInfo(ctx context.Context, threadID string) (*myprs.ThreadInfo, error) {
	info, err := g.gh.ThreadInfo(ctx, threadID)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	return info, nil
}

func (g dThreadGh) Login(context.Context) (string, error) { return "jane", nil }

func (g dThreadGh) PRs(context.Context, string, []string) (myprs.Search, error) {
	g.rig.mu.Lock()
	defer g.rig.mu.Unlock()

	return myprs.Search{PRs: g.rig.prs, Capped: false, Failed: nil, Rate: nil}, nil
}

func dThreadOf(id string, change ...func(one *myprs.Thread)) myprs.Thread {
	named := func(one *myprs.Thread) {
		one.ID = id
		one.Text = "Handle the empty case."
	}

	return thread(append([]func(one *myprs.Thread){named}, change...)...)
}

func dPR(change ...func(pull *myprs.PR)) *myprs.PR {
	threads := func(pull *myprs.PR) {
		pull.Threads = []myprs.Thread{dThreadOf(dTID), dThreadOf(dOther)}
	}

	return pr(append([]func(pull *myprs.PR){threads}, change...)...)
}

func dPull(change map[string]any) map[string]any {
	pull := map[string]any{
		"number": 7, "state": "OPEN", "author": map[string]any{"login": "jane"},
		"repository": map[string]any{"nameWithOwner": "org/repo"},
	}
	maps.Copy(pull, change)

	return pull
}

func dNode(login, body string) map[string]any {
	return map[string]any{"author": map[string]any{"login": login}, "body": body}
}

func dISO(ms float64) string {
	return time.UnixMilli(int64(ms)).UTC().Format("2006-01-02T15:04:05.000Z")
}

func dNodeAt(login, body string, ms float64) map[string]any {
	node := dNode(login, body)
	node["createdAt"] = dISO(ms)

	return node
}

func dInfo() map[string]any {
	return map[string]any{
		"id": dTID, "isResolved": false,
		"comments":    map[string]any{"nodes": []any{dNode("sam", "Handle the empty case.")}},
		"pullRequest": dPull(nil),
	}
}

func dProp(text, state string) *myprs.Proposal {
	return &myprs.Proposal{
		ThreadID: dTID, Text: text, State: state, Error: nil, LaunchAt: 0, TriedAt: nil,
	}
}

func dReplyArgs(text string) []string {
	return []string{
		"api", "graphql", "-f", "query=" + myprs.ReplyQuery, "-f", "threadId=" + dTID,
		"-f", "body=" + text,
	}
}

func dResolveArgs(id string) []string {
	return []string{"api", "graphql", "-f", "query=" + myprs.ResolveQuery, "-f", "threadId=" + id}
}

// dJSON is value as JSON, for comparing.
func dJSON(t *testing.T, value any) string {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return string(data)
}

// dSame fails unless got and want are the same as JSON.
func dSame(t *testing.T, what string, got, want any) {
	t.Helper()

	if dJSON(t, got) != dJSON(t, want) {
		t.Errorf("%s: got %s, want %s", what, dJSON(t, got), dJSON(t, want))
	}
}

// dBody is the reply's body as JSON.stringify writes it.
func dBody(res reviews.Reply) string { return js.Stringify(res.Body) }

func dField(res reviews.Reply, key string) any {
	if res.Body == nil {
		return nil
	}

	found, _ := res.Body.Get(key)

	return found
}

func dSeed(t *testing.T, file string, change func(saved *myprs.File)) {
	t.Helper()

	saved := myprs.LoadFile(file)
	change(saved)

	err := myprs.SaveFile(file, saved, nil)
	if err != nil {
		t.Fatalf("seeding %s: %v", file, err)
	}
}

func newDThreadRig(t *testing.T, opts dThreadOpts) *dThreadRig {
	t.Helper()

	file := opts.file
	if file == "" {
		file = filepath.Join(t.TempDir(), "myprs.json")
	}

	if opts.proposals != nil {
		dSeed(t, file, func(saved *myprs.File) {
			myprs.TrackOf(saved, dKey, worldT0).Proposals = opts.proposals
		})
	}

	prs := opts.prs
	if prs == nil {
		prs = []*myprs.PR{dPR()}
	}

	world := newWorld(t)
	world.Scope.Slack = false
	world.Scope.DraftChannel = nil
	rig := &dThreadRig{
		t: t, world: world, file: file, prs: prs, info: dInfo(),
		entered: make(chan struct{}, 1),
	}
	deps := world.Deps(file)
	deps.Gh = dThreadGh{
		gh: myprs.Gh{Exec: rig.exec, Trigger: deps.Config.GreptileTrigger}, rig: rig,
	}
	deps.Writes = myprs.Writes{Exec: rig.exec, Trigger: deps.Config.GreptileTrigger}
	deps.Config.Me = opts.me
	deps.Config.Dry = opts.dry
	rig.m = myprs.New(context.Background(), deps)

	t.Cleanup(rig.checkCalls)
	t.Cleanup(rig.checkNothingElse)

	return rig
}

// rigged is a rig after one poll.
func rigged(t *testing.T, opts dThreadOpts) *dThreadRig {
	t.Helper()

	rig := newDThreadRig(t, opts)
	rig.poll()

	return rig
}

func (r *dThreadRig) poll() {
	r.m.Refresh()
	tick(r.m)
}

func (r *dThreadRig) change(change func(fake *dThreadRig)) {
	r.mu.Lock()
	defer r.mu.Unlock()

	change(r)
}

func (r *dThreadRig) exec(_ context.Context, args []string) (string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string{}, args...))
	r.mu.Unlock()

	query := ""
	if len(args) > 3 {
		query = args[3]
	}

	switch query {
	case "query=" + myprs.ThreadQuery:
		return r.answerThread()
	case "query=" + myprs.ReplyQuery:
		return r.answerReply(args)
	case "query=" + myprs.ResolveQuery:
		return r.answerResolve()
	}

	r.t.Errorf("unexpected gh call: %q", args)

	return "", &myprs.FailureError{Message: "unexpected gh call"}
}

func (r *dThreadRig) answerThread() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.failInfo != nil {
		return "", r.failInfo
	}

	return dJSON(r.t, map[string]any{"data": map[string]any{"node": r.info}}), nil
}

func (r *dThreadRig) answerReply(args []string) (string, error) {
	r.mu.Lock()
	hold := r.hold
	r.mu.Unlock()

	if hold != nil {
		r.entered <- struct{}{}

		<-hold
	}

	now := r.world.now()

	r.mu.Lock()
	defer r.mu.Unlock()

	// GitHub adds the reply as the thread's newest comment, at the time of the write.
	land := func() {
		nodes := dNodesOf(r.info)
		r.info["comments"] = map[string]any{
			"nodes": append(nodes, dNodeAt("jane", strings.TrimPrefix(args[7], "body="), now)),
		}
	}

	if r.failReply != nil {
		if r.landOnFail {
			land()
		}

		return "", r.failReply
	}

	land()

	return `{"data":{"addPullRequestReviewThreadReply":{"comment":{"id":"PRRC_1"}}}}`, nil
}

func dNodesOf(info map[string]any) []any {
	comments, _ := info["comments"].(map[string]any)
	nodes, _ := comments["nodes"].([]any)

	return append([]any{}, nodes...)
}

func (r *dThreadRig) answerResolve() (string, error) {
	state := dNoProposal

	entry, found := myprs.LoadFile(r.file).PRs.Get(dKey)
	if found {
		if len(entry.Proposals) > 0 {
			state = entry.Proposals[0].State
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.atResolve = append(r.atResolve, state)

	if r.failResolve != nil {
		return "", r.failResolve
	}

	return `{"data":{"resolveReviewThread":{"thread":{"id":"` + dTID +
		`","isResolved":true}}}}`, nil
}

// checkCalls is Bun's closing test, for every rig: every gh call was the thread read, the reply
// or the resolve, as a constant query with name=value variables, and nothing else.
func (r *dThreadRig) checkCalls() {
	queries := []string{
		"query=" + myprs.ThreadQuery, "query=" + myprs.ReplyQuery, "query=" + myprs.ResolveQuery,
	}
	bad := []string{
		"pr", "comment", "merge", "close", "edit", "review", "delete", "--force", "--method",
		"-X", "-F", "--input",
	}

	for _, call := range r.allCalls() {
		if len(call) < 4 {
			r.t.Errorf("short gh call %q", call)

			continue
		}

		dSame(r.t, "the call's start", call[:3], []string{"api", "graphql", "-f"})

		if !slices.Contains(queries, call[3]) {
			r.t.Errorf("unknown query in %q", call)
		}

		for index := 4; index < len(call); index += 2 {
			if call[index] != "-f" {
				r.t.Errorf("argument %d of %q is not -f", index, call)
			}

			if index+1 >= len(call) {
				r.t.Errorf("no variable after argument %d of %q", index, call)

				continue
			}

			if !dVariable.MatchString(call[index+1]) {
				r.t.Errorf("argument %d of %q is not a variable", index+1, call)
			}
		}

		for _, word := range bad {
			if slices.Contains(call, word) {
				r.t.Errorf("%q in %q", word, call)
			}
		}
	}
}

// checkNothingElse is Bun's fakes that fail: neither the click nor the poll behind it checks a
// clone's origin, runs git, launches a round or drafts in Slack.
func (r *dThreadRig) checkNothingElse() {
	r.world.Change(func(w *World) {
		dSame(r.t, "origin checks", w.Matched, nil)
		dSame(r.t, "git calls", len(w.GitCalls), 0)
		dSame(r.t, "launches", len(w.Launched), 0)
		dSame(r.t, "drafts", len(w.Drafts), 0)
	})
}

func (r *dThreadRig) allCalls() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([][]string{}, r.calls...)
}

// writes are the mutations only (the thread read is not a write).
func (r *dThreadRig) writes() [][]string {
	out := [][]string{}

	for _, call := range r.allCalls() {
		if len(call) < 4 {
			continue
		}

		switch call[3] {
		case "query=" + myprs.ReplyQuery, "query=" + myprs.ResolveQuery:
			out = append(out, call)
		}
	}

	return out
}

func (r *dThreadRig) proposalOf(threadID string) *myprs.Proposal {
	entry, found := myprs.LoadFile(r.file).PRs.Get(dKey)
	if !found {
		return nil
	}

	for _, proposal := range entry.Proposals {
		if proposal.ThreadID == threadID {
			return proposal
		}
	}

	return nil
}

func (r *dThreadRig) proposal() *myprs.Proposal { return r.proposalOf(dTID) }

func (r *dThreadRig) state() string {
	found := r.proposal()
	if found == nil {
		return dNoProposal
	}

	return found.State
}

func (r *dThreadRig) click(body any) reviews.Reply { return r.clickOn(dTID, body) }

func (r *dThreadRig) clickOn(id string, body any) reviews.Reply {
	return r.m.Thread(context.Background(), "org/repo", 7.0, id, body)
}

// wait moves the clock past the 5 s click guard.
func (r *dThreadRig) wait() { r.world.Advance(dGuardPastMs) }

// lastBy makes the thread's comments one by login, or none for a nil body.
func (r *dThreadRig) lastBy(login string, body *string) {
	r.change(func(fake *dThreadRig) {
		nodes := []any{}
		if body != nil {
			nodes = append(nodes, dNode(login, *body))
		}

		fake.info["comments"] = map[string]any{"nodes": nodes}
	})
}

// held holds the reply: the click runs on its own goroutine, and held answers once the reply is
// waiting. release lets it go and answers the click's reply.
func (r *dThreadRig) held(body string) func() reviews.Reply {
	hold := make(chan struct{})

	r.change(func(fake *dThreadRig) { fake.hold = hold })

	result := make(chan reviews.Reply, 1)

	go func() { result <- r.click(body) }()

	select {
	case <-r.entered:
	case <-time.After(testtime.Scaled(dWaitLimit)):
		r.t.Fatal("the reply was never written")
	}

	return func() reviews.Reply {
		close(hold)

		select {
		case res := <-result:
			return res
		case <-time.After(testtime.Scaled(dWaitLimit)):
			r.t.Fatal("the click never answered")
		}

		return reviews.Reply{Code: 0, Body: nil}
	}
}

func dFailure(message string) error { return &myprs.FailureError{Message: message} }

func TestDThreadRepliesThenResolves(t *testing.T) {
	t.Parallel()

	rig := rigged(t, dThreadOpts{proposals: []*myprs.Proposal{
		dProp("what the session proposed", myprs.StateProposed),
	}})
	res := rig.click("Fixed in abc.\n\nSee the new test.")
	dSame(t, "code", res.Code, 200)
	dSame(t, "body", dBody(res), `{"ok":true}`)
	dSame(t, "writes", rig.writes(), [][]string{
		dReplyArgs("Fixed in abc.\n\nSee the new test."), dResolveArgs(dTID),
	})

	// The read of the thread comes before both writes.
	calls := rig.allCalls()

	queries := make([]string, 0, len(calls))
	for _, call := range calls {
		queries = append(queries, call[3])
	}

	dSame(t, "order", queries, []string{
		"query=" + myprs.ThreadQuery, "query=" + myprs.ReplyQuery, "query=" + myprs.ResolveQuery,
	})
	dSame(t, "proposal", rig.proposal(),
		dProp("Fixed in abc.\n\nSee the new test.", myprs.StatePosted))
}

func TestDThreadPostsTheCleanedBoxText(t *testing.T) {
	t.Parallel()

	rig := rigged(t, dThreadOpts{proposals: []*myprs.Proposal{
		dProp("stored text", myprs.StateProposed),
	}})
	rig.click("line one\r\nline\u202e two\x00\t\n\n\n\nline three  ")

	writes := rig.writes()
	if len(writes) == 0 {
		t.Fatal("nothing written")
	}

	dSame(t, "reply", writes[0], dReplyArgs("line one\nline two \n\nline three"))

	for _, call := range rig.allCalls() {
		for _, arg := range call {
			if strings.Contains(arg, "stored text") {
				t.Errorf("the stored proposal was posted: %q", call)
			}
		}
	}
}

func TestDThreadEmptyTextResolvesOnly(t *testing.T) {
	t.Parallel()

	for _, tt := range []string{"", "   \n\t ", "\u202e\x00"} {
		t.Run(dJSON(t, tt), func(t *testing.T) {
			t.Parallel()

			rig := rigged(t, dThreadOpts{proposals: []*myprs.Proposal{
				dProp("stored text", myprs.StateProposed),
			}})
			res := rig.click(tt)
			dSame(t, "code", res.Code, 200)
			dSame(t, "body", dBody(res), `{"ok":true}`)
			dSame(t, "writes", rig.writes(), [][]string{dResolveArgs(dTID)})
			dSame(t, "state", rig.state(), myprs.StatePosted)
		})
	}
}

func TestDThreadClickMakesTheProposal(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, body string
		writes     [][]string
	}{
		{name: "resolve only", body: "", writes: [][]string{dResolveArgs(dTID)}},
		{
			name: "reply", body: "A reply.",
			writes: [][]string{dReplyArgs("A reply."), dResolveArgs(dTID)},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := rigged(t, dThreadOpts{})
			if rig.proposal() != nil {
				t.Fatal("a proposal before the click")
			}

			dSame(t, "code", rig.click(tt.body).Code, 200)
			dSame(t, "writes", rig.writes(), tt.writes)
			dSame(t, "proposal", rig.proposal(), dProp(tt.body, myprs.StatePosted))
		})
	}
}

func TestDThreadFailedResolveIsRetriedAlone(t *testing.T) {
	t.Parallel()

	rig := rigged(t, dThreadOpts{proposals: []*myprs.Proposal{dProp("p", myprs.StateProposed)}})
	rig.change(func(fake *dThreadRig) { fake.failResolve = dFailure("gh: HTTP 502") })

	first := rig.click("The reply.")
	dSame(t, "code", first.Code, 200)
	dSame(t, "ok", dField(first, "ok"), false)

	why, _ := dField(first, "error").(string)
	if !strings.Contains(why, "HTTP 502") {
		t.Errorf("error %q", why)
	}

	dSame(t, "writes", rig.writes(), [][]string{
		dReplyArgs("The reply."), dResolveArgs(dTID),
	})
	dSame(t, "state", rig.state(), myprs.StateReplied)

	rig.change(func(fake *dThreadRig) { fake.failResolve = nil })
	rig.wait()

	again := rig.click("The reply, edited and not to be posted again")
	dSame(t, "code", again.Code, 200)
	dSame(t, "body", dBody(again), `{"ok":true}`)
	dSame(t, "writes", rig.writes(), [][]string{
		dReplyArgs("The reply."), dResolveArgs(dTID), dResolveArgs(dTID),
	})
	dSame(t, "state", rig.state(), myprs.StatePosted)
	dSame(t, "error", rig.proposal().Error, nil)
}

func TestDThreadRestartDoesNotPostAgain(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "myprs.json")
	first := rigged(t, dThreadOpts{
		file: file, proposals: []*myprs.Proposal{dProp("p", myprs.StateProposed)},
	})
	first.change(func(fake *dThreadRig) { fake.failResolve = dFailure("gh: HTTP 502") })
	first.click("The reply.")
	dSame(t, "first writes", len(first.writes()), 2)

	// The process is gone: a new one reads what the first saved.
	second := rigged(t, dThreadOpts{file: file})
	dSame(t, "state read", second.state(), myprs.StateReplied)

	res := second.click("The reply.")
	dSame(t, "code", res.Code, 200)
	dSame(t, "body", dBody(res), `{"ok":true}`)
	dSame(t, "writes", second.writes(), [][]string{dResolveArgs(dTID)})
	dSame(t, "state", second.state(), myprs.StatePosted)
}

func TestDThreadStoredRepliedBeforeTheResolve(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		// onThread is that the reply is already the thread's last comment.
		onThread bool
	}{
		{name: "posted by the click", onThread: false},
		{name: "found on the thread", onThread: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := rigged(t, dThreadOpts{proposals: []*myprs.Proposal{
				dProp("p", myprs.StateProposed),
			}})
			if tt.onThread {
				rig.lastBy("jane", new("The reply."))
			}

			dSame(t, "code", rig.click("The reply.").Code, 200)
			// At the moment the resolve was asked, the file already said replied.
			rig.change(func(fake *dThreadRig) {
				dSame(t, "at resolve", fake.atResolve, []string{myprs.StateReplied})
			})
		})
	}
}

func TestDThreadFailedReplyIsStoredAndRetried(t *testing.T) {
	t.Parallel()

	rig := rigged(t, dThreadOpts{proposals: []*myprs.Proposal{dProp("p", myprs.StateProposed)}})
	rig.change(func(fake *dThreadRig) {
		fake.failReply = dFailure("gh: HTTP 403 \u202e forbidden")
	})

	res := rig.click("The reply.")
	dSame(t, "code", res.Code, 200)
	dSame(t, "ok", dField(res, "ok"), false)

	why, _ := dField(res, "error").(string)
	if !dForbidden.MatchString(why) {
		t.Errorf("error %q", why)
	}

	dSame(t, "writes", rig.writes(), [][]string{dReplyArgs("The reply.")})

	stored := rig.proposal()
	dSame(t, "state", stored.State, myprs.StateFailed)

	if stored.Error == nil {
		t.Fatal("no error stored")
	}

	if !strings.Contains(*stored.Error, "HTTP 403") {
		t.Errorf("stored error %q", *stored.Error)
	}

	if strings.Contains(*stored.Error, "\u202e") {
		t.Errorf("stored error keeps the bidi control: %q", *stored.Error)
	}

	// A retry after the guard posts it again, because the thread's last comment is not this
	// reply (nothing landed), and resolves.
	rig.change(func(fake *dThreadRig) { fake.failReply = nil })
	rig.wait()
	dSame(t, "retry code", rig.click("The reply.").Code, 200)
	dSame(t, "writes", rig.writes(), [][]string{
		dReplyArgs("The reply."), dReplyArgs("The reply."), dResolveArgs(dTID),
	})
	dSame(t, "state", rig.state(), myprs.StatePosted)
}

func TestDThreadLandedReplyIsNotPostedAgain(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		// botAnswered is that a bot answered the reply, so it is not the last comment.
		botAnswered bool
	}{
		{name: "the reply is the last comment", botAnswered: false},
		{name: "a bot answered it since", botAnswered: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := rigged(t, dThreadOpts{proposals: []*myprs.Proposal{
				dProp("p", myprs.StateProposed),
			}})
			rig.change(func(fake *dThreadRig) {
				fake.failReply = dFailure("gh: timed out")
				fake.landOnFail = true
			})

			first := rig.click("The reply.")
			dSame(t, "ok", dField(first, "ok"), false)
			dSame(t, "state", rig.state(), myprs.StateFailed)
			// The time of the try is saved with the proposal.
			dSame(t, "triedAt", rig.proposal().TriedAt, worldT0)

			if tt.botAnswered {
				rig.change(func(fake *dThreadRig) {
					fake.info["comments"] = map[string]any{"nodes": append(dNodesOf(fake.info),
						dNodeAt("greptile-apps", "Thanks, looks good.", worldT0+30_000))}
				})
			}

			rig.change(func(fake *dThreadRig) { fake.failReply = nil })
			rig.wait()

			res := rig.click("The reply.")
			dSame(t, "body", dBody(res), `{"ok":true}`)
			dSame(t, "writes", rig.writes(), [][]string{
				dReplyArgs("The reply."), dResolveArgs(dTID),
			})
			dSame(t, "proposal", rig.proposal(), dProp("The reply.", myprs.StatePosted))
		})
	}
}

func TestDThreadTryTimeIsSavedBeforeTheReply(t *testing.T) {
	t.Parallel()

	rig := rigged(t, dThreadOpts{proposals: []*myprs.Proposal{dProp("p", myprs.StateProposed)}})
	release := rig.held("The reply.")

	entry, found := myprs.LoadFile(rig.file).PRs.Get(dKey)
	if !found {
		t.Fatal("no entry saved")
	}

	dSame(t, "triedAt", entry.Proposals[0].TriedAt, worldT0)
	dSame(t, "code", release().Code, 200)
}

// dSameText is one row of the check whether the reply is on the thread already.
type dSameText struct {
	name       string
	proposal   *myprs.Proposal
	nodes      []any
	noComments bool
	box        string
	posts      bool
}

func dSameTextRows() []dSameText {
	triedFailed := dProp("p", myprs.StateFailed)
	triedFailed.TriedAt = new(float64(worldT0))
	reply := "The reply."

	return []dSameText{
		{
			name: "mine before the first try", proposal: triedFailed, box: reply, posts: true,
			nodes: []any{
				dNodeAt("jane", reply, worldT0-10*dMinuteMs),
				dNodeAt("sam", "Not quite.", worldT0-5*dMinuteMs),
			},
		},
		{
			name: "no try: only the last counts", proposal: dProp("p", myprs.StateFailed),
			box: reply, posts: true,
			nodes: []any{
				dNodeAt("jane", reply, worldT0), dNodeAt("sam", "Not quite.", worldT0+1000),
			},
		},
		{name: "last by someone else", nodes: []any{dNode("sam", reply)}, box: reply, posts: true},
		{
			name: "mine, other text", nodes: []any{dNode("jane", "Another reply.")}, box: reply,
			posts: true,
		},
		{
			name: "mine, longer", nodes: []any{dNode("jane", "The reply. And more")}, box: reply,
			posts: true,
		},
		{name: "mine, empty", nodes: []any{dNode("jane", "")}, box: reply, posts: true},
		{name: "no comment", nodes: []any{}, box: reply, posts: true},
		{
			name: "unreadable", box: reply, posts: true,
			nodes: []any{map[string]any{"author": nil, "body": reply}, 7},
		},
		{name: "no comments at all", noComments: true, box: reply, posts: true},
		{
			name:     "ingested again after an unresolve",
			proposal: dProp("Already posted.", myprs.StateProposed),
			nodes:    []any{dNode("jane", "Already posted.")}, box: "Already posted.",
			posts: false,
		},
		{
			name: "CRLF and trailing blanks", box: "Line one\n\nLine two", posts: false,
			nodes: []any{dNode("JANE", "Line one\r\n\r\nLine two  \n")},
		},
		{
			name: "outer blanks", box: reply, posts: false,
			nodes: []any{dNode("JANE", "  The reply.\r\n")},
		},
		{
			name: "inner blanks count", box: "The  reply.", posts: true,
			nodes: []any{dNode("JANE", reply)},
		},
		{
			name: "case counts", box: reply, posts: true,
			nodes: []any{dNode("JANE", "the reply.")},
		},
	}
}

func TestDThreadReplyAlreadyOnTheThread(t *testing.T) {
	t.Parallel()

	for _, tt := range dSameTextRows() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			proposal := tt.proposal
			if proposal == nil {
				proposal = dProp("p", myprs.StateProposed)
			}

			rig := rigged(t, dThreadOpts{proposals: []*myprs.Proposal{proposal}})
			rig.change(func(fake *dThreadRig) {
				fake.info["comments"] = map[string]any{"nodes": tt.nodes}
				if tt.noComments {
					delete(fake.info, "comments")
				}
			})

			dSame(t, "code", rig.click(tt.box).Code, 200)

			want := [][]string{dResolveArgs(dTID)}
			if tt.posts {
				want = [][]string{dReplyArgs(tt.box), dResolveArgs(dTID)}
			}

			dSame(t, "writes", rig.writes(), want)
			dSame(t, "state", rig.state(), myprs.StatePosted)
		})
	}
}

func TestDThreadNotAnUnresolvedThreadOfThePR(t *testing.T) {
	t.Parallel()

	for _, tt := range []string{"PRRT_resolved1", "PRRT_both1", "PRRT_notlisted1"} {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()

			rig := rigged(t, dThreadOpts{prs: []*myprs.PR{pr(func(pull *myprs.PR) {
				pull.Threads = []myprs.Thread{
					dThreadOf("PRRT_resolved1", func(one *myprs.Thread) { one.Resolved = true }),
					dThreadOf("PRRT_both1", func(one *myprs.Thread) {
						one.Resolved = true
						one.Outdated = true
					}),
					dThreadOf(dTID),
				}
			})}})
			res := rig.clickOn(tt, "x")
			dSame(t, "code", res.Code, 409)
			dSame(t, "body", dBody(res),
				`{"error":"that thread is not an unresolved thread of this PR"}`)
			dSame(t, "calls", rig.allCalls(), [][]string{})
		})
	}
}

func TestDThreadOutdatedUnresolvedThreadIsHandled(t *testing.T) {
	t.Parallel()

	rig := rigged(t, dThreadOpts{
		prs: []*myprs.PR{pr(func(pull *myprs.PR) {
			pull.Threads = []myprs.Thread{
				dThreadOf(dTID, func(one *myprs.Thread) { one.Outdated = true }),
			}
		})},
		proposals: []*myprs.Proposal{dProp("Fixed in abc1234.", myprs.StateProposed)},
	})
	res := rig.click("Fixed in abc1234.")
	dSame(t, "body", dBody(res), `{"ok":true}`)
	dSame(t, "writes", rig.writes(), [][]string{
		dReplyArgs("Fixed in abc1234."), dResolveArgs(dTID),
	})
	dSame(t, "state", rig.state(), myprs.StatePosted)
}

func TestDThreadPRNotListedOrFork(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		repo   string
		number float64
		code   int
		rig    func(t *testing.T) *dThreadRig
	}{
		{
			name: "fork", repo: "org/repo", number: 7, code: 409,
			rig: func(t *testing.T) *dThreadRig {
				t.Helper()

				return rigged(t, dThreadOpts{prs: []*myprs.PR{
					dPR(func(pull *myprs.PR) { pull.Fork = true }),
				}})
			},
		},
		{name: "other number", repo: "org/repo", number: 8, code: 404, rig: dRiggedPlain},
		{name: "other repo", repo: "org/other", number: 7, code: 404, rig: dRiggedPlain},
		{
			name: "before any poll", repo: "org/repo", number: 7, code: 404,
			rig: func(t *testing.T) *dThreadRig {
				t.Helper()

				return newDThreadRig(t, dThreadOpts{})
			},
		},
		{
			name: "left the open list", repo: "org/repo", number: 7, code: 404,
			rig: func(t *testing.T) *dThreadRig {
				t.Helper()

				rig := rigged(t, dThreadOpts{})
				rig.change(func(fake *dThreadRig) { fake.prs = []*myprs.PR{} })
				rig.poll()

				return rig
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := tt.rig(t)
			res := rig.m.Thread(context.Background(), tt.repo, tt.number, dTID, "x")
			dSame(t, "code", res.Code, tt.code)
			dSame(t, "calls", rig.allCalls(), [][]string{})
		})
	}
}

func dRiggedPlain(t *testing.T) *dThreadRig {
	t.Helper()

	return rigged(t, dThreadOpts{})
}

func TestDThreadFreshReadMismatchWritesNothing(t *testing.T) {
	t.Parallel()

	open := func(change map[string]any) map[string]any {
		return map[string]any{"isResolved": false, "pullRequest": dPull(change)}
	}

	for _, tt := range []struct {
		name string
		code int
		info map[string]any
		fail error
	}{
		{
			name: "other repo", code: 409,
			info: open(map[string]any{
				"repository": map[string]any{"nameWithOwner": "org/other"},
			}),
		},
		{name: "other PR number", code: 409, info: open(map[string]any{"number": 8})},
		{name: "PR closed", code: 409, info: open(map[string]any{"state": "CLOSED"})},
		{name: "PR merged", code: 409, info: open(map[string]any{"state": "MERGED"})},
		{
			name: "someone else's PR", code: 409,
			info: open(map[string]any{"author": map[string]any{"login": "sam"}}),
		},
		{name: "author gone", code: 409, info: open(map[string]any{"author": nil})},
		{
			name: "thread resolved", code: 409,
			info: map[string]any{"isResolved": true, "pullRequest": dPull(nil)},
		},
		{name: "gh read fails", code: 502, fail: dFailure("gh: HTTP 502")},
		{name: "not a thread", code: 502, info: map[string]any{"notAThread": true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := rigged(t, dThreadOpts{proposals: []*myprs.Proposal{
				dProp("p", myprs.StateProposed),
			}})
			rig.change(func(fake *dThreadRig) {
				if tt.info != nil {
					fake.info = tt.info
				}

				fake.failInfo = tt.fail
			})

			res := rig.click("A reply.")
			dSame(t, "code", res.Code, tt.code)

			if _, isText := dField(res, "error").(string); !isText {
				t.Errorf("no error text in %s", dBody(res))
			}

			dSame(t, "writes", rig.writes(), [][]string{})
			dSame(t, "calls", len(rig.allCalls()), 1)
			dSame(t, "state", rig.state(), myprs.StateProposed)
		})
	}
}

func TestDThreadLoginIsConfiguredOrGhs(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		me   string
		info map[string]any
		code int
	}{
		{
			name: "gh's login, without regard to case", me: "", code: 200,
			info: map[string]any{
				"id": dTID, "isResolved": false, "pullRequest": dPull(map[string]any{
					"author":     map[string]any{"login": "JANE"},
					"repository": map[string]any{"nameWithOwner": "Org/Repo"},
				}),
			},
		},
		{name: "CONN_REVIEW_ME set to someone else", me: "someone-else", code: 409},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := rigged(t, dThreadOpts{me: tt.me})
			if tt.info != nil {
				rig.change(func(fake *dThreadRig) { fake.info = tt.info })
			}

			dSame(t, "code", rig.click("").Code, tt.code)

			if tt.code != 200 {
				dSame(t, "writes", rig.writes(), [][]string{})
			}
		})
	}
}

func TestDThreadBadInputs(t *testing.T) {
	t.Parallel()

	object := js.NewObject()
	object.Set("a", 1.0)

	for index, tt := range [][4]any{
		{"nope", 7.0, dTID, "x"},
		{42.0, 7.0, dTID, "x"},
		{nil, 7.0, dTID, "x"},
		{"org/repo", "7", dTID, "x"},
		{"org/repo", 0.0, dTID, "x"},
		{"org/repo", 1.5, dTID, "x"},
		{"org/repo", float64(1 << 53), dTID, "x"},
		{"org/repo", 7.0, "", "x"},
		{"org/repo", 7.0, "short", "x"},
		{"org/repo", 7.0, "PRRT_kwDO abc", "x"},
		{"org/repo", 7.0, strings.Repeat("a", 201), "x"},
		{"org/repo", 7.0, 42.0, "x"},
		{"org/repo", 7.0, dTID, 42.0},
		{"org/repo", 7.0, dTID, nil},
		{"org/repo", 7.0, dTID, object},
		{"org/repo", 7.0, dTID, strings.Repeat("a", 2001)},
	} {
		t.Run(dJSON(t, index), func(t *testing.T) {
			t.Parallel()

			rig := rigged(t, dThreadOpts{})
			res := rig.m.Thread(context.Background(), tt[0], tt[1], tt[2], tt[3])
			dSame(t, "code", res.Code, 400)
			dSame(t, "calls", rig.allCalls(), [][]string{})
		})
	}
}

func TestDThreadTextOver2000(t *testing.T) {
	t.Parallel()

	rig := rigged(t, dThreadOpts{})
	dSame(t, "over", rig.click(strings.Repeat("a", 2001)).Code, 400)
	dSame(t, "at the limit once cleaned",
		rig.click("\u202e"+strings.Repeat("a", 2000)+"\x00").Code, 200)

	writes := rig.writes()
	if len(writes) == 0 {
		t.Fatal("nothing written")
	}

	dSame(t, "body length", len(writes[0][7]), len("body=")+2000)
	dSame(t, "calls", len(rig.allCalls()), 3)
}

func TestDThreadTwoClicksWithinTheGuardWriteOnce(t *testing.T) {
	t.Parallel()

	rig := rigged(t, dThreadOpts{})
	dSame(t, "first", rig.click("").Code, 200)
	dSame(t, "second", rig.click("").Code, 409)
	dSame(t, "with text", rig.click("A reply.").Code, 409)
	dSame(t, "writes", rig.writes(), [][]string{dResolveArgs(dTID)})
	// Another thread is its own click.
	dSame(t, "other thread", rig.clickOn(dOther, "").Code, 200)
	dSame(t, "writes", rig.writes(), [][]string{dResolveArgs(dTID), dResolveArgs(dOther)})
}

func TestDThreadClickInFlightIsRefused(t *testing.T) {
	t.Parallel()

	rig := rigged(t, dThreadOpts{})
	release := rig.held("A reply.")
	// The 5 s are over, so only the click still in flight is what refuses this one.
	rig.wait()
	dSame(t, "in flight", rig.click("A reply.").Code, 409)
	dSame(t, "first", release().Code, 200)
	dSame(t, "writes", rig.writes(), [][]string{
		dReplyArgs("A reply."), dResolveArgs(dTID),
	})
	// After 5 s the click is allowed again.
	rig.change(func(fake *dThreadRig) { fake.hold = nil })
	rig.wait()
	dSame(t, "later", rig.click("").Code, 200)
}

func TestDThreadGuardIsPerThread(t *testing.T) {
	t.Parallel()

	rig := rigged(t, dThreadOpts{})
	rig.change(func(fake *dThreadRig) { fake.failResolve = dFailure("gh: down") })
	dSame(t, "failed", dField(rig.click(""), "ok"), false)

	// A different thread of the same PR is not held back.
	rig.change(func(fake *dThreadRig) {
		fake.info = map[string]any{"id": dOther, "isResolved": false, "pullRequest": dPull(nil)}
		fake.failResolve = nil
	})
	dSame(t, "other thread", rig.clickOn(dOther, "").Code, 200)
	dSame(t, "same thread", rig.click("").Code, 409)
	rig.wait()
	dSame(t, "after the guard", rig.click("").Code, 200)
}

func TestDThreadDryRunWritesNothing(t *testing.T) {
	t.Parallel()

	rig := newDThreadRig(t, dThreadOpts{dry: true})
	res := rig.click("A reply.")
	dSame(t, "code", res.Code, 200)
	dSame(t, "body", dBody(res),
		`{"ok":false,"error":"dry run (CONN_MYPRS_DRY): nothing was posted or resolved"}`)
	// Inputs are still checked.
	dSame(t, "bad input", rig.click(42.0).Code, 400)
	dSame(t, "calls", rig.allCalls(), [][]string{})

	if rig.proposal() != nil {
		t.Error("a proposal was stored")
	}
}

func TestDThreadIDAndTextOnlyInVariables(t *testing.T) {
	t.Parallel()

	rig := rigged(t, dThreadOpts{proposals: []*myprs.Proposal{dProp("p", myprs.StateProposed)}})
	rig.click("Unique reply text 7f3a $threadId mutation")

	calls := rig.allCalls()
	dSame(t, "calls", len(calls), 3)

	for _, call := range calls {
		if strings.Contains(call[3], dTID) {
			t.Errorf("the thread id is in the query %q", call[3])
		}

		if strings.Contains(call[3], "Unique reply text") {
			t.Errorf("the text is in the query %q", call[3])
		}
	}
}

func TestDThreadQueriesAreTheKnownOnes(t *testing.T) {
	t.Parallel()

	// The one read query is not a mutation, and only the two known mutations exist.
	if strings.Contains(myprs.ThreadQuery, "mutation") {
		t.Error("the thread read is a mutation")
	}

	for _, query := range []string{myprs.ReplyQuery, myprs.ResolveQuery} {
		if !strings.HasPrefix(query, "mutation(") {
			t.Errorf("not a mutation: %q", query)
		}
	}
}
