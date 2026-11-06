package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/settings"
)

// newOpener is where the modules' sessions start, as the settings say at each start: on a
// terminal host holds, in cmux or the person's own terminal command, or as a line to paste. A
// hosted session starts from this process's environment, which is the login shell's (the
// desktop app loads it at launch, and conn serve runs in one); the host takes Conn's own
// variables out of it.
func newOpener(cfg config.Config, store *settings.Store, host *pty.Host) launch.Opener {
	return launch.New(launch.Deps{
		Sessions: func() settings.Sessions { return store.Get().Sessions },
		Start:    host.Start,
		Cmux:     cmux.Open(cfg.CmuxBin),
		Terminal: openTerminal,
		LookPath: exec.LookPath,
		Environ:  os.Environ,
	})
}

// openTerminal starts the person's terminal command, its arguments as they are with no shell,
// and does not wait for it: a terminal app runs for as long as its window is open. It gets a
// process group of its own, so a Ctrl-C to conn serve leaves it open, and is reaped when it
// exits. Neither the request's end nor Conn's ends it.
func openTerminal(ctx context.Context, arguments []string) error {
	command := exec.CommandContext(context.WithoutCancel(ctx), arguments[0], arguments[1:]...)
	command.Dir = ""
	command.Env = nil
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = nil
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	err := command.Start()
	if err != nil {
		return fmt.Errorf("starting your terminal command: %w", err)
	}

	go func() { _ = command.Wait() }()

	return nil
}
