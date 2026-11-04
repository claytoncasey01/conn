package shellenv

import (
	"context"
	"time"
)

// Marker is the text that starts the environment in the shell's output.
const Marker = marker

// Script is the command the login shell is asked to run.
const Script = script

// LoadWith is loadWith, so a test can use a time limit shorter than the real one.
func LoadWith(
	ctx context.Context, shell, exe string, base []string, limit time.Duration,
) ([]string, error) {
	return loadWith(ctx, shell, exe, base, limit)
}
