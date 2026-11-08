package app

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/claytoncasey01/conn/internal/server"
)

// The quit stops the sessions only once the launch's resume has ended: a session the resume
// started after the stop would outlive the quit. synctest.Wait returns once every goroutine of
// the test is blocked, so a quit that would stop without waiting has stopped by then.
func TestTheQuitStopsTheSessionsOnlyOnceTheResumeHasEnded(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		resumed, hold := make(chan struct{}), make(chan struct{})
		stopped := false

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		// The resume, which the test alone ends.
		go func() {
			<-ctx.Done()
			<-hold
			close(resumed)
		}()

		none := func() {}
		life := hostedLife{
			recorder: openedRecorder(t), checks: nil, resumed: nil, bump: none, ticked: nil,
			tickTimeout: 0, settle: newResumeSettle(),
		}
		steps := quitStepsOf(life, resumed, none, cancel, func() { stopped = true }, none)
		quitted := make(chan struct{})

		go func() {
			defer close(quitted)

			quit(steps)
		}()

		released := false
		defer func() {
			if !released {
				close(hold)
			}
		}()

		synctest.Wait()

		if stopped {
			t.Fatal("the sessions stopped while the resume still ran")
		}

		released = true

		close(hold)
		<-quitted

		if !stopped {
			t.Error("the sessions never stopped")
		}
	})
}

// Whatever waits for a quit (conn stop, the desktop app) waits as long as every step of it may
// take.
func TestQuitTimeoutCoversEveryStep(t *testing.T) {
	t.Parallel()

	steps := []time.Duration{
		ShutdownTimeout, server.TerminalsCloseTimeout, quitResumeTimeout, quitStopTimeout,
	}

	var total time.Duration
	for _, step := range steps {
		total += step
	}

	if QuitTimeout < total {
		t.Errorf("QuitTimeout is %v, less than its steps' %v", QuitTimeout, total)
	}
}
