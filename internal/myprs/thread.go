package myprs

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
)

const (
	textDryThread  = "dry run (CONN_MYPRS_DRY): nothing was posted or resolved"
	textNotPosted  = "the reply is posted, but the thread was not resolved: "
	codeBadGateway = http.StatusBadGateway
)

// threadClick is one click's facts: the PR, the thread, the cleaned reply and its guard.
type threadClick struct {
	repo, key, threadID, body, guard string
	number                           float64
	file                             *File
}

// Thread is POST AND RESOLVE or RESOLVE pressed for one thread: the reply (when there is text),
// then the resolve. Every refusal comes before any write: the inputs, the PR in the list and
// open, the thread an unresolved one of it in the list, a click not too soon after the last, and
// a fresh read of GitHub that says the thread is on this PR (open and mine) and still
// unresolved. The text posted is the box's, cleaned, never the stored proposal. The proposal is
// saved as replied before the resolve, so a failed resolve (or a restart) is retried with the
// resolve alone and a reply is never posted twice. A write GitHub refused is HTTP 200
// {ok:false, error}.
func (m *MyPrs) Thread(ctx context.Context, repo, number, threadID, body any) reviews.Reply {
	click, refusal := threadInputs(repo, number, threadID, body)
	if refusal != nil {
		return *refusal
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// A click with no poll behind it has nothing real to write. Not ok: the panel holds a thread
	// it was told went through until the snapshot shows it posted, which a dry run never does.
	if m.dry() {
		return reply("ok", false, "error", textDryThread)
	}

	if refused := m.threadRefusal(&click); refused != nil {
		return *refused
	}

	m.threadBusy[click.guard] = true
	defer delete(m.threadBusy, click.guard)

	onThread, refused := m.readThread(ctx, click)
	if refused != nil {
		return *refused
	}

	return m.writeThread(ctx, click, onThread)
}

func threadInputs(repo, number, threadID, body any) (threadClick, *reviews.Reply) {
	name, count, refusal := prOf(repo, number)
	if refusal != nil {
		return threadClick{}, refusal
	}

	threadText, isText := threadID.(string)
	if !isText {
		refused := bad(http.StatusBadRequest, "not a thread id")

		return threadClick{}, &refused
	}

	if !ThreadID.MatchString(threadText) {
		refused := bad(http.StatusBadRequest, "not a thread id")

		return threadClick{}, &refused
	}

	raw, isText := body.(string)
	if !isText {
		refused := bad(http.StatusBadRequest, "the reply must be text")

		return threadClick{}, &refused
	}

	cleaned := CleanMultiline(raw, noCut)
	if js.Len(cleaned) > MaxReplyUnits {
		refused := bad(http.StatusBadRequest, "the reply is over "+js.Number(MaxReplyUnits)+
			" characters")

		return threadClick{}, &refused
	}

	key := EntryKey(name, count)

	return threadClick{
		repo: name, key: key, threadID: threadText, body: cleaned, guard: key + ":" + threadText,
		number: count, file: nil,
	}, nil
}

// threadRefusal refuses a click before any read: a reply and a resolve are GitHub writes, so
// they follow the GitHub module's switch and the allowlist.
func (m *MyPrs) threadRefusal(click *threadClick) *reviews.Reply {
	if refused := m.gate(click.repo); refused != "" {
		refusal := bad(http.StatusConflict, refused)

		return &refusal
	}

	pull, found := m.prs.Get(click.key)
	if !found {
		refusal := bad(http.StatusNotFound, "that PR is not in the list, or is not open")

		return &refusal
	}

	if m.file == nil {
		refusal := bad(http.StatusNotFound, "that PR is not in the list, or is not open")

		return &refusal
	}

	click.file = m.file

	if pull.Fork {
		refusal := bad(http.StatusConflict,
			"fork PR: only a PR from a branch of the repo itself is handled here")

		return &refusal
	}

	index := slices.IndexFunc(pull.Threads, func(t Thread) bool { return t.ID == click.threadID })
	if index == -1 {
		refusal := bad(http.StatusConflict, "that thread is not an unresolved thread of this PR")

		return &refusal
	}

	if pull.Threads[index].Resolved {
		refusal := bad(http.StatusConflict, "that thread is not an unresolved thread of this PR")

		return &refusal
	}

	return m.clickedSoon(click)
}

// clickedSoon refuses a second click for the thread within threadGapMs, and notes this one.
func (m *MyPrs) clickedSoon(click *threadClick) *reviews.Reply {
	now := m.now()
	refusal := bad(http.StatusConflict, "this thread was just clicked: give it a few seconds")

	if m.threadBusy[click.guard] {
		return &refusal
	}

	if last, found := m.threadAt[click.guard]; found {
		if now-last < threadGapMs {
			return &refusal
		}
	}

	for guard, at := range m.threadAt {
		if now-at >= threadGapMs {
			delete(m.threadAt, guard)
		}
	}

	m.threadAt[click.guard] = now

	return nil
}

// readThread is GitHub's own word, right before the write (the list may be a poll old): the
// thread must be on this PR, open and mine, and unresolved. It answers whether the reply is
// already on the thread: one posted before an unresolve is its last comment, and one GitHub took
// whose answer was lost is among its newest comments, made since the first try (less the
// clocks' skew). Only my own comment with the same text (line ends and outer blanks aside)
// counts.
func (m *MyPrs) readThread(ctx context.Context, click threadClick) (bool, *reviews.Reply) {
	var (
		login string
		info  *ThreadInfo
		err   error
	)

	if m.me != nil {
		login = *m.me
	}

	m.outside(func() {
		if login == "" {
			login = m.deps.Config.Me
		}

		if login == "" {
			login, err = m.deps.Gh.Login(ctx)
			if err != nil {
				return
			}
		}

		info, err = m.deps.Gh.ThreadInfo(ctx, click.threadID)
	})

	if err != nil {
		refusal := bad(codeBadGateway, "could not read the thread from GitHub: "+message(err))

		return false, &refusal
	}

	if why := threadMismatch(info, click, login); why != "" {
		refusal := bad(http.StatusConflict, why)

		return false, &refusal
	}

	return m.alreadyReplied(info, click, login), nil
}

func threadMismatch(info *ThreadInfo, click threadClick, login string) string {
	if !sameLogin(info.Repo, click.repo) {
		return "that thread is not on this PR"
	}

	if info.Number != click.number {
		return "that thread is not on this PR"
	}

	if info.State != "OPEN" {
		return "that PR is not open any more"
	}

	if info.Author == "" {
		return "that PR is not yours"
	}

	if !sameLogin(info.Author, login) {
		return "that PR is not yours"
	}

	if info.Resolved {
		return "that thread is already resolved"
	}

	return ""
}

func (m *MyPrs) alreadyReplied(info *ThreadInfo, click threadClick, login string) bool {
	body := js.Trim(click.body)
	if body == "" {
		return false
	}

	mine := func(by, text string) bool {
		if !sameLogin(by, login) {
			return false
		}

		return js.Trim(strings.ReplaceAll(text, "\r\n", "\n")) == body
	}

	if info.Last != nil {
		if mine(info.Last.By, info.Last.Body) {
			return true
		}
	}

	triedAt := m.triedAt(click)
	if triedAt == nil {
		return false
	}

	for _, comment := range info.Recent {
		if comment.At == nil {
			continue
		}

		if *comment.At < *triedAt-AskSkewMs {
			continue
		}

		if mine(comment.By, comment.Body) {
			return true
		}
	}

	return false
}

func (m *MyPrs) triedAt(click threadClick) *float64 {
	entry, found := click.file.PRs.Get(click.key)
	if !found {
		return nil
	}

	for _, proposal := range entry.Proposals {
		if proposal.ThreadID == click.threadID {
			return proposal.TriedAt
		}
	}

	return nil
}

// proposal is the thread's proposal, made (stamped 0: a click is no round, so any round's file
// may still propose for this thread) when there is none. It is looked up again after each call
// to GitHub: a poll in between may have replaced the proposals, or pruned the entry.
func (m *MyPrs) proposal(click threadClick) *Proposal {
	entry := TrackOf(click.file, click.key, m.now())

	for _, proposal := range entry.Proposals {
		if proposal.ThreadID == click.threadID {
			return proposal
		}
	}

	made := &Proposal{
		ThreadID: click.threadID, Text: click.body, State: StateProposed, Error: nil,
		LaunchAt: 0, TriedAt: nil,
	}
	entry.Proposals = append(entry.Proposals, made)

	return made
}

func (m *MyPrs) threadFailed(click threadClick, why, state string) reviews.Reply {
	proposal := m.proposal(click)
	if state != "" {
		proposal.State = state
	}

	proposal.Error = &why

	m.save()
	m.bump()

	return reply("ok", false, "error", why)
}

func (m *MyPrs) replied(click threadClick) {
	proposal := m.proposal(click)
	proposal.State = StateReplied
	proposal.Text = click.body
	proposal.Error = nil
	proposal.TriedAt = nil

	m.save()
}

// writeThread posts the reply (unless it is on the thread already, or there is none) and
// resolves the thread. The gate is asked again before each write: GitHub switched off, or the
// repo taken off the list, since the click is a reason to write nothing.
func (m *MyPrs) writeThread(ctx context.Context, click threadClick, onThread bool,
) reviews.Reply {
	if gone := m.gate(click.repo); gone != "" {
		return bad(http.StatusConflict, gone)
	}

	if refused := m.postReply(ctx, click, onThread); refused != nil {
		return *refused
	}

	if gone := m.gate(click.repo); gone != "" {
		if m.proposal(click).State == StateReplied {
			return m.threadFailed(click, textNotPosted+gone, "")
		}

		return m.threadFailed(click, gone, StateFailed)
	}

	var err error

	m.outside(func() { err = m.deps.Writes.ResolveThread(ctx, click.threadID) })

	if err != nil {
		why := message(err)
		if why == "" {
			why = "the resolve failed"
		}

		if m.proposal(click).State == StateReplied {
			return m.threadFailed(click, textNotPosted+why, "")
		}

		return m.threadFailed(click, why, StateFailed)
	}

	proposal := m.proposal(click)
	proposal.State = StatePosted
	proposal.Error = nil

	m.save()
	m.bump()

	return reply("ok", true)
}

// postReply posts the reply when it is due, saving the try before the write: GitHub may take it
// and the answer be lost (or Conn stop), and the next click must find it.
func (m *MyPrs) postReply(ctx context.Context, click threadClick, onThread bool,
) *reviews.Reply {
	if onThread {
		if m.proposal(click).State != StateReplied {
			m.replied(click)

			return nil
		}
	}

	if click.body == "" {
		return nil
	}

	proposal := m.proposal(click)
	if proposal.State == StateReplied {
		return nil
	}

	if proposal.TriedAt == nil {
		now := m.now()
		proposal.TriedAt = &now

		m.save()
	}

	var err error

	m.outside(func() { err = m.deps.Writes.ReplyToThread(ctx, click.threadID, click.body) })

	if err != nil {
		why := message(err)
		if why == "" {
			why = "the reply failed"
		}

		failed := m.threadFailed(click, why, StateFailed)

		return &failed
	}

	m.replied(click)

	return nil
}
