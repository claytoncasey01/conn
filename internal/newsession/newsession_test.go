package newsession_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/newsession"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/realpath"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	allowedRepo = "acme/widgets"
	hostedID    = "0123456789abcdef0123456789abcdef"
	// sessionsMax is the most sessions the rig's host runs, and sessionsFull a host at it.
	sessionsMax  = 12
	sessionsFull = sessionsMax
	cooldown     = time.Minute
)

var errNoRandom = errors.New("no random bytes")

// rig is what an API reads and what it opened, each of which a case may change before api
// builds it. Its clones are real folders, under a link on macOS (/var), checked and resolved
// as the app does.
type rig struct {
	mu      sync.Mutex
	current settings.Settings
	clones  map[string]string
	running int
	dry     bool
	now     time.Time
	opened  []pty.Spec
	answer  launch.Opened
	idErr   error
	// resolve stands in for realpath.Resolve when a case sets it.
	resolve func(path string) string
	// afterCheck runs after the folder's first check and before the start.
	afterCheck func()
	repoList   []string
}

func newRig(t *testing.T) *rig {
	t.Helper()

	clone := makeClone(t, filepath.Join(t.TempDir(), "widgets"))
	current := settings.Defaults()
	current.Repos.Allow = []string{"acme/*"}

	return &rig{
		mu: sync.Mutex{}, current: current, clones: map[string]string{allowedRepo: clone},
		now:      time.UnixMilli(1_800_000_000_000),
		answer:   launch.Opened{OK: true, Where: settings.WhereInside, HostedID: hostedID},
		repoList: []string{allowedRepo},
	}
}

// makeClone is a folder at path with a .git folder in it, each the person's own.
func makeClone(t *testing.T, path string) string {
	t.Helper()

	err := os.MkdirAll(filepath.Join(path, ".git"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	return path
}

func (r *rig) api() *newsession.API { return newsession.New(r.deps()) }

func (r *rig) resolveOf() func(string) string {
	if r.resolve != nil {
		return r.resolve
	}

	return realpath.Resolve
}

// deps are the API's deps over the rig.
func (r *rig) deps() newsession.Deps {
	return newsession.Deps{
		Settings: func() settings.Settings { return r.current },
		Repos:    func(settings.Settings) []string { return r.repoList },
		CloneFor: func(repo string, _ settings.Settings) (string, bool) {
			clone, found := r.clones[strings.ToLower(repo)]

			return clone, found
		},
		CheckClone: checkClone, Resolve: r.resolveOf(),
		Open: func(_ context.Context, spec pty.Spec) launch.Opened {
			r.mu.Lock()
			defer r.mu.Unlock()

			r.opened = append(r.opened, spec)

			return r.answer
		},
		Claude: claudeProgram, Dry: r.dry, Room: roomOf(r.running),
		NewID: func() (string, error) {
			// The seam between the first check of the folder and the start.
			if r.afterCheck != nil {
				r.afterCheck()
			}

			return conversationID, r.idErr
		},
		Now: func() time.Time { return r.now }, Cooldown: cooldown,
	}
}

func (r *rig) openedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.opened)
}

func start(t *testing.T, api *newsession.API, body string) newsession.Reply {
	t.Helper()

	reply, err := api.Start(t.Context(), object(t, body))
	if err != nil {
		t.Fatal(err)
	}

	return reply
}

// errorOf is a refusal's message, "" for a reply that is not one.
func errorOf(reply newsession.Reply) string {
	text := string(js.Marshal(reply.Body))
	parsed, _ := js.Parse([]byte(text))
	object, _ := parsed.(*js.Object)
	message, _ := object.Get("error")
	said, _ := message.(string)

	return said
}

