// Package costtest_test holds the screen model to its cost on the largest screen: feeding a chunk
// of output allocates nothing and stays inside a time budget, whatever the chunk holds. It is
// its own package so that its one test has the test binary to itself: allocations and time
// counted while other tests ran would count theirs too.
package costtest_test

import (
	"errors"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// The largest screen the host runs: its most columns, and the most rows its cell limit leaves
// at that width. TestTheLargestScreenIsTheHostsLargest checks both against the host.
const (
	fullColumns = 1000
	fullRows    = 262
)

// chunkBytes is the most output the host reads, and so feeds, at once.
const chunkBytes = 32 << 10

// chunkFeedBudget is the most feeding one chunk may take on the largest screen, whatever the
// chunk holds; under the race detector it is raceSlowdown times that, and CONN_TEST_TIME_SCALE
// stretches both.
const chunkFeedBudget = 20 * time.Millisecond

// feedRuns is how many times a chunk is fed; for time, the fastest counts, so a busy machine
// does not fail the test.
const feedRuns = 5

// countRounds is how many times the allocations of feedRuns feeds are counted; the fewest
// counts.
const countRounds = 3

// maxTimeAttempts bounds how many times the fastest feed is taken before it is out of budget.
const maxTimeAttempts = 5

// costUnit is a sequence repeated to fill a chunk: each is a way to make the model work hard.
type costUnit struct {
	name string
	unit string
}

func costUnits() []costUnit {
	wide := string(rune(0x65e5))

	return []costUnit{
		{"text", "abcdefgh"},
		{"wide text", wide + wide},
		{"full reset (RIS)", "\x1bc"},
		{"erase display", "\x1b[2J"},
		{"fill a line, then erase display", "a\x1b[999b\x1b[2J"},
		{"erase below from the top", "\x1b[H\x1b[J"},
		{"erase line", "\x1b[K"},
		{"erase line from the start", "\x1b[1;1H\x1b[2K"},
		{"erase characters", "\x1b[1;1H\x1b[999X"},
		{"erase one character in wide text", "\x1b[1;2H\x1b[X"},
		{"delete characters", "\x1b[1;1H\x1b[999P"},
		{"delete one character in wide text", "\x1b[1;2H\x1b[P"},
		{"insert characters", "\x1b[1;1H\x1b[999@"},
		{"insert one character in wide text", "\x1b[1;2H\x1b[@"},
		{"insert lines", "\x1b[H\x1b[999L"},
		{"delete lines", "\x1b[H\x1b[999M"},
		{"scroll up", "\x1b[999S"},
		{"scroll down", "\x1b[999T"},
		{"line feeds at the bottom", "\x1b[999;1H\n\n\n\n\n\n"},
		{"repeat", "a\x1b[999b"},
		{"repeat wide", wide + "\x1b[999b"},
		{"alternate screen in and out", "\x1b[?1049h\x1b[?1049l"},
		{"alternate screen 47 in and out", "\x1b[?47h\x1b[?47l"},
		{"cursor addressing", "\x1b[99;999Hx"},
		{"long parameters", "\x1b[" + strings.Repeat("9;", 40) + "m"},
		{"titles", "\x1b]0;" + strings.Repeat("t", 100) + "\a"},
		{"C1 controls", "\u009b2J\u009d0;t\u009c"},
	}
}

// chunkOf is unit repeated to the size of one read of output.
func chunkOf(unit string) []byte {
	return []byte(strings.Repeat(unit, chunkBytes/len(unit)))
}

// filledScreen is the largest screen, every row of it full of wide characters: the state that
// makes edits and clears cost the most.
func filledScreen(tb testing.TB) *pty.Screen {
	tb.Helper()

	screen, err := pty.NewScreen(pty.Size{Columns: fullColumns, Rows: fullRows})
	if err != nil {
		tb.Fatal(err)
	}

	screen.Feed([]byte(strings.Repeat(string(rune(0x65e5)), fullColumns*fullRows/2)))

	return screen
}

// TestFeedingAChunkCostsLittleAtFullSize is the package's one test: for every costly sequence,
// feeding a chunk of it to the largest screen in its steady state (after one feed of it, not
// counted) allocates nothing, and stays in the budget. The time is the fastest of feedRuns
// feeds, taken again up to maxTimeAttempts times: a machine busy with other tests slows some
// attempts, but a feed that is too slow is slow in every one.
func TestFeedingAChunkCostsLittleAtFullSize(t *testing.T) {
	t.Parallel()

	checkLargestScreen(t)

	budget := testtime.Scaled(chunkFeedBudget * raceSlowdown)

	for _, unit := range costUnits() {
		screen := filledScreen(t)
		chunk := chunkOf(unit.unit)

		allocations := allocationsFeeding(screen, chunk)
		if allocations != 0 {
			t.Errorf("%s: feeding a chunk allocates %v times, want none", unit.name, allocations)
		}

		fastest := fastestFeedWithin(t, chunk, budget)
		if fastest > budget {
			t.Errorf("%s: feeding a chunk took %v, more than %v", unit.name, fastest, budget)
		}
	}
}

// allocationsFeeding is the fewest allocations feedRuns feeds of chunk made in any of
// countRounds counts. testing.AllocsPerRun refuses to run in a parallel test, so the process's
// own count is read; something else in the process (the runtime, the race detector) can
// allocate during one count, but a feed that allocates does in every count, so the fewest is
// the feeds' own.
func allocationsFeeding(screen *pty.Screen, chunk []byte) uint64 {
	// One feed first, not counted: the bound is the steady state's.
	screen.Feed(chunk)
	_ = screen.Queries()

	fewest := uint64(math.MaxUint64)

	for range countRounds {
		var before, after runtime.MemStats

		runtime.ReadMemStats(&before)

		for range feedRuns {
			screen.Feed(chunk)
			_ = screen.Queries()
		}

		runtime.ReadMemStats(&after)

		fewest = min(fewest, after.Mallocs-before.Mallocs)
	}

	return fewest
}

// fastestFeedWithin is fastestFeed, taken again, up to maxTimeAttempts times, until it is
// within budget; the last one taken.
func fastestFeedWithin(t *testing.T, chunk []byte, budget time.Duration) time.Duration {
	t.Helper()

	fastest := fastestFeed(t, chunk)
	for range maxTimeAttempts - 1 {
		if fastest <= budget {
			return fastest
		}

		fastest = fastestFeed(t, chunk)
	}

	return fastest
}

// fastestFeed is the fastest of feedRuns feeds of chunk, each to a freshly filled screen.
func fastestFeed(t *testing.T, chunk []byte) time.Duration {
	t.Helper()

	fastest := time.Duration(1<<63 - 1)

	for range feedRuns {
		screen := filledScreen(t)
		start := time.Now()

		screen.Feed(chunk)

		fastest = min(fastest, time.Since(start))
	}

	return fastest
}

// checkLargestScreen fails unless fullColumns by fullRows is a size the host runs, and one row
// more is not.
func checkLargestScreen(t *testing.T) {
	t.Helper()

	_, err := pty.NewScreen(pty.Size{Columns: fullColumns, Rows: fullRows})
	if err != nil {
		t.Fatalf("the largest screen is refused: %v", err)
	}

	_, err = pty.NewScreen(pty.Size{Columns: fullColumns, Rows: fullRows + 1})
	if !errors.Is(err, pty.ErrInvalidSize) {
		t.Fatalf("a screen one row larger is not refused: %v", err)
	}

	_, err = pty.NewScreen(pty.Size{Columns: fullColumns + 1, Rows: 1})
	if !errors.Is(err, pty.ErrInvalidSize) {
		t.Fatalf("a screen one column wider is not refused: %v", err)
	}
}

func BenchmarkScreenFeed(b *testing.B) {
	for _, unit := range costUnits() {
		b.Run(unit.name, func(b *testing.B) {
			screen := filledScreen(b)
			chunk := chunkOf(unit.unit)

			b.SetBytes(int64(len(chunk)))
			b.ReportAllocs()

			for b.Loop() {
				screen.Feed(chunk)
			}
		})
	}
}
