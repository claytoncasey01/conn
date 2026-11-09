// Package cmux starts a command in a new cmux workspace. The command string is run by a shell, so
// everything that goes into one here is written by argv.ShellLine, and a name is also reduced to
// a safe alphabet first.
package cmux

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/claytoncasey01/conn/internal/argv"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/run"
)

const (
	nameCap    = 64
	idShown    = 8
	errorUnits = 400
	timeout    = 20 * time.Second
	maxOutput  = 8 << 20
)

var (
	sessionID = regexp.MustCompile(`^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$`)
	unsafe    = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	dashes    = regexp.MustCompile(`-{2,}`)
	// ErrNotSessionID refuses an id that is not a UUID before it can reach a command.
	ErrNotSessionID = errors.New("not a session id")
)

// IsSessionID is whether s is a session id: a session id from a URL or a file is only ever a
// UUID.
func IsSessionID(s string) bool { return sessionID.MatchString(s) }

// WorkspaceName is a name reduced to [A-Za-z0-9._-]: every other run of characters becomes one
// '-', and no name starts with '-' or '.' (it would read as a flag or a hidden file) or ends with
// one. Nothing left means the first 8 characters of the id.
func WorkspaceName(name, sessionID string) string {
	safe := dashes.ReplaceAllString(unsafe.ReplaceAllString(name, "-"), "-")
	safe = js.Cut(safe, nameCap)
	safe = strings.TrimRight(strings.TrimLeft(safe, "-."), "-.")

	if safe == "" {
		return js.Cut(sessionID, idShown)
	}

	return safe
}

// ResumeArgs are the arguments to claude that reopen an archived session. name is the session's
// own name, or nil when it has none (the archive then called it by its first prompt): --name is
// left off so the resume does not rename it.
func ResumeArgs(sessionID string, name *string) ([]string, error) {
	if !IsSessionID(sessionID) {
		return nil, ErrNotSessionID
	}

	if name == nil {
		return []string{"--resume", sessionID}, nil
	}

	return []string{"--resume", sessionID, "--name", WorkspaceName(*name, sessionID)}, nil
}

// ResumeCommand is ResumeArgs as the command a person reads: claude and its arguments, one
// shell line.
func ResumeCommand(sessionID string, name *string) (string, error) {
	args, err := ResumeArgs(sessionID, name)
	if err != nil {
		return "", err
	}

	return argv.ShellLine(append([]string{"claude"}, args...)), nil
}

// Opened is how a start went: ok, or why not with the command to paste instead.
type Opened struct {
	OK            bool
	Error         string
	ManualCommand string
}

// textNotTypable is why a command was not handed to cmux.
const textNotTypable = "the command (the program and its arguments, the session's name " +
	"among them) holds a character other than printable ASCII, which cmux would type into a " +
	"shell: open sessions inside Conn instead"

// Opener starts a command in a new cmux workspace.
type Opener func(ctx context.Context, name, cwd, command string) Opened

// Open starts command in a new cmux workspace in cwd, without focusing it, with the cmux program
// bin; a command holding a byte other than printable ASCII is not handed to cmux, which types it
// into a shell. Only the command is checked: the folder is handed to cmux as its --cwd argument,
// not as part of the command, so Conn types nothing with it. Whether cmux itself types a cd with
// it is cmux's own (cmux was not available to check). When cmux fails the person gets the
// command to paste: the folder through argv.ShellLine, so it is one word whatever it holds,
// unless the line holds a byte other than printable ASCII (argv.PastableLine). cwd and name go
// to cmux as arguments, never through a shell here.
func Open(bin string) Opener {
	return func(ctx context.Context, name, cwd, command string) Opened {
		// cmux types the command into a shell in the new workspace, where a line editor acts on
		// a byte other than printable ASCII before any quote is read: it is not handed over.
		if !argv.PastableLine(command) {
			return Opened{OK: false, Error: textNotTypable, ManualCommand: ""}
		}

		result := run.Cmd(ctx, bin, []string{
			"workspace", "create", "--name", name, "--cwd", cwd, "--command", command,
			"--focus", "false",
		}, run.Opts{Dir: "", Timeout: timeout, MaxOutputBytes: maxOutput, Stdin: "", Env: nil})
		if result.Err == nil {
			return Opened{OK: true, Error: "", ManualCommand: ""}
		}

		why := result.Stderr
		if why == "" {
			why = result.Err.Error()
		}

		why = js.Cut(js.Trim(why), errorUnits)

		line := argv.ShellLine([]string{"cd", cwd}) + " && " + command

		// A byte other than printable ASCII would reach a line editor before any quote is
		// read: no line.
		if !argv.PastableLine(line) {
			return Opened{
				OK: false, Error: why + "; the folder or the command holds a character other " +
					"than printable ASCII, so there is no command to paste", ManualCommand: "",
			}
		}

		return Opened{OK: false, Error: why, ManualCommand: line}
	}
}
