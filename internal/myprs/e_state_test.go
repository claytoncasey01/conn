package myprs_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/myprs"
)

// The port of myPrsState.test.ts: the cases the recorded answers (rowThreads, threadCounts,
// botReviewed, protectedHead) do not reach: the bounds (they hold at most 5 threads), Bun's
// protected list, a bidi default branch and the exact boundaries.

// eHeads is the protected list of myPrsState.test.ts.
func eHeads() []string { return []string{"main", "master", "develop", "staging", "release/*"} }

func eWithThreads(threads ...myprs.Thread) *myprs.PR {
	return pr(func(p *myprs.PR) { p.Threads = threads })
}

func eThreadAt(id string, at float64, outdated bool) myprs.Thread {
	return thread(func(one *myprs.Thread) {
		one.ID = id
		one.At = at
		one.Outdated = outdated
	})
}

func TestEProtectedHead(t *testing.T) {
	t.Parallel()

	trunk := "trunk"
	defaultMain := "main"
	tests := []struct {
		name, head string
		// defaultBranch nil is a repo with no default branch.
		defaultBranch *string
		heads         []string
		// want is "" for no refusal, else a pattern the reason must match.
		want string
	}{
		{"feature", "feature/a", &defaultMain, eHeads(), ""},
		{"main-fix", "main-fix", &defaultMain, eHeads(), ""},
		{"mainline", "mainline", &defaultMain, eHeads(), ""},
		{"release alone", "release", &defaultMain, eHeads(), ""},
		{"releases/1", "releases/1", &defaultMain, eHeads(), ""},
		{"Develop", "Develop", &defaultMain, eHeads(), ""},
		{"fix/release/1", "fix/release/1", &defaultMain, eHeads(), ""},
		{"main", "main", &trunk, eHeads(), "long-lived"},
		{"master", "master", &trunk, eHeads(), "long-lived"},
		{"develop", "develop", &trunk, eHeads(), "long-lived"},
		{"staging", "staging", &trunk, eHeads(), "long-lived"},
		{"release/1.2", "release/1.2", &trunk, eHeads(), "long-lived"},
		{"release/x/y", "release/x/y", &trunk, eHeads(), "long-lived"},
		{"default", "trunk", &trunk, eHeads(), "default branch"},
		{"no default", "trunk", nil, eHeads(), ""},
		{"no default, listed", "trunk", nil, []string{"trunk"}, "long-lived"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pull := pr(func(p *myprs.PR) {
				p.HeadRef = tt.head
				p.DefaultBranch = tt.defaultBranch
			})
			why := myprs.ProtectedHead(pull, tt.heads)

			if tt.want == "" {
				if why != "" {
					t.Errorf("%s refused: %s", tt.head, why)
				}

				return
			}

			if !strings.Contains(why, tt.head) {
				t.Errorf("the reason does not name %s: %q", tt.head, why)
			}

			if !strings.Contains(why, tt.want) {
				t.Errorf("the reason does not say %s: %q", tt.want, why)
			}

			if tt.want == "long-lived" {
				if strings.Contains(why, "default") {
					t.Errorf("a listed name is called the default: %q", why)
				}
			}
		})
	}
}

func TestEProtectedHeadComparesTheDefaultExactly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, defaultBranch string
		refused             bool
	}{
		{"bidi", "main\u202e", false},
		{"same", "main", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pull := pr(func(p *myprs.PR) {
				p.HeadRef = "main"
				p.DefaultBranch = &tt.defaultBranch
			})

			if (myprs.ProtectedHead(pull, []string{}) != "") != tt.refused {
				t.Errorf("refused is not %v", tt.refused)
			}
		})
	}
}

func TestEBotReviewedBoundaries(t *testing.T) {
	t.Parallel()

	const since = 1000

	edited := func(at float64) *float64 { return &at }
	tests := []struct {
		name     string
		at       float64
		editedAt *float64
		want     bool
	}{
		{"created at since", since, nil, true},
		{"edited after since", 500, edited(1500), true},
		{"edited just before since", 500, edited(999), false},
		{"never edited, before since", 500, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pull := pr(func(p *myprs.PR) {
				p.HeadSha = "head1"
				p.Comments = []myprs.Comment{
					{By: "a-bot", At: tt.at, EditedAt: tt.editedAt, Body: ""},
				}
			})

			if myprs.BotReviewed(pull, "a-bot", since) != tt.want {
				t.Errorf("not %v", tt.want)
			}
		})
	}
}

