package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"syscall"
	"time"

	"github.com/claytoncasey01/conn/internal/app"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/instance"
)

const (
	// stopWait bounds the wait for a stopped Conn to let its data folder go: its server gives
	// open requests a shutdown timeout to finish, then stops its hosted sessions.
	stopWait = app.QuitTimeout + time.Second
)

var (
	errDidNotStop = errors.New("the running conn did not stop")
	errNotConn    = errors.New("the lock file names a process that is not a conn")
)

// stop stops the conn serve holding the data folder, and waits until it has let the folder go.
// Nothing running is not an error.
func stop(ctx context.Context, lookup config.LookupFunc, stdout, stderr io.Writer) int {
	dataDir := config.FromEnv(lookup).DataDir

	_, running, err := instance.Running(dataDir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn: %v\n", err)

		return exitError
	}

	if !running {
		_, _ = fmt.Fprintln(stdout, "conn not running")

		return exitOK
	}

	holder, err := app.WaitForHolder(ctx, dataDir)
	if errors.Is(err, app.ErrStoppedMeanwhile) {
		_, _ = fmt.Fprintln(stdout, "conn not running")

		return exitOK
	}

	if err == nil {
		err = terminate(ctx, dataDir, holder.PID)
	}

	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn: %v\n", err)

		return exitError
	}

	_, _ = fmt.Fprintln(stdout, "conn stopped")

	return exitOK
}

// terminate asks the process holding the data folder to stop, and waits until the folder is
// free.
func terminate(ctx context.Context, dataDir string, pid int) error {
	// The lock proves a Conn holds the folder, and only that Conn writes the id.
	if pid <= 1 {
		return fmt.Errorf("%w: %d", errNotConn, pid)
	}

	err := syscall.Kill(pid, syscall.SIGTERM)
	if err != nil {
		return fmt.Errorf("stopping process %d: %w", pid, err)
	}

	deadline := time.Now().Add(stopWait)

	for time.Now().Before(deadline) {
		_, running, err := instance.Running(dataDir)
		if err != nil {
			return fmt.Errorf("reading the lock file: %w", err)
		}

		if !running {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for process %d to stop: %w", pid, ctx.Err())
		case <-time.After(app.HolderPoll):
		}
	}

	return fmt.Errorf("%w within %s (process %d)", errDidNotStop, stopWait, pid)
}