// TestStartRefusesAndOpensNothing: each refusal answers its code and message, and opens nothing.
func TestStartRefusesAndOpensNothing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		arrange  func(t *testing.T, r *rig)
		body     string
		wantCode int
		wantSays string
	}{
		{
			"a repository not on the allowlist", nil, `{"repo":"other/widgets"}`,
			http.StatusBadRequest, "is not an allowed repository",
		},
		{
			"no allowlist (setup)", func(_ *testing.T, r *rig) { r.current.Repos.Allow = nil },
			`{"repo":"acme/widgets"}`, http.StatusBadRequest, "is not an allowed repository",
		},
		{"no clone", nil, `{"repo":"acme/gadgets"}`, http.StatusConflict, "No local clone"},
		{
			"a .git that is a link", gitLink, `{"repo":"acme/widgets"}`,
			http.StatusConflict, ".git is a link",
		},
		{
			"a .git that points elsewhere", gitPointer, `{"repo":"acme/widgets"}`,
			http.StatusConflict, "points at a git folder elsewhere",
		},
		{
			"a folder above the clone others may write", writableParent,
			`{"repo":"acme/widgets"}`, http.StatusConflict, "other users may write",
		},
		{
			"a link in a folder above the clone", linkedParent, `{"repo":"acme/widgets"}`,
			http.StatusConflict, "is a link",
		},
		{
			"the clone swapped for a link after the check", swappedAfterCheck,
			`{"repo":"acme/widgets"}`, http.StatusConflict, "is a link",
		},
		{
			"a clone that is a file", fileClone, `{"repo":"acme/widgets"}`,
			http.StatusConflict, "is not a folder",
		},
		{
			"a clone that is gone", goneClone, `{"repo":"acme/widgets"}`,
			http.StatusConflict, "cannot be used",
		},
		{
			"a bad name", nil, `{"repo":"acme/widgets","name":"a\u001b[2Jb"}`,
			http.StatusBadRequest, "control character",
		},
		{
			"a field it does not take", nil, `{"repo":"acme/widgets","cwd":"/"}`,
			http.StatusBadRequest, `"cwd" is not a field`,
		},
		{"no body fields", nil, `{}`, http.StatusBadRequest, "Choose a repository."},
		{
			"the host full", func(_ *testing.T, r *rig) { r.running = sessionsFull },
			`{"repo":"acme/widgets"}`, http.StatusConflict, "already runs 12 sessions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			setting := newRig(t)
			if tt.arrange != nil {
				tt.arrange(t, setting)
			}

			reply := start(t, setting.api(), tt.body)
			if reply.Code != tt.wantCode {
				t.Errorf("code %d, want %d", reply.Code, tt.wantCode)
			}

			if !strings.Contains(errorOf(reply), tt.wantSays) {
				t.Errorf("says %q, want it to say %q", errorOf(reply), tt.wantSays)
			}

			if setting.openedCount() != 0 {
				t.Errorf("opened %d sessions", setting.openedCount())
			}
		})
	}
}

// realClone is the rig's clone with every link resolved, as the API checks it.
func realClone(t *testing.T, setting *rig) string {
	t.Helper()

	folder, err := filepath.EvalSymlinks(setting.clones[allowedRepo])
	if err != nil {
		t.Fatal(err)
	}

	return folder
}

func gitLink(t *testing.T, setting *rig) {
	t.Helper()

	clone := realClone(t, setting)
	other := makeClone(t, filepath.Join(t.TempDir(), "other"))

	err := os.Remove(filepath.Join(clone, ".git"))
	if err == nil {
		err = os.Symlink(filepath.Join(other, ".git"), filepath.Join(clone, ".git"))
	}

	if err != nil {
		t.Fatal(err)
	}
}

func gitPointer(t *testing.T, setting *rig) {
	t.Helper()

	clone := realClone(t, setting)

	err := os.Remove(filepath.Join(clone, ".git"))
	if err == nil {
		err = os.WriteFile(filepath.Join(clone, ".git"), []byte("gitdir: /elsewhere/.git\n"),
			0o600)
	}

	if err != nil {
		t.Fatal(err)
	}
}

func writableParent(t *testing.T, setting *rig) {
	t.Helper()

	err := os.Chmod(filepath.Dir(realClone(t, setting)), 0o770)
	if err != nil {
		t.Fatal(err)
	}
}

// linkedParent is a clone reached through a link that the clone's own path does not resolve: the
// folder CloneFor names is real, but a folder above it is swapped for a link to a copy.
func linkedParent(t *testing.T, setting *rig) {
	t.Helper()

	base := realTempFolder(t)
	copied := makeClone(t, filepath.Join(base, "real", "widgets"))

	err := os.Symlink(filepath.Dir(copied), filepath.Join(base, "projects"))
	if err != nil {
		t.Fatal(err)
	}

	setting.clones[allowedRepo] = filepath.Join(base, "projects", "widgets")
	setting.resolve = func(path string) string { return path }
}

// swappedAfterCheck swaps the checked clone for a link to another clone once the first check is
// done: the check right before the start refuses it.
func swappedAfterCheck(t *testing.T, setting *rig) {
	t.Helper()

	clone := realClone(t, setting)
	other := makeClone(t, filepath.Join(t.TempDir(), "hooks"))
	setting.afterCheck = func() {
		err := os.Rename(clone, clone+"-moved")
		if err == nil {
			err = os.Symlink(other, clone)
		}

		if err != nil {
			t.Error(err)
		}
	}
}

func realTempFolder(t *testing.T) string {
	t.Helper()

	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	return folder
}

