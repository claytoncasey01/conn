package app

import (
	"os"
	"os/exec"

	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/settings"
)

// newOpener is where the modules' sessions start, as the settings say at each start: on a
// terminal host holds, in cmux, or as a line to paste. A
// hosted session starts from this process's environment, which is the login shell's (the
// desktop app loads it at launch, and conn serve runs in one); the host takes Conn's own
// variables out of it.
func newOpener(cfg config.Config, store *settings.Store, host *pty.Host) launch.Opener {
	return launch.New(launch.Deps{
		Sessions: func() settings.Sessions { return store.Get().Sessions },
		Start:    host.Start,
		Cmux:     cmux.Open(cfg.CmuxBin),
		LookPath: exec.LookPath,
		Environ:  os.Environ,
	})
}
