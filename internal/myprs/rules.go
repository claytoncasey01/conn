package myprs

// The pure rules behind the My PRs rows: what counts as an open finding, whether a bot has
// looked at the current head, when the Greptile ask is due and when a PR is ready for its Slack
// draft. No I/O and no clock, so every rule is tested with plain values; the poller supplies the
// facts (the sessions, the replies file, the time).

import (
	"cmp"
	"math"
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
)

const (
	// RowThreadsMax is the most threads that go to the page for one PR (GitHub's page of
	// threads is the same size).
	RowThreadsMax = 100
	// errorUnitsMax is where a stored error is cut when shown.
	errorUnitsMax = 300
	// AskGapMs is the least time between two asks (a failed one included), and how long the
	// session must have been quiet.
	AskGapMs = 60_000
	// AskTriesMax is the most Greptile asks per head, the first included. gh gives up on a
	// comment after 30 s whether or not GitHub took it, so a try is never known to have failed:
	// a later list that has a comment of mine made since the try (less AskSkewMs, for the
	// clocks) counts as the ask, and once the tries are used nothing more is posted for that
	// head.
	AskTriesMax = 3
	// AskSkewMs is how far a comment's time may be off the try's, the two clocks apart.
	AskSkewMs = 2 * 60_000
	// DraftRetryMs is how soon a failed Slack draft is tried again for the same head (a new head
	// is drafted at once).
	DraftRetryMs = 15 * 60_000
	// DraftTriesMax is the most attempts per head, the first included. A run can report a
	// failure after the draft was in fact saved, so a retry may duplicate a draft: it is
	// bounded, and the row keeps the text to copy once the attempts are used.
	DraftTriesMax = 3
)

// RowProposal is a thread's proposed reply as the page shows it.
type RowProposal struct {
	Text  string  `json:"text"`
	State string  `json:"state"`
	Error *string `json:"error"`
}

// RowThread is an unresolved thread as the page shows it.
type RowThread struct {
	ID       string       `json:"id"`
	By       string       `json:"by"`
	Path     *string      `json:"path"`
	Line     *float64     `json:"line"`
	Text     string       `json:"text"`
	Outdated bool         `json:"outdated"`
	Proposal *RowProposal `json:"proposal"`
}

// RowThreads are the unresolved threads of a PR, joined with the reply proposed for each: the
// open ones first, then the outdated ones (a later commit changed their line, the usual sign a
// finding was fixed, so they still want a reply and a resolve), each oldest first, so an
// outdated one never pushes an open one past the cap. A proposal for a resolved or unlisted
// thread is not shown. Every string is cleaned again here, the reply with its line breaks kept.
func RowThreads(pull *PR, proposals []*Proposal) []RowThread {
	byID := map[string]*Proposal{}
	for _, proposal := range proposals {
		byID[proposal.ThreadID] = proposal
	}

	open := []Thread{}

	for _, thread := range pull.Threads {
		if !thread.Resolved {
			open = append(open, thread)
		}
	}

	slices.SortStableFunc(open, compareThreads)

	shown := open[:min(len(open), RowThreadsMax)]
	out := make([]RowThread, 0, len(shown))

	for _, thread := range shown {
		out = append(out, rowThread(thread, byID[thread.ID]))
	}

	return out
}

func compareThreads(left, right Thread) int {
	if order := cmp.Compare(boolNumber(left.Outdated), boolNumber(right.Outdated)); order != 0 {
		return order
	}

	if order := cmp.Compare(left.At, right.At); order != 0 {
		return order
	}

	return js.CompareUTF16(left.ID, right.ID)
}

func boolNumber(value bool) int {
	if value {
		return 1
	}

	return 0
}

func rowThread(thread Thread, proposal *Proposal) RowThread {
	var path *string

	if thread.Path != nil {
		cleaned := Clean(*thread.Path, TextUnitsMax)
		path = &cleaned
	}

	row := RowThread{
		ID: thread.ID, By: Clean(thread.By, TextUnitsMax), Path: path, Line: thread.Line,
		Text: Clean(thread.Text, TextUnitsMax), Outdated: thread.Outdated, Proposal: nil,
	}

	if proposal != nil {
		var why *string

		if proposal.Error != nil {
			cleaned := Clean(*proposal.Error, errorUnitsMax)
			why = &cleaned
		}

		row.Proposal = &RowProposal{
			Text: CleanMultiline(proposal.Text, noCut), State: proposal.State, Error: why,
		}
	}

	return row
}

// Counts are a PR's unresolved threads: open ones by who started them, and the outdated ones
// (shown, not counted as open).
type Counts struct {
	OpenBot   int `json:"openBot"`
	OpenHuman int `json:"openHuman"`
	Outdated  int `json:"outdated"`
}

// ThreadCounts are the open threads split by who started them; Outdated is the unresolved ones
// whose line a later commit changed.
func ThreadCounts(pull *PR, bots []string) Counts {
	out := Counts{OpenBot: 0, OpenHuman: 0, Outdated: 0}

	for _, thread := range pull.Threads {
		switch {
		case thread.Resolved:
		case thread.Outdated:
			out.Outdated++
		case slices.Contains(bots, thread.By):
			out.OpenBot++
		default:
			out.OpenHuman++
		}
	}

	return out
}

