// Package shellenv reads the user's whole environment from their login shell. An app opened from
// the Dock gets the bare environment launchd gives it, with none of what the shell's startup files
// set (PATH, tokens, the folders gh, git and claude are found in). The app runs its own
// executable inside the login shell with --print-env, which prints the shell's environment
// between two markers, and takes that environment from the output.
//
// The environment holds secrets, so nothing here logs it, writes it or puts it in an error: an
// error says only why the shell gave none (its exit code, the time limit, the output cap), never
// what the shell printed.
package shellenv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/claytoncasey01/conn/internal/run"
)

const (
	// timeout bounds the whole shell run: a profile that waits for input must not hold the app.
	timeout = 10 * time.Second

	// maxOutputBytes caps what the shell may print, banner and environment together.
	maxOutputBytes = 1 << 20

	// marker starts the environment in the output. Its NUL bytes cannot be in a banner, so it
	// finds where the entries begin whatever the shell's startup files printed first.
	marker = "\x00CONN-ENV\x00"

	// markerEnd follows the last entry, so a complete environment is told from one cut short
	// whatever the shell did after it (a job left running in the background that keeps the output
	// open, an exit code from a logout hook). No entry can hold it: an entry has no NUL.
	markerEnd = "\x00CONN-ENV-END\x00"

	// exeVariable carries the executable's path to the shell, so the path is never part of the
	// script and its quoting is the same in fish, zsh and bash.
	exeVariable = "CONN_ENV_EXE"

	// script is what the login shell runs: the app, asked to print its environment.
	script = `"$` + exeVariable + `" --print-env`
)

// The reasons Load gives when the shell's environment could not be read. Each is short and says
// nothing the shell printed, so it can be shown on screen and written to a log.
var (
	// ErrNoEnvironment is a shell whose output holds no complete environment: no marker (it did
	// not run the app), or no end marker (the app's output was cut short).
	ErrNoEnvironment = errors.New("printed no environment")
	// ErrOutputTooLarge is a shell that printed more than maxOutputBytes.
	ErrOutputTooLarge = errors.New("printed too much")
	// ErrTimedOut is a shell that did not finish within the time limit.
	ErrTimedOut = errors.New("timed out")
	// ErrShellFailed is a shell that exited with an error, was stopped by a signal, or could not
	// be started.
	ErrShellFailed = errors.New("failed")
)

// Print writes marker, then each NAME=VALUE entry of environ followed by NUL, then markerEnd:
// what the app prints for --print-env. An entry with no '=' or an empty name is left out, as
// parse would drop it. It returns the first write error.
func Print(out io.Writer, environ []string) error {
	_, err := io.WriteString(out, marker)
	if err != nil {
		return fmt.Errorf("writing the marker: %w", err)
	}

	for _, entry := range environ {
		if !isEntry(entry) {
			continue
		}

		_, err = io.WriteString(out, entry+"\x00")
		if err != nil {
			return fmt.Errorf("writing an entry: %w", err)
		}
	}

	_, err = io.WriteString(out, markerEnd)
	if err != nil {
		return fmt.Errorf("writing the end marker: %w", err)
	}

	return nil
}

