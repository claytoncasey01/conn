package app

import (
	"context"
	"io"
	"log"
	"testing"
	"testing/fstest"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/pty"
)

// The launch waits for the first tick of the session list the owners' checks read.
func TestTheLaunchWaitsForTheSessionListItReads(t *testing.T) {
	t.Parallel()

	world := newOwnerWorld(t)
	writeOwnerFile(t, world.data+"/settings.json", world.settings)
	world.env["HOME"] = t.TempDir()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	lookup := func(key string) (string, bool) {
		value, found := world.env[key]

		return value, found
	}
	logger := log.New(io.Discard, "", 0)
	host := pty.NewHost(pty.Config{
		Spawner: func(pty.Spec) (pty.Process, error) { return nil, errNeverSpawns },
		Limits:  pty.DefaultLimits(), Answers: pty.Answers{}, TurnWaited: nil,
	})

	handler, life := newHandler(ctx, config.FromEnv(lookup), loadSettings(world.data, logger,
		io.Discard), logger, fstest.MapFS{}, host)

	if life.ticked != handler.Sessions.Ticked() {
		t.Error("the launch waits for another session list than the one the checks read")
	}

	if life.tickTimeout != firstTickTimeout {
		t.Errorf("the launch waits %v, want %v", life.tickTimeout, firstTickTimeout)
	}
}