// BotReviewed is whether bot has reviewed the current head: a review of that commit, or an
// issue comment made or edited at or after since (when Conn asked). A bot may edit its one
// summary comment in place and a commit's date is not its push time, so time is measured from
// the ask, not from the commit.
func BotReviewed(pull *PR, bot string, since float64) bool {
	for _, review := range pull.Reviews {
		if review.By == bot {
			if review.Sha == pull.HeadSha {
				return true
			}
		}
	}

	for _, comment := range pull.Comments {
		if comment.By != bot {
			continue
		}

		edited := 0.0
		if comment.EditedAt != nil {
			edited = *comment.EditedAt
		}

		if math.Max(comment.At, edited) >= since {
			return true
		}
	}

	return false
}

// ProtectedHead is why the PR's branch is not one a Self-Review may push to, or "" when it may:
// the repo's default branch, or a name in heads (a trailing `*` matches by prefix: `release/*`).
// Branch names are compared exactly, as git does. The names in the reason are GitHub's: clean
// them before showing it.
func ProtectedHead(pull *PR, heads []string) string {
	ref := pull.HeadRef
	why := "a self-review pushes its fixes straight onto the PR's branch, so it only runs on a " +
		"feature branch"

	if pull.DefaultBranch != nil {
		if ref == *pull.DefaultBranch {
			return "its branch " + ref + " is the repo's default branch: " + why
		}
	}

	for _, head := range heads {
		if headMatches(ref, head) {
			return "its branch " + ref + " is a long-lived branch (" + head + "): " + why
		}
	}

	return ""
}

func headMatches(ref, head string) bool {
	if prefix, wild := strings.CutSuffix(head, "*"); wild {
		return strings.HasPrefix(ref, prefix)
	}

	return ref == head
}

// RoundFacts are what the poller knows of a launch's session: whether it is gone, idle, waiting
// for input, how long it has been quiet, and the replies file's mtime (nil when absent).
type RoundFacts struct {
	Launch         *Launch
	SessionGone    bool
	SessionIdle    bool
	SessionWaiting bool
	QuietMs        float64
	RepliesAt      *float64
}

// RoundOver is whether the round of a launch is over: its session is gone, or
// `.conn/replies.json` was written after the launch (the session's last step), the session is
// idle and not waiting for input, and it has been quiet for a minute. A session stopped on a
// permission prompt or a question looks idle too, which is why the file, not the idleness, is
// the signal.
func RoundOver(facts RoundFacts) bool {
	if facts.SessionGone {
		return true
	}

	if facts.RepliesAt == nil {
		return false
	}

	for _, ok := range []bool{
		*facts.RepliesAt > facts.Launch.At, facts.SessionIdle, !facts.SessionWaiting,
		facts.QuietMs >= AskGapMs,
	} {
		if !ok {
			return false
		}
	}

	return true
}

// AskFacts are RoundFacts with the PR, the last ask and the time.
type AskFacts struct {
	RoundFacts

	PR        *PR
	LastAskAt float64
	Now       float64
}

// AskDue is whether the Greptile review is to be asked for now. Only for a launch still armed,
// on an open PR that is not a draft, whose head is neither the one the round started from
// (nothing was pushed in it) nor the one already asked for, and only once the round is over.
func AskDue(facts AskFacts) bool {
	launch := facts.Launch
	pull := facts.PR

	if !launch.Armed {
		return false
	}

	if pull.Draft {
		return false
	}

	if pull.HeadSha == launch.HeadSha {
		return false
	}

	if launch.AskedSha != nil {
		if pull.HeadSha == *launch.AskedSha {
			return false
		}
	}

	if facts.Now-facts.LastAskAt < AskGapMs {
		return false
	}

	return RoundOver(facts.RoundFacts)
}

// AskedLaunch is the launch whose ask decides the status and the draft for head: the newest one
// that asked Greptile for it, past any later round that ended with nothing pushed and nothing
// asked. Nil while the newest round is still armed: it may yet push. An older round still armed
// is never asked for, so it does not block.
func AskedLaunch(launches []*Launch, head string) *Launch {
	for index, launch := range slices.Backward(launches) {
		if launch.AskedSha != nil {
			if *launch.AskedSha == head {
				return launch
			}

			return nil
		}

		if launch.Armed {
			if index == len(launches)-1 {
				return nil
			}
		}
	}

	return nil
}

// ReadyFor is whether the PR is ready for a Slack draft: this launch asked for the current head,
// the PR is not a draft and its threads were all read, greptile has reviewed the head, and no
// open thread of a bot in bots remains (a human's does not block). An outdated thread is not
// counted as open.
func ReadyFor(pull *PR, launch *Launch, bots []string, greptile string) bool {
	if launch == nil {
		return false
	}

	if launch.AskedAt == nil {
		return false
	}

	if launch.AskedSha == nil {
		return false
	}

	if *launch.AskedSha != pull.HeadSha {
		return false
	}

	if pull.Draft {
		return false
	}

	if pull.Partial {
		return false
	}

	if !BotReviewed(pull, greptile, *launch.AskedAt) {
		return false
	}

	return ThreadCounts(pull, bots).OpenBot == 0
}
