package sessions_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/testtime"
)

var errNotFound = errors.New("no pull requests found")

// maxParked bounds the parked writes a test world keeps; more are not kept.
const maxParked = 16

// world is a fake machine: a home with ~/.claude, a process table, environments and gh.
type world struct {
	t       *testing.T
	home    string
	data    string
	procs   []string
	envs    map[float64]string
	allowed map[string]bool
	repos   map[string]string
	ups     map[string]string
	prs     map[string]string
	mutex   sync.Mutex
	asked   []string
	term    *terminal
	// runs are the Open work runs the session list reads.
	runs []sessions.Run
	// envAsked are the processes whose environment was read.
	envAsked []float64
	// host runs the hosted sessions, made by the first hosted one; nil until then.
	host *pty.Host
	// spawning is the process id the next hosted program gets.
	spawning int
	programs map[int]*program
	// parked is told of each hosted write that waits for its turn.
	parked chan pty.ID
}

// fakeRun is an Open work run in its worktrees, the first its lead's.
type fakeRun struct {
	key       string
	worktrees []string
}

func (r fakeRun) Key() string { return r.key }

func (r fakeRun) Worktree() string { return r.worktrees[0] }

func (r fakeRun) Worktrees() []string { return r.worktrees }

func (r fakeRun) Facts() *sessions.RunFacts { return &sessions.RunFacts{State: "", PRState: ""} }

func newWorld(t *testing.T) *world {
	t.Helper()

	return &world{
		t: t, home: t.TempDir(), data: t.TempDir(), procs: []string{"1 0 /sbin/launchd"},
		envs: map[float64]string{}, allowed: map[string]bool{"acme/widgets": true},
		repos: map[string]string{}, ups: map[string]string{}, prs: map[string]string{},
		mutex: sync.Mutex{}, asked: nil, term: newTerminal(), envAsked: nil, host: nil,
		spawning: 0, programs: map[int]*program{}, parked: make(chan pty.ID, maxParked),
	}
}

func (w *world) write(path, text string) {
	w.t.Helper()

	err := os.MkdirAll(filepath.Dir(path), 0o700)
	if err != nil {
		w.t.Fatal(err)
	}

	err = os.WriteFile(path, []byte(text), 0o600)
	if err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) folder(name, repo string) string {
	w.t.Helper()

	path := filepath.Join(w.home, "work", name)

	err := os.MkdirAll(path, 0o700)
	if err != nil {
		w.t.Fatal(err)
	}

	if repo != "" {
		w.repos[path] = repo
	}

	return path
}

func (w *world) register(file string, entry map[string]any) {
	w.t.Helper()

	data, err := json.Marshal(entry)
	if err != nil {
		w.t.Fatal(err)
	}

	w.write(filepath.Join(w.home, ".claude", "sessions", file), string(data))
}

// transcript writes a session's transcript under the slug of its folder.
func (w *world) transcript(slug, sessionID string, lines ...string) string {
	path := filepath.Join(w.home, ".claude", "projects", slug, sessionID+".jsonl")
	w.write(path, strings.Join(lines, ""))

	return path
}

func (w *world) deps() sessions.Deps {
	always := func() bool { return true }
	never := func() bool { return false }

	return sessions.Deps{
		Home: w.home, DataDir: w.data, Kinds: rules(w.t),
		Procs: func() (string, error) { return strings.Join(w.procs, "\n"), nil },
		Env: func(pid float64) ([]string, error) {
			w.mutex.Lock()
			w.envAsked = append(w.envAsked, pid)
			w.mutex.Unlock()

			env, found := w.envs[pid]
			if !found {
				return nil, errNotFound
			}

			// A test writes an environment as its entries, space-separated.
			return strings.Fields(env), nil
		},
		PRView: func(_ context.Context, cwd string, args []string) ([]byte, error) {
			w.mutex.Lock()
			defer w.mutex.Unlock()

			w.asked = append(w.asked, cwd+"|"+strings.Join(args, " "))

			answer, found := w.prs[args[len(args)-1]]
			if !found {
				return nil, errNotFound
			}

			return []byte(answer), nil
		},
		Cmux: w.term.cmux, Stat: w.term.stat, Kill: w.term.kill,
		RepoOf:   func(folder string) string { return w.repos[folder] },
		Upstream: func(_, branch string) string { return w.ups[branch] },
		Hooks: sessions.Hooks{
			Runs: func() []sessions.Run {
				if w.runs == nil {
					return []sessions.Run{}
				}

				return w.runs
			},
			History: func() any { return nil }, Reviews: func() any { return nil }, MyPrs: nil,
			Setup: never, OpenWork: never,
			RepoVisible: func(repo string) bool { return w.allowed[repo] }, GithubOn: always,
		},
		Now:  time.Now,
		Host: w.host,
	}
}

