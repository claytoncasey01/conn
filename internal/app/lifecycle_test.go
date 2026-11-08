package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/app"
	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/history"
	"github.com/claytoncasey01/conn/internal/hosted"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// The lifecycle of the hosted sessions across a quit and a launch, with History as the owner:
// its archive is a file in the data folder, so a launch's owner check runs on real state.

const (
	claudeProgram = "/opt/conn-test/bin/claude"
	// lifeWait bounds each wait of the lifecycle tests.
	lifeWait = 10 * time.Second
	// pidLifeFirst is the first process id a lifecycle fake reports.
	pidLifeFirst = 7000
	lifeFileMode = 0o600
	lifeDirMode  = 0o700
)

// archived is one session History holds: its id, name and folder.
type archived struct {
	id, name, folder string
}

// lifeWorld is a data folder whose settings allow acme/* with GitHub off (nothing polls), and
// whose History holds sessions, each in a folder of acme/widgets with its transcript.
type lifeWorld struct {
	env      map[string]string
	sessions []archived
}

func newLifeWorld(t *testing.T, settingsExtra string, ids ...string) lifeWorld {
	t.Helper()

	env := environment(t)
	env["CONN_CLAUDE"] = claudeProgram
	data := env["CONN_DATA"]

	writeLifeFile(t, filepath.Join(data, "settings.json"), `{"version":1,"repos":{"allow":`+
		`["acme/*"],"clones":{},"cloneRoot":null},"github":{"enabled":false}`+settingsExtra+`}`)

	world := lifeWorld{env: env, sessions: nil}
	for index, id := range ids {
		world.sessions = append(world.sessions, archived{
			id: id, name: fmt.Sprintf("fix widget %d", index+1), folder: widgetsFolder(t),
		})
	}

	world.archive(t)

	return world
}

func writeLifeFile(t *testing.T, path, text string) {
	t.Helper()

	err := os.MkdirAll(filepath.Dir(path), lifeDirMode)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(path, []byte(text), lifeFileMode)
	if err != nil {
		t.Fatal(err)
	}
}

// widgetsFolder is a new folder whose origin is acme/widgets.
func widgetsFolder(t *testing.T) string {
	t.Helper()

	folder := filepath.Join(t.TempDir(), "widgets")
	writeLifeFile(t, filepath.Join(folder, ".git", "config"),
		"[remote \"origin\"]\n\turl = git@github.com:acme/widgets.git\n")

	return folder
}

