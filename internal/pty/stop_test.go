package pty_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

func TestStopEscalatesToAKillAfterTheGrace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		ignoreTerminate bool
		wantSignals     []string
		wantCode        int
	}{
		{"a polite stop is enough", false, []string{signalTerminate}, fakeTerminatedCode},
		{
			"a program that ignores it is killed", true,
			[]string{signalTerminate, signalKill},
			fakeKilledCode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			limits := pty.DefaultLimits()
			limits.StopGrace = testGrace
			spawner := &fakes{ignoreTerminate: tt.ignoreTerminate}
			host := pty.NewHost(pty.Config{Spawner: spawner.spawn, Limits: limits})

			sessionID, err := host.Start(t.Context(), testSpec(t))
			if err != nil {
				t.Fatal(err)
			}

			began := time.Now()

			err = host.Stop(t.Context(), sessionID)
			if err != nil {
				t.Fatal(err)
			}

			assertStopped(t, host, spawner.last(t), sessionID, tt.wantSignals, tt.wantCode)

			if tt.ignoreTerminate {
				waited := time.Since(began)
				if waited < testGrace {
					t.Fatalf("killed after %v, before the grace of %v", waited, testGrace)
				}
			}
		})
	}
}

// assertStopped checks that the session ended after exactly the signals wanted.
func assertStopped(
	t *testing.T, host *pty.Host, process *fakeProcess, sessionID pty.ID,
	signals []string, code int,
) {
	t.Helper()

	got := process.signalsSoFar()
	if !slices.Equal(got, signals) {
		t.Fatalf("signals = %v, want %v", got, signals)
	}

	entry, _ := info(t, host, sessionID)
	if entry.ExitCode == nil {
		t.Fatal("Stop returned before the session ended")
	}

	if *entry.ExitCode != code {
		t.Fatalf("ExitCode = %d, want %d", *entry.ExitCode, code)
	}
}

func TestStopGivesUpWhenItsContextEnds(t *testing.T) {
	t.Parallel()

	spawner := &fakes{ignoreTerminate: true}
	host := pty.NewHost(pty.Config{Spawner: spawner.spawn, Limits: pty.DefaultLimits()})

	t.Cleanup(func() { _ = spawner.last(t).Kill() })

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), testGrace)
	defer cancel()

	err = host.Stop(ctx, sessionID)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop = %v, want context.DeadlineExceeded", err)
	}

	// A stop that gives up still kills what it asked to stop.
	got := spawner.last(t).signalsSoFar()
	if !slices.Equal(got, []string{signalTerminate, signalKill}) {
		t.Fatalf("signals = %v, want the polite stop, then a kill", got)
	}
}

func TestStopOnAnEndedSessionDoesNothing(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	spawner.last(t).finish(0)
	waitEnded(t, host, sessionID)

	err = host.Stop(t.Context(), sessionID)
	if err != nil {
		t.Fatalf("Stop on an ended session = %v, want nil", err)
	}

	if len(spawner.last(t).signalsSoFar()) != 0 {
		t.Fatal("Stop signalled a program that had ended")
	}
}

func TestStopAllStopsEverySessionAndRefusesNewOnes(t *testing.T) {
	t.Parallel()

	limits := pty.DefaultLimits()
	limits.StopGrace = testGrace
	spawner := &fakes{ignoreTerminate: true}
	host := pty.NewHost(pty.Config{Spawner: spawner.spawn, Limits: limits})

	const sessionsCount = 3

	ids := make([]pty.ID, 0, sessionsCount)

	for range sessionsCount {
		sessionID, err := host.Start(t.Context(), testSpec(t))
		if err != nil {
			t.Fatal(err)
		}

		ids = append(ids, sessionID)
	}

	err := host.StopAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	for _, sessionID := range ids {
		entry, _ := info(t, host, sessionID)
		if entry.ExitCode == nil {
			t.Fatalf("session %s is still running after StopAll", sessionID)
		}
	}

	_, err = host.Start(t.Context(), testSpec(t))
	if !errors.Is(err, pty.ErrClosed) {
		t.Fatalf("Start after StopAll = %v, want ErrClosed", err)
	}
}

