package settingsapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/modules"
	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/settings"
	"github.com/claytoncasey01/conn/internal/settingsapi"
)

var (
	errUnexpectedGh = errors.New("gh was not expected")
	errNotInTable   = errors.New("unexpected gh")
	errDiskFull     = errors.New("settings could not be saved: disk full")
)

// holder is a settings holder the test controls.
type holder struct {
	mu       sync.Mutex
	state    settings.Settings
	dropped  []string
	backedUp string
	updates  []string
	update   func(name string, body any) settings.UpdateResult
}

func (h *holder) Get() settings.Settings {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.state.Clone()
}

func (h *holder) Setup() bool { return len(h.Get().Repos.Allow) == 0 }

func (h *holder) Dropped() []string { return h.dropped }

func (h *holder) BackedUp() string { return h.backedUp }

func (h *holder) Update(name string, body any) settings.UpdateResult {
	h.mu.Lock()
	h.updates = append(h.updates, name+" "+js.Stringify(body))
	h.mu.Unlock()

	if h.update != nil {
		return h.update(name, body)
	}

	return settings.UpdateResult{OK: true}
}

func withAllow(allow ...string) settings.Settings {
	state := settings.Defaults()
	state.Repos.Allow = allow

	return state
}

// ghRecorder answers gh calls from a function and keeps every call.
type ghRecorder struct {
	mu     sync.Mutex
	calls  []string
	answer func(args string) (string, error)
}

func (g *ghRecorder) exec(_ context.Context, args []string) (string, error) {
	key := strings.Join(args, " ")

	g.mu.Lock()
	g.calls = append(g.calls, key)
	g.mu.Unlock()

	if g.answer == nil {
		return "", errUnexpectedGh
	}

	return g.answer(key)
}

func (g *ghRecorder) callList() []string {
	g.mu.Lock()
	defer g.mu.Unlock()

	return append([]string{}, g.calls...)
}

// table answers gh from a fixed table; a call not in it fails.
func table(answers map[string]string) func(string) (string, error) {
	return func(key string) (string, error) {
		out, found := answers[key]
		if !found {
			return "", fmt.Errorf("%w %s", errNotInTable, key)
		}

		return out, nil
	}
}

type world struct {
	holder   *holder
	sessions map[string]string
	order    []string
	myPrs    []string
	clones   map[string]origin.Clone
	gh       *ghRecorder
	modules  *modules.Statuses
}

func newWorld(state settings.Settings) *world {
	return &world{
		holder:   &holder{state: state},
		sessions: map[string]string{},
		clones:   map[string]origin.Clone{},
		gh:       &ghRecorder{},
	}
}

// session adds a live session in folder, whose repository is repo ("" for none).
func (w *world) session(folder, repo string) {
	w.sessions[folder] = repo
	w.order = append(w.order, folder)
}

func (w *world) api() *settingsapi.API {
	return settingsapi.New(settingsapi.Deps{
		Settings:     w.holder,
		Sessions:     func() []string { return w.order },
		MyPrRepos:    func() []string { return w.myPrs },
		HistoryRepos: nil,
		OriginOf:     func(folder string) string { return w.sessions[folder] },
		CloneFor: func(repo string) (origin.Clone, bool) {
			clone, found := w.clones[repo]

			return clone, found
		},
		Gh:      w.gh.exec,
		Modules: w.modules,
	})
}

func handle(
	t *testing.T, api *settingsapi.API, route settingsapi.Route, body string,
) (int, string) {
	t.Helper()

	var parsed any

	if body != "" {
		value, err := js.Parse([]byte(body))
		if err != nil {
			t.Fatalf("Parse(%q): %v", body, err)
		}

		parsed = value
	}

	reply := api.Handle(t.Context(), route, parsed)

	encoded, err := json.Marshal(reply.Body)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	return reply.Code, string(encoded)
}

func get(t *testing.T, api *settingsapi.API) string {
	t.Helper()

	code, body := handle(t, api, settingsapi.Route{Kind: settingsapi.KindGet}, "")
	if code != 200 {
		t.Fatalf("GET = %d, want 200", code)
	}

	return body
}

