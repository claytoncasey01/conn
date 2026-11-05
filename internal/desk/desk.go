// Package desk is the rules for opening a window on a Conn server: own it, attach to it, or
// start over. The window is a program of its own, and it needs a Conn to show. When no Conn holds
// the data folder it starts one and owns it, and stops it with the window. When one does it
// attaches to that one, and leaves it running when the window goes. When what it showed stops
// answering it starts over from the top. Everything the rules touch (the server, the lock, the
// network, the window, the clock) is passed in as Deps, so the rules are tested without a window.
package desk

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claytoncasey01/conn/internal/app"
	"github.com/claytoncasey01/conn/internal/instance"
)

const (
	// probeEvery is how often an attached window asks its server whether it is there.
	probeEvery = 2 * time.Second

	// probeTimeout bounds one of those questions.
	probeTimeout = 2 * time.Second

	// probeFailuresMax is how many questions in a row go unanswered before the window starts over.
	probeFailuresMax = 3

	// addressWaitMax is the longest the window waits for a Conn that holds the folder to say where
	// it listens. A Conn writes its address a moment after it takes the lock.
	addressWaitMax = 5 * time.Second

	// restartsMax is how many times the window may start over inside restartWindow. One more is a
	// Conn that keeps stopping, and the window says so rather than start over for ever.
	restartsMax   = 5
	restartWindow = time.Minute
)

// addressPolls is how many times the window looks at the lock while it waits for an address.
const addressPolls = int(addressWaitMax / probeEvery)

// ErrGaveUp is a window that started over more than restartsMax times inside restartWindow.
var ErrGaveUp = errors.New("conn keeps stopping")

// Action is what the window does about the data folder.
type Action int

// The three things a window does, by who holds the data folder.
const (
	// Own is to start a Conn, because nobody holds the folder.
	Own Action = iota
	// Attach is to show the Conn that holds the folder.
	Attach
	// WaitForAddress is to wait for the Conn that holds the folder to say where it listens.
	WaitForAddress
)

// Decide is what to do given whether a Conn holds the folder and the address it wrote: a free
// lock is ours to take, a lock held with an address is one to attach to, and a lock held without
// one yet is one to wait on.
func Decide(running bool, holderURL string) Action {
	if !running {
		return Own
	}

	if holderURL == "" {
		return WaitForAddress
	}

	return Attach
}

// Deps is everything Run touches outside itself.
type Deps struct {
	// Start starts a Conn on the data folder that ends when ctx does (app.Start with the
	// process's environment).
	Start func(ctx context.Context) (*app.Server, error)
	// Holder is the Conn holding the data folder (instance.Running).
	Holder func() (instance.Holder, bool, error)
	// Probe is one cheap GET of a route the server has. Nil means the server answered.
	Probe func(ctx context.Context, url string) error
	// Open loads url in the window.
	Open func(url string)
	// Fail shows message in a native dialog: the window cannot go on.
	Fail func(message string)
	// Now is the clock, for counting restarts.
	Now func() time.Time
	// After is a channel that fires once d has passed (time.After), so a test never sleeps.
	After func(d time.Duration) <-chan time.Time
}

// result is how one pass over the rules ended.
type result int

const (
	// finished is the context ending: Run returns.
	finished result = iota
	// again is a pass to run at once, because what it waited for has changed.
	again
	// restarted is a pass to run again after counting it as a restart.
	restarted
)

// session is the state of one Run: its Deps, the address it showed last, and its restarts.
type session struct {
	deps     Deps
	lastURL  string
	restarts restarts
}

// Run opens the window and keeps it on a live server until ctx ends. When nobody holds the data
// folder it owns a server: Start, Open, and on the end of ctx it ends the server and waits for
// it to let the folder go. When a Conn holds it, it attaches: Open, then Probe every probeEvery,
// and probeFailuresMax in a row starts over from the top, as does an owned server that ends by
// itself. More than restartsMax restarts inside restartWindow is Fail and an error wrapping
// ErrGaveUp. An attached window never stops the server it attached to.
//
// The restart count bounds the passes: each pass that does not end Run counts as a restart or
// waits for something that changed.
func Run(ctx context.Context, deps Deps) error {
	requireDeps(deps)

	running := &session{deps: deps, lastURL: "", restarts: restarts{times: nil}}

	for {
		outcome, err := running.pass(ctx)
		if err != nil {
			return err
		}

		switch outcome {
		case finished:
			return nil
		case again:
		case restarted:
			if running.restarts.record(deps.Now()) {
				return running.giveUp()
			}
		}
	}
}

