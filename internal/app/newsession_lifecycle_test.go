package app_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/app"
	"github.com/claytoncasey01/conn/internal/hosted"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/secret"
)

// startNewSession presses New session for repo on server and is the status it answered.
func startNewSession(t *testing.T, server *app.Server, repo string) int {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		server.URL+"/api/sessions/new", strings.NewReader(`{"repo":"`+repo+`"}`))
	if err != nil {
		t.Fatal(err)
	}

	request.Header.Set(secret.Header, server.Secret.Authorization())
	request.Header.Set("X-Conn", "1")
	request.Header.Set("Origin", server.URL)

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	_ = response.Body.Close()

	return response.StatusCode
}

// startedRecord checks that the one session a New session press opened is in clone and recorded
// under History with the conversation id it was started on, and is that record.
func startedRecord(t *testing.T, world lifeWorld, opened []pty.Spec, clone string) hosted.Record {
	t.Helper()

	if len(opened) != 1 {
		t.Fatalf("the start opened %d sessions, want 1", len(opened))
	}

	if opened[0].Dir != clone {
		t.Errorf("opened in %q, want the clone %q", opened[0].Dir, clone)
	}

	recorded := readRecords(t, world.data())
	if len(recorded) != 1 {
		t.Fatalf("the record holds %d sessions, want the started one", len(recorded))
	}

	if recorded[0].Owner != hosted.OwnerHistory {
		t.Errorf("owner %q, want %q", recorded[0].Owner, hosted.OwnerHistory)
	}

	if !slices.Contains(opened[0].Argv, recorded[0].ClaudeSessionID) {
		t.Errorf("claude was started as %q, the record names conversation %q", opened[0].Argv,
			recorded[0].ClaudeSessionID)
	}

	return recorded[0]
}

// resumedOn checks that a launch opened one session in clone, resuming conversation.
func resumedOn(t *testing.T, resumed []pty.Spec, clone, conversation string) {
	t.Helper()

	if len(resumed) != 1 {
		t.Fatalf("the launch resumed %d sessions, want 1", len(resumed))
	}

	if resumed[0].Dir != clone {
		t.Errorf("resumed in %q, want %q", resumed[0].Dir, clone)
	}

	if !slices.Contains(resumed[0].Argv, "--resume") {
		t.Errorf("resumed as %q, want --resume", resumed[0].Argv)
	}

	if !slices.Contains(resumed[0].Argv, conversation) {
		t.Errorf("resumed as %q, want conversation %q", resumed[0].Argv, conversation)
	}
}

// TestANewSessionResumesOnItsConversationAfterAQuit: a session started from the page, through the
// whole app, is recorded under the conversation id Conn chose; once claude has written a
// transcript for it, the next launch resumes it there, on that conversation.
func TestANewSessionResumesOnItsConversationAfterAQuit(t *testing.T) {
	t.Parallel()

	world := newLifeWorld(t, "")
	folder := widgetsFolder(t)
	writeLifeFile(t, world.data()+"/settings.json", `{"version":1,"repos":{"allow":["acme/*"],`+
		`"clones":{"acme/widgets":"`+folder+`"},"cloneRoot":null},"github":{"enabled":false}}`)

	first := newSpawns(world.data())
	server, quit := launch(t, world, first)

	code := startNewSession(t, server, "acme/widgets")
	if code != http.StatusOK {
		t.Fatalf("New session answered %d, want %d", code, http.StatusOK)
	}

	record := startedRecord(t, world, first.started(), folder)

	quit()

	// Claude wrote a transcript while the session ran.
	world.sessions = []archived{{id: record.ClaudeSessionID, name: record.Name, folder: folder}}
	world.archive(t)

	second := newSpawns(world.data())
	launch(t, world, second)
	resumedOn(t, second.started(), folder, record.ClaudeSessionID)
}
