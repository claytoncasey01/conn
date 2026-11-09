package historyapi_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/history"
	"github.com/claytoncasey01/conn/internal/historyapi"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/reviews"
)

const allowedRepo = "acme/widgets"

// launchRig is an archive with one ended session in folder, and the deps of an API over it,
// each of which a case may change before api builds it.
type launchRig struct {
	folder     string
	transcript string
	deps       historyapi.Deps
	opened     []pty.Spec
}

func newLaunchRig(t *testing.T) *launchRig {
	t.Helper()

	folder := filepath.Join(realTemp(t), "widgets")

	err := os.MkdirAll(folder, 0o700)
	if err != nil {
		t.Fatal(err)
	}

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
		SessionID: resumedID, Name: "fix the widget", Cwd: folder, Kind: "session",
		Source: "transcript", TranscriptPath: transcript, Named: true, Final: true,
		Outcome: "ended",
	})
	if err != nil {
		t.Fatal(err)
	}

	rig := &launchRig{folder: folder, transcript: transcript, deps: historyapi.Deps{}}
	rig.deps = historyapi.Deps{
		DB: archive, Summarizer: nil,
		LiveIDs:     func(context.Context) map[string]bool { return map[string]bool{} },
		Allow:       func() []string { return []string{allowedRepo} },
		RepoOf:      func(string) string { return allowedRepo },
		CheckFolder: reviews.CheckSafeFolder,
		Open: func(_ context.Context, spec pty.Spec) launch.Opened {
			rig.opened = append(rig.opened, spec)

			return launch.Opened{OK: true}
		},
		Claude: "/opt/claude/bin/claude", Dry: func() bool { return false },
		Now: time.Now, ResumeCooldown: time.Minute,
	}

	return rig
}

func (r *launchRig) api() *historyapi.API { return historyapi.New(r.deps) }

func TestResumeAtLaunchIsWhatResumeOpens(t *testing.T) {
	t.Parallel()

	rig := newLaunchRig(t)

	got, ok := rig.api().ResumeAtLaunch(t.Context(), resumedID, rig.folder)
	if !ok {
		t.Fatal("not resumed")
	}

	_, err := rig.api().Handle(t.Context(),
		historyapi.Route{Kind: historyapi.RouteResume, ID: resumedID, AgentID: ""}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(rig.opened) != 1 {
		t.Fatalf("RESUME opened %d sessions", len(rig.opened))
	}

	want := rig.opened[0]
	if !slices.Equal(got.Argv, want.Argv) {
		t.Errorf("argv %q, want %q", got.Argv, want.Argv)
	}

	if got.Dir != want.Dir {
		t.Errorf("folder %q, want %q", got.Dir, want.Dir)
	}

	if got.Name != want.Name {
		t.Errorf("name %q, want %q", got.Name, want.Name)
	}
}

// launchRefusal is a record ResumeAtLaunch refuses: arrange changes the rig and is the id and
// folder to check.
type launchRefusal struct {
	name    string
	arrange func(t *testing.T, rig *launchRig) (string, string)
}

// recordRefusals are records that name no session History holds as it is.
func recordRefusals() []launchRefusal {
	return []launchRefusal{
		{"an id that is not a UUID", func(_ *testing.T, rig *launchRig) (string, string) {
			return "--continue", rig.folder
		}},
		{"a session History does not hold", func(_ *testing.T, rig *launchRig) (string, string) {
			return "11111111-2222-4333-8444-555555555555", rig.folder
		}},
		{"a folder that is not the row's", func(_ *testing.T, _ *launchRig) (string, string) {
			return resumedID, "/etc"
		}},
	}
}

// stateRefusals are records of a session RESUME, or the allowlist, would not open now.
func stateRefusals() []launchRefusal {
	return []launchRefusal{
		{"a repository off the allowlist", func(_ *testing.T, rig *launchRig) (string, string) {
			rig.deps.RepoOf = func(string) string { return "acme/other" }

			return resumedID, rig.folder
		}},
		{"a folder of no repository", func(_ *testing.T, rig *launchRig) (string, string) {
			rig.deps.RepoOf = func(string) string { return "" }

			return resumedID, rig.folder
		}},
		{"no way to tell the repository", func(_ *testing.T, rig *launchRig) (string, string) {
			rig.deps.RepoOf = nil

			return resumedID, rig.folder
		}},
		{"no allowlist", func(_ *testing.T, rig *launchRig) (string, string) {
			rig.deps.Allow = nil

			return resumedID, rig.folder
		}},
		{"a dry run", func(_ *testing.T, rig *launchRig) (string, string) {
			rig.deps.Dry = func() bool { return true }

			return resumedID, rig.folder
		}},
		{"a session that runs", func(_ *testing.T, rig *launchRig) (string, string) {
			rig.deps.LiveIDs = func(context.Context) map[string]bool {
				return map[string]bool{resumedID: true}
			}

			return resumedID, rig.folder
		}},
		{"a folder that is gone", removed(func(rig *launchRig) string { return rig.folder })},
		{
			"a transcript that is gone",
			removed(func(rig *launchRig) string { return rig.transcript }),
		},
	}
}

// folderRefusals are records whose folder has changed since the session ran so that a session
// may not start in it by its path.
func folderRefusals() []launchRefusal {
	return []launchRefusal{
		{"a folder that became a link", func(t *testing.T, rig *launchRig) (string, string) {
			t.Helper()

			linkInPlace(t, rig.folder)

			return resumedID, rig.folder
		}},
		{"a folder others may write", func(t *testing.T, rig *launchRig) (string, string) {
			t.Helper()

			err := os.Chmod(rig.folder, 0o777)
			if err != nil {
				t.Fatal(err)
			}

			return resumedID, rig.folder
		}},
		{"a folder above it others may write", func(t *testing.T, rig *launchRig) (string, string) {
			t.Helper()

			err := os.Chmod(filepath.Dir(rig.folder), 0o777)
			if err != nil {
				t.Fatal(err)
			}

			return resumedID, rig.folder
		}},
	}
}

// linkInPlace moves folder aside and puts a link to a new folder in its place.
func linkInPlace(t *testing.T, folder string) {
	t.Helper()

	err := os.Rename(folder, folder+"-moved")
	if err == nil {
		err = os.Symlink(t.TempDir(), folder)
	}

	if err != nil {
		t.Fatal(err)
	}
}

func TestResumeAtLaunchRefuses(t *testing.T) {
	t.Parallel()

	tests := slices.Concat(recordRefusals(), stateRefusals(), folderRefusals())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newLaunchRig(t)
			sessionID, folder := tt.arrange(t, rig)

			got, ok := rig.api().ResumeAtLaunch(t.Context(), sessionID, folder)
			if ok {
				t.Errorf("resumed %+v", got)
			}
		})
	}
}

