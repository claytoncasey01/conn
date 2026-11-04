package myprs_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
)

// The port of myPrsReplies.test.ts, case by case: the recorded answers (section replies) reach
// these rules only at random. Bun's hostile getter is not portable: a parsed JSON value has no
// getters.

// eReply is one entry of a replies file, as JSON.
func eReply(threadID, text string) string {
	entry := js.NewObject()
	entry.Set("threadId", threadID)
	entry.Set("text", text)

	return js.Stringify(entry)
}

// eReplies is a replies file holding entries (JSON each).
func eReplies(entries ...string) string {
	return `{"threads":[` + strings.Join(entries, ",") + "]}"
}

// eIDs are t<from> to t<to-1>.
func eIDs(from, to int) []string {
	out := make([]string, 0, to-from)
	for index := from; index < to; index++ {
		out = append(out, "t"+strconv.Itoa(index))
	}

	return out
}

func eSet(names ...string) map[string]bool {
	out := map[string]bool{}
	for _, name := range names {
		out[name] = true
	}

	return out
}

func eAccept(t *testing.T, raw string, open, settled []string) myprs.Replies {
	t.Helper()

	parsed, err := js.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("%s: %v", raw, err)
	}

	return myprs.AcceptReplies(parsed, eSet(open...), eSet(settled...))
}

