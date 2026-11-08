package reviews_test

import (
	"context"

	"github.com/claytoncasey01/conn/internal/argv"
	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/settings"
)

// asCmux is an Opener that opens every session as cmux does where the settings say "My
// terminal" with no command of the person's own: open is given the session's name, folder and
// its arguments as one shell line, and its answer is the launch's.
func asCmux(open cmux.Opener) launch.Opener {
	return func(ctx context.Context, spec pty.Spec) launch.Opened {
		opened := open(ctx, spec.Name, spec.Dir, argv.ShellLine(spec.Argv))

		return launch.Opened{
			OK: opened.OK, Where: settings.WhereTerminal, HostedID: "",
			Place: "in a new cmux workspace", Tried: "a cmux workspace", Error: opened.Error,
			ManualCommand: opened.ManualCommand,
		}
	}
}
