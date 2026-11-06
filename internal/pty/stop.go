package pty

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// errGraceOver is a polite stop that the program's group outlived.
var errGraceOver = errors.New("the group outlived the polite stop")

// Stop stops the session's program and everything in its process group: a polite stop, then, if
// anything in the group is left after the grace, a kill. It returns once the session has ended,
// or with ctx's error if ctx ends first. Stopping a session that has ended does nothing.
func (h *Host) Stop(ctx context.Context, sessionID ID) error {
	stopping, err := h.find(sessionID)
	if err != nil {
		return err
	}

	return stopSession(ctx, stopping, h.limits.StopGrace)
}

// StopAll stops every running session at once, waits for them all (or for ctx), and refuses
// every Start after it. It is what quitting Conn does.
func (h *Host) StopAll(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true

	var running []*session

	for _, held := range h.sessions {
		if held.exitCode == nil {
			running = append(running, held)
		}
	}

	h.mu.Unlock()

	var (
		group      sync.WaitGroup
		problemsMu sync.Mutex
		problems   []error
	)

	for _, stopping := range running {
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

// stopSession is Stop for one session.
func stopSession(ctx context.Context, stopping *session, grace time.Duration) error {
	select {
	case <-stopping.ended:
		return nil
	default:
	}

	err := stopping.process.Terminate()
	if err != nil {
		return fmt.Errorf("stopping session %q: %w", stopping.name, err)
	}

	err = waitGroupGone(ctx, stopping.process, grace)
	if errors.Is(err, errGraceOver) {
		err = stopping.process.Kill()
	}

	if err != nil {
		return fmt.Errorf("stopping session %q: %w", stopping.name, err)
	}

	return waitEnded(ctx, stopping)
}

// waitGroupGone waits until no process is left in the program's group, for at most grace.
func waitGroupGone(ctx context.Context, process Process, grace time.Duration) error {
	deadline := time.NewTimer(grace)
	defer deadline.Stop()

	poll := time.NewTicker(stopPoll)
	defer poll.Stop()

	for process.GroupAlive() {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for the polite stop: %w", ctx.Err())
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
