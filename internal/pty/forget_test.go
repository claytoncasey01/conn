package pty_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// Forget lets go of a session only once its program has ended and its group is known to be
// empty; anything else is refused and the session is kept.
func TestForgetLetsGoOfOnlyAnEndedSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// end ends the session as the case needs, and is the id to forget.
		end       func(t *testing.T, host *pty.Host, spawner *fakes) pty.ID
		wantErr   error
		wantFound bool
	}{
		{"one still running", startOnly, pty.ErrStillRunning, true},
		{"one that ended by itself", endByItself, nil, false},
		{"one that was stopped", stopped, nil, false},
		{"one the host does not hold", unknown, pty.ErrNoSession, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host, spawner := newFakeHost(t, pty.DefaultLimits())
			sessionID := tt.end(t, host, spawner)

			err := host.Forget(sessionID)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Forget = %v, want %v", err, tt.wantErr)
			}

			_, found := info(t, host, sessionID)
			if found != tt.wantFound {
				t.Fatalf("in Live after Forget = %v, want %v", found, tt.wantFound)
			}
		})
	}
}

// A session whose group no signal reaches is never forgotten while that is so: its number is
// still its own and must stay watched.
func TestForgetKeepsASessionWhoseGroupIsUnreachable(t *testing.T) {
	t.Parallel()

	spawner := &fakes{ignoreTerminate: true, orphans: true, afterKill: killLeavesUnreachable}
	limits := pty.DefaultLimits()
	limits.StopGrace = testGrace
	host := pty.NewHost(pty.Config{Spawner: spawner.spawn, Limits: limits})

	stuck, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	spawner.last(t).finish(0)
	waitEndedWithin(t, host, stuck, unreachableWait)

	err = host.Forget(stuck)
	if !errors.Is(err, pty.ErrStillRunning) {
		t.Fatalf("Forget = %v, want %v", err, pty.ErrStillRunning)
	}

	_, found := info(t, host, stuck)
	if !found {
		t.Fatal("a session whose group is unreachable was forgotten")
	}
}

// A stop made while another stop of the session is under way returns only once the session has
// ended, as Stop says, so a Forget right after it lets the session go: the second of two stops
// pressed at once never reports a session ended that is still in the list.
func TestASecondStopReturnsOnceTheSessionHasEnded(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())
	sessionID := startOnly(t, host, spawner)

	first := make(chan error, 1)

	go func() { first <- host.Stop(context.WithoutCancel(t.Context()), sessionID) }()

	waitSignalled(t, spawner.last(t))

	err := host.Stop(t.Context(), sessionID)
	if err != nil {
		t.Fatalf("the second Stop = %v, want nil", err)
	}

	entry, _ := info(t, host, sessionID)
	if entry.ExitCode == nil {
		t.Fatal("the second Stop returned before the session ended")
	}

	err = host.Forget(sessionID)
	if err != nil {
		t.Fatalf("Forget after the second Stop = %v, want nil", err)
	}

	err = <-first
	if err != nil {
		t.Fatalf("the first Stop = %v, want nil", err)
	}
}

// waitSignalled waits until a stop has signalled process.
func waitSignalled(t *testing.T, process *fakeProcess) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(waitLimit))

	for len(process.signalsSoFar()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the stop sent no signal")
		}

		time.Sleep(waitPoll)
	}
}

// A forgotten session gives back nothing twice: the next Start is admitted as before.
func TestAForgottenSessionFreesNoSecondSlot(t *testing.T) {
	t.Parallel()

	limits := pty.DefaultLimits()
	limits.MaxSessions = 1
	host, spawner := newFakeHost(t, limits)

	sessionID := endByItself(t, host, spawner)

	err := host.Forget(sessionID)
	if err != nil {
		t.Fatal(err)
	}

	room := host.Room()
	if room.Running != 0 {
		t.Fatalf("Room.Running = %d after a forget, want 0", room.Running)
	}
}

func startOnly(t *testing.T, host *pty.Host, _ *fakes) pty.ID {
	t.Helper()

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	return sessionID
}

func endByItself(t *testing.T, host *pty.Host, spawner *fakes) pty.ID {
	t.Helper()

	sessionID := startOnly(t, host, spawner)
	spawner.last(t).finish(0)
	waitEnded(t, host, sessionID)

	return sessionID
}

func stopped(t *testing.T, host *pty.Host, spawner *fakes) pty.ID {
	t.Helper()

	sessionID := startOnly(t, host, spawner)

	err := host.Stop(t.Context(), sessionID)
	if err != nil {
		t.Fatal(err)
	}

	return sessionID
}

func unknown(t *testing.T, _ *pty.Host, _ *fakes) pty.ID {
	t.Helper()

	sessionID, err := pty.ParseID("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}

	return sessionID
}