func TestRouteOf(t *testing.T) {
	t.Parallel()

	tests := make([]struct {
		path  string
		route settingsapi.Route
		ours  bool
	}, 0, 23)
	tests = append(tests, []struct {
		path  string
		route settingsapi.Route
		ours  bool
	}{
		{path: "/api/health", ours: false},
		{path: "/api/settingsx", ours: false},
		{path: "/api/settings", ours: true, route: settingsapi.Route{Kind: settingsapi.KindGet}},
		{
			path: "/api/settings/repos", ours: true,
			route: settingsapi.Route{Kind: settingsapi.KindPut, Section: "repos"},
		},
		{
			path: "/api/settings/reviews", ours: true,
			route: settingsapi.Route{Kind: settingsapi.KindPut, Section: "reviews"},
		},
		{
			path: "/api/settings/browse", ours: true,
			route: settingsapi.Route{Kind: settingsapi.KindBrowse},
		},
		{
			path: "/api/settings/check/atlassian", ours: true,
			route: settingsapi.Route{Kind: settingsapi.KindCheck, Module: "atlassian"},
		},
	}...)

	for _, path := range []string{
		"/api/settings/", "/api/settings/repos/", "/api/settings/Repos", "/api/settings/other",
		"/api/settings//repos", "/api/settings/%72epos", "/api/settings/__proto__",
		"/api/settings/browse/x", "/api/settings/repos/x", "/api/settings/check",
		"/api/settings/check/", "/api/settings/check/other", "/api/settings/check/Slack",
		"/api/settings/check/github/x", "/api/settings/check/__proto__",
		"/api/settings/check//slack",
	} {
		tests = append(tests, struct {
			path  string
			route settingsapi.Route
			ours  bool
		}{path: path, ours: true, route: settingsapi.Route{Kind: settingsapi.KindNone}})
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()

			route, ours := settingsapi.RouteOf(tt.path)
			if ours != tt.ours {
				t.Fatalf("ours = %v, want %v", ours, tt.ours)
			}

			if route != tt.route {
				t.Errorf("RouteOf(%q) = %+v, want %+v", tt.path, route, tt.route)
			}
		})
	}
}

func TestGetTheDocument(t *testing.T) {
	t.Parallel()

	world := newWorld(withAllow("acme/*", "jane/widgets", "nobody/none"))
	world.session("/w/a", "acme/infra")
	world.session("/w/b", "acme/infra")
	world.session("/w/c", "jane/widgets")
	world.session("/w/d", "other/thing")
	world.session("/w/e", "")
	world.myPrs = []string{"acme/app", "Acme/App", "third/party"}
	world.clones["acme/infra"] = origin.Clone{Path: "/w/a", How: origin.HowProjects}
	world.holder.dropped = []string{
		`repos.allow "*/*" dropped: that would allow every repository`,
	}

	settingsJSON, err := json.Marshal(world.holder.Get())
	if err != nil {
		t.Fatal(err)
	}

	want := `{"settings":` + string(settingsJSON) + `,"setup":false,"seen":[` +
		`{"repo":"acme/infra","from":"session"},{"repo":"jane/widgets","from":"session"},` +
		`{"repo":"other/thing","from":"session"},{"repo":"acme/app","from":"myprs"},` +
		`{"repo":"third/party","from":"myprs"}],"clones":[` +
		`{"repo":"acme/infra","path":"/w/a","how":"projects"},` +
		`{"repo":"jane/widgets","path":null,"how":null},` +
		`{"repo":"acme/app","path":null,"how":null}],` +
		`"matched":{"acme/*":2,"jane/widgets":1,"nobody/none":0},` +
		`"dropped":["repos.allow \"*/*\" dropped: that would allow every repository"],` +
		`"backedUp":null}`

	got := get(t, world.api())
	if got != want {
		t.Errorf("GET =\n%s\nwant\n%s", got, want)
	}
}

func TestGetSaysWhereTheFirstSaveKeptAnUnreadableFile(t *testing.T) {
	t.Parallel()

	world := newWorld(withAllow("acme/*"))
	world.holder.backedUp = "/data/settings.json.bak"

	got := get(t, world.api())
	if !strings.Contains(got, `"backedUp":"/data/settings.json.bak"`) {
		t.Errorf("GET = %s, want the backup's path", got)
	}
}

