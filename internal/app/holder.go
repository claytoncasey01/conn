package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claytoncasey01/conn/internal/instance"
)

const (
	// holderWait bounds the wait for a Conn that holds the data folder to say where it listens:
	// it takes the lock a moment before it opens its port.
	holderWait = 5 * time.Second
	// holderPoll is how often the lock file is read while waiting.
	holderPoll = 50 * time.Millisecond
)

var (
	// ErrStoppedMeanwhile is a Conn that let the data folder go while WaitForHolder waited.
	ErrStoppedMeanwhile = errors.New("the running conn stopped while it was being asked")
	// ErrNoAddress is a Conn that held the data folder for holderWait without saying where it
	// listens.
	ErrNoAddress = errors.New("the running conn did not say where it listens")
)

// WaitForHolder is the Conn holding the data folder, once it has said where it listens.
func WaitForHolder(ctx context.Context, dataDir string) (instance.Holder, error) {
	deadline := time.Now().Add(holderWait)

	for {
		holder, running, err := instance.Running(dataDir)
		if err != nil {
			return instance.Holder{}, fmt.Errorf("reading the lock file: %w", err)
		}

		if !running {
			return instance.Holder{}, ErrStoppedMeanwhile
		}

		if holder.URL != "" {
			return holder, nil
		}

		if time.Now().After(deadline) {
			return instance.Holder{}, fmt.Errorf("%w within %s", ErrNoAddress, holderWait)
		}

		select {
		case <-ctx.Done():
			return instance.Holder{}, fmt.Errorf("waiting for the running conn: %w", ctx.Err())
		case <-time.After(holderPoll):
		}
	}
}