func TestALeaderThatExitsLeavesNoGroupBehind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		ignoreTerminate bool
		wantSignals     []string
	}{
		{"the group obeys the polite stop", false, []string{signalTerminate}},
		{"the group ignores it and is killed", true, []string{signalTerminate, signalKill}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			limits := pty.DefaultLimits()
			limits.StopGrace = testGrace
			spawner := &fakes{ignoreTerminate: tt.ignoreTerminate, orphans: true}
			host := pty.NewHost(pty.Config{Spawner: spawner.spawn, Limits: limits})

			sessionID, err := host.Start(t.Context(), testSpec(t))
			if err != nil {
				t.Fatal(err)
			}

			process := spawner.last(t)
			process.finish(0)

			entry := waitEnded(t, host, sessionID)
			if process.Group() != pty.GroupGone {
				t.Fatal("the session ended with its group still alive")
			}

			got := process.signalsSoFar()
			if !slices.Equal(got, tt.wantSignals) {
				t.Fatalf("signals = %v, want %v", got, tt.wantSignals)
			}

			if *entry.ExitCode != 0 {
				t.Fatalf("ExitCode = %d, want the leader's own 0", *entry.ExitCode)
			}
		})
	}
}

func TestAProcessIDOfOneOrLessIsABug(t *testing.T) {
	t.Parallel()

	for _, pid := range []int{-1, 1} {
		t.Run(strconv.Itoa(pid), func(t *testing.T) {
			t.Parallel()

			spawner := &fakes{pid: pid}
			host := pty.NewHost(pty.Config{Spawner: spawner.spawn, Limits: pty.DefaultLimits()})

			defer func() {
				if recover() == nil {
					t.Fatalf("a process id of %d was accepted", pid)
				}
			}()

			_, _ = host.Start(t.Context(), testSpec(t))
		})
	}
}

func TestStopAllWaitsForAStartInFlight(t *testing.T) {
	t.Parallel()

	spawner := &fakes{spawning: make(chan struct{}, 1), release: make(chan struct{})}
	host := pty.NewHost(pty.Config{Spawner: spawner.spawn, Limits: pty.DefaultLimits()})

	started := make(chan error, 1)

	go func() {
		_, err := host.Start(t.Context(), testSpec(t))
		started <- err
	}()

	<-spawner.spawning

	stopped := make(chan error, 1)

	go func() { stopped <- host.StopAll(t.Context()) }()

	select {
	case err := <-stopped:
		t.Fatalf("StopAll returned (%v) while a Start was spawning", err)
	case <-time.After(testGrace):
	}

	close(spawner.release)

	err := <-started
	if !errors.Is(err, pty.ErrClosed) {
		t.Fatalf("the Start in flight = %v, want ErrClosed", err)
	}

	err = <-stopped
	if err != nil {
		t.Fatalf("StopAll = %v, want nil", err)
	}

	got := spawner.last(t).signalsSoFar()
	if !slices.Equal(got, []string{signalKill}) {
		t.Fatalf("the program started during StopAll got %v, want a kill", got)
	}
}

func TestStopAllGivesUpOnAStartInFlightWhenItsContextEnds(t *testing.T) {
	t.Parallel()

	spawner := &fakes{spawning: make(chan struct{}, 1), release: make(chan struct{})}
	host := pty.NewHost(pty.Config{Spawner: spawner.spawn, Limits: pty.DefaultLimits()})

	t.Cleanup(func() { close(spawner.release) })

	go func() { _, _ = host.Start(context.WithoutCancel(t.Context()), testSpec(t)) }()

	<-spawner.spawning

	ctx, cancel := context.WithTimeout(t.Context(), testGrace)
	defer cancel()

	err := host.StopAll(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StopAll = %v, want context.DeadlineExceeded", err)
	}
}

// unreachableWait bounds the wait for a session whose group stays unreachable: the grace, then
// killWait after the kill, then the usual margin.
const unreachableWait = testGrace + pty.KillWait + waitLimit

// A group whose members no signal can reach (EPERM: another user's, or setuid) is not taken for
// gone: the stop waits through it for at most killWait, and a group still unreachable then is
// reported, recorded on the session, and keeps the session (and its running slot) from being
// forgotten.
func TestAGroupNoSignalReachesIsNotTakenForGone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		afterKill killOutcome
		wantErr   error
	}{
		{"unreachable then gone", killLeavesUnreachableBriefly, nil},
		{"unreachable for good", killLeavesUnreachable, pty.ErrGroupUnreachable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			limits := pty.DefaultLimits()
			limits.StopGrace = testGrace
			limits.SessionsMax = 1
			spawner := &fakes{ignoreTerminate: true, afterKill: tt.afterKill}
			host := pty.NewHost(pty.Config{Spawner: spawner.spawn, Limits: limits})

			sessionID, err := host.Start(t.Context(), testSpec(t))
			if err != nil {
				t.Fatal(err)
			}

			err = host.Stop(t.Context(), sessionID)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Stop = %v, want %v", err, tt.wantErr)
			}

			assertGroupOutcome(t, host, sessionID, tt.wantErr)
		})
	}
}

