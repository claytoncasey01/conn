package historyapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/history"
	"github.com/claytoncasey01/conn/internal/historyapi"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/safefolder"
	"github.com/claytoncasey01/conn/internal/settings"
)

const resumedID = "0f8fad5b-d9cb-469f-a165-70867728950e"

// resumeRig is an archive holding one ended session in folder, and an API whose opener records
// the specs it is given and answers opened.
type resumeRig struct {
	api    *historyapi.API
	specs  []pty.Spec
	opened launch.Opened
}

func newResumeRig(t *testing.T, folder, name string, named bool) *resumeRig {
	t.Helper()

	archive, err := history.Open(t.Context(), filepath.Join(t.TempDir(), "history.db"),
		history.Options{
			OriginOf: func(string) string { return "" }, Now: func() float64 { return 1 },
		})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = archive.Close() })

	transcript := filepath.Join(t.TempDir(), resumedID+".jsonl")

	err = os.WriteFile(transcript, []byte("{}\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = archive.Upsert(t.Context(), history.Entry{
		SessionID: resumedID, Name: name, Cwd: folder, Kind: "session", Source: "transcript",
		TranscriptPath: transcript, Named: named, Final: true, Outcome: "ended",
	})
	if err != nil {
		t.Fatal(err)
	}

	rig := &resumeRig{
		api: nil, specs: nil,
		opened: launch.Opened{
			OK: true, Where: settings.WhereInside, HostedID: "", Place: "inside Conn", Tried: "",
			Error: "", ManualCommand: "",
		},
	}
	rig.api = historyapi.New(historyapi.Deps{
		DB: archive, Summarizer: nil,
		LiveIDs:     func(context.Context) map[string]bool { return map[string]bool{} },
		Allow:       nil,
		CheckFolder: safefolder.Check,
		Open: func(_ context.Context, spec pty.Spec) launch.Opened {
			rig.specs = append(rig.specs, spec)

			return rig.opened
		},
		Claude: "/opt/claude/bin/claude", Dry: func() bool { return false },
		Now: time.Now, ResumeCooldown: time.Minute,
	})

	return rig
}

func (r *resumeRig) resume(t *testing.T) historyapi.Reply {
	t.Helper()

	reply, err := r.api.Handle(t.Context(),
		historyapi.Route{Kind: historyapi.RouteResume, ID: resumedID, AgentID: ""}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	return reply
}

// TestAResumeStartsClaudeOnItsConversationInItsFolder: the archived session's folder is the
// spec's folder (a hostile name included, never an argument), its name is reduced to a safe
// word, and the arguments are golden.
func TestAResumeStartsClaudeOnItsConversationInItsFolder(t *testing.T) {
	t.Parallel()

	folder := filepath.Join(realTemp(t), "it's $(id) `x`\n--dangerously-skip-permissions")

	err := os.MkdirAll(folder, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	rig := newResumeRig(t, folder, "fix; rm -rf ~ $(id)", true)

	reply := rig.resume(t)
	if reply.Code != http.StatusOK {
		t.Fatalf("code %d: %+v", reply.Code, reply.Body)
	}

	if len(rig.specs) != 1 {
		t.Fatalf("opened %d", len(rig.specs))
	}

	spec := rig.specs[0]
	if spec.Dir != folder {
		t.Errorf("folder %q", spec.Dir)
	}

	spec.Dir = "/work/the-folder"

	got, err := json.MarshalIndent(map[string]any{
		"argv": spec.Argv, "dir": spec.Dir, "name": spec.Name,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join("testdata", "argv-history-resume.json")

	if os.Getenv("CONN_UPDATE_GOLDEN") == "1" {
		err = os.WriteFile(path, append(got, '\n'), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(got)+"\n" != string(want) {
		t.Errorf("the resume's argv changed:\n%s\nwant:\n%s", got, want)
	}
}

// TestAResumeThatDidNotStartGivesTheLineToPaste: a session set to open as a command (or one
// that failed to start) answers ok:false with the opener's words and the line to paste.
func TestAResumeThatDidNotStartGivesTheLineToPaste(t *testing.T) {
	t.Parallel()

	rig := newResumeRig(t, realTemp(t), "a session", false)
	rig.opened = launch.Opened{
		OK: false, Where: settings.WhereCommand, HostedID: "", Place: "", Tried: "",
		Error: "run this", ManualCommand: "cd /w && claude --resume " + resumedID,
	}

	reply := rig.resume(t)

	var body []byte

	buffer := bytes.NewBuffer(nil)
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)

	err := encoder.Encode(reply.Body)
	if err != nil {
		t.Fatal(err)
	}

	body = bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))

	want := `{"ok":false,"error":"run this","manualCommand":"cd /w && claude --resume ` +
		resumedID + `"}`
	if string(body) != want {
		t.Errorf("body %s", body)
	}

	if len(rig.specs[0].Argv) != 3 {
		t.Errorf("an unnamed session is resumed without --name: %q", rig.specs[0].Argv)
	}
}
