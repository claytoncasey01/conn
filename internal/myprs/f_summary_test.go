package myprs_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
)

// The self-review round's summary: read from the replies file, kept on the launch, shown on the
// row.

const (
	// fMaxSummaryUnits is the longest summary kept; the code's own limit is private.
	fMaxSummaryUnits = 8000
	fSummary         = "- Fixed the lost write: the lock was released early.\n" +
		"- Left the retry: not this PR."
)

// fFile is a replies file holding summary and no thread entries.
func fFile(summary string) *string {
	data, err := json.Marshal(summary)
	if err != nil {
		panic("marshalling a summary: " + err.Error())
	}

	return new(`{"threads":[],"summary":` + string(data) + `}`)
}

func TestFAcceptSummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want *string
	}{
		{
			"a string", `{"summary":"- Fixed the race: it lost writes."}`,
			new("- Fixed the race: it lost writes."),
		},
		{"line breaks kept", `{"summary":"a\r\nb\n\n\n\n\nc"}`, new("a\nb\n\nc")},
		{"invisible text removed", "{\"summary\":\"a\\u202eb\\u0007c\\u200bd\"}", new("abcd")},
		{
			"as long as allowed", `{"summary":"` + strings.Repeat("x", fMaxSummaryUnits) + `"}`,
			new(strings.Repeat("x", fMaxSummaryUnits)),
		},
		{
			"one unit too long",
			`{"summary":"` + strings.Repeat("x", fMaxSummaryUnits+1) + `"}`, nil,
		},
		{"missing", `{"threads":[]}`, nil},
		{"null", `{"summary":null}`, nil},
		{"a number", `{"summary":7}`, nil},
		{"a list", `{"summary":["x"]}`, nil},
		{"blank", `{"summary":"  \n\t "}`, nil},
		{"only invisible text", "{\"summary\":\"\\u200b\\u202e\"}", nil},
		{"not an object", `"x"`, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parsed, err := js.Parse([]byte(tt.raw))
			if err != nil {
				t.Fatalf("%s: %v", tt.raw, err)
			}

			got := myprs.AcceptSummary(parsed)
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}

			if got != nil && *got != *tt.want {
				t.Fatalf("got %q, want %q", *got, *tt.want)
			}
		})
	}

	t.Run("undefined", func(t *testing.T) {
		t.Parallel()

		if got := myprs.AcceptSummary(nil); got != nil {
			t.Fatalf("got %q, want nil", *got)
		}
	})
}

func TestFSummaryIsStoredOnTheNewestLaunch(t *testing.T) {
	t.Parallel()

	round := cReplying(t, cReplyOpts{
		file:  fFile(fSummary),
		extra: []func(launch *myprs.Launch){func(*myprs.Launch) {}},
	})
	round.poll(0)

	launches := round.saved()
	cSame(t, "older launch", launches[0].Summary, (*string)(nil))
	cSame(t, "newest launch", launches[1].Summary, new(fSummary))
	cSame(t, "row", round.row().RoundSummary, &myprs.RoundSummary{
		Round: 2, LaunchAt: worldT0 - hourMs, Text: fSummary,
	})
}

func TestFSummaryComesFromTheSameFileAsTheReplies(t *testing.T) {
	t.Parallel()

	round := cReplying(t, cReplyOpts{file: new(
		`{"threads":[{"threadId":"t1","text":"Fixed."}],"summary":"- Fixed t1."}`)})
	round.poll(0)

	cSame(t, "proposals", round.proposals(), []*myprs.Proposal{cProposed("t1", "Fixed.")})
	cSame(t, "summary", round.saved()[0].Summary, new("- Fixed t1."))
}

func TestFAFileWithoutASummaryStillTakesItsRepliesAndShowsNone(t *testing.T) {
	t.Parallel()

	round := cReplying(t, cReplyOpts{})
	round.poll(0)

	cSame(t, "proposals", round.proposals(), []*myprs.Proposal{cProposed("t1",
		"Fixed in the last commit.")})
	cSame(t, "summary", round.saved()[0].Summary, (*string)(nil))
	cSame(t, "row", round.row().RoundSummary, (*myprs.RoundSummary)(nil))
}

func TestFABadSummaryDoesNotCostTheReplies(t *testing.T) {
	t.Parallel()

	round := cReplying(t, cReplyOpts{file: new(
		`{"threads":[{"threadId":"t1","text":"Fixed."}],"summary":7}`)})
	round.poll(0)

	cSame(t, "proposals", round.proposals(), []*myprs.Proposal{cProposed("t1", "Fixed.")})
	cSame(t, "row", round.row().RoundSummary, (*myprs.RoundSummary)(nil))
}

func TestFTheRowKeepsTheLastSummaryWhileTheNewestRoundHasNone(t *testing.T) {
	t.Parallel()

	earlier := func(launch *myprs.Launch) { launch.Summary = new("- Round one.") }
	round := cReplying(t, cReplyOpts{noFile: true, extra: []func(launch *myprs.Launch){earlier}})
	round.poll(0)

	cSame(t, "row", round.row().RoundSummary, &myprs.RoundSummary{
		Round: 1, LaunchAt: worldT0 - 3*hourMs, Text: "- Round one.",
	})
}

func TestFTheNewestRoundsSummaryWins(t *testing.T) {
	t.Parallel()

	earlier := func(launch *myprs.Launch) { launch.Summary = new("- Round one.") }
	round := cReplying(t, cReplyOpts{
		file:  fFile("- Round two."),
		extra: []func(launch *myprs.Launch){earlier},
	})
	round.poll(0)

	cSame(t, "row", round.row().RoundSummary, &myprs.RoundSummary{
		Round: 2, LaunchAt: worldT0 - hourMs, Text: "- Round two.",
	})
}

func TestFARewrittenSummaryReplacesTheStoredOne(t *testing.T) {
	t.Parallel()

	stale := func(launch *myprs.Launch) { launch.Summary = new("- Older text.") }
	round := cReplying(t, cReplyOpts{
		file: fFile("- Newer text."), launch: []func(launch *myprs.Launch){stale},
	})
	round.poll(0)

	cSame(t, "summary", round.saved()[0].Summary, new("- Newer text."))
}

func TestFASummaryOutlivesARestart(t *testing.T) {
	t.Parallel()

	round := cReplying(t, cReplyOpts{file: fFile("- Kept.")})
	round.poll(0)

	_, restarted := cRestart(t, round.file, round.cur)
	tick(restarted)

	rows := restarted.Snapshot().Rows

	if len(rows) == 0 {
		t.Fatal("the snapshot has no row")
	}

	cSame(t, "row", rows[0].RoundSummary, &myprs.RoundSummary{
		Round: 2, LaunchAt: worldT0 - hourMs, Text: "- Kept.",
	})
}
