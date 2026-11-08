package pty

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// errGraceOver is a wait that the program's group outlived.
	errGraceOver = errors.New("the group outlived the wait")
	// errGaveUp is a stop whose caller stopped waiting.
	errGaveUp = errors.New("the caller stopped waiting")
)

// Stop stops the session's program and everything in its process group: a polite stop, then, if
// anything in the group is left after the grace, a kill, then a wait for the group to empty. It
// returns once the session has ended, or with ctx's error if ctx ends first, having sent the
// kill. A session whose group is known to be empty is not signalled again: its number may belong
// to a new program by now.
func (h *Host) Stop(ctx context.Context, sessionID ID) error {
	stopping, err := h.find(sessionID)
	if err != nil {
		return err
	}

	return h.stopSession(ctx, stopping)
}

// StopIfStill is Stop, but only if the screen read that gave mark still stands once the session's
// write turn is taken, by WriteIfStill's checks (no input since the read, the program in the
// foreground and not reading a line, the bottom count lines still passing still); otherwise it
// returns why, as WriteIfStill does, and signals nothing. The polite stop is sent inside the
// turn, so no input reaches the program between the check and it; the rest of the stop (the
// grace, the kill, the wait) runs after the turn is given back, as Stop's does.
func (h *Host) StopIfStill(
	ctx context.Context, sessionID ID, mark ScreenMark, count int,
	still func(lines []string) bool,
) error {
	if still == nil {
		panic("pty: StopIfStill needs a check")
	}

	stopping, err := h.runningSession(sessionID)
	if err != nil {
		return err
	}

	err = h.takeTurn(stopping)
	if err != nil {
		return err
	}

	check := screenCheck{mark: mark, count: count, still: still}

	err = check.passes(stopping)
	if err == nil {
		h.markStopping(stopping)
		err = h.terminateUnlessStopped(stopping)
	}

	<-stopping.writeTurn

	if err != nil {
		return err
	}

	return h.stopSession(ctx, stopping)
}

// terminateUnlessStopped sends the polite stop, unless the group is known to be empty or has no
// member now (its number may belong to a new program by then), as stopGroup does. stopSession,
// after the turn, sends its own polite stop as well; a second SIGTERM is harmless.
func (h *Host) terminateUnlessStopped(stopping *session) error {
	if h.isGroupStopped(stopping) {
		return nil
	}

	if stopping.process.Group() == GroupGone {
		return nil
	}

	err := stopping.process.Terminate()
	if err != nil {
		return fmt.Errorf("stopping session %s: %w", stopping.sessionID, err)
	}

	return nil
}

// StopAll refuses every Start from now on, waits for the Starts already spawning (for at most
// ctx), then stops every session whose group is not known to be empty, all at once, and waits
// for them. It is what quitting Conn does. It returns every problem, joined. (The plan had it
// return nothing; quitting can now say what did not stop.)
func (h *Host) StopAll(ctx context.Context) error {
	var problems []error

	err := h.closeStarts(ctx)
	if err != nil {
		problems = append(problems, err)
	}

	var (
		group      sync.WaitGroup
		problemsMu sync.Mutex
	)

	for _, stopping := range h.unstoppedSessions() {
		group.Go(func() {
			err := h.stopSession(ctx, stopping)
			if err != nil {
				problemsMu.Lock()
				defer problemsMu.Unlock()

				problems = append(problems, err)
			}
		})
	}

	group.Wait()

	return errors.Join(problems...)
}

// closeStarts refuses every Start from now on and waits, for at most ctx, until no Start is
// still spawning: one that finishes now kills its program itself.
func (h *Host) closeStarts(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true
	h.signalStartsDone()
	startsDone := h.startsDone
	h.mu.Unlock()

	select {
	case <-startsDone:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for sessions still starting: %w", ctx.Err())
	}
}

// unstoppedSessions is every session whose group is not known to be empty.
func (h *Host) unstoppedSessions() []*session {
	h.mu.Lock()
	defer h.mu.Unlock()

	var unstopped []*session

	for _, held := range h.sessions {
		if !held.groupStopped {
			unstopped = append(unstopped, held)
		}
	}

	return unstopped
}

// stopSession is Stop for one session.
func (h *Host) stopSession(ctx context.Context, stopping *session) error {
	if h.isGroupStopped(stopping) {
		return nil
	}

	h.markStopping(stopping)

	err := stopGroup(ctx.Done(), stopping.process, h.limits.StopGrace)
	if errors.Is(err, errGaveUp) {
		err = errors.Join(err, ctx.Err())
	}

	if err != nil {
		return fmt.Errorf("stopping session %s: %w", stopping.sessionID, err)
	}

	h.groupEmptied(stopping)

	return waitEnded(ctx, stopping)
}

// markStopping records that a stop of the session has begun.
func (h *Host) markStopping(held *session) {
	h.mu.Lock()
	defer h.mu.Unlock()

	held.stopping = true
}

// stopGroup stops whatever is in the program's group: a polite stop, then a kill if anything is
// left after grace, or as soon as giveUp delivers, then a wait of at most killWait for the group
// to empty. A signal is sent only while the group has a member, so its number is still its own.
// A group with members no signal reaches is waited through, never taken for gone.
func stopGroup(giveUp <-chan struct{}, process Process, grace time.Duration) error {
	if process.Group() == GroupGone {
		return nil
	}

	err := process.Terminate()
	if err != nil {
		return errors.Join(err, killGroup(giveUp, process))
	}

	err = waitGroupGone(giveUp, process, grace)

	switch {
	case err == nil:
		return nil
	case errors.Is(err, errGraceOver):
		return killGroup(giveUp, process)
	default:
		// The caller gave up waiting; it does not give up the kill.
		return errors.Join(err, killGroup(nil, process))
	}
}

// killGroup kills the program's group if anything is left in it, and waits at most killWait for
// it to empty.
func killGroup(giveUp <-chan struct{}, process Process) error {
	if process.Group() == GroupGone {
		return nil
	}

	err := process.Kill()
	if err != nil {
		return fmt.Errorf("killing the program's group: %w", err)
	}

	err = waitGroupGone(giveUp, process, killWait)
	if !errors.Is(err, errGraceOver) {
		return err
	}

	if process.Group() == GroupUnreachable {
		return ErrGroupUnreachable
	}

	return errTimedOut
}

// waitGroupGone waits until nothing is left in the program's group, for at most limit. Members
// no signal reaches count as left.
func waitGroupGone(giveUp <-chan struct{}, process Process, limit time.Duration) error {
	deadline := time.NewTimer(limit)
	defer deadline.Stop()

	poll := time.NewTicker(stopPoll)
	defer poll.Stop()

	for process.Group() != GroupGone {
		select {
		case <-giveUp:
			return errGaveUp
		case <-deadline.C:
			return errGraceOver
		case <-poll.C:
		}
	}

	return nil
}

// waitEnded waits for the session to end, for at most killWait.
func waitEnded(ctx context.Context, stopping *session) error {
	deadline := time.NewTimer(killWait)
	defer deadline.Stop()

	select {
	case <-stopping.ended:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for session %s to end: %w", stopping.sessionID, ctx.Err())
	case <-deadline.C:
		return fmt.Errorf("stopping session %s: %w", stopping.sessionID, errTimedOut)
	}
}
