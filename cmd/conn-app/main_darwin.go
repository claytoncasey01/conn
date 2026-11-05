// Command conn-app is Conn as a macOS app: a window on the Conn server, kept in the Dock. It owns
// a server it starts in-process when no Conn holds the data folder, and attaches to the one that
// does otherwise (internal/desk has the rules). It reads the login shell's environment while the
// splash shows, before the server starts, because an app the Dock starts has none of what the
// shell's startup files set.
//
//	conn-app              open the window
//	conn-app --print-env  print this process's environment for the login shell run (shellenv)
//
// The package is darwin only: every file carries the _darwin suffix, so on other systems
// go build ./... skips it and the CLI stays pure Go.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/claytoncasey01/conn/internal/shellenv"
)

// Exit codes.
const (
	exitOK    = 0
	exitError = 1
)

const (
	// printEnvFlag is the argument shellenv.Load runs this program with inside the login shell.
	printEnvFlag = "--print-env"

	// shellDefault is the login shell when $SHELL is empty: the one macOS gives a new account.
	shellDefault = "/bin/zsh"
)

// main is the only place that touches the process: its arguments, environment, output streams
// and exit code. --print-env is answered before any window code runs, because the login shell
// runs this same program to print its environment.
func main() {
	if printEnvAsked(os.Args) {
		os.Exit(printEnv())
	}

	os.Exit(run(os.LookupEnv, os.Stderr))
}

// printEnvAsked says whether the program was run with --print-env: args is os.Args, so the
// program's name comes first.
func printEnvAsked(args []string) bool {
	if len(args) <= 1 {
		return false
	}

	return args[1] == printEnvFlag
}

// printEnv writes the environment for shellenv to read, and is the exit code.
func printEnv() int {
	err := shellenv.Print(os.Stdout, os.Environ())
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "conn-app: %v\n", err)

		return exitError
	}

	return exitOK
}

// loadEnvironment sets this process's environment to the login shell's merged over its own, so
// the server and every program it starts inherit it. It is the one place the environment is
// set, and it runs before the server starts. It returns a short reason the shell's environment
// could not be read, for the notice; the process then keeps the environment it started with. The
// environment itself is never printed, and neither is anything the shell printed.
//
// The window is up by now, so other threads may read the environment: the variables are set on
// the main thread, where Cocoa reads them, to keep setenv from racing those reads.
func loadEnvironment(ctx context.Context) error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = shellDefault
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding this program to run in the login shell: %w", err)
	}

	environ, loadErr := shellenv.Load(ctx, shell, exe, os.Environ())

	// A quit while the shell ran ends ctx, and the app is going: nothing is set, because the main
	// thread InvokeSync waits for may be the one waiting for this goroutine to end.
	if ctx.Err() != nil {
		return fmt.Errorf("reading the login shell's environment: %w", ctx.Err())
	}

	var setErr error

	application.InvokeSync(func() { setErr = setEnvironment(environ) })

	return errors.Join(loadErr, setErr)
}

// setEnvironment sets each NAME=VALUE entry of environ in this process, and returns every entry
// that could not be set.
func setEnvironment(environ []string) error {
	var problems []error

	for _, entry := range environ {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}

		err := os.Setenv(name, value)
		if err != nil {
			problems = append(problems, fmt.Errorf("setting %s: %w", name, err))
		}
	}

	return errors.Join(problems...)
}
