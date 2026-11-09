package server

import (
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
)

// spend is one frame offered to a budget: after advancing the clock, size bytes of input (or a
// resize), and whether it should be let through.
type spend struct {
	advance time.Duration
	size    int
	want    bool
	resize  bool
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

	maxFrame := maxInputFrameBytes
	framesInBurst := inputBytesBurst / maxFrame

	tests := []struct {
		name   string
		spends []spend
	}{
		{"one keystroke", []spend{{0, 1, true, false}}},
		{
			"the whole byte burst, then one byte more",
			append(repeatSpend(framesInBurst, spend{0, maxFrame, true, false}),
				spend{0, 1, false, false}),
		},
		{
			"the whole frame burst, then one frame more",
			append(repeatSpend(inputFramesBurst, spend{0, 1, true, false}),
				spend{0, 1, false, false}),
		},
		{
			"bytes come back at their rate",
			append(repeatSpend(framesInBurst, spend{0, maxFrame, true, false}),
				spend{time.Second / 2, inputBytesPerSecond / 2, true, false},
				spend{0, 1, false, false}),
		},
		{
			"frames come back at their rate",
			append(append(repeatSpend(inputFramesBurst, spend{0, 1, true, false}),
				spend{time.Second, 1, true, false}),
				append(repeatSpend(inputFramesPerSecond-1, spend{0, 1, true, false}),
					spend{0, 1, false, false})...),
		},
		{
			"a long rest refills no more than the burst",
			append(append([]spend{{time.Hour, 0, true, false}},
				repeatSpend(inputFramesBurst-1, spend{0, 1, true, false})...),
				spend{0, 1, false, false}),
		},
		{
			"a clock that goes back refills nothing",
			append(repeatSpend(inputFramesBurst, spend{0, 1, true, false}),
				spend{-time.Hour, 1, false, false}),
		},
		{
			"the whole resize burst, then one resize more",
			append(repeatSpend(inputResizesBurst, spend{0, 0, true, true}),
				spend{0, 0, false, true}),
		},
		{
			"resizes come back at their rate",
			append(append(repeatSpend(inputResizesBurst, spend{0, 0, true, true}),
				spend{time.Second, 0, true, true}),
				append(repeatSpend(inputResizesPerSecond-1, spend{0, 0, true, true}),
					spend{0, 0, false, true})...),
		},
		{
			"input still goes when resizes are spent",
			append(repeatSpend(inputResizesBurst, spend{0, 0, true, true}),
				spend{0, 1, true, false}),
		},
		// A refused frame is not charged: the connection that sent it is closed, and the
		// session's other viewers keep what was left.
		{
			"a refused frame costs nothing",
			append(repeatSpend(framesInBurst-1, spend{0, maxFrame, true, false}),
				spend{0, maxFrame + 1, false, false}, spend{0, maxFrame, true, false}),
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

				budget, allowed = spendInput(budget, now,
					inputCost{bytes: offered.size, resize: offered.resize})
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
		if !held.spend(first, inputCost{bytes: 1, resize: false}) {
			t.Fatal("the first viewer was refused within the burst")
		}
	}

	for range inputFramesBurst / 2 {
		if !held.spend(first, inputCost{bytes: 1, resize: false}) {
			t.Fatal("the second viewer was refused within the burst")
		}
	}

	if held.spend(first, inputCost{bytes: 1, resize: false}) {
		t.Fatal("two viewers of one session spent more than one burst")
	}

	if !held.spend(second, inputCost{bytes: 1, resize: false}) {
		t.Fatal("another session's budget was spent")
	}

	held.leave(first)
	held.leave(first)
	held.leave(second)

	// Leaving does not give a fresh budget: a viewer that comes back finds what was left.
	held.join(first)

	if held.spend(first, inputCost{bytes: 1, resize: false}) {
		t.Fatal("rejoining refilled a spent budget")
	}

	held.leave(first)

	// The second session's budget refills in under a second, the first's once a second has
	// passed; a budget is forgotten only once it is full and nobody holds it.
	if held.count() != 2 {
		t.Fatalf("%d sessions held while their budgets refill, want 2", held.count())
	}

	now = now.Add(2 * time.Second)

	held.join(pty.ID("c"))
	held.leave(pty.ID("c"))

	if held.count() != 0 {
		t.Fatalf("%d sessions held once every budget refilled, want 0", held.count())
	}
}

// A budget nobody holds is kept while a viewer still holds another, and only full ones go.
func TestOnlyFullBudgetsAreForgotten(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_000_000, 0)
	held := newBudgets(func() time.Time { return now })

	held.join(pty.ID("a"))
	held.leave(pty.ID("a"))

	if held.count() != 0 {
		t.Fatalf("%d sessions held after an untouched budget was left, want 0", held.count())
	}

	held.join(pty.ID("b"))
	held.spend(pty.ID("b"), inputCost{bytes: 1, resize: false})
	held.leave(pty.ID("b"))

	if held.count() != 1 {
		t.Fatalf("%d sessions held after a spent budget was left, want 1", held.count())
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

	held.spend(pty.ID("a"), inputCost{bytes: 1, resize: false})
}