func (w *world) lookups() []string {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	return append([]string{}, w.asked...)
}

type detail struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Status    string   `json:"status"`
	StartedAt *float64 `json:"startedAt"`
	Repo      *string  `json:"repo"`
	CanReply  bool     `json:"canReply"`
	Hosted    bool     `json:"hosted"`
	ExitCode  *int     `json:"exitCode"`
	Shells    float64  `json:"shells"`
	PR        *struct {
		Number float64 `json:"number"`
	} `json:"pr"`
	Agents []struct {
		ID       string  `json:"id"`
		Type     string  `json:"type"`
		ParentID *string `json:"parentId"`
	} `json:"agents"`
	Lead struct {
		Task string `json:"task"`
	} `json:"lead"`
}

type snap struct {
	Sessions []detail `json:"sessions"`
}

func start(t *testing.T, w *world) *sessions.Loop {
	t.Helper()

	loop := sessions.NewLoop(w.deps())

	go loop.Run(t.Context())

	return loop
}

func read(t *testing.T, loop *sessions.Loop) (snap, string) {
	t.Helper()

	data, err := loop.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	var got snap

	err = json.Unmarshal(data, &got)
	if err != nil {
		t.Fatalf("%v: %s", err, data)
	}

	return got, string(data)
}

func names(got snap) []string {
	out := make([]string, 0, len(got.Sessions))

	for _, session := range got.Sessions {
		out = append(out, session.Name)
	}

	return out
}

func stamp(ago time.Duration) string {
	return time.Now().Add(-ago).UTC().Format("2006-01-02T15:04:05.000Z")
}

// line is one transcript line, its newline included.
func line(kind, at, extra string) string {
	return `{"type":"` + kind + `","timestamp":"` + at + `"` + extra + "}\n"
}

// TestTheRegistryShowsLiveClaudeSessionsOnly: a session shows while its process is a claude; a
// helper run, a file with a byte order mark, a dead process, another program and a file that is
// not JSON do not.
func TestTheRegistryShowsLiveClaudeSessionsOnly(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	folder := fake.folder("plain", "")
	now := float64(time.Now().UnixMilli())

	fake.procs = append(fake.procs, "10 1 /usr/local/bin/claude", "11 1 /bin/node",
		"12 1 /usr/local/bin/claude", "13 1 /Applications/Claude", "14 1 claude")
	fake.register("10.json", map[string]any{
		"pid": 10, "sessionId": "s-10", "cwd": folder,
		"name": "shown", "status": "busy", "updatedAt": now,
	})
	fake.register("11.json", map[string]any{
		"pid": 11, "sessionId": "s-11", "cwd": folder,
		"name": "node",
	})
	fake.register("12.json", map[string]any{
		"pid": 12, "sessionId": "s-12", "cwd": folder,
		"name": "conn:research",
	})
	fake.register("13.json", map[string]any{
		"pid": 13, "sessionId": "abcdef0123", "cwd": folder,
		"updatedAt": now - 1,
	})
	fake.register("99.json", map[string]any{"pid": 99, "sessionId": "s-99", "cwd": folder})
	fake.write(filepath.Join(fake.home, ".claude", "sessions", "14.json"),
		"\ufeff"+`{"pid":14,"sessionId":"s-14","cwd":"`+folder+`"}`)
	fake.write(filepath.Join(fake.home, ".claude", "sessions", "15.json"), "not json")

	got, _ := read(t, start(t, fake))

	if strings.Join(names(got), ",") != "shown,abcdef01" {
		t.Errorf("sessions %v", names(got))
	}
}

// TestAStartTimeThatDoesNotParseIsNull: with no registry startedAt and only a timestamp that
// does not parse, the session's startedAt is null, not a failed snapshot.
func TestAStartTimeThatDoesNotParseIsNull(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	folder := fake.folder("nan", "")
	fake.procs = append(fake.procs, "20 1 claude")
	fake.register("20.json", map[string]any{
		"pid": 20, "sessionId": "s-20", "cwd": folder,
		"name": "nan",
	})
	fake.transcript(strings.ReplaceAll(folder, "/", "-"), "s-20",
		`{"type":"user","timestamp":"garbage","message":{"content":"hi"}}`+"\n")

	got, text := read(t, start(t, fake))

	if len(got.Sessions) != 1 {
		t.Fatalf("sessions %v", names(got))
	}

	if !strings.Contains(text, `"startedAt":null`) {
		t.Errorf("snapshot %s", text)
	}

	if got.Sessions[0].Lead.Task != "hi" {
		t.Errorf("task %q", got.Sessions[0].Lead.Task)
	}
}

