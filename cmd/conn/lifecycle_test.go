package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/history"
	"github.com/claytoncasey01/conn/internal/hosted"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// A signal to `conn serve` is a full quit: the session it resumed at launch, a real program on a
// real terminal, is stopped, and the record still names it for the next launch.

const (
	signalSessionID = "0f8fad5b-d9cb-469f-a165-70867728950e"
	// lastLaunchID is the host's id of the session in the last launch's record.
	lastLaunchID = "000000000000000000000000000000aa"
	// standInClaude is the claude a child serve runs: it waits to be stopped.
	standInClaude = "#!/bin/sh\nexec sleep 60\n"
	// childExitWait bounds the wait for a signalled child to exit.
	childExitWait = 15 * time.Second
	scriptMode    = 0o700
	privateFile   = 0o600
)

// signalWorld is a data folder whose History holds one ended session in a folder of an allowed
// repository, recorded as running at the last quit, and the environment a child serve over it
// needs: its own home and a claude that waits.
func signalWorld(t *testing.T) (string, []string) {
	t.Helper()

	dataDir := t.TempDir()
	folder := filepath.Join(t.TempDir(), "widgets")
	claude := filepath.Join(t.TempDir(), "claude")

	writeFile(t, filepath.Join(folder, ".git", "config"),
		"[remote \"origin\"]\n\turl = git@github.com:acme/widgets.git\n", privateFile)
	writeFile(t, claude, standInClaude, scriptMode)
	writeFile(t, filepath.Join(dataDir, "settings.json"), `{"version":1,"repos":{"allow":`+
		`["acme/*"],"clones":{},"cloneRoot":null},"github":{"enabled":false}}`, privateFile)

	archiveSession(t, dataDir, folder)

	err := hosted.Write(dataDir, []hosted.Record{{
		ID: lastLaunchID, Owner: hosted.OwnerHistory, Name: "fix",
		Dir: folder, ClaudeSessionID: signalSessionID,
	}})
	if err != nil {
		t.Fatal(err)
	}

	return dataDir, []string{"HOME=" + t.TempDir(), "CONN_CLAUDE=" + claude}
}

func writeFile(t *testing.T, path, text string, mode os.FileMode) {
	t.Helper()

	err := os.MkdirAll(filepath.Dir(path), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(path, []byte(text), mode)
	if err != nil {
		t.Fatal(err)
	}
}

func archiveSession(t *testing.T, dataDir, folder string) {
	t.Helper()

	archive, err := history.Open(t.Context(), filepath.Join(dataDir, "history.db"), history.Options{
		OriginOf: func(string) string { return "acme/widgets" }, Now: func() float64 { return 1 },
	})
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = archive.Close() }()

	transcript := filepath.Join(t.TempDir(), signalSessionID+".jsonl")
	writeFile(t, transcript, "{}\n", privateFile)

	err = archive.Upsert(t.Context(), history.Entry{
		SessionID: signalSessionID, Name: "fix", Cwd: folder, Kind: "session",
		Source: "transcript", TranscriptPath: transcript, Named: true, Final: true,
		Outcome: "ended",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestASignalStopsTheSessionsAndKeepsTheirRecord(t *testing.T) {
	t.Parallel()

	for _, signal := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			t.Parallel()

			dataDir, extra := signalWorld(t)
			child, _ := startChild(t, dataDir, extra...)

			resumed := resumedRecord(t, dataDir)

			err := child.Process.Signal(signal)
			if err != nil {
				t.Fatal(err)
			}

			waitExit(t, child.Wait)

			after, err := hosted.Read(dataDir)
			if err != nil {
				t.Fatal(err)
			}

			if len(after) != 1 {
				t.Fatalf("the record after the quit holds %+v, want the resumed session", after)
			}

			if after[0].ID != resumed.ID {
				t.Errorf("the record names %q, want the resumed %q", after[0].ID, resumed.ID)
			}
		})
	}
}

// resumedRecord waits, within lockWait, for the record the child writes of the session it
// resumed at launch, in place of the last launch's.
func resumedRecord(t *testing.T, dataDir string) hosted.Record {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(lockWait))

	for time.Now().Before(deadline) {
		records, err := hosted.Read(dataDir)
		if err != nil {
			t.Fatal(err)
		}

		if len(records) == 1 {
			if records[0].ID != lastLaunchID {
				return records[0]
			}
		}

		time.Sleep(lockPoll)
	}

	t.Fatal("the child never recorded the session it resumed")

	return hosted.Record{}
}

// waitExit waits for a signalled child to exit cleanly, within childExitWait.
func waitExit(t *testing.T, wait func() error) {
	t.Helper()

	exited := make(chan error, 1)

	go func() { exited <- wait() }()

	select {
	case err := <-exited:
		if err != nil {
			t.Errorf("the child exited with %v, want 0", err)
		}
	case <-time.After(testtime.Scaled(childExitWait)):
		t.Fatal("the child did not exit")
	}
}