func TestERowThreadsBounds(t *testing.T) {
	t.Parallel()

	if myprs.RowThreadsMax != 100 {
		t.Errorf("RowThreadsMax %d", myprs.RowThreadsMax)
	}

	t.Run("current ahead of outdated", func(t *testing.T) {
		t.Parallel()

		threads := make([]myprs.Thread, 0, myprs.RowThreadsMax+1)
		for index := range myprs.RowThreadsMax {
			threads = append(threads, eThreadAt("o"+strconv.Itoa(index), float64(index), true))
		}

		threads = append(threads, eThreadAt("now", 10_000, false))
		rows := myprs.RowThreads(eWithThreads(threads...), nil)

		if len(rows) != myprs.RowThreadsMax {
			t.Fatalf("%d rows", len(rows))
		}

		if rows[0].ID != "now" {
			t.Errorf("first %s", rows[0].ID)
		}
	})

	t.Run("bounded", func(t *testing.T) {
		t.Parallel()

		threads := make([]myprs.Thread, 0, myprs.RowThreadsMax+20)
		for index := range myprs.RowThreadsMax + 20 {
			threads = append(threads, eThreadAt("t"+strconv.Itoa(index), float64(index), false))
		}

		rows := myprs.RowThreads(eWithThreads(threads...), nil)

		if len(rows) != myprs.RowThreadsMax {
			t.Fatalf("%d rows", len(rows))
		}

		if rows[0].ID != "t0" {
			t.Errorf("first %s", rows[0].ID)
		}
	})
}

func TestERowThreadsLeaveThePROrder(t *testing.T) {
	t.Parallel()

	pull := eWithThreads(eThreadAt("c", 30, false), eThreadAt("b", 10, false),
		eThreadAt("a", 10, false), eThreadAt("d", 20, false))
	rows := myprs.RowThreads(pull, nil)
	got := make([]string, 0, len(rows))

	for _, row := range rows {
		got = append(got, row.ID)
	}

	if strings.Join(got, ",") != "a,b,d,c" {
		t.Errorf("rows %v", got)
	}

	kept := make([]string, 0, len(pull.Threads))
	for _, one := range pull.Threads {
		kept = append(kept, one.ID)
	}

	if strings.Join(kept, ",") != "c,b,a,d" {
		t.Errorf("the PR's threads were reordered: %v", kept)
	}
}

func TestERowThreadsCleanEveryString(t *testing.T) {
	t.Parallel()

	path := "a\u0000.ts"
	pull := eWithThreads(thread(func(one *myprs.Thread) {
		one.ID = "a"
		one.By = "sa\u202em"
		one.Path = &path
		one.Text = "x\u200fy" + strings.Repeat("z", 1000)
	}))
	why := "bad\u0007" + strings.Repeat("e", 1000)
	rows := myprs.RowThreads(pull, []*myprs.Proposal{{
		ThreadID: "a", Text: "one\r\ntwo\u202e", State: myprs.StateFailed, Error: &why,
		LaunchAt: 0, TriedAt: nil,
	}})
	row := rows[0]

	if row.By != "sam" {
		t.Errorf("by %q", row.By)
	}

	if *row.Path != "a.ts" {
		t.Errorf("path %q", *row.Path)
	}

	if strings.ContainsRune(row.Text, '\u200f') {
		t.Error("the text keeps U+200F")
	}

	if len([]rune(row.Text)) > 400 {
		t.Errorf("the text is %d long", len([]rune(row.Text)))
	}

	if row.Proposal.Text != "one\ntwo" {
		t.Errorf("proposal %q", row.Proposal.Text)
	}

	if strings.ContainsRune(*row.Proposal.Error, '\u0007') {
		t.Error("the error keeps U+0007")
	}

	if len(*row.Proposal.Error) > 300 {
		t.Errorf("the error is %d long", len(*row.Proposal.Error))
	}
}
