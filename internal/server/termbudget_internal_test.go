package server

import (
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
)

// spend is one frame offered to a budget: after advancing the clock, size bytes, and whether it
// should be let through.
type spend struct {
	advance time.Duration
	size    int
	want    bool
}

// repeatSpend is count copies of one spend.
func repeatSpend(count int, one spend) []spend {
	spends := make([]spend, count)
	for index := range spends {
		spends[index] = one
	}

	return spends
}

func TestSpendInputKeepsBothRates(t *testing.T) {
	t.Parallel()

	frameMax := inputFrameBytesMax
	framesInBurst := inputBytesBurst / frameMax

	tests := []struct {
		name   string
		spends []spend
	}{
		{"one keystroke", []spend{{0, 1, true}}},
		{
			"the whole byte burst, then one byte more",
			append(repeatSpend(framesInBurst, spend{0, frameMax, true}), spend{0, 1, false}),
		},
		{
			"the whole frame burst, then one frame more",
			append(repeatSpend(inputFramesBurst, spend{0, 1, true}), spend{0, 1, false}),
		},
		{
			"bytes come back at their rate",
			append(repeatSpend(framesInBurst, spend{0, frameMax, true}),
				spend{time.Second / 2, inputBytesPerSecond / 2, true},
				spend{0, 1, false}),
		},
		{
			"frames come back at their rate",
			append(append(repeatSpend(inputFramesBurst, spend{0, 1, true}),
				spend{time.Second, 1, true}),
				append(repeatSpend(inputFramesPerSecond-1, spend{0, 1, true}),
					spend{0, 1, false})...),
		},
		{
			"a long rest refills no more than the burst",
			append(append([]spend{{time.Hour, 0, true}},
				repeatSpend(inputFramesBurst-1, spend{0, 1, true})...), spend{0, 1, false}),
		},
		{
			"a clock that goes back refills nothing",
			append(repeatSpend(inputFramesBurst, spend{0, 1, true}), spend{-time.Hour, 1, false}),
		},
		// A refused frame is not charged: the connection that sent it is closed, and the
		// session's other viewers keep what was left.
		{
			"a refused frame costs nothing",
			append(repeatSpend(framesInBurst-1, spend{0, frameMax, true}),
				spend{0, frameMax + 1, false}, spend{0, frameMax, true}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			now := time.Unix(1_000_000, 0)
			budget := fullBudget(now)

			for index, offered := range tt.spends {
				now = now.Add(offered.advance)

				var allowed bool

				budget, allowed = spendInput(budget, now, offered.size)
				if allowed != offered.want {
					t.Fatalf("frame %d of %d bytes: allowed %v, want %v",
						index, offered.size, allowed, offered.want)
				}
			}
		})
	}
}

// The budget is the session's, not the connection's: two viewers of one session share it, and a
// session's entry is gone once its last viewer has left, so the table never outgrows the
// connections open.
func TestBudgetsAreSharedPerSessionAndForgotten(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_000_000, 0)
	held := newBudgets(func() time.Time { return now })

	first, second := pty.ID("a"), pty.ID("b")

	held.join(first)
	held.join(first)
	held.join(second)

	for range inputFramesBurst / 2 {
		if !held.spend(first, 1) {
			t.Fatal("the first viewer was refused within the burst")
		}
	}

	for range inputFramesBurst / 2 {
		if !held.spend(first, 1) {
			t.Fatal("the second viewer was refused within the burst")
		}
	}

	if held.spend(first, 1) {
		t.Fatal("two viewers of one session spent more than one burst")
	}

	if !held.spend(second, 1) {
		t.Fatal("another session's budget was spent")
	}

	held.leave(first)

	if held.count() != 2 {
		t.Fatalf("%d sessions held after one of two viewers left, want 2", held.count())
	}

	held.leave(first)
	held.leave(second)

	if held.count() != 0 {
		t.Fatalf("%d sessions held after every viewer left, want 0", held.count())
	}
}

// Spending for a session nobody joined is a mistake in the server's code, not a client's.
func TestSpendingWithoutJoiningIsABug(t *testing.T) {
	t.Parallel()

	held := newBudgets(time.Now)

	defer func() {
		if recover() == nil {
			t.Fatal("spend for a session nobody joined did not panic")
		}
	}()

	held.spend(pty.ID("a"), 1)
}
