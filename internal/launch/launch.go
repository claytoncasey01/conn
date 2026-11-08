// Package launch starts the sessions Conn's modules open (an Open work build, a review, a
// self-review, a resumed History session) where the person's settings say: on a terminal Conn
// hosts, in a new cmux workspace, or not at all, with the command shown for the person to paste.
// No setting names a program: the terminal app is cmux (CONN_CMUX) and the program is claude
// (CONN_CLAUDE), each from the environment Conn was started with.
//
// A module says what to run as a pty.Spec: the program and its arguments as a list, never a
// shell string, the folder, and a name. Nothing here adds a shell. The program runs as it is on
// Conn's terminal, so the process Conn starts is the session's own (the session list finds a
// hosted session by its process id); cmux and a pasted line get the arguments as one
// shell line from argv.ShellLine, the only code that quotes for a shell, so a value cannot end
// its word or run anything there either.
package launch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/claytoncasey01/conn/internal/argv"
	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	// argumentBytesMax bounds one argument, and the folder: the most one argument may hold on
	// Linux (MAX_ARG_STRLEN), so a session macOS would start also starts there.
	argumentBytesMax = 128 << 10

	// The places a session opens, as the logs say them.
	placeInside = "inside Conn"
	placeCmux   = "in a new cmux workspace"

	// What a start that failed tried to open, as the logs say it.
	triedInside = "a session inside Conn"
	triedCmux   = "a cmux workspace"

	// textNotPastable is why a line to paste is not offered.
	textNotPastable = "; the folder, the name or an argument holds a character other than " +
		"printable ASCII, so there is no command to paste: a shell's line editor can act on it " +
		"before any quote is read"

	// commandShown is what a session set to open as a command says instead of an error.
	commandShown = "sessions open as a command (Settings, Where sessions open): run this in a " +
		"terminal to start the session"
)

var (
	// ErrInvalidSession is a spec no session may be started from; each problem wraps it.
	ErrInvalidSession = errors.New("cannot start the session")
	// ErrNoProgram is a program that is not found, or not found at an absolute path.
	ErrNoProgram = errors.New("the program was not found")
)

// Opened is how a start went. OK is a session that started, inside Conn (HostedID is the host's
// id for it) or in a terminal app. Otherwise ManualCommand is the line to paste, when there is
// one, and Error says why (for WhereCommand, that the setting asks for the line). Place says
// where it opened and Tried what a failed start tried, each as a log line words it.
type Opened struct {
	OK            bool
	Where         string
	HostedID      pty.ID
	Place         string
	Tried         string
	Error         string
	ManualCommand string
}

// Opener starts a session from spec where the settings say, and says how it went. It never
// fails other than through Opened.
type Opener func(ctx context.Context, spec pty.Spec) Opened

// Deps are what an Opener reads and calls.
type Deps struct {
	// Sessions are the sessions settings now: read at each start.
	Sessions func() settings.Sessions
	// Start starts a session on a terminal Conn hosts (pty.Host.Start).
	Start func(ctx context.Context, spec pty.Spec) (pty.ID, error)
	// Cmux opens a command in a new cmux workspace.
	Cmux cmux.Opener
	// LookPath finds a program named without a folder, as exec.LookPath does.
	LookPath func(file string) (string, error)
	// Environ is the environment a hosted session starts from (the login environment);
	// pty.ChildEnv takes Conn's own variables out of it.
	Environ func() []string
}

// New is an Opener over deps.
func New(deps Deps) Opener {
	return func(ctx context.Context, spec pty.Spec) Opened {
		where := deps.Sessions()

		err := Check(spec)
		if err != nil {
			return Opened{
				OK: false, Where: where.Where, HostedID: "", Place: "", Tried: triedFor(where),
				Error: err.Error(), ManualCommand: "",
			}
		}

		switch where.Where {
		case settings.WhereInside:
			return startInside(ctx, deps, spec)
		case settings.WhereTerminal:
			return startInTerminal(ctx, deps, spec)
		case settings.WhereCommand:
			line, why := pasteOffer(spec)

			return Opened{
				OK: false, Where: settings.WhereCommand, HostedID: "", Place: "", Tried: "",
				Error: commandShown + why, ManualCommand: line,
			}
		}

		panic(fmt.Sprintf("launch: sessions.where %q is none of the choices settings allows",
			where.Where))
	}
}

// Check returns every problem with spec, each wrapping ErrInvalidSession, or nil: a program and
// a folder, a name, no NUL anywhere (no program can be given one), no other control character,
// bidi control or bytes that are not UTF-8 anywhere (argv.Pastable: each would reach the line to
// paste), and nothing longer than argumentBytesMax. It never quotes a value.
func Check(spec pty.Spec) error {
	var problems []error

	if len(spec.Argv) == 0 {
		problems = append(problems, fmt.Errorf("%w: there is no program to run", ErrInvalidSession))
	}

	for index, argument := range spec.Argv {
		problems = append(problems, checkText(fmt.Sprintf("argument %d", index), argument)...)
	}

	if !filepath.IsAbs(spec.Dir) {
		problems = append(problems, fmt.Errorf("%w: the folder is not an absolute path",
			ErrInvalidSession))
	}

	problems = append(problems, checkText("the folder", spec.Dir)...)
	problems = append(problems, checkShown("the folder", spec.Dir)...)

	if spec.Name == "" {
		problems = append(problems, fmt.Errorf("%w: the session has no name", ErrInvalidSession))
	}

	problems = append(problems, checkText("the name", spec.Name)...)
	problems = append(problems, checkShown("the name", spec.Name)...)

	return errors.Join(problems...)
}

