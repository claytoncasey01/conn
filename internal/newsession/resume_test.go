package newsession_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/history"
	"github.com/claytoncasey01/conn/internal/historyapi"
	"github.com/claytoncasey01/conn/internal/hosted"
	"github.com/claytoncasey01/conn/internal/newsession"
	"github.com/claytoncasey01/conn/internal/pty"
)

// startRecorded starts a session in the rig through a recorder, as the app wires it (the History
// owner's opener over the settings' opener), and is the record it wrote.
func startRecorded(t *testing.T, setting *rig) hosted.Record {
	t.Helper()

	recorder := hosted.NewRecorder(hosted.RecorderDeps{
		Dir: t.TempDir(),
		Live: func() []pty.Info {
			return []pty.Info{{ID: hostedID, Name: "", Dir: "", PID: 0}}
		},
		Linked: func() map[pty.ID]string { return map[pty.ID]string{} },
		Write:  func(string, []hosted.Record) error { return nil },
		Failed: func(err error) { t.Errorf("not recorded: %v", err) },
	})
	deps := setting.deps()
	// The rig's opener stands for the settings' opener; the recorder wraps it as the app does.
	deps.Open = recorder.Opener(hosted.OwnerHistory, deps.Open)

	reply := start(t, newsession.New(deps), `{"repo":"acme/widgets","name":"fix the widget"}`)
	if reply.Code != http.StatusOK {
		t.Fatalf("code %d", reply.Code)
	}

	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("recorded %+v, want one session", records)
	}

	return records[0]
}

func TestAStartIsRecordedAsAHistorySessionOnItsConversation(t *testing.T) {
	t.Parallel()

	setting := newRig(t)
	record := startRecorded(t, setting)

	realFolder, err := filepath.EvalSymlinks(setting.clones[allowedRepo])
	if err != nil {
		t.Fatal(err)
	}

	if record.Owner != hosted.OwnerHistory {
		t.Errorf("owner %q, want %q", record.Owner, hosted.OwnerHistory)
	}

	if record.ClaudeSessionID != conversationID {
		t.Errorf("conversation %q, want %q", record.ClaudeSessionID, conversationID)
	}

	if record.Dir != realFolder {
		t.Errorf("folder %q, want %q", record.Dir, realFolder)
	}

	if hosted.CheckRecord(record) != nil {
		t.Errorf("a record the file refuses: %v", hosted.CheckRecord(record))
	}
}

// TestALaunchResumesAStartOnceItHasATranscript: History's check at launch resumes the record once
// the archive holds the conversation in the record's folder, and not before.
func TestALaunchResumesAStartOnceItHasATranscript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		archived bool
		want     bool
	}{
		{"with a transcript", true, true},
		{"before claude wrote one", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			setting := newRig(t)
			record := startRecorded(t, setting)
			archive := openArchive(t)

			if tt.archived {
				archiveConversation(t, archive, record.Dir)
			}

			check := historyapi.New(historyapi.Deps{
				DB: archive, Summarizer: nil,
				LiveIDs: func(context.Context) map[string]bool { return map[string]bool{} },
				Allow:   func() []string { return setting.current.Repos.Allow },
				RepoOf:  func(string) string { return allowedRepo },
				Open:    nil, Claude: claudeProgram, Dry: func() bool { return false },
				Now: time.Now, ResumeCooldown: 0,
			})

			spec, resumed := check.ResumeAtLaunch(t.Context(), record.ClaudeSessionID, record.Dir)
			if resumed != tt.want {
				t.Fatalf("resumed %v, want %v", resumed, tt.want)
			}

			if resumed {
				if spec.Dir != record.Dir {
					t.Errorf("resumed in %q, want %q", spec.Dir, record.Dir)
				}
			}
		})
	}
}

func openArchive(t *testing.T) *history.DB {
	t.Helper()

	archive, err := history.Open(t.Context(), filepath.Join(t.TempDir(), "history.db"),
		history.Options{
			OriginOf: func(string) string { return "" }, Now: func() float64 { return 1 },
		})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = archive.Close() })

	return archive
}

// archiveConversation is the archive's row of the started conversation, as claude reports it: in
// folder, with a transcript.
func archiveConversation(t *testing.T, archive *history.DB, folder string) {
	t.Helper()

	transcript := filepath.Join(t.TempDir(), conversationID+".jsonl")

	err := os.WriteFile(transcript, []byte("{}\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = archive.Upsert(t.Context(), history.Entry{
		SessionID: conversationID, Name: "fix-the-widget", Cwd: folder, Kind: "session",
		Source: "transcript", TranscriptPath: transcript, Named: true, Final: true,
		Outcome: "ended",
	})
	if err != nil {
		t.Fatal(err)
	}
}