// parse is the NUL-terminated entries between the first marker and the first markerEnd after
// it. Anything before the marker (a banner) or after the end marker (what a shell prints as it
// exits, such as a logout message or a terminal escape) is ignored. An entry with no '=' or an
// empty name is dropped. It returns ErrNoEnvironment when the output holds no marker, or no end
// marker after it: an environment cut short is not used, because it may lack PATH or a token.
func parse(output []byte) ([]string, error) {
	_, after, found := bytes.Cut(output, []byte(marker))
	if !found {
		return nil, ErrNoEnvironment
	}

	between, _, found := bytes.Cut(after, []byte(markerEnd))
	if !found {
		return nil, ErrNoEnvironment
	}

	// Cutting at the last NUL drops text no entry's NUL ends, which Print never writes.
	terminated := between[:max(bytes.LastIndexByte(between, 0), 0)]
	entries := make([]string, 0)

	for raw := range bytes.SplitSeq(terminated, []byte{0}) {
		entry := string(raw)
		if isEntry(entry) {
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

// isEntry says whether entry is NAME=VALUE with a name: an index of 0 is an empty name and -1 is
// no '='; both are junk, not an entry.
func isEntry(entry string) bool {
	return strings.Index(entry, "=") > 0
}

// merge is base with each entry of fromShell replacing the entry of the same name or being added
// after them. The order of base is kept, and base is not changed.
func merge(base, fromShell []string) []string {
	merged := slices.Clone(base)
	places := make(map[string]int, len(base)+len(fromShell))

	for place, entry := range merged {
		places[name(entry)] = place
	}

	for _, entry := range fromShell {
		place, found := places[name(entry)]
		if found {
			merged[place] = entry

			continue
		}

		places[name(entry)] = len(merged)
		merged = append(merged, entry)
	}

	return merged
}

// withoutExeVariable is entries without exeVariable. The app set it only to be run in the shell,
// and the shell's environment printed it back, so merging it would leak the path of the app's
// own executable into every program the app starts.
func withoutExeVariable(entries []string) []string {
	return slices.DeleteFunc(entries, func(entry string) bool {
		return name(entry) == exeVariable
	})
}

// name is the variable name of an entry: what comes before the first '='.
func name(entry string) string {
	before, _, _ := strings.Cut(entry, "=")

	return before
}

// loginShellArgs are the arguments that make a shell run the script as a login, interactive
// shell. Fish, zsh, bash and sh all take the same flags, so the shell's name does not change
// them.
func loginShellArgs() []string {
	return []string{"-l", "-i", "-c", script}
}

// Load runs shell as a login shell that runs exe with --print-env and returns base with the
// printed environment merged over it. exe goes to the shell in its environment, never into the
// script. A complete environment (both markers, within the output cap) is used however the shell
// ended, because a job a profile leaves in the background keeps the output open and fails the
// run after the environment was printed. Otherwise it returns base unchanged and a short error
// saying why, wrapping one of the Err values above.
func Load(ctx context.Context, shell, exe string, base []string) ([]string, error) {
	return loadWith(ctx, shell, exe, base, timeout)
}

// loadWith is Load with the time limit as a parameter.
func loadWith(
	ctx context.Context, shell, exe string, base []string, limit time.Duration,
) ([]string, error) {
	result := run.Cmd(ctx, shell, loginShellArgs(), run.Opts{
		Dir:            "",
		Timeout:        limit,
		MaxOutputBytes: maxOutputBytes,
		Stdin:          "",
		Env:            append(slices.Clone(base), exeVariable+"="+exe),
	})
	if result.Overflowed != "" {
		return base, reason(shell, fmt.Errorf("%w (more than %d bytes)", ErrOutputTooLarge,
			maxOutputBytes))
	}

	fromShell, parseErr := parse([]byte(result.Stdout))
	if parseErr == nil {
		return merge(base, withoutExeVariable(fromShell)), nil
	}

	return base, reason(shell, why(result, limit, parseErr))
}

// why is the short reason a run gave no environment: how the shell ended when that was an
// error, else what parse said. It never holds what the shell printed: run's own error carries
// the shell's error output, which a profile with tracing on fills with its exports.
func why(result run.Result, limit time.Duration, parseErr error) error {
	switch {
	case result.Killed:
		return fmt.Errorf("%w after %s", ErrTimedOut, limit)
	case result.Signal != "":
		return fmt.Errorf("%w: stopped by %s", ErrShellFailed, result.Signal)
	case result.Code > 0:
		return fmt.Errorf("%w with exit code %d", ErrShellFailed, result.Code)
	case result.OutputLeftOpen:
		return parseErr
	case result.Spawn != "":
		return fmt.Errorf("%w: could not be started", ErrShellFailed)
	default:
		return parseErr
	}
}

// reason names the shell in a short reason.
func reason(shell string, err error) error {
	return fmt.Errorf("login shell %s: %w", shell, err)
}
