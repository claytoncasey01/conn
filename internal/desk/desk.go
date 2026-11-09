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
	"github.com/claytoncasey01/conn/internal/secret"
)

const (
	// probeEvery is how often an attached window asks its server whether it is there.
	probeEvery = 2 * time.Second

	// ProbeTimeout bounds one of those questions. The app's probe client takes its own limits
	// from it.
	ProbeTimeout = 2 * time.Second

	// maxProbeFailures is how many questions in a row go unanswered before the window starts over.
	maxProbeFailures = 3

	// maxAddressWait is the least the window waits for a Conn that holds the folder to say where
	// it listens, rounded up to whole polls (see addressPolls). A Conn writes its address a moment
	// after it takes the lock; conn's own wait for it is this long, to the poll.
	maxAddressWait = app.HolderWait

	// maxRestartsInARow is how many times the window may start over with no answer from a Conn in
	// between. One more is a Conn that keeps stopping or never answers, and the window says so
	// rather than start over for ever. There is no time window: a Conn that hangs makes each
	// restart slow, and a count over a minute would then never fill.
	maxRestartsInARow = 5
)

// addressPolls is how many times the window looks at the lock while it waits for an address: the
// wait is rounded up to a whole number of polls, so it is never shorter than maxAddressWait.
const addressPolls = int((maxAddressWait + probeEvery - 1) / probeEvery)

// errGaveUp is a window that started over more than maxRestartsInARow times in a row with no
// answer from a Conn in between.
var errGaveUp = errors.New("conn keeps stopping")

// action is what the window does about the data folder.
type action int

// The three things a window does, by who holds the data folder.
const (
	// ownAction is to start a Conn, because nobody holds the folder.
	ownAction action = iota
	// attachAction is to show the Conn that holds the folder.
	attachAction
	// waitForAddressAction is to wait for the Conn that holds the folder to say where it listens.
	waitForAddressAction
)

// decide is what to do given whether a Conn holds the folder and the address it wrote: a free
// lock is ours to take, a lock held with an address is one to attach to, and a lock held without
// one yet is one to wait on.
func decide(running bool, holderURL string) action {
	if !running {
		return ownAction
	}

	if holderURL == "" {
		return waitForAddressAction
	}

	return attachAction
}

// Deps is everything Run touches outside itself.
type Deps struct {
	// Start starts a Conn on the data folder that ends when ctx does (app.Start with the
	// process's environment).
	Start func(ctx context.Context) (*app.Server, error)
	// Holder is the Conn holding the data folder (instance.Running).
	Holder func() (instance.Holder, bool, error)
	// Probe is one cheap GET of a route the server at url has, with its secret. Nil means the
	// server answered.
	Probe func(ctx context.Context, url string, key secret.Secret) error
	// Open loads page in the window: the server's address with its secret in the fragment
	// (PageURL).
	Open func(page string)
	// Fail shows message in a native dialog: the window cannot go on.
	Fail func(message string)
	// After is a channel that fires once d has passed (time.After), so a test never sleeps.
	After func(d time.Duration) <-chan time.Time
}

// result is how one pass over the rules ended.
type result int

const (
	// finished is the context ending: Run returns.
	finished result = iota
	// restarted is a pass to run again, counted as a restart: what it showed went away, or what
	// it waited for changed.
	restarted
)

// session is the state of one Run: its Deps, the address it showed last (never with its secret,
// because it goes into a message), how many times it has started over since a Conn last answered
// a probe, and why the server of the latest pass ended.
type session struct {
	deps           Deps
	lastURL        string
	restartsInARow int
	// serverErr is the error the owned server of the latest pass ended with, or nil when that
	// pass did not end that way (or the server ended cleanly).
	serverErr error
}