func TestGetInSetupOffersWhatWasSeenAndMatchesNothing(t *testing.T) {
	t.Parallel()

	world := newWorld(withAllow())
	world.holder.state.Repos.Allow = nil
	world.session("/w/a", "acme/infra")
	world.myPrs = []string{"acme/app"}
	world.clones["acme/infra"] = origin.Clone{Path: "/w/a", How: origin.HowSettings}

	got := get(t, world.api())
	for _, part := range []string{
		`"setup":true`,
		`"seen":[{"repo":"acme/infra","from":"session"},{"repo":"acme/app","from":"myprs"}]`,
		`"clones":[]`, `"matched":{}`, `"dropped":[]`,
	} {
		if !strings.Contains(got, part) {
			t.Errorf("GET = %s, want it to hold %s", got, part)
		}
	}
}

func TestGetNeverListsAMalformedRepository(t *testing.T) {
	t.Parallel()

	world := newWorld(withAllow())
	world.session("/w/a", "acme/infra")
	world.myPrs = []string{"../x", "a b/c", "acme/*", "ok/one?x=1"}

	got := get(t, world.api())
	if !strings.Contains(got, `"seen":[{"repo":"acme/infra","from":"session"}]`) {
		t.Errorf("GET = %s, want only acme/infra seen", got)
	}

	if !strings.Contains(got, `"setup":true`) {
		t.Errorf("GET = %s, want an empty list to be setup", got)
	}

	if len(world.gh.callList()) != 0 {
		t.Errorf("GET ran gh: %q", world.gh.callList())
	}
}

func TestGetSeesAtMost200Repositories(t *testing.T) {
	t.Parallel()

	world := newWorld(withAllow())
	for index := range 250 {
		world.myPrs = append(world.myPrs, "acme/r"+js.Number(float64(index)))
	}

	var document struct {
		Seen []settingsapi.Seen `json:"seen"`
	}

	err := json.Unmarshal([]byte(get(t, world.api())), &document)
	if err != nil {
		t.Fatal(err)
	}

	if len(document.Seen) != 200 {
		t.Errorf("len(seen) = %d, want 200", len(document.Seen))
	}
}