func fileClone(t *testing.T, setting *rig) {
	t.Helper()

	file := filepath.Join(t.TempDir(), "widgets")

	err := os.WriteFile(file, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	setting.clones[allowedRepo] = file
}

func goneClone(t *testing.T, setting *rig) {
	t.Helper()

	setting.clones[allowedRepo] = filepath.Join(t.TempDir(), "gone")
}

// TestStartOpensClaudeInTheClonesRealFolder: the session opened is Spec's, in the clone's real
// path (the folder claude reports, which History's row holds), and the reply names the session.
func TestStartOpensClaudeInTheClonesRealFolder(t *testing.T) {
	t.Parallel()

	setting := newRig(t)
	reply := start(t, setting.api(), `{"repo":"ACME/Widgets","name":"fix the widget"}`)

	if reply.Code != http.StatusOK {
		t.Fatalf("code %d: %s", reply.Code, js.Marshal(reply.Body))
	}

	if setting.openedCount() != 1 {
		t.Fatalf("opened %d sessions", setting.openedCount())
	}

	realFolder, err := filepath.EvalSymlinks(setting.clones[allowedRepo])
	if err != nil {
		t.Fatal(err)
	}

	want := newsession.Spec(claudeProgram, conversationID, realFolder, newsession.Request{
		Repo: "ACME/Widgets", Name: "fix-the-widget",
	})
	got := setting.opened[0]

	if !slices.Equal(got.Argv, want.Argv) {
		t.Errorf("argv %q, want %q", got.Argv, want.Argv)
	}

	if got.Dir != realFolder {
		t.Errorf("folder %q, want %q", got.Dir, realFolder)
	}

	if string(js.Marshal(reply.Body)) != `{"ok":true,"id":"`+hostedID+`"}` {
		t.Errorf("reply %s", js.Marshal(reply.Body))
	}
}

// TestASecondStartInTheRepositoryWaitsForTheCooldown: a double press opens one session; once the
// cooldown has passed, a start there opens another, and a start in another repository never
// waits.
func TestASecondStartInTheRepositoryWaitsForTheCooldown(t *testing.T) {
	t.Parallel()

	setting := newRig(t)
	setting.clones["acme/gadgets"] = setting.clones[allowedRepo]
	api := setting.api()

	start(t, api, `{"repo":"acme/widgets"}`)

	again := start(t, api, `{"repo":"ACME/WIDGETS"}`)
	if again.Code != http.StatusConflict {
		t.Errorf("a second press: code %d", again.Code)
	}

	other := start(t, api, `{"repo":"acme/gadgets"}`)
	if other.Code != http.StatusOK {
		t.Errorf("another repository: code %d", other.Code)
	}

	setting.now = setting.now.Add(cooldown)

	later := start(t, api, `{"repo":"acme/widgets"}`)
	if later.Code != http.StatusOK {
		t.Errorf("after the cooldown: code %d", later.Code)
	}

	if setting.openedCount() != 3 {
		t.Errorf("opened %d sessions, want 3", setting.openedCount())
	}
}

// TestTwoStartsAtOnceOpenOne: the check and the mark of a start are one step.
func TestTwoStartsAtOnceOpenOne(t *testing.T) {
	t.Parallel()

	setting := newRig(t)
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	api := newsession.New(newsession.Deps{
		Settings: func() settings.Settings { return setting.current },
		Repos:    func(settings.Settings) []string { return nil },
		CloneFor: func(string, settings.Settings) (string, bool) {
			return setting.clones[allowedRepo], true
		},
		CheckClone: checkClone, Resolve: realpath.Resolve,
		Open: func(context.Context, pty.Spec) launch.Opened {
			entered <- struct{}{}

			<-release

			return launch.Opened{OK: true}
		},
		Claude: claudeProgram, Dry: false, Room: roomOf(0),
		NewID: func() (string, error) { return conversationID, nil },
		Now:   time.Now, Cooldown: cooldown,
	})

	first := make(chan newsession.Reply, 1)
	body := object(t, `{"repo":"acme/widgets"}`)

	go func() {
		reply, _ := api.Start(t.Context(), body)
		first <- reply
	}()

	<-entered

	second := start(t, api, `{"repo":"acme/widgets"}`)

	close(release)

	if second.Code != http.StatusConflict {
		t.Errorf("the press while one opens: code %d", second.Code)
	}

	if (<-first).Code != http.StatusOK {
		t.Error("the first press did not open")
	}
}

// TestTheCapCountsOnlySessionsInsideConn: with sessions opening in a terminal or as a command,
// the host's sessions do not stop a start; a start that only shows the command starts no
// cooldown.
func TestTheCapCountsOnlySessionsInsideConn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		where      string
		answer     launch.Opened
		wantOpened int
	}{
		{"in a terminal", settings.WhereTerminal, launch.Opened{OK: true}, 1},
		{"as a command", settings.WhereCommand, launch.Opened{
			OK: false, Error: "sessions open as a command", ManualCommand: "cd /w && claude",
		}, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			setting := newRig(t)
			setting.current.Sessions.Where = tt.where
			setting.running = sessionsFull
			setting.answer = tt.answer
			api := setting.api()

			first := start(t, api, `{"repo":"acme/widgets"}`)
			if first.Code != http.StatusOK {
				t.Fatalf("code %d: %s", first.Code, js.Marshal(first.Body))
			}

			start(t, api, `{"repo":"acme/widgets"}`)

			if setting.openedCount() != tt.wantOpened {
				t.Errorf("opened %d, want %d", setting.openedCount(), tt.wantOpened)
			}
		})
	}
}

