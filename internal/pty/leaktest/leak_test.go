//go:build unix

// Package leaktest_test checks that hosting sessions on real terminals leaves no descriptor and no
// goroutine behind. It is its own package so that its one test has the test binary to itself:
// any other test running beside it would open descriptors and start goroutines of its own.
package leaktest_test

import (
	"errors"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// rounds is how many times the test fills the host and empties it again.
const rounds = 3

// settleLimit bounds how long the counts may take to come back, and settlePoll is how often they
// are looked at.
const (
	settleLimit = 5 * time.Second
	settlePoll  = 10 * time.Millisecond
)

// maxDescriptorsScan is how many descriptors the count looks at.
const maxDescriptorsScan = 4096

// stopGrace is short: the programs here all obey a polite stop.
const stopGrace = 200 * time.Millisecond

func TestStartingAndStoppingLeavesNothingBehind(t *testing.T) {
	t.Parallel()

	// An *os.File left open is closed by its finalizer at some garbage collection, which would
	// hide the leak; with collection off, a leaked descriptor stays open to be counted.
	previous := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(previous)

	limits := pty.DefaultLimits()
	limits.StopGrace = stopGrace

	host := pty.NewHost(pty.Config{Spawner: pty.Spawn, Limits: limits})
	dir := t.TempDir()

	// A first round opens what the runtime opens once (its poller), so it is not counted.
	fillAndEmpty(t, host, dir, limits.MaxSessions)

	descriptors := descriptorsOpen(t)
	goroutines := runtime.NumGoroutine()

	for range rounds {
		fillAndEmpty(t, host, dir, limits.MaxSessions)
	}

	waitSettled(t, "descriptors", descriptors, func() int { return descriptorsOpen(t) })
	waitSettled(t, "goroutines", goroutines, runtime.NumGoroutine)
}

// fillAndEmpty starts count sessions, half of which end by themselves, and stops the rest.
func fillAndEmpty(t *testing.T, host *pty.Host, dir string, count int) {
	t.Helper()

	ids := make([]pty.ID, 0, count)

	for index := range count {
		script := "exec sleep 600"
		if index%2 == 0 {
			script = "echo done"
		}

		sessionID, err := host.Start(t.Context(), pty.Spec{
			Argv: []string{"/bin/sh", "-c", script}, Dir: dir, Env: nil, Name: "leak",
		})
		if errors.Is(err, pty.ErrUnsupported) {
			t.Skip("terminals are not supported here")
		}

		if err != nil {
			t.Fatal(err)
		}

		ids = append(ids, sessionID)
	}

	for _, sessionID := range ids {
		err := host.Stop(t.Context(), sessionID)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// descriptorsOpen is how many of this process's first maxDescriptorsScan descriptors are open.
// It asks each one, rather than listing /dev/fd, since macOS leaves a revoked terminal out of
// that listing while the descriptor is still open.
func descriptorsOpen(t *testing.T) int {
	t.Helper()

	count := 0

	for descriptor := range maxDescriptorsScan {
		_, err := unix.FcntlInt(uintptr(descriptor), unix.F_GETFD, 0)
		if err == nil {
			count++
		}
	}

	return count
}

// waitSettled waits until count() is back to baseline or below.
func waitSettled(t *testing.T, what string, baseline int, count func() int) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(settleLimit))

	for time.Now().Before(deadline) {
		if count() <= baseline {
			return
		}

		time.Sleep(settlePoll)
	}

	t.Fatalf("%s: %d open after the sessions ended, %d before", what, count(), baseline)
}