// Run opens the window and keeps it on a live server until ctx ends. When nobody holds the data
// folder it owns a server: Start, Open, and on the end of ctx it ends the server and waits for
// it to let the folder go. When a Conn holds it, it attaches: Open, then Probe every probeEvery,
// and maxProbeFailures in a row starts over from the top, as does an owned server that ends by
// itself. Only an answered probe resets the count of restarts, so an owned server that ends by
// itself counts toward it however long it ran. More than maxRestartsInARow restarts in a row is
// Fail and an error wrapping errGaveUp. An attached window never stops the server it attached to.
//
// The restart count bounds the passes: each pass that does not end Run counts toward it, and only
// a pass that got an answer, and so waited at least probeEvery, resets it.
func Run(ctx context.Context, deps Deps) error {
	requireDeps(deps)

	running := &session{deps: deps, lastURL: "", restartsInARow: 0, serverErr: nil}

	for {
		outcome, err := running.pass(ctx)
		if err != nil {
			return err
		}

		if outcome == finished {
			return nil
		}

		running.restartsInARow++
		if running.restartsInARow > maxRestartsInARow {
			return running.giveUp()
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

	s.serverErr = nil

	holder, running, err := s.deps.Holder()
	if err != nil {
		return finished, s.failWith("Conn could not check its data folder", err)
	}

	switch decide(running, holder.URL) {
	case ownAction:
		return s.own(ctx)
	case attachAction:
		return s.attach(ctx, holder.URL, holder.Secret)
	case waitForAddressAction:
		return s.waitForAddress(ctx)
	}

	panic("desk.pass: decide gave an action this switch does not know")
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
	s.deps.Open(PageURL(server.URL, server.Secret))

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

		// Err is read only after Done, as it must be, and is kept for the message if Run gives up.
		s.serverErr = server.Err()

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

// attach shows the Conn at url, with the secret it published, and asks it every probeEvery
// whether it is there, until ctx ends or maxProbeFailures questions in a row go unanswered. It
// never stops that Conn.
func (s *session) attach(ctx context.Context, url string, key secret.Secret) (result, error) {
	s.lastURL = url
	s.deps.Open(PageURL(url, key))

	failures := 0

	for failures < maxProbeFailures {
		select {
		case <-ctx.Done():
			return finished, nil
		case <-s.deps.After(probeEvery):
		}

		if ended(ctx) {
			return finished, nil
		}

		if s.probe(ctx, url, key) == nil {
			failures = 0
			s.restartsInARow = 0

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
func (s *session) probe(ctx context.Context, url string, key secret.Secret) error {
	probeCtx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()

	return s.deps.Probe(probeCtx, url, key)
}

// PageURL is the page of the server at url with key in its fragment, which the page takes and
// removes from the address bar: a fragment is never sent to a server, so the secret stays out of
// every request line and every log of one. A zero key (a Conn from before the secret, which asks
// for none) gives url as it is. The result holds the secret: it is opened, never printed or put
// in a message.
func PageURL(url string, key secret.Secret) string {
	if key.IsZero() {
		return url
	}

	return url + "/#k=" + key.String()
}

// waitForAddress waits up to maxAddressWait for the Conn that holds the folder to say where it
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

		if decide(running, holder.URL) != waitForAddressAction {
			return restarted, nil
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
// last pointed and, when an owned server's end was the cause of the last restart, why it ended.
// It does not say why each earlier restart happened (a Conn that stopped, that never answered,
// or that took the folder first), because the count does not keep it.
func (s *session) giveUp() error {
	last := s.lastURL
	if last == "" {
		last = "no address yet"
	}

	sentence, clause := "", ""

	if s.serverErr != nil {
		ended := shortReason(s.serverErr.Error())
		sentence = " The Conn it started last ended with: " + ended + "."
		clause = ", the last Conn ended with: " + ended
	}

	s.deps.Fail(fmt.Sprintf(
		"Conn could not be kept open: this window started over %d times in a row without an "+
			"answer from Conn, so it gave up. It was last on %s.%s",
		s.restartsInARow, last, sentence))

	return fmt.Errorf("%w: %d restarts in a row with no answer, last on %s%s",
		errGaveUp, s.restartsInARow, last, clause)
}
