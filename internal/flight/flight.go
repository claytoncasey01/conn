// Package flight runs one call at a time per key and shares its answer with every caller that
// arrives while it runs: a double click, or the same page open in two tabs, costs one run.
package flight

import (
	"context"
	"sync"
)

// call is one run in flight.
type call[V any] struct {
	done  chan struct{}
	value V
	// waiters counts the callers waiting for it.
	waiters int
}

// Group holds the runs in flight. The zero value is ready to use.
type Group[K comparable, V any] struct {
	mu    sync.Mutex
	calls map[K]*call[V]
}

// Do runs work for key, or joins the run already in flight for it, and returns its answer. The
// next Do after a run ends runs again.
//
// The run outlives a caller that goes away, so its effects still happen: work gets ctx without its
// cancellation, and must bound itself. A caller whose ctx ends first gets false. A panic in work
// is a program error and stops the process, as one in any goroutine does.
func (g *Group[K, V]) Do(ctx context.Context, key K, work func(ctx context.Context) V) (V, bool) {
	g.mu.Lock()

	if g.calls == nil {
		g.calls = map[K]*call[V]{}
	}

	running, found := g.calls[key]
	if !found {
		running = &call[V]{done: make(chan struct{})}
		g.calls[key] = running

		go g.run(context.WithoutCancel(ctx), key, running, work)
	}

	running.waiters++

	g.mu.Unlock()

	defer g.leave(running)

	select {
	case <-running.done:
		return running.value, true
	case <-ctx.Done():
		var zero V

		return zero, false
	}
}

// Waiting is how many callers wait for the run in flight for key; 0 when none is.
func (g *Group[K, V]) Waiting(key K) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	running, found := g.calls[key]
	if !found {
		return 0
	}

	return running.waiters
}

func (g *Group[K, V]) leave(running *call[V]) {
	g.mu.Lock()
	defer g.mu.Unlock()

	running.waiters--
}

func (g *Group[K, V]) run(
	ctx context.Context, key K, running *call[V], work func(ctx context.Context) V,
) {
	running.value = work(ctx)

	g.mu.Lock()
	delete(g.calls, key)
	g.mu.Unlock()

	close(running.done)
}
