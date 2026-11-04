package flight_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/flight"
	"github.com/claytoncasey01/conn/internal/testtime"
)

func TestCallersInFlightShareOneRun(t *testing.T) {
	t.Parallel()

	var (
		group flight.Group[string, int]
		runs  atomic.Int32
	)

	release := make(chan struct{})
	slow := func(context.Context) int {
		<-release

		return int(runs.Add(1))
	}

	answers := make(chan int, 2)

	for range 2 {
		go func() {
			value, _ := group.Do(t.Context(), "key", slow)
			answers <- value
		}()
	}

	waitFor(t, func() bool { return group.Waiting("key") == 2 })
	close(release)

	first, second := <-answers, <-answers
	if first != 1 {
		t.Errorf("first = %d, want 1", first)
	}

	if second != 1 {
		t.Errorf("second = %d, want the same run", second)
	}

	next, _ := group.Do(t.Context(), "key", func(context.Context) int { return int(runs.Add(1)) })
	if next != 2 {
		t.Errorf("next = %d, want a new run", next)
	}
}

func TestKeysRunApart(t *testing.T) {
	t.Parallel()

	var group flight.Group[string, string]

	first, _ := group.Do(t.Context(), "a", func(context.Context) string { return "a" })
	second, _ := group.Do(t.Context(), "b", func(context.Context) string { return "b" })

	if first+second != "ab" {
		t.Errorf("answers = %q, want ab", first+second)
	}
}

func TestARunOutlivesACallerThatWentAway(t *testing.T) {
	t.Parallel()

	var group flight.Group[string, int]

	ctx, cancel := context.WithCancel(t.Context())
	release := make(chan struct{})
	finished := make(chan error, 1)

	go func() {
		_, answered := group.Do(ctx, "key", func(runCtx context.Context) int {
			<-release

			finished <- runCtx.Err()

			return 1
		})
		if answered {
			t.Error("Do() = ok for a caller that went away")
		}
	}()

	waitFor(t, func() bool { return group.Waiting("key") == 1 })
	cancel()
	waitFor(t, func() bool { return group.Waiting("key") == 0 })
	close(release)

	err := <-finished
	if err != nil {
		t.Errorf("the run's context ended with its caller: %v", err)
	}
}

func TestWaitingCountsTheCallersOfARunInFlight(t *testing.T) {
	t.Parallel()

	var group flight.Group[string, int]

	if group.Waiting("key") != 0 {
		t.Fatal("Waiting with nothing in flight != 0")
	}

	release := make(chan struct{})
	done := make(chan struct{})

	go func() {
		group.Do(t.Context(), "key", func(context.Context) int {
			<-release

			return 1
		})
		close(done)
	}()

	waitFor(t, func() bool { return group.Waiting("key") == 1 })
	close(release)
	<-done

	if group.Waiting("key") != 0 {
		t.Errorf("Waiting after the run = %d, want 0", group.Waiting("key"))
	}
}

func waitFor(t *testing.T, done func() bool) {
	t.Helper()

	limit := testtime.Scaled(5 * time.Second)

	deadline := time.Now().Add(limit)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("waited %s for a condition that never held", limit)
		}

		time.Sleep(time.Millisecond)
	}
}
