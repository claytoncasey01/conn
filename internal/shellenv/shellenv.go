// Package shellenv reads the user's whole environment from their login shell. An app opened from
// the Dock gets the bare environment launchd gives it, with none of what the shell's startup files
// set (PATH, tokens, the folders gh, git and claude are found in). The app runs its own
// executable inside the login shell with --print-env, which prints the shell's environment after
// a marker, and takes that environment from the output.
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

	// outputBytesMax caps what the shell may print, banner and environment together.
	outputBytesMax = 1 << 20

	// marker starts the environment in the output. Its NUL bytes cannot be in a banner, so it
	// finds where the entries begin whatever the shell's startup files printed first.
	marker = "\x00CONN-ENV\x00"

	// exeVariable carries the executable's path to the shell, so the path is never part of the
	// script and its quoting is the same in fish, zsh and bash.
	exeVariable = "CONN_ENV_EXE"

	// script is what the login shell runs: the app, asked to print its environment.
	script = `"$` + exeVariable + `" --print-env`
)

// ErrNoEnvironment is the error for a shell whose output holds no marker: it did not run the app.
var ErrNoEnvironment = errors.New("the login shell printed no environment")

// Print writes marker, then each entry of environ followed by NUL: what the app prints for
// --print-env. It returns the first write error.
func Print(out io.Writer, environ []string) error {
	_, err := io.WriteString(out, marker)
	if err != nil {
		return fmt.Errorf("writing the marker: %w", err)
	}

	for _, entry := range environ {
		_, err = io.WriteString(out, entry+"\x00")
		if err != nil {
			return fmt.Errorf("writing an entry: %w", err)
		}
	}

	return nil
}

// Parse is the entries between the first marker and the end of output. Anything before the marker
// (a banner) is ignored, and an entry with no '=' or an empty name is dropped. It returns
// ErrNoEnvironment when the output holds no marker.
func Parse(output []byte) ([]string, error) {
	_, after, found := bytes.Cut(output, []byte(marker))
	if !found {
		return nil, ErrNoEnvironment
	}

	entries := make([]string, 0)

	for raw := range bytes.SplitSeq(after, []byte{0}) {
		entry := string(raw)

		// An index of 0 is an empty name and -1 is no '='; both are junk, not an entry.
		if strings.Index(entry, "=") > 0 {
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

// Merge is base with each entry of fromShell replacing the entry of the same name or being added
// after them. The order of base is kept, and base is not changed.
func Merge(base, fromShell []string) []string {
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

// name is the variable name of an entry: what comes before the first '='.
func name(entry string) string {
	before, _, _ := strings.Cut(entry, "=")

	return before
}

// Args are the arguments that make shell run the script as a login, interactive shell. Fish, zsh,
// bash and sh all take the same flags, so the shell's name does not change them; the parameter is
// the one place a shell that needs different flags would be told apart.
func Args(_ string) []string {
	return []string{"-l", "-i", "-c", script}
}

// Load runs shell as a login shell that runs exe with --print-env and returns base with the
// printed environment merged over it. exe goes to the shell in its environment, never into the
// script. On a timeout, an exit error or an output without an environment it returns base
// unchanged and an error saying why.
func Load(ctx context.Context, shell, exe string, base []string) ([]string, error) {
	return loadWith(ctx, shell, exe, base, timeout)
}

// loadWith is Load with the time limit as a parameter.
func loadWith(
	ctx context.Context, shell, exe string, base []string, limit time.Duration,
) ([]string, error) {
	result := run.Cmd(ctx, shell, Args(shell), run.Opts{
		Dir:            "",
		Timeout:        limit,
		OutputBytesMax: outputBytesMax,
		Stdin:          "",
		Env:            append(slices.Clone(base), exeVariable+"="+exe),
	})
	if result.Err != nil {
		return base, fmt.Errorf("running the login shell %q: %w", shell, result.Err)
	}

	fromShell, err := Parse([]byte(result.Stdout))
	if err != nil {
		return base, fmt.Errorf("reading the environment of the login shell %q: %w", shell, err)
	}

	return Merge(base, fromShell), nil
}
