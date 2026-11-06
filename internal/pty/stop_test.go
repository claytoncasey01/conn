package pty_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
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

	got := spawner.last(t).signalsSoFar()
	if !slices.Equal(got, []string{signalTerminate}) {
		t.Fatalf("signals = %v, want only the polite stop", got)
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