func TestPut(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		update   func(name string, body any) settings.UpdateResult
		wantCode int
		wantBody string
	}{
		{name: "a valid section", wantCode: 200, wantBody: `{"ok":true}`},
		{
			name: "a refusal names the entry",
			update: func(name string, body any) settings.UpdateResult {
				return settings.UpdateResult{
					OK: false, Error: "bad " + name + " " + js.Stringify(body),
				}
			},
			wantCode: 400,
			wantBody: `{"error":"bad slack {\"enabled\":true,\"channels\":[\"#code-review\"]}"}`,
		},
		{
			name: "a save that failed is a 500",
			update: func(string, any) settings.UpdateResult {
				return settings.UpdateResult{
					OK: false, Error: errDiskFull.Error(), SaveFailed: true,
				}
			},
			wantCode: 500, wantBody: `{"error":"settings could not be saved: disk full"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := newWorld(withAllow("acme/*"))
			world.holder.update = tt.update

			code, body := handle(t, world.api(),
				settingsapi.Route{Kind: settingsapi.KindPut, Section: "slack"},
				`{"enabled":true,"channels":["#code-review"]}`)
			if code != tt.wantCode {
				t.Errorf("code = %d, want %d", code, tt.wantCode)
			}

			if body != tt.wantBody {
				t.Errorf("body = %s, want %s", body, tt.wantBody)
			}

			want := `slack {"enabled":true,"channels":["#code-review"]}`
			if world.holder.updates[0] != want {
				t.Errorf("update = %s, want the body whole: %s", world.holder.updates[0], want)
			}
		})
	}
}

func TestNoneIsNotFound(t *testing.T) {
	t.Parallel()

	code, body := handle(t, newWorld(withAllow("acme/*")).api(), settingsapi.Route{}, "")
	if code != 404 {
		t.Errorf("code = %d, want 404", code)
	}

	if body != `{"error":"not found"}` {
		t.Errorf("body = %s", body)
	}
}

// statuses are module statuses whose probes record their calls.
func statuses(holder *holder, calls *[]string, guard *sync.Mutex) *modules.Statuses {
	probe := func(name string, result modules.ProbeResult) modules.Probe {
		return func(context.Context) (modules.ProbeResult, error) {
			guard.Lock()

			*calls = append(*calls, name)

			guard.Unlock()

			return result, nil
		}
	}

	return modules.NewStatuses(modules.Deps{
		Enabled: func(name string) bool { return holder.Get().Switch(name) },
		Probes: map[string]modules.Probe{
			settings.ModuleGitHub: probe("github",
				modules.ProbeResult{OK: true, Detail: "Signed in as jane"}),
			settings.ModuleSlack: probe("slack",
				modules.ProbeResult{OK: false, Detail: "Slack did not answer"}),
			settings.ModuleAtlassian: probe("atlassian",
				modules.ProbeResult{OK: true, Detail: "Atlassian connector works"}),
		},
		Now: func() time.Time { return time.UnixMilli(4242) },
	})
}

func TestGetCarriesTheModuleStatusesFromMemory(t *testing.T) {
	t.Parallel()

	var (
		guard sync.Mutex
		calls []string
	)

	world := newWorld(withAllow("acme/*"))
	world.modules = statuses(world.holder, &calls, &guard)
	world.modules.Record(settings.ModuleGitHub, true, "Signed in as jane")

	want := `"modules":{"github":{"state":"ok","detail":"Signed in as jane","at":4242},` +
		`"slack":{"state":"off","detail":"Off: conn makes no calls","at":null},` +
		`"atlassian":{"state":"off","detail":"Off: conn makes no calls","at":null}}`

	got := get(t, world.api())
	if !strings.HasSuffix(got, want+"}") {
		t.Errorf("GET = %s, want it to end with %s", got, want)
	}

	if len(calls) != 0 {
		t.Errorf("GET probed: %q", calls)
	}
}

func TestGetWithoutModuleStatusesCarriesNone(t *testing.T) {
	t.Parallel()

	got := get(t, newWorld(withAllow("acme/*")).api())
	if strings.Contains(got, `"modules"`) {
		t.Errorf("GET = %s, want no modules", got)
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()

	var (
		guard sync.Mutex
		calls []string
	)

	state := withAllow("acme/*")
	state.Slack.Enabled = true
	state.Atlassian.Enabled = true

	world := newWorld(state)
	world.modules = statuses(world.holder, &calls, &guard)
	api := world.api()

	slack := settingsapi.Route{Kind: settingsapi.KindCheck, Module: "slack"}
	code, body := handle(t, api, slack, "")
	want := `{"status":{"state":"failing","detail":"Slack did not answer","at":4242}}`

	if code != 200 {
		t.Errorf("code = %d, want 200", code)
	}

	if body != want {
		t.Errorf("body = %s, want %s", body, want)
	}

	handle(t, api, settingsapi.Route{Kind: settingsapi.KindCheck, Module: "github"}, "")
	got := get(t, api)

	if !strings.Contains(got, `"atlassian":{"state":"unchecked"`) {
		t.Errorf("GET = %s, want atlassian unchecked", got)
	}

	if strings.Join(calls, ",") != "slack,github" {
		t.Errorf("calls = %q, want slack then github", calls)
	}
}

func TestCheckAModuleThatIsOff(t *testing.T) {
	t.Parallel()

	var (
		guard sync.Mutex
		calls []string
	)

	world := newWorld(withAllow("acme/*"))
	world.modules = statuses(world.holder, &calls, &guard)

	code, body := handle(t, world.api(),
		settingsapi.Route{Kind: settingsapi.KindCheck, Module: "slack"}, "")
	if code != 409 {
		t.Errorf("code = %d, want 409", code)
	}

	if body != `{"error":"Slack is off: save its switch on before checking it"}` {
		t.Errorf("body = %s", body)
	}

	if len(calls) != 0 {
		t.Errorf("probes ran: %q", calls)
	}
}

func TestCheckWithNoModuleStatuses(t *testing.T) {
	t.Parallel()

	code, _ := handle(t, newWorld(withAllow("acme/*")).api(),
		settingsapi.Route{Kind: settingsapi.KindCheck, Module: "github"}, "")
	if code != 404 {
		t.Errorf("code = %d, want 404", code)
	}
}
