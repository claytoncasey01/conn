package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestThePRBodyArrivesOnGhsStandardInput: what shWithInput is given reaches the program's
// standard input whole, as gh reads a body from "-": a stub gh writes what it read to a file.
// The body is longer than a pipe's buffer and holds characters of several bytes.
func TestThePRBodyArrivesOnGhsStandardInput(t *testing.T) {
	t.Parallel()

	folder := t.TempDir()
	stub := filepath.Join(folder, "gh")
	got := filepath.Join(folder, "body-read")

	err := os.WriteFile(stub, []byte("#!/bin/sh\ncat > '"+got+"'\n"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	body := strings.Repeat("## What changed\n\nUn café, s'il vous plaît.\n", 1000)

	_, err = shWithInput(t.Context(), stub, []string{"pr", "create", "--body-file", "-"},
		folder, body, 0)
	if err != nil {
		t.Fatal(err)
	}

	read, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}

	if len(read) == 0 {
		t.Fatal("gh read nothing on its standard input")
	}

	if string(read) != body {
		t.Errorf("gh read %d bytes, want the %d of the body", len(read), len(body))
	}
}