func TestERepliesAccepted(t *testing.T) {
	t.Parallel()

	open := []string{"t1", "t2", "t3"}
	settledFile := eReplies(eReply("done", "Fixed."), eReply("done", "again"), eReply("t1", "y"),
		eReply("nope", "z"))
	fenced := "Fixed:\n- renamed X\n- added test\n\n```ts\nfoo()\n```"
	tests := []struct {
		name, raw     string
		open, settled []string
		want          string
	}{
		{
			"valid", eReplies(eReply("t1", "Done: renamed it.")), open, nil,
			`{"accepted":[{"threadId":"t1","text":"Done: renamed it."}],"dropped":0}`,
		},
		{
			"empty text is resolve only", eReplies(eReply("t1", "")), open, nil,
			`{"accepted":[{"threadId":"t1","text":""}],"dropped":0}`,
		},
		{
			"not an open thread", eReplies(eReply("other", "x"), `{"threadId":7,"text":"x"}`,
				`{"text":"x"}`, eReply("t2", "y")), open, nil,
			`{"accepted":[{"threadId":"t2","text":"y"}],"dropped":3}`,
		},
		{
			"a repeat", eReplies(eReply("t1", "first"), eReply("t1", "second")), open, nil,
			`{"accepted":[{"threadId":"t1","text":"first"}],"dropped":1}`,
		},
		{
			"bidi and controls", eReplies(eReply("t1", "a\u202eb\u0007c\u200bd\u0085e\u001b[31mf")),
			open, nil, `{"accepted":[{"threadId":"t1","text":"abcde[31mf"}],"dropped":0}`,
		},
		{
			"junk entries", eReplies("null", `"x"`, "3", "[]", "{}"), open, nil,
			`{"accepted":[],"dropped":5}`,
		},
		{
			"a fenced block", eReplies(eReply("t1", fenced)), open, nil,
			`{"accepted":[` + eReply("t1", fenced) + `],"dropped":0}`,
		},
		{
			"CRLF and a run of newlines", eReplies(eReply("t1", "a\r\nb\n\n\n\n\nc")), open, nil,
			`{"accepted":[{"threadId":"t1","text":"a\nb\n\nc"}],"dropped":0}`,
		},
		{
			"separators and C1",
			eReplies(eReply("t1", "a\u202eb\u0007c\u2028d\u200be\u0085f\u009fg")),
			open, nil, `{"accepted":[{"threadId":"t1","text":"abc\ndefg"}],"dropped":0}`,
		},
		{
			"all whitespace", eReplies(eReply("t1", " \n\t\r\n \u200b ")), open, nil,
			`{"accepted":[{"threadId":"t1","text":""}],"dropped":0}`,
		},
		{
			"settled", settledFile, open,
			[]string{"done"},
			`{"accepted":[{"threadId":"t1","text":"y"}],"dropped":2}`,
		},
		{
			"before it was settled", settledFile, append([]string{"done"}, open...), nil,
			`{"accepted":[{"threadId":"done","text":"Fixed."},{"threadId":"t1","text":"y"}],` +
				`"dropped":2}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			eSameJSON(t, tt.name, eAccept(t, tt.raw, tt.open, tt.settled), tt.want)
		})
	}
}

func TestERepliesNothingFromAFileThatIsNotOne(t *testing.T) {
	t.Parallel()

	for _, tt := range []string{
		"null", `"x"`, "7", "true", "[]", `[{"threadId":"t1","text":"x"}]`, "{}",
		`{"threads":null}`, `{"threads":{}}`, `{"threads":"x"}`,
	} {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()

			eSameJSON(t, tt, eAccept(t, tt, []string{"t1"}, nil), `{"accepted":[],"dropped":0}`)
		})
	}

	t.Run("undefined", func(t *testing.T) {
		t.Parallel()

		eSameJSON(t, "nil", myprs.AcceptReplies(nil, eSet("t1"), nil),
			`{"accepted":[],"dropped":0}`)
	})
}

func TestERepliesBounds(t *testing.T) {
	t.Parallel()

	many := func(ids []string) string {
		entries := make([]string, 0, len(ids))
		for _, id := range ids {
			entries = append(entries, eReply(id, "ok"))
		}

		return eReplies(entries...)
	}
	tests := []struct {
		name, raw     string
		open, settled []string
		// lengths are the accepted texts' lengths in UTF-16 units.
		lengths []int
		dropped int
	}{
		{
			"2000 is kept, 2001 and a number are not", eReplies(
				eReply("t1", strings.Repeat("a", 2000)), eReply("t2", strings.Repeat("a", 2001)),
				`{"threadId":"t3","text":5}`),
			[]string{"t1", "t2", "t3"},
			nil,
			[]int{2000},
			2,
		},
		{
			"over 2000 only before cleaning",
			eReplies(eReply("t1", strings.Repeat("a", 2000)+strings.Repeat("\u202e", 10))),
			[]string{"t1"},
			nil,
			[]int{2000},
			0,
		},
		{
			"over 2000 after cleaning, newlines included",
			eReplies(eReply("t1", strings.Repeat("a", 1998)+"\n\n\n\n\n\nb")),
			[]string{"t1"},
			nil,
			[]int{},
			1,
		},
		{
			"2000 after cleaning",
			eReplies(eReply("t1", strings.Repeat("a", 1997)+"\n\n\n\n\n\nb"+
				strings.Repeat("\u200b", 50))),
			[]string{"t1"},
			nil,
			[]int{2000},
			0,
		},
		{"50 of 60", many(eIDs(0, 60)), eIDs(0, 60), nil, eRepeat(2, 50), 10},
		{"50 of 51", many(eIDs(0, 51)), eIDs(0, 51), nil, eRepeat(2, 50), 1},
		{
			"a settled one counts toward the 50", many(eIDs(0, 51)), eIDs(1, 51),
			[]string{"t0"},
			eRepeat(2, 49), 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := eAccept(t, tt.raw, tt.open, tt.settled)
			lengths := make([]int, 0, len(got.Accepted))

			for _, one := range got.Accepted {
				lengths = append(lengths, js.Len(one.Text))
			}

			eSameJSON(t, "lengths", lengths, js.Stringify(tt.lengths))

			if got.Dropped != tt.dropped {
				t.Errorf("dropped %d", got.Dropped)
			}
		})
	}

	t.Run("the first 50 in order", func(t *testing.T) {
		t.Parallel()

		got := eAccept(t, many(eIDs(0, 60)), eIDs(0, 60), nil)
		ids := make([]string, 0, len(got.Accepted))

		for _, one := range got.Accepted {
			ids = append(ids, one.ThreadID)
		}

		if strings.Join(ids, ",") != strings.Join(eIDs(0, 50), ",") {
			t.Errorf("ids %v", ids)
		}
	})
}

// eRepeat is count copies of value.
func eRepeat(value, count int) []int {
	out := make([]int, 0, count)
	for range count {
		out = append(out, value)
	}

	return out
}