// TestPullRequestsAreLookedUpOnceInTheBackground: a branch's PR is looked up with gh through the
// branch it tracks, shows once found, and is not looked up again within the TTL; a default
// branch, a head of digits and a repo off the list are never looked up.
func TestPullRequestsAreLookedUpOnceInTheBackground(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	one := fake.folder("one", "acme/widgets")
	two := fake.folder("two", "acme/widgets")
	off := fake.folder("off", "other/secret")
	fake.ups["feature/x"] = "feature/x-up"
	fake.ups["topic"] = "123"
	fake.prs["feature/x-up"] = `{"number":42,"url":"https://github.com/acme/widgets/pull/42"}`
	fake.procs = append(fake.procs, "30 1 claude", "31 1 claude", "32 1 claude", "33 1 claude")

	for _, entry := range []struct {
		pid            int
		folder, branch string
	}{{30, one, "feature/x"}, {31, one, "main"}, {32, two, "topic"}, {33, off, "feature/x"}} {
		id := "s-" + filepath.Base(entry.folder) + "-" + entry.branch
		fake.register(strconv.Itoa(entry.pid)+".json", map[string]any{
			"pid": entry.pid, "sessionId": strings.ReplaceAll(id, "/", "_"), "cwd": entry.folder,
		})
		fake.transcript(strings.ReplaceAll(entry.folder, "/", "-"),
			strings.ReplaceAll(id, "/", "_"),
			line("user", stamp(time.Minute),
				`,"gitBranch":"`+entry.branch+`","message":{"content":"x"}`))
	}

	loop := start(t, fake)
	read(t, loop)

	deadline := time.Now().Add(testtime.Scaled(5 * time.Second))
	for time.Now().Before(deadline) {
		got, _ := read(t, loop)
		for _, session := range got.Sessions {
			if session.PR != nil {
				if session.PR.Number != 42 {
					t.Errorf("PR %v", session.PR.Number)
				}

				asked := fake.lookups()
				want := one + "|pr view --repo acme/widgets --json number,url -- feature/x-up"

				if strings.Join(asked, ";") != want {
					t.Errorf("gh was asked %q", asked)
				}

				return
			}
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("the PR never showed")
}

// TestSubagentsAreReadInNameOrderWithTheirMeta: a session's subagents come from its subagents
// folder, shown oldest first; a meta file with a byte order mark says nothing, and a nested
// agent names its parent.
func TestSubagentsAreReadInNameOrderWithTheirMeta(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	folder := fake.folder("agents", "")
	fake.procs = append(fake.procs, "40 1 claude")
	fake.register("40.json", map[string]any{"pid": 40, "sessionId": "s-40", "cwd": folder})

	path := fake.transcript(strings.ReplaceAll(folder, "/", "-"), "s-40",
		line("assistant", stamp(time.Minute),
			`,"message":{"content":[{"type":"tool_use","id":"t1","name":"Agent","input":{}}]}`))
	dir := strings.TrimSuffix(path, ".jsonl") + "/subagents/"

	fake.write(dir+"agent-b.jsonl", line("assistant", stamp(time.Minute),
		`,"message":{"content":[{"type":"tool_use","id":"u1","name":"Agent","input":{}}]}`))
	fake.write(dir+"agent-b.meta.json", `{"agentType":"Explore","toolUseId":"t1"}`)
	fake.write(dir+"agent-a.jsonl", line("user", stamp(time.Second), `,"message":{"content":"a"}`))
	fake.write(dir+"agent-a.meta.json", "\ufeff"+`{"agentType":"Plan","toolUseId":"u1"}`)
	fake.write(dir+"agent-c.jsonl",
		line("user", stamp(2*time.Minute), `,"message":{"content":"c"}`))
	fake.write(dir+"agent-c.meta.json", `{"agentType":"general-purpose","toolUseId":"u1"}`)
	fake.write(dir+"notes.txt", "x")

	got, _ := read(t, start(t, fake))
	if len(got.Sessions) != 1 {
		t.Fatalf("sessions %v", names(got))
	}

	agents := got.Sessions[0].Agents
	summary := make([]string, 0, len(agents))

	for _, agent := range agents {
		parent := "-"
		if agent.ParentID != nil {
			parent = *agent.ParentID
		}

		summary = append(summary, agent.ID+":"+agent.Type+":"+parent)
	}

	if strings.Join(summary, ",") != "c:general-purpose:b,b:Explore:-,a:agent:-" {
		t.Errorf("agents %v", summary)
	}
}

// TestCmuxAndShells: a CLI under the cmux app whose environment names its surface can be
// replied to; its shells are counted.
func TestCmuxAndShells(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	folder := fake.folder("cmux", "")
	fake.procs = append(fake.procs, "500 1 /Applications/cmux.app/Contents/MacOS/cmux",
		"600 500 /bin/zsh", "50 600 claude", "51 50 /bin/zsh", "52 50 -bash", "53 50 python3",
		"60 1 claude")
	fake.envs[50] = "50 claude CMUX_WORKSPACE_ID=11111111-2222-3333-4444-555555555555 " +
		"CMUX_SURFACE_ID=AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE"
	fake.envs[60] = fake.envs[50]
	fake.register("50.json", map[string]any{
		"pid": 50, "sessionId": "s-50", "cwd": folder,
		"name": "in-cmux", "updatedAt": 2,
	})
	fake.register("60.json", map[string]any{
		"pid": 60, "sessionId": "s-60", "cwd": folder,
		"name": "not-under-cmux", "updatedAt": 1,
	})

	got, _ := read(t, start(t, fake))
	if len(got.Sessions) != 2 {
		t.Fatalf("sessions %v", names(got))
	}

	if !got.Sessions[0].CanReply {
		t.Error("the session in cmux cannot be replied to")
	}

	if got.Sessions[0].Shells != 2 {
		t.Errorf("shells = %v", got.Sessions[0].Shells)
	}

	if got.Sessions[1].CanReply {
		t.Error("a session not under the cmux app can be replied to")
	}
}

// TestASessionInARepoOffTheListIsHiddenUnlessConnStartedIt: a review conn started under its
// reviews folder stays, kind review; another session in the same repo is hidden, its agent pages
// too.
func TestASessionInARepoOffTheListIsHiddenUnlessConnStartedIt(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	off := fake.folder("secret", "other/secret")
	review := filepath.Join(fake.data, "reviews", "pr-12-other--secret", "secret")
	fake.write(filepath.Join(review, "README"), "x")
	fake.repos[review] = "other/secret"
	fake.procs = append(fake.procs, "70 1 claude", "71 1 claude")
	fake.register("70.json", map[string]any{
		"pid": 70, "sessionId": "s-70", "cwd": off,
		"name": "secret",
	})
	fake.register("71.json", map[string]any{
		"pid": 71, "sessionId": "s-71", "cwd": review,
		"name": "pr-12-other--secret",
	})

	loop := start(t, fake)
	got, _ := read(t, loop)

	if strings.Join(names(got), ",") != "pr-12-other--secret" {
		t.Fatalf("sessions %v", names(got))
	}

	if got.Sessions[0].Kind != sessions.KindReview {
		t.Errorf("kind %s", got.Sessions[0].Kind)
	}

	_, found, err := loop.AgentDetail(t.Context(), "70", "lead")
	if err != nil {
		t.Fatal(err)
	}

	if found {
		t.Error("a hidden session's lead page was served")
	}

	_, found, _ = loop.AgentDetail(t.Context(), "71", "lead")
	if !found {
		t.Error("the review's lead page was not served")
	}
}

func TestABuildSessionInAnyWorktreeOfItsRunShows(t *testing.T) {
	t.Parallel()

	fake := newWorld(t)
	lead := fake.folder("widgets-proj-1", "acme/widgets")
	second := fake.folder("gadgets-proj-1", "other/gadgets")
	elsewhere := fake.folder("gadgets", "other/gadgets")
	fake.runs = []sessions.Run{fakeRun{key: "PROJ-1", worktrees: []string{lead, second}}}
	fake.procs = append(fake.procs, "80 1 claude", "81 1 claude")
	fake.register("80.json", map[string]any{
		"pid": 80, "sessionId": "s-80", "cwd": second, "name": "PROJ-1",
	})
	fake.register("81.json", map[string]any{
		"pid": 81, "sessionId": "s-81", "cwd": elsewhere, "name": "PROJ-1",
	})

	got, _ := read(t, start(t, fake))

	if strings.Join(names(got), ",") != "PROJ-1" {
		t.Fatalf("sessions %v", names(got))
	}

	if got.Sessions[0].ID != "80" {
		t.Errorf("shown %s, want the session in the run's second worktree", got.Sessions[0].ID)
	}
}

// TestTheSnapshotIsReusedUntilSomethingChanges: the same bytes while nothing changed, and a new
// snapshot after a bump.
func TestTheSnapshotIsReusedUntilSomethingChanges(t *testing.T) {
	t.Parallel()

	loop := start(t, newWorld(t))
	_, first := read(t, loop)

	time.Sleep(5 * time.Millisecond)

	_, again := read(t, loop)
	if again != first {
		t.Error("a snapshot was rebuilt with nothing changed")
	}

	loop.Bump()

	_, bumped := read(t, loop)
	if bumped == first {
		t.Error("a bump left the snapshot as it was")
	}
}