func TestACommandToPasteIsTheReply(t *testing.T) {
	t.Parallel()

	setting := newRig(t)
	setting.answer = launch.Opened{
		OK: false, Error: "sessions open as a command", ManualCommand: "cd /w && claude",
	}

	reply := start(t, setting.api(), `{"repo":"acme/widgets"}`)

	want := `{"ok":false,"error":"sessions open as a command","manualCommand":"cd /w && claude"}`
	if string(js.Marshal(reply.Body)) != want {
		t.Errorf("reply %s, want %s", js.Marshal(reply.Body), want)
	}
}

// TestADryRunStartsNothing: it says what it would run, opens nothing and starts no cooldown.
func TestADryRunStartsNothing(t *testing.T) {
	t.Parallel()

	setting := newRig(t)
	setting.dry = true
	api := setting.api()

	reply := start(t, api, `{"repo":"acme/widgets"}`)

	again := start(t, api, `{"repo":"acme/widgets"}`)
	if again.Code != http.StatusOK {
		t.Errorf("a second dry press: code %d", again.Code)
	}

	if setting.openedCount() != 0 {
		t.Errorf("opened %d sessions", setting.openedCount())
	}

	body := string(js.Marshal(reply.Body))
	if !strings.Contains(body, `"dry":true`) {
		t.Errorf("reply %s is not dry", body)
	}

	if !strings.Contains(body, "--session-id "+conversationID) {
		t.Errorf("reply %s does not say the command", body)
	}
}

func TestAnIDThatCannotBeDrawnIsAnError(t *testing.T) {
	t.Parallel()

	setting := newRig(t)
	setting.idErr = errNoRandom

	_, err := setting.api().Start(t.Context(), object(t, `{"repo":"acme/widgets"}`))
	if !errors.Is(err, errNoRandom) {
		t.Errorf("error %v, want %v", err, errNoRandom)
	}

	if setting.openedCount() != 0 {
		t.Errorf("opened %d sessions", setting.openedCount())
	}
}

// TestChoicesOfferOnlyWhereAStartWouldWork: a repository with no clone, or not allowed, is not
// offered; the answer says where sessions open and whether the run is dry.
func TestChoicesOfferOnlyWhereAStartWouldWork(t *testing.T) {
	t.Parallel()

	setting := newRig(t)
	setting.repoList = []string{"acme/gadgets", allowedRepo, "other/widgets"}
	setting.clones["other/widgets"] = setting.clones[allowedRepo]
	setting.current.Sessions.Where = settings.WhereCommand

	reply := setting.api().Choices()

	want := `{"repos":["acme/widgets"],"where":"command","dry":false}`
	if string(js.Marshal(reply.Body)) != want {
		t.Errorf("choices %s, want %s", js.Marshal(reply.Body), want)
	}
}

// TestACloneSetAsALinkOpensInTheFolderItPointsTo: the link is resolved first and the real folder
// is what is checked and started in.
func TestACloneSetAsALinkOpensInTheFolderItPointsTo(t *testing.T) {
	t.Parallel()

	setting := newRig(t)
	realFolder := realClone(t, setting)
	link := filepath.Join(t.TempDir(), "widgets")

	err := os.Symlink(realFolder, link)
	if err != nil {
		t.Fatal(err)
	}

	setting.clones[allowedRepo] = link

	reply := start(t, setting.api(), `{"repo":"acme/widgets"}`)
	if reply.Code != http.StatusOK {
		t.Fatalf("code %d: %s", reply.Code, js.Marshal(reply.Body))
	}

	if setting.opened[0].Dir != realFolder {
		t.Errorf("folder %q, want %q", setting.opened[0].Dir, realFolder)
	}
}

// checkClone is the app's clone check: newsession.CheckClone over reviews.CheckSafeFolder.
func checkClone(folder string) error {
	err := newsession.CheckClone(folder, reviews.CheckSafeFolder)
	if err != nil {
		return fmt.Errorf("checking the clone: %w", err)
	}

	return nil
}

// roomOf is a host with running sessions running, at most sessionsMax.
func roomOf(running int) func() pty.Room {
	return func() pty.Room { return pty.Room{Running: running, SessionsMax: sessionsMax} }
}