// removed is an arrangement that removes the path it names, then checks the good record.
func removed(path func(rig *launchRig) string) func(t *testing.T, rig *launchRig) (string, string) {
	return func(t *testing.T, rig *launchRig) (string, string) {
		t.Helper()

		err := os.RemoveAll(path(rig))
		if err != nil {
			t.Fatal(err)
		}

		return resumedID, rig.folder
	}
}

// TestRESUMERefusesAFolderThatBecameALink: RESUME runs the same folder check as a launch, and
// says what is wrong.
func TestRESUMERefusesAFolderThatBecameALink(t *testing.T) {
	t.Parallel()

	rig := newLaunchRig(t)
	linkInPlace(t, rig.folder)

	reply, err := rig.api().Handle(t.Context(),
		historyapi.Route{Kind: historyapi.RouteResume, ID: resumedID, AgentID: ""}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if reply.Code != http.StatusConflict {
		t.Errorf("RESUME answered %d, want %d", reply.Code, http.StatusConflict)
	}

	if !strings.Contains(string(js.Marshal(reply.Body)), rig.folder+" is a link") {
		t.Errorf("RESUME said %s", js.Marshal(reply.Body))
	}

	if len(rig.opened) != 0 {
		t.Errorf("RESUME opened %d sessions", len(rig.opened))
	}
}

// A session resumed at launch is not live until it registers: a RESUME meanwhile opens no
// second one on the same conversation.
func TestAResumeRightAfterTheLaunchsIsRefused(t *testing.T) {
	t.Parallel()

	rig := newLaunchRig(t)
	api := rig.api()

	_, ok := api.ResumeAtLaunch(t.Context(), resumedID, rig.folder)
	if !ok {
		t.Fatal("not resumed")
	}

	reply, err := api.Handle(t.Context(),
		historyapi.Route{Kind: historyapi.RouteResume, ID: resumedID, AgentID: ""}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if reply.Code != http.StatusConflict {
		t.Errorf("RESUME answered %d, want %d", reply.Code, http.StatusConflict)
	}

	if len(rig.opened) != 0 {
		t.Errorf("RESUME opened %d sessions", len(rig.opened))
	}
}

// realTemp is a new folder by its real path, as claude records a session's folder.
func realTemp(t *testing.T) string {
	t.Helper()

	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Private whatever the umask, as Conn's own folders are: a test folder another user could
	// write would rightly be refused.
	err = os.Chmod(folder, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	return folder
}
