package pty

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// errGraceOver is a polite stop that the program's group outlived.
	errGraceOver = errors.New("the group outlived the polite stop")
	// errGaveUp is a stop whose caller stopped waiting.
	errGaveUp = errors.New("the caller stopped waiting")
)

// Stop stops the session's program and everything in its process group: a polite stop, then, if
// anything in the group is left after the grace, a kill. It returns once the session has ended,
// or with ctx's error if ctx ends first, having sent the kill. Stopping a session that has ended
// does nothing: its group was stopped before it was recorded as ended.
func (h *Host) Stop(ctx context.Context, sessionID ID) error {
	stopping, err := h.find(sessionID)
	if err != nil {
		return err
	}

	return stopSession(ctx, stopping, h.limits.StopGrace)
}

// StopAll refuses every Start from now on, waits for the Starts already spawning (for at most
// ctx), then stops every running session at once and waits for them all. It is what quitting
// Conn does. It returns every problem, joined. (The plan had it return nothing; quitting can now
// say what did not stop.)
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

	for _, stopping := range h.runningSessions() {
		group.Go(func() {
			err := stopSession(ctx, stopping, h.limits.StopGrace)
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

// runningSessions is every session that has not ended.
func (h *Host) runningSessions() []*session {
	h.mu.Lock()
	defer h.mu.Unlock()

	var running []*session

	for _, held := range h.sessions {
		if held.exitCode == nil {
			running = append(running, held)
		}
	}

	return running
}

// stopSession is Stop for one session.
func stopSession(ctx context.Context, stopping *session, grace time.Duration) error {
	select {
	case <-stopping.ended:
		return nil
	default:
	}

	err := stopGroup(ctx.Done(), stopping.process, grace)
	if errors.Is(err, errGaveUp) {
		err = errors.Join(err, ctx.Err())
	}

	if err != nil {
		return fmt.Errorf("stopping session %q: %w", stopping.name, err)
	}

	return waitEnded(ctx, stopping)
}

// stopGroup stops whatever is in the program's group: a polite stop, then a kill if anything is
// left after grace, or as soon as giveUp delivers. Each signal is sent only while the group has a
// member, so the group's number is still its own and cannot have been given to another process.
func stopGroup(giveUp <-chan struct{}, process Process, grace time.Duration) error {
	if !process.GroupAlive() {
		return nil
	}

	err := process.Terminate()
	if err != nil {
		return errors.Join(err, killGroup(process))
	}

	err = waitGroupGone(giveUp, process, grace)

	switch {
	case err == nil:
		return nil
	case errors.Is(err, errGraceOver):
		return killGroup(process)
	default:
		// The caller gave up waiting; it does not give up the kill.
		return errors.Join(err, killGroup(process))
	}
}

// killGroup kills the program's group if it has a member left.
func killGroup(process Process) error {
	if !process.GroupAlive() {
		return nil
	}

	err := process.Kill()
	if err != nil {
		return fmt.Errorf("killing the program's group: %w", err)
	}

	return nil
}

// waitGroupGone waits until no process is left in the program's group, for at most grace.
func waitGroupGone(giveUp <-chan struct{}, process Process, grace time.Duration) error {
	deadline := time.NewTimer(grace)
	defer deadline.Stop()

	poll := time.NewTicker(stopPoll)
	defer poll.Stop()

	for process.GroupAlive() {
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
		return fmt.Errorf("waiting for session %q to end: %w", stopping.name, ctx.Err())
	case <-deadline.C:
		return fmt.Errorf("stopping session %q: %w", stopping.name, errTimedOut)
	}
}
