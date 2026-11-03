package sessions

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// tickEvery is how often the transcripts are tailed.
	tickEvery = 600 * time.Millisecond
	// pushGapMs is the least time between two pushes of a changed snapshot.
	pushGapMs = 250
	// pushEveryMs is how often the snapshot is pushed even when nothing changed: agent status
	// moves with the clock, and the push is the stream's keepalive.
	pushEveryMs = 5000
)

// ErrStopped is the answer to a request made after the loop has stopped.
var ErrStopped = errors.New("the session list has stopped")

// Loop owns the session list: one goroutine tails the transcripts every 600 ms, answers the
// requests for the snapshot and agent pages, and pushes the snapshot to the pages listening.
type Loop struct {
	store    *store
	bumps    atomic.Int64
	requests chan func(*store)
	hub      *Hub
	done     chan struct{}
}

// NewLoop is a loop over the machine deps describe; it does nothing until Run.
func NewLoop(deps Deps) *Loop {
	loop := &Loop{
		store: nil, requests: make(chan func(*store)), hub: newHub(),
		done: make(chan struct{}),
	}
	loop.store = newStore(deps, loop.bumps.Load, loop.done)

	return loop
}

// Hub is where the pages listening for live updates are.
func (l *Loop) Hub() *Hub { return l.hub }

// Bump moves the snapshot's version: something it shows changed outside the session list.
func (l *Loop) Bump() { l.bumps.Add(1) }

// Run ticks until ctx ends, then closes every live-update stream.
func (l *Loop) Run(ctx context.Context) {
	defer close(l.done)
	defer l.hub.Close()

	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()

	pusher := pusher{lastSent: -1, lastSentAt: 0}

	l.store.tick()

	for {
		select {
		case <-ctx.Done():
			return
		case request := <-l.requests:
			request(l.store)
		case result := <-l.store.prDone:
			l.store.settlePR(result)
		case read := <-l.store.askDone:
			l.store.screenRead(read)
		case <-ticker.C:
			l.store.tick()

			// The waiting sessions' screens are read in the background, only while a page is
			// open: nothing else uses the asks.
			if l.hub.Count() > 0 {
				l.store.readAsks(ctx, l.store.now())
			}

			pusher.push(ctx, l.store, l.hub)
		}
	}
}

// pusher decides when the snapshot goes out to the pages listening.
type pusher struct {
	lastSent   int64
	lastSentAt float64
}

// push sends the snapshot when it changed (at most every 250 ms) or every 5 s, and only while a
// page listens: the snapshot is built only when something reads it.
func (p *pusher) push(ctx context.Context, state *store, hub *Hub) {
	if hub.Count() == 0 {
		return
	}

	now := state.now()
	version := state.currentVersion()
	due := now-p.lastSentAt >= pushEveryMs

	if version != p.lastSent {
		if now-p.lastSentAt >= pushGapMs {
			due = true
		}
	}

	if !due {
		return
	}

	p.lastSent = version
	p.lastSentAt = now

	hub.send(frame(state.snapshotBytes(ctx)))
}

func frame(snapshot []byte) []byte {
	return append(append([]byte("data: "), snapshot...), '\n', '\n')
}

// Snapshot is the snapshot as JSON: the cached one while nothing changed and it is under 5 s
// old, else a fresh one.
func (l *Loop) Snapshot(ctx context.Context) ([]byte, error) {
	var out []byte

	err := l.do(ctx, func(state *store) { out = state.snapshotBytes(ctx) })

	return out, err
}

// AgentDetail is one agent page of a live session, and false when there is none.
func (l *Loop) AgentDetail(ctx context.Context, pid, agentID string) ([]byte, bool, error) {
	var (
		out   []byte
		found bool
	)

	err := l.do(ctx, func(state *store) { out, found = state.agentDetail(pid, agentID) })

	return out, found, err
}

// LiveSessions are the live sessions as plain fields; none once the loop has stopped.
func (l *Loop) LiveSessions(ctx context.Context) []LiveSession {
	var out []LiveSession

	err := l.do(ctx, func(state *store) { out = state.liveSessions() })
	if err != nil {
		return nil
	}

	return out
}

// do runs a request on the loop's goroutine and waits for it.
func (l *Loop) do(ctx context.Context, request func(*store)) error {
	finished := make(chan struct{})
	wrapped := func(state *store) {
		defer close(finished)

		request(state)
	}

	select {
	case l.requests <- wrapped:
	case <-l.done:
		return ErrStopped
	case <-ctx.Done():
		return fmt.Errorf("waiting for the session list: %w", ctx.Err())
	}

	<-finished

	return nil
}

// Hub is the pages listening for live updates.
type Hub struct {
	mutex   sync.Mutex
	clients map[*Client]bool
	closed  chan struct{}
	once    sync.Once
}

// Client is one page listening: the frames it has not written yet.
type Client struct {
	frames chan []byte
}

func newHub() *Hub {
	return &Hub{mutex: sync.Mutex{}, clients: map[*Client]bool{}, closed: make(chan struct{})}
}

// Frames are the frames to write, newest kept when the page falls behind.
func (c *Client) Frames() <-chan []byte { return c.frames }

// Join adds a page.
func (h *Hub) Join() *Client {
	client := &Client{frames: make(chan []byte, 1)}

	h.mutex.Lock()
	h.clients[client] = true
	h.mutex.Unlock()

	return client
}

// Leave removes a page.
func (h *Hub) Leave(client *Client) {
	h.mutex.Lock()
	delete(h.clients, client)
	h.mutex.Unlock()
}

// Count is how many pages listen.
func (h *Hub) Count() int {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	return len(h.clients)
}

// Closed is closed when the server stops: every stream ends.
func (h *Hub) Closed() <-chan struct{} { return h.closed }

// Close ends every stream.
func (h *Hub) Close() { h.once.Do(func() { close(h.closed) }) }

// send hands a frame to every page, replacing one it has not written yet.
func (h *Hub) send(data []byte) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	for client := range h.clients {
		select {
		case client.frames <- data:
			continue
		default:
		}

		select {
		case <-client.frames:
		default:
		}

		select {
		case client.frames <- data:
		default:
		}
	}
}
