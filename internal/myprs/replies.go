package myprs

import "github.com/claytoncasey01/conn/internal/js"

// The reply file a Self-Review session writes (`.conn/replies.json`): what of it Conn believes.
// The file is written by a model and read from a folder Conn does not control: every field is
// checked and the file can never grow a proposal list without limit.

const (
	// MaxReplyUnits is the longest reply text after cleaning, in UTF-16 units; a longer one is
	// dropped whole, never cut.
	MaxReplyUnits = 2000
	// RepliesPerRead is the most entries taken from one read of the file.
	RepliesPerRead = 50
	// MaxRepliesFileBytes is the largest file that is read at all.
	MaxRepliesFileBytes = 256 * 1024
	// ConnFolder is the worktree's folder of Conn's own files.
	ConnFolder = ".conn"
	// RepliesFile is the file in ConnFolder a Self-Review session writes its replies to.
	RepliesFile = "replies.json"
)

// Accepted is an entry of the file that may become a proposal.
type Accepted struct {
	ThreadID string `json:"threadId"`
	Text     string `json:"text"`
}

// Replies are what one read of the file gives: the entries taken, and how many were not.
type Replies struct {
	Accepted []Accepted `json:"accepted"`
	Dropped  int        `json:"dropped"`
}

// AcceptReplies are the entries of `{"threads":[{"threadId","text"}]}` that may become
// proposals: the thread is one of open (the PR's unresolved threads, outdated ones included),
// the text a string of at most MaxReplyUnits units once cleaned with its line breaks kept (an
// empty or all-blank one means resolve only), a thread appears once (the first entry wins) and
// no more than RepliesPerRead are kept. An otherwise good entry for a thread in settled
// (resolved since the file was written) is passed over like a taken one. Dropped counts every
// other entry, so the count for one file stays the same while its threads are resolved.
func AcceptReplies(raw any, open, settled map[string]bool) Replies {
	out := Replies{Accepted: []Accepted{}, Dropped: 0}

	threads, isList := field(raw, "threads").([]any)
	if !isList {
		return out
	}

	seen := map[string]bool{}
	passed := 0

	for _, value := range threads {
		if len(out.Accepted)+passed >= RepliesPerRead {
			break
		}

		threadID, cleaned, ok := replyEntry(value, seen)
		if !ok {
			continue
		}

		if settled[threadID] {
			seen[threadID] = true
			passed++

			continue
		}

		if !open[threadID] {
			continue
		}

		seen[threadID] = true
		out.Accepted = append(out.Accepted, Accepted{ThreadID: threadID, Text: cleaned})
	}

	out.Dropped = len(threads) - len(out.Accepted) - passed

	return out
}

// replyEntry is an entry's thread and cleaned text, when it is an object with a thread not seen
// yet and a text that is not too long.
func replyEntry(value any, seen map[string]bool) (string, string, bool) {
	if object(value) == nil {
		return "", "", false
	}

	threadID, isID := text(field(value, "threadId"))
	body, isText := text(field(value, "text"))

	if !isID {
		return "", "", false
	}

	if seen[threadID] {
		return "", "", false
	}

	if !isText {
		return "", "", false
	}

	cleaned := CleanMultiline(body, noCut)
	if js.Len(cleaned) > MaxReplyUnits {
		return "", "", false
	}

	return threadID, cleaned, true
}
