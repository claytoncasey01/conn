package reviews

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
)

// TestADraftFileIsUnreadableForEveryReasonAtOnce: draftOf names why a file cannot be read, and a
// readable one has none.
func TestADraftFileIsUnreadableForEveryReasonAtOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		read    bool
		want    []string
	}{
		{"not read", "", false, []string{problemNotRead}},
		{
			"over the cap", strings.Repeat(" ", MaxDraftFileBytes+1), true,
			[]string{"the file is over 512 KiB"},
		},
		{"not JSON", `{"commit":`, true, []string{problemNotJSON}},
		{
			"no commit or verdict", `{"summary":"S."}`, true,
			[]string{"commit: not a 40-character commit sha", "verdict: not approve or comment"},
		},
		{"readable", storedDraftJSON("null", "0", "null"), true, nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, problems := draftOf(test.content, test.read)
			if !slices.Equal(problems, test.want) {
				t.Errorf("problems %q, want %q", problems, test.want)
			}
		})
	}
}

// TestAStoredDraftReadsBackAsItWasStored: a draft whose text was cleaned when it was accepted is
// the same draft once stored and read back, so cleaning again changes nothing.
func TestAStoredDraftReadsBackAsItWasStored(t *testing.T) {
	t.Parallel()

	raw, err := js.Parse([]byte(`{"commit":"` + storedCommit + `",` +
		`"summary":"Sound.  \r\nTwo lines. \r\n","verdict":"comment","findings":[` +
		`{"path":"a.go","line":2,"severity":"P2","body":"Cap it. \r\n\r\n\r\nPlease.  "},` +
		`{"path":"/abs","line":1,"severity":"P3","body":"x"}]}`))
	if err != nil {
		t.Fatal(err)
	}

	draft, problems := AcceptDraft(raw)
	if len(problems) > 0 {
		t.Fatal(problems)
	}

	posted := 9.0
	stored := StoredDraft{
		Draft: draft, Inline: []bool{true}, FileAt: 5, PostedAt: &posted, URL: nil,
		SetAside: nil,
	}

	parsed, err := js.Parse(js.Marshal(stored))
	if err != nil {
		t.Fatal(err)
	}

	if back := asStoredDraft(parsed); !reflect.DeepEqual(back, &stored) {
		t.Errorf("read back %+v, want %+v", back, stored)
	}
}

// storedCommit is a commit as a stored draft names it.
const storedCommit = "0123456789abcdef0123456789abcdef01234567"

// storedDraftJSON is a stored draft with one finding, with fields replaced by what a row sets.
func storedDraftJSON(inline, dropped, setAside string) string {
	return `{"commit":"` + storedCommit + `","summary":"Sound.","verdict":"approve",` +
		`"findings":[{"path":"a.go","line":2,"side":"RIGHT","severity":"P3","body":"Nit."}],` +
		`"dropped":` + dropped + `,"inline":` + inline + `,"fileAt":5,"postedAt":null,` +
		`"url":null,"setAside":` + setAside + `}`
}

// TestAPostedDraftStaysPostedWhenItNoLongerReadsBack: a posted draft whose findings no longer
// pass is kept as posted (when, and its URL), so its file is never taken, and posted, again.
func TestAPostedDraftStaysPostedWhenItNoLongerReadsBack(t *testing.T) {
	t.Parallel()

	parsed, err := js.Parse([]byte(`{"commit":"` + storedCommit + `","summary":"S.",` +
		`"verdict":"comment","findings":[{"path":"/etc","line":1,"severity":"P3","body":"x"}],` +
		`"dropped":0,"inline":null,"fileAt":5,"postedAt":9,"url":"https://x/1",` +
		`"setAside":null}`))
	if err != nil {
		t.Fatal(err)
	}

	stored := asStoredDraft(parsed)
	if stored == nil {
		t.Fatal("the posted draft was dropped")
	}

	if stored.PostedAt == nil || *stored.PostedAt != 9 {
		t.Errorf("postedAt %v, want 9", stored.PostedAt)
	}

	if stored.URL == nil || *stored.URL != "https://x/1" {
		t.Errorf("url %v, want the review's", stored.URL)
	}

	if !posted(&Launch{Draft: stored}) {
		t.Error("the launch does not count as posted, so its file would be taken again")
	}
}

// TestAStoredDraftIsReadBackOnlyWhole: reviews.json is read back strictly, so a stored draft is
// either the one Conn wrote or none, and its placements never fall out of step with its findings.
func TestAStoredDraftIsReadBackOnlyWhole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		text       string
		wantDraft  bool
		wantInline int
	}{
		{"as written", storedDraftJSON("[true]", "2", "null"), true, 1},
		{"set aside", storedDraftJSON("[true]", "0", `"newer"`), true, 1},
		{"no placements", storedDraftJSON("null", "0", "null"), true, 0},
		{"placements out of step", storedDraftJSON("[true,false]", "0", "null"), true, 0},
		{"an unknown reason", storedDraftJSON("[true]", "0", `"bored"`), false, 0},
		{"a count that is not whole", storedDraftJSON("[true]", "1.5", "null"), false, 0},
		{"a bad finding", `{"commit":"` + storedCommit + `","summary":"S.",` +
			`"verdict":"comment","findings":[{"path":"/etc","line":1,"severity":"P3",` +
			`"body":"x"}],"dropped":0,"inline":null,"fileAt":5,"postedAt":null,"url":null,` +
			`"setAside":null}`, false, 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			parsed, err := js.Parse([]byte(test.text))
			if err != nil {
				t.Fatal(err)
			}

			stored := asStoredDraft(parsed)
			if got := stored != nil; got != test.wantDraft {
				t.Fatalf("a draft read: %v, want %v", got, test.wantDraft)
			}

			if stored == nil {
				return
			}

			if len(stored.Inline) != test.wantInline {
				t.Errorf("inline %v, want %d placements", stored.Inline, test.wantInline)
			}
		})
	}
}

// TestAStoredPostedVerdictIsOneConnPosts: the verdict a review was posted as reads back when it
// is one Conn sends, and as not kept otherwise, never failing the draft it is on.
func TestAStoredPostedVerdictIsOneConnPosts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"approve", VerdictApprove, VerdictApprove},
		{"comment", VerdictComment, VerdictComment},
		{"absent", nil, ""},
		{"request changes", "request_changes", ""},
		{"not text", 1.0, ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := ""
			if verdict := postedVerdictOf(test.value); verdict != nil {
				got = *verdict
			}

			if got != test.want {
				t.Errorf("verdict %q, want %q", got, test.want)
			}
		})
	}
}
