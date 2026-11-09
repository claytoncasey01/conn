package shellenv

import (
	"context"
	"time"
)

// Marker is the text that starts the environment in the shell's output.
const Marker = marker

// MarkerEnd is the text that follows the last entry.
const MarkerEnd = markerEnd

// MaxOutputBytes is the cap on what the shell may print.
const MaxOutputBytes = maxOutputBytes

// Script is the command the login shell is asked to run.
const Script = script

// LoadWith is loadWith, so a test can use a time limit shorter than the real one.
func LoadWith(
	ctx context.Context, shell, exe string, base []string, limit time.Duration,
) ([]string, error) {
	return loadWith(ctx, shell, exe, base, limit)
}

// Parse is parse, so a test states the rule on plain values.
func Parse(output []byte) ([]string, error) {
	return parse(output)
}

// Merge is merge.
func Merge(base, fromShell []string) []string {
	return merge(base, fromShell)
}

// Args is loginShellArgs.
func Args() []string {
	return loginShellArgs()
}