// assertGroupOutcome checks what a session whose group was unreachable left behind.
func assertGroupOutcome(t *testing.T, host *pty.Host, sessionID pty.ID, wantErr error) {
	t.Helper()

	entry := waitEndedWithin(t, host, sessionID, unreachableWait)

	_, err := host.Start(t.Context(), testSpec(t))

	if wantErr == nil {
		if entry.StopError != "" {
			t.Fatalf("StopError = %q, want none", entry.StopError)
		}

		if err != nil {
			t.Fatalf("Start after a stopped group = %v, want a session", err)
		}

		return
	}

	if !strings.Contains(entry.StopError, "unreachable") {
		t.Fatalf("StopError = %q, want it to say the group is unreachable", entry.StopError)
	}

	if !errors.Is(err, pty.ErrSessionsFull) {
		t.Fatalf("Start beside an unreachable group = %v, want ErrSessionsFull", err)
	}
}

// A program whose leftover group no signal reaches is recorded as such when it exits by itself,
// and its session is never forgotten while that is so.
func TestAnUnreachableGroupKeepsItsSession(t *testing.T) {
	t.Parallel()

	limits := pty.DefaultLimits()
	limits.StopGrace = testGrace
	spawner := &fakes{ignoreTerminate: true, orphans: true, afterKill: killLeavesUnreachable}
	host := pty.NewHost(pty.Config{Spawner: spawner.spawn, Limits: limits})

	stuck, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	spawner.last(t).finish(0)

	entry := waitEndedWithin(t, host, stuck, unreachableWait)
	if entry.StopError == "" {
		t.Fatal("the session ended with an unreachable group and no StopError")
	}

	spawner.afterKill = killClears
	spawner.orphans = false

	for range pty.EndedMax + 1 {
		sessionID, err := host.Start(t.Context(), testSpec(t))
		if err != nil {
			t.Fatal(err)
		}

		spawner.last(t).finish(0)
		waitEnded(t, host, sessionID)
	}

	_, found := info(t, host, stuck)
	if !found {
		t.Fatal("a session whose group is still unreachable was forgotten")
	}
}

// Once a session's group is known to be empty, nothing signals its number again: a new program
// may have been given it.
func TestAStoppedGroupIsNeverSignalledAgain(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	process := spawner.last(t)
	process.finish(0)
	waitEnded(t, host, sessionID)
	process.reuse()

	err = host.Stop(t.Context(), sessionID)
	if err != nil {
		t.Fatalf("Stop = %v, want nil", err)
	}

	err = host.StopAll(t.Context())
	if err != nil {
		t.Fatalf("StopAll = %v, want nil", err)
	}

	if len(process.signalsSoFar()) != 0 {
		t.Fatalf("a reused group number got %v", process.signalsSoFar())
	}
}

// A session whose stop is under way says so in Live while its program still runs, so a quit in
// that moment does not record it to resume.
func TestLiveSaysAStopIsUnderWay(t *testing.T) {
	t.Parallel()

	limits := pty.DefaultLimits()
	limits.StopGrace = testGrace
	spawner := &fakes{ignoreTerminate: true, afterKill: killClears}
	host := pty.NewHost(pty.Config{Spawner: spawner.spawn, Limits: limits})

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	entry, _ := info(t, host, sessionID)
	if entry.Stopping {
		t.Fatal("a session no one stops says it is stopping")
	}

	stopped := make(chan error, 1)

	go func() { stopped <- host.Stop(t.Context(), sessionID) }()

	waitStopping(t, host, sessionID)

	err = <-stopped
	if err != nil {
		t.Fatal(err)
	}
}

// waitStopping waits until Live says the session's stop is under way while it still runs.
func waitStopping(t *testing.T, host *pty.Host, sessionID pty.ID) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(waitLimit))

	for time.Now().Before(deadline) {
		entry, _ := info(t, host, sessionID)
		if entry.Stopping {
			if entry.ExitCode == nil {
				return
			}
		}

		time.Sleep(waitPoll)
	}

	t.Fatal("Live never said the stop was under way while the program ran")
}