// checkText is what is wrong with one value of a spec, named what.
func checkText(what, value string) []error {
	var problems []error

	if strings.ContainsRune(value, 0) {
		problems = append(problems, fmt.Errorf("%w: %s holds a NUL byte", ErrInvalidSession, what))
	}

	if len(value) > argumentBytesMax {
		problems = append(problems, fmt.Errorf("%w: %s is %d bytes, more than %d",
			ErrInvalidSession, what, len(value), argumentBytesMax))
	}

	// A NUL is said above; any other control, a bidi control or bytes that are not UTF-8 would
	// reach the line to paste, where a line editor acts on them before any quote is read.
	if !argv.Pastable(strings.ReplaceAll(value, "\x00", "")) {
		problems = append(problems, fmt.Errorf("%w: %s holds a control character or is not "+
			"UTF-8", ErrInvalidSession, what))
	}

	return problems
}

// checkShown is what is wrong with a value people read (the folder, the name): a zero-width or
// tag character, which makes it shown differently from what it is.
func checkShown(what, value string) []error {
	for _, char := range value {
		if argv.Invisible(char) {
			return []error{fmt.Errorf("%w: %s holds a character that draws nothing",
				ErrInvalidSession, what)}
		}
	}

	return nil
}

// PasteLine is the line a person pastes into their own shell to start spec: into its folder,
// then its program, each word through argv.ShellLine. The folder is absolute, so cd never reads
// it as an option.
func PasteLine(spec pty.Spec) string {
	return argv.ShellLine([]string{"cd", spec.Dir}) + " && " + argv.ShellLine(spec.Argv)
}

// pasteOffer is the line to paste for spec, or none and why (to add to the words already
// shown): a line is offered only when every byte is printable ASCII (argv.PastableLine).
func pasteOffer(spec pty.Spec) (string, string) {
	line := PasteLine(spec)
	if argv.PastableLine(line) {
		return line, ""
	}

	return "", textNotPastable
}

// startInside starts spec on a terminal Conn hosts. The program is run as it is when it is an
// absolute path, and found on PATH otherwise; it must be found at an absolute path, so the
// process the host starts is the program itself.
func startInside(ctx context.Context, deps Deps, spec pty.Spec) Opened {
	failed := func(err error) Opened {
		line, why := pasteOffer(spec)

		return Opened{
			OK: false, Where: settings.WhereInside, HostedID: "", Place: "", Tried: triedInside,
			Error: err.Error() + why, ManualCommand: line,
		}
	}

	program, err := findProgram(deps.LookPath, spec.Argv[0])
	if err != nil {
		return failed(err)
	}

	hosted := pty.Spec{
		Argv: append([]string{program}, spec.Argv[1:]...), Dir: spec.Dir, Env: deps.Environ(),
		Name: spec.Name,
	}

	hostedID, err := deps.Start(ctx, hosted)
	if err != nil {
		return failed(fmt.Errorf("starting the session inside Conn: %w", err))
	}

	return Opened{
		OK: true, Where: settings.WhereInside, HostedID: hostedID, Place: placeInside,
		Tried: "", Error: "", ManualCommand: "",
	}
}

// findProgram is program as an absolute path: as it is, or found on PATH. It must be claude
// itself, or a wrapper that execs it in its place: the session list links a hosted session to
// claude's registry by the process id the host started, so a wrapper that starts claude as a
// child leaves the row unlinked (the page says so) and its replies and answers refused.
func findProgram(lookPath func(string) (string, error), program string) (string, error) {
	if filepath.IsAbs(program) {
		return program, nil
	}

	found, err := lookPath(program)
	if err != nil {
		return "", fmt.Errorf("%w: %s is not on PATH; set CONN_CLAUDE to the absolute path of "+
			"claude itself (a wrapper must exec claude, not start it as a child)", ErrNoProgram,
			program)
	}

	if !filepath.IsAbs(found) {
		return "", fmt.Errorf("%w: %s was found at a relative path; set CONN_CLAUDE to the "+
			"absolute path of claude itself (a wrapper must exec claude, not start it as a child)",
			ErrNoProgram, program)
	}

	return found, nil
}

// startInTerminal opens spec in a new cmux workspace, with the program as one shell line; when
// cmux fails, cmux leaves the line to paste.
func startInTerminal(ctx context.Context, deps Deps, spec pty.Spec) Opened {
	opened := deps.Cmux(ctx, spec.Name, spec.Dir, argv.ShellLine(spec.Argv))

	return Opened{
		OK: opened.OK, Where: settings.WhereTerminal, HostedID: "", Place: placeCmux,
		Tried: triedCmux, Error: opened.Error, ManualCommand: opened.ManualCommand,
	}
}

// triedFor is what a start in where would have tried.
func triedFor(where settings.Sessions) string {
	switch where.Where {
	case settings.WhereInside:
		return triedInside
	case settings.WhereTerminal:
		return triedCmux
	}

	return ""
}