// archive puts the world's sessions in History, each with a transcript.
func (w lifeWorld) archive(t *testing.T) {
	t.Helper()

	archive, err := history.Open(t.Context(), filepath.Join(w.env["CONN_DATA"], "history.db"),
		history.Options{
			OriginOf: func(string) string { return "acme/widgets" },
			Now:      func() float64 { return 1 },
		})
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = archive.Close() }()

	for _, session := range w.sessions {
		transcript := filepath.Join(t.TempDir(), session.id+".jsonl")
		writeLifeFile(t, transcript, "{}\n")

		err = archive.Upsert(t.Context(), history.Entry{
			SessionID: session.id, Name: session.name, Cwd: session.folder, Kind: "session",
			Source: "transcript", TranscriptPath: transcript, Named: true, Final: true,
			Outcome: "ended",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// record is the record of the index-th session as the last launch would have left it.
func (w lifeWorld) record(index int) hosted.Record {
	session := w.sessions[index]

	return hosted.Record{
		ID: fmt.Sprintf("%032x", index+1), Owner: hosted.OwnerHistory, Name: session.name,
		Dir: session.folder, ClaudeSessionID: session.id,
	}
}

// resumeSpec is what History's RESUME opens for the index-th session.
func (w lifeWorld) resumeSpec(index int) pty.Spec {
	session := w.sessions[index]
	name := session.name
	args, _ := cmux.ResumeArgs(session.id, &name)

	return pty.Spec{
		Argv: append([]string{claudeProgram}, args...), Dir: session.folder, Env: nil,
		Name: cmux.WorkspaceName(name, session.id),
	}
}

func (w lifeWorld) data() string { return w.env["CONN_DATA"] }

// lifeProgram is a hosted fake: it runs until it is stopped or ends by itself, and when it is
// stopped it says whether the record named its session then.
type lifeProgram struct {
	pid    int
	output chan []byte
	closed chan struct{}
	exited chan struct{}
	end    sync.Once
	shut   sync.Once
	// atStop is called once, at the first stop.
	atStop func()
}

func (p *lifeProgram) Read(buffer []byte) (int, error) {
	select {
	case chunk := <-p.output:
		return copy(buffer, chunk), nil
	case <-p.closed:
		return 0, errProgramClosed
	}
}

func (p *lifeProgram) Write(data []byte) (int, error) { return len(data), nil }
func (p *lifeProgram) Resize(pty.Size) error          { return nil }
func (p *lifeProgram) PID() int                       { return p.pid }
func (p *lifeProgram) Keyboard() (pty.Keyboard, error) {
	return pty.Keyboard{Foreground: true, LineMode: false}, nil
}

func (p *lifeProgram) Terminate() error {
	p.atStop()
	p.exit()

	return nil
}

func (p *lifeProgram) Kill() error { return p.Terminate() }

func (p *lifeProgram) Group() pty.GroupState {
	select {
	case <-p.exited:
		return pty.GroupGone
	default:
		return pty.GroupRunning
	}
}

func (p *lifeProgram) Wait() int {
	<-p.exited

	return 0
}

func (p *lifeProgram) Close() error {
	p.shut.Do(func() { close(p.closed) })

	return nil
}

// exit ends the program by itself, as a session that ended before the quit.
func (p *lifeProgram) exit() { p.end.Do(func() { close(p.exited) }) }

// spawns is a launch's host: every spec it was asked to start, and each program it started.
type spawns struct {
	mu       sync.Mutex
	specs    []pty.Spec
	programs []*lifeProgram
	// recordAtStop is, for each program stopped, whether the record named a session in its
	// folder when it was stopped.
	recordAtStop []bool
	data         string
}

func newSpawns(data string) *spawns { return &spawns{mu: sync.Mutex{}, data: data} }

func (s *spawns) host() *pty.Host {
	return pty.NewHost(pty.Config{
		Spawner: s.spawn, Limits: pty.DefaultLimits(), Answers: pty.Answers{}, TurnWaited: nil,
	})
}

func (s *spawns) spawn(spec pty.Spec) (pty.Process, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	program := &lifeProgram{
		pid: pidLifeFirst + len(s.programs), output: make(chan []byte),
		closed: make(chan struct{}), exited: make(chan struct{}), end: sync.Once{},
		shut: sync.Once{}, atStop: nil,
	}
	folder := spec.Dir
	program.atStop = func() { s.stopped(folder) }

	s.specs = append(s.specs, spec)
	s.programs = append(s.programs, program)

	return program, nil
}

// stopped notes whether the record names a session in folder now.
func (s *spawns) stopped(folder string) {
	// A record that cannot be read names nothing: Read returns no records with its error.
	records, _ := hosted.Read(s.data)
	named := slices.ContainsFunc(records, func(record hosted.Record) bool {
		return record.Dir == folder
	})

	s.mu.Lock()
	defer s.mu.Unlock()

	s.recordAtStop = append(s.recordAtStop, named)
}

func (s *spawns) started() []pty.Spec {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.specs)
}

// launch starts a Conn on world over spawned, and is it and what quits it.
func launch(t *testing.T, world lifeWorld, spawned *spawns) (*app.Server, func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	server, err := app.StartWith(ctx, lookupFrom(world.env), io.Discard, spawned.host())
	if err != nil {
		cancel()
		t.Fatal(err)
	}

	quit := func() {
		cancel()

		select {
		case <-server.Done():
		case <-time.After(testtime.Scaled(lifeWait)):
			t.Fatal("the server did not stop")
		}
	}

	t.Cleanup(quit)

	return server, quit
}

func writeRecords(t *testing.T, data string, records ...hosted.Record) {
	t.Helper()

	err := hosted.Write(data, records)
	if err != nil {
		t.Fatal(err)
	}
}

func readRecords(t *testing.T, data string) []hosted.Record {
	t.Helper()

	records, err := hosted.Read(data)
	if err != nil {
		t.Fatal(err)
	}

	return records
}

func sameSpecs(t *testing.T, got, want []pty.Spec) {
	t.Helper()

	if !slices.Equal(specLines(got), specLines(want)) {
		t.Errorf("started %q, want %q", specLines(got), specLines(want))
	}
}

// specLines are specs as text to compare: each one's folder and arguments.
func specLines(specs []pty.Spec) []string {
	lines := make([]string, 0, len(specs))
	for _, spec := range specs {
		lines = append(lines, spec.Dir+" "+strings.Join(spec.Argv, " "))
	}

	return lines
}

// snapshotResumed is the resumed sessions the page's snapshot lists.
func snapshotResumed(t *testing.T, server *app.Server) []hosted.Resumed {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		server.URL+"/api/snapshot", nil)
	if err != nil {
		t.Fatal(err)
	}

	request.Header.Set(secret.Header, server.Secret.Authorization())
	request.Header.Set("X-Conn", "1")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = response.Body.Close() }()

	var body struct {
		Resumed *struct {
			Sessions []hosted.Resumed `json:"sessions"`
		} `json:"resumed"`
	}

	err = json.NewDecoder(response.Body).Decode(&body)
	if err != nil {
		t.Fatal(err)
	}

	if body.Resumed == nil {
		return nil
	}

	return body.Resumed.Sessions
}