// ended says whether ctx is done.
func ended(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// requireDeps panics when a Dep is missing: a program that forgot one is a bug, not input.
func requireDeps(deps Deps) {
	missing := []struct {
		name string
		nil  bool
	}{
		{"Start", deps.Start == nil},
		{"Holder", deps.Holder == nil},
		{"Probe", deps.Probe == nil},
		{"Open", deps.Open == nil},
		{"Fail", deps.Fail == nil},
		{"Now", deps.Now == nil},
		{"After", deps.After == nil},
	}

	for _, dep := range missing {
		if dep.nil {
			panic("desk.Run: Deps." + dep.name + " is not set")
		}
	}
}

// pass looks at the data folder once and does what Decide says.
func (s *session) pass(ctx context.Context) (result, error) {
	if ended(ctx) {
		return finished, nil
	}

	holder, running, err := s.deps.Holder()
	if err != nil {
		return finished, s.failWith("Conn could not check its data folder", err)
	}

	switch Decide(running, holder.URL) {
	case Own:
		return s.own(ctx)
	case Attach:
		return s.attach(ctx, holder.URL)
	case WaitForAddress:
		return s.waitForAddress(ctx)
	}

	panic("desk.pass: Decide gave an action this switch does not know")
}

// own starts a Conn, shows it, and keeps it until ctx ends or it ends by itself.
func (s *session) own(ctx context.Context) (result, error) {
	serverCtx, stop := context.WithCancel(ctx)
	defer stop()

	server, err := s.deps.Start(serverCtx)
	if err != nil {
		return s.startFailed(err)
	}

	s.lastURL = server.URL
	s.deps.Open(server.URL)

	select {
	case <-ctx.Done():
		stop()
		// The server shuts down within the time app gives it, so this wait has an end.
		<-server.Done()

		return finished, nil
	case <-server.Done():
		if ended(ctx) {
			return finished, nil
		}

		return restarted, nil
	}
}

// startFailed is what a Start that failed means: a Conn that took the folder between the look and
// the start is a restart, and anything else is a message and the end of Run.
func (s *session) startFailed(err error) (result, error) {
	if errors.Is(err, instance.ErrHeld) {
		return restarted, nil
	}

	return finished, s.failWith("Conn could not start", err)
}

// attach shows the Conn at url and asks it every probeEvery whether it is there, until ctx ends
// or probeFailuresMax questions in a row go unanswered. It never stops that Conn.
func (s *session) attach(ctx context.Context, url string) (result, error) {
	s.lastURL = url
	s.deps.Open(url)

	failures := 0

	for failures < probeFailuresMax {
		select {
		case <-ctx.Done():
			return finished, nil
		case <-s.deps.After(probeEvery):
		}

		if ended(ctx) {
			return finished, nil
		}

		if s.probe(ctx, url) == nil {
			failures = 0

			continue
		}

		failures++
	}

	if ended(ctx) {
		return finished, nil
	}

	return restarted, nil
}

// probe is one question to the server, with a time limit.
func (s *session) probe(ctx context.Context, url string) error {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	return s.deps.Probe(probeCtx, url)
}

// waitForAddress waits up to addressWaitMax for the Conn that holds the folder to say where it
// listens, or to let the folder go. A Conn that says nothing in that time counts as a restart.
func (s *session) waitForAddress(ctx context.Context) (result, error) {
	for range addressPolls {
		select {
		case <-ctx.Done():
			return finished, nil
		case <-s.deps.After(probeEvery):
		}

		holder, running, err := s.deps.Holder()
		if err != nil {
			return finished, s.failWith("Conn could not check its data folder", err)
		}

		if Decide(running, holder.URL) != WaitForAddress {
			return again, nil
		}
	}

	return restarted, nil
}

// failWith shows what could not be done and why, and gives the error to Run's caller.
func (s *session) failWith(what string, err error) error {
	s.deps.Fail(what + ": " + err.Error())

	return fmt.Errorf("%s: %w", what, err)
}

// giveUp tells the person that the window has started over too many times, and says where it was
// last pointed.
func (s *session) giveUp() error {
	last := s.lastURL
	if last == "" {
		last = "no address yet"
	}

	s.deps.Fail(fmt.Sprintf(
		"Conn stopped answering %d times within %s, so this window gave up. It was last on %s.",
		restartsMax+1, restartWindow, last))

	return fmt.Errorf("%w: more than %d restarts within %s, last on %s",
		ErrGaveUp, restartsMax, restartWindow, last)
}

// restarts is when the window last started over, newest last.
type restarts struct {
	times []time.Time
}

// record notes a restart at now, forgets those older than restartWindow, and says whether more
// than restartsMax are left. The list never holds more than restartsMax+1 times.
func (r *restarts) record(now time.Time) bool {
	kept := r.times[:0]

	for _, when := range r.times {
		if now.Sub(when) < restartWindow {
			kept = append(kept, when)
		}
	}

	kept = append(kept, now)
	r.times = kept

	return len(r.times) > restartsMax
}
