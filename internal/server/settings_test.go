package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/modules"
	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/server"
	"github.com/claytoncasey01/conn/internal/settings"
	"github.com/claytoncasey01/conn/internal/settingsapi"
)

const (
	ownOrigin = "http://127.0.0.1:7777"
	seeded    = `{"version":1,"repos":{"allow":["acme/*"]}}`
)

var errNothingMayRun = errors.New("nothing outside the process may run in this test")

// rig is a server over a real settings store in a temp data folder. Nothing it holds may run gh
// or a probe: ran counts any attempt.
type rig struct {
	handler http.Handler
	data    string
	store   *settings.Store
	mu      sync.Mutex
	ran     int
}

// boot starts a rig whose data folder holds file as settings.json, or no file when it is "".
func boot(t *testing.T, file string) *rig {
	t.Helper()

	data := t.TempDir()
	if file != "" {
		err := os.WriteFile(filepath.Join(data, "settings.json"), []byte(file), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	held := &rig{data: data, store: settings.NewStore(log.New(io.Discard, "", 0))}
	held.store.Reload(data)

	refuse := func(context.Context) (modules.ProbeResult, error) {
		held.attempted()

		return modules.ProbeResult{}, errNothingMayRun
	}

	statuses := modules.NewStatuses(modules.Deps{
		Enabled: func(name string) bool { return held.store.Get().Switch(name) },
		Probes: map[string]modules.Probe{
			settings.ModuleGitHub: refuse, settings.ModuleSlack: refuse,
			settings.ModuleAtlassian: refuse,
		},
		Now: time.Now,
	})

	held.handler = &server.Server{
		UI: bundle(), Secret: testKey(t), Clients: nil, Logger: log.New(io.Discard, "", 0),
		Settings: settingsapi.New(settingsapi.Deps{
			Settings: held.store,
			Gh: func(context.Context, []string) (string, error) {
				held.attempted()

				return "", errNothingMayRun
			},
			Modules: statuses,
		}),
	}

	return held
}

func (r *rig) attempted() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.ran++
}

func (r *rig) attempts() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.ran
}

// request is one request to the rig. Without headers it is a write from Conn's own page.
type request struct {
	method  string
	target  string
	body    string
	headers map[string]string
}

func own() map[string]string { return map[string]string{"X-Conn": "1", "Origin": ownOrigin} }

func (r *rig) send(t *testing.T, sent request) answer {
	t.Helper()

	incoming := httptest.NewRequestWithContext(
		t.Context(), sent.method, sent.target, strings.NewReader(sent.body))
	incoming.Host = "127.0.0.1:7777"
	incoming.Header.Set(secret.Header, testKey(t).Authorization())

	headers := sent.headers
	if headers == nil {
		headers = own()
	}

	for key, value := range headers {
		if key == "Host" {
			incoming.Host = value

			continue
		}

		incoming.Header.Set(key, value)
	}

	recorder := httptest.NewRecorder()
	r.handler.ServeHTTP(recorder, incoming)

	response := recorder.Result()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	_ = response.Body.Close()

	return answer{status: response.StatusCode, header: response.Header, body: string(body)}
}

func (r *rig) put(t *testing.T, section, body string) answer {
	t.Helper()

	return r.send(t, request{
		method: http.MethodPut, target: "/api/settings/" + section, body: body, headers: nil,
	})
}

// document is the parts of GET /api/settings a test reads.
type document struct {
	Settings settings.Settings `json:"settings"`
	Setup    bool              `json:"setup"`
	Matched  map[string]int    `json:"matched"`
	Dropped  []string          `json:"dropped"`
	BackedUp *string           `json:"backedUp"`
	Modules  json.RawMessage   `json:"modules"`
}

func (r *rig) document(t *testing.T) (document, string) {
	t.Helper()

	got := r.send(t, request{
		method: http.MethodGet, target: "/api/settings", body: "", headers: map[string]string{},
	})
	if got.status != http.StatusOK {
		t.Fatalf("GET /api/settings = %d %s", got.status, got.body)
	}

	var parsed document

	err := json.Unmarshal([]byte(got.body), &parsed)
	if err != nil {
		t.Fatal(err)
	}

	return parsed, got.body
}

func (r *rig) file(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(r.data, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func TestNoSettingsFileIsSetupAndWritesNothing(t *testing.T) {
	t.Parallel()

	held := boot(t, "")
	parsed, raw := held.document(t)

	if !parsed.Setup {
		t.Error("setup = false, want true")
	}

	for _, part := range []string{
		`"allow":null`, `"seen":[]`, `"clones":[]`, `"matched":{}`, `"dropped":[]`,
		`"backedUp":null`, `"github":{"enabled":true}`,
	} {
		if !strings.Contains(raw, part) {
			t.Errorf("GET = %s, want it to hold %s", raw, part)
		}
	}

	_, err := os.Stat(filepath.Join(held.data, "settings.json"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("settings.json: %v, want none written", err)
	}
}

func TestAPutOfReposEndsSetupInAPrivateFile(t *testing.T) {
	t.Parallel()

	held := boot(t, "")

	got := held.put(t, "repos",
		`{"allow":["ACME/*","jane/widgets"],"clones":{"acme/infra":"/work/infra"}}`)
	if got.status != http.StatusOK {
		t.Fatalf("PUT = %d %s", got.status, got.body)
	}

	if got.body != `{"ok":true}` {
		t.Errorf("PUT = %s, want {\"ok\":true}", got.body)
	}

	_, raw := held.document(t)
	for _, part := range []string{
		`"repos":{"allow":["acme/*","jane/widgets"],"clones":{"acme/infra":"/work/infra"},` +
			`"cloneRoot":null}`,
		`"setup":false`, `"matched":{"acme/*":0,"jane/widgets":0}`,
	} {
		if !strings.Contains(raw, part) {
			t.Errorf("GET = %s, want it to hold %s", raw, part)
		}
	}

	info, err := os.Stat(filepath.Join(held.data, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestAPutReplacesJustItsSection(t *testing.T) {
	t.Parallel()

	held := boot(t, seeded)
	for _, put := range []struct{ section, body string }{
		{"slack", `{"enabled":true,"channels":["#code-review"],"dms":true,` +
			`"draftChannel":"#code-review"}`},
		{"reviews", `{"requested":false,"allOpen":true}`},
		{"github", `{"enabled":false}`},
		{"atlassian", `{"enabled":true,"jira":{"projects":["PROJ"],"buildRepo":"acme/app"}}`},
	} {
		got := held.put(t, put.section, put.body)
		if got.status != http.StatusOK {
			t.Fatalf("PUT %s = %d %s", put.section, got.status, got.body)
		}
	}

	_, raw := held.document(t)
	for _, part := range []string{
		`"allow":["acme/*"]`,
		`"slack":{"enabled":true,"channels":["#code-review"],"dms":true,` +
			`"draftChannel":"#code-review"}`,
		`"reviews":{"requested":false,"allOpen":true}`, `"github":{"enabled":false}`,
		`"atlassian":{"enabled":true,"jira":{"projects":["PROJ"],"buildRepo":"acme/app",` +
			`"baseBranch":null,"baseBranches":{},"reviewMove":"all","sessionLayout":"shared"}}`,
	} {
		if !strings.Contains(raw, part) {
			t.Errorf("GET = %s, want it to hold %s", raw, part)
		}
	}
}

func TestAnInvalidPutNamesTheEntryAndChangesNothing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		section string
		body    string
		names   string
	}{
		{section: "repos", body: `{"allow":["acme/*","acme/re*"]}`, names: "acme/re*"},
		{section: "repos", body: `{"allow":["*/*"]}`, names: "*/*"},
		{section: "repos", body: `{"allow":[]}`, names: "at least one"},
		{section: "repos", body: `{"allow":["../x"]}`, names: "../x"},
		{section: "slack", body: `{"enabled":true,"channels":["#Bad Name"]}`, names: "#Bad Name"},
		{
			section: "slack", body: `{"enabled":true,"channels":["#a"],"draftChannel":"#b"}`,
			names: "#b",
		},
		{
			section: "atlassian", body: `{"enabled":true,"jira":{"projects":["bad key"]}}`,
			names: "bad key",
		},
		{
			section: "atlassian", body: `{"enabled":true,"jira":{"buildRepo":"other/app"}}`,
			names: "other/app",
		},
		{section: "github", body: `{"enabled":"yes"}`, names: "github.enabled"},
		{section: "reviews", body: `{"requested":true}`, names: "reviews"},
	}

	for _, tt := range tests {
		t.Run(tt.section+" "+tt.body, func(t *testing.T) {
			t.Parallel()

			held := boot(t, seeded)

			got := held.put(t, tt.section, tt.body)
			if got.status != http.StatusBadRequest {
				t.Errorf("PUT = %d, want 400", got.status)
			}

			var refusal settingsapi.ErrorBody

			err := json.Unmarshal([]byte(got.body), &refusal)
			if err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(refusal.Error, tt.names) {
				t.Errorf("error = %q, want it to name %q", refusal.Error, tt.names)
			}

			if held.file(t) != seeded {
				t.Errorf("settings.json = %s, want it unchanged", held.file(t))
			}
		})
	}
}

func TestTheBodyMustBeAJSONObject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "text", body: "not json", want: http.StatusBadRequest},
		{name: "an array", body: "[]", want: http.StatusBadRequest},
		{name: "a number", body: "5", want: http.StatusBadRequest},
		{name: "a string", body: `"x"`, want: http.StatusBadRequest},
		{name: "null", body: "null", want: http.StatusBadRequest},
		{
			name: "over 64 KB",
			body: `{"allow":["acme/*"],"pad":"` + strings.Repeat("x", 70_000) + `"}`,
			want: http.StatusRequestEntityTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			held := boot(t, seeded)

			got := held.put(t, "repos", tt.body)
			if got.status != tt.want {
				t.Errorf("PUT = %d, want %d", got.status, tt.want)
			}

			if held.file(t) != seeded {
				t.Errorf("settings.json = %s, want it unchanged", held.file(t))
			}
		})
	}
}

func TestAnEmptyBodyIsAnEmptyObject(t *testing.T) {
	t.Parallel()

	got := boot(t, seeded).put(t, "github", "")
	if got.status != http.StatusBadRequest {
		t.Errorf("PUT = %d, want 400: {} is not a valid github section", got.status)
	}

	if !strings.Contains(got.body, "github.enabled") {
		t.Errorf("PUT = %s, want the section's own refusal", got.body)
	}
}

// guardCases are writes the guard refuses, and the page's own write it lets through.
func guardCases() map[string]map[string]string {
	return map[string]map[string]string{
		"nothing":          {},
		"the header alone": {"X-Conn": "1"},
		"an origin alone":  {"Origin": ownOrigin},
		"another site":     {"X-Conn": "1", "Origin": "http://evil.example"},
		"an opaque origin": {"X-Conn": "1", "Origin": "null"},
		"another host":     {"X-Conn": "1", "Origin": ownOrigin, "Host": "evil.example"},
	}
}

func TestAWriteNeedsTheHeaderAndALocalOrigin(t *testing.T) {
	t.Parallel()

	for name, headers := range guardCases() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			held := boot(t, seeded)

			got := held.send(t, request{
				method: http.MethodPut, target: "/api/settings/github",
				body: `{"enabled":false}`, headers: headers,
			})
			if got.status != http.StatusForbidden {
				t.Errorf("PUT = %d %s, want 403", got.status, got.body)
			}

			if held.file(t) != seeded {
				t.Errorf("settings.json = %s, want it unchanged", held.file(t))
			}
		})
	}
}

func TestThePagesOwnWriteGetsThrough(t *testing.T) {
	t.Parallel()

	got := boot(t, seeded).put(t, "github", `{"enabled":false}`)
	if got.status != http.StatusOK {
		t.Errorf("PUT = %d %s, want 200", got.status, got.body)
	}
}

func TestBrowseIsRefusedBeforeAnyGhRuns(t *testing.T) {
	t.Parallel()

	for name, headers := range guardCases() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			held := boot(t, seeded)

			got := held.send(t, request{
				method: http.MethodPost, target: "/api/settings/browse", body: "{}",
				headers: headers,
			})
			if got.status != http.StatusForbidden {
				t.Errorf("POST = %d %s, want 403", got.status, got.body)
			}

			if held.attempts() != 0 {
				t.Error("gh ran before the guard refused the request")
			}
		})
	}
}

func TestGetFromAnotherOriginIsRefused(t *testing.T) {
	t.Parallel()

	got := boot(t, seeded).send(t, request{
		method: http.MethodGet, target: "/api/settings",
		headers: map[string]string{"Origin": "http://evil.example"},
	})
	if got.status != http.StatusForbidden {
		t.Errorf("GET = %d, want 403", got.status)
	}
}

func TestEachRouteHasOneMethod(t *testing.T) {
	t.Parallel()

	const notAllowed = http.StatusMethodNotAllowed

	tests := []struct {
		method string
		target string
		want   int
	}{
		{method: http.MethodPost, target: "/api/settings", want: http.StatusMethodNotAllowed},
		{method: http.MethodPut, target: "/api/settings", want: http.StatusMethodNotAllowed},
		{method: http.MethodGet, target: "/api/settings/github", want: http.StatusMethodNotAllowed},
		{method: http.MethodPost, target: "/api/settings/github", want: notAllowed},
		{method: http.MethodGet, target: "/api/settings/browse", want: http.StatusMethodNotAllowed},
		{method: http.MethodPut, target: "/api/settings/browse", want: http.StatusMethodNotAllowed},
		{method: http.MethodPut, target: "/api/settings/nope", want: http.StatusNotFound},
		{method: http.MethodPut, target: "/api/settings/", want: http.StatusNotFound},
		{method: http.MethodPut, target: "/api/settings/%72epos", want: http.StatusNotFound},
		{method: http.MethodGet, target: "/api/settings/check/slack", want: notAllowed},
		{method: http.MethodPut, target: "/api/settings/check/slack", want: notAllowed},
		{method: http.MethodPost, target: "/api/settings/check", want: http.StatusNotFound},
		{method: http.MethodPost, target: "/api/settings/check/", want: http.StatusNotFound},
		{method: http.MethodPost, target: "/api/settings/check/other", want: http.StatusNotFound},
		{method: http.MethodPost, target: "/api/settings/check/slack/x", want: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.target, func(t *testing.T) {
			t.Parallel()

			got := boot(t, seeded).send(t, request{
				method: tt.method, target: tt.target, body: "{}", headers: nil,
			})
			if got.status != tt.want {
				t.Errorf("%s %s = %d, want %d", tt.method, tt.target, got.status, tt.want)
			}
		})
	}
}

func TestASettingsFileThatIsNotJSONIsSetupAndLeftAlone(t *testing.T) {
	t.Parallel()

	broken := `{"repos": {"allow": ["acme/*"`
	held := boot(t, broken)
	parsed, _ := held.document(t)

	if !parsed.Setup {
		t.Error("setup = false, want true")
	}

	if !strings.Contains(strings.Join(parsed.Dropped, "\n"), "not valid JSON") {
		t.Errorf("dropped = %q, want it to say the file is not valid JSON", parsed.Dropped)
	}

	if held.file(t) != broken {
		t.Errorf("settings.json = %s, want it left alone", held.file(t))
	}
}

func TestAHandEditedFileWidensNothingAndSaysWhatWasDropped(t *testing.T) {
	t.Parallel()

	held := boot(t, `{"repos":{"allow":["*","*/*","acme/re*","ACME/Widgets ","../x",5],`+
		`"clones":{"acme/widgets":"relative"}},"github":{"enabled":"yes"}}`)
	parsed, raw := held.document(t)

	if !strings.Contains(raw, `"repos":{"allow":["acme/widgets"],"clones":{}`) {
		t.Errorf("GET = %s, want only acme/widgets allowed and no clone", raw)
	}

	if !parsed.Settings.GitHub.Enabled {
		t.Error("github.enabled = false, want the default")
	}

	if parsed.Setup {
		t.Error("setup = true, want false")
	}

	said := strings.Join(parsed.Dropped, "\n")
	for _, gone := range []string{"*/*", "acme/re*", "../x", "relative"} {
		if !strings.Contains(said, gone) {
			t.Errorf("dropped = %q, want it to name %q", said, gone)
		}
	}
}

func TestAnAllowOfTheWrongTypeIsSetup(t *testing.T) {
	t.Parallel()

	_, raw := boot(t, `{"repos":{"allow":"acme/*"}}`).document(t)
	for _, part := range []string{`"setup":true`, `"allow":null`} {
		if !strings.Contains(raw, part) {
			t.Errorf("GET = %s, want it to hold %s", raw, part)
		}
	}
}

func TestTheFirstSaveOverAnUnreadableFileKeepsABackup(t *testing.T) {
	t.Parallel()

	original := `{"repos":{"allow":["acme/*"],},"slack":{"enabled":true}}`
	held := boot(t, original)

	before, _ := held.document(t)
	if before.BackedUp != nil {
		t.Errorf("backedUp = %q before a save, want null", *before.BackedUp)
	}

	for _, put := range []struct{ section, body string }{
		{"repos", `{"allow":["jane/widgets"]}`}, {"github", `{"enabled":false}`},
	} {
		got := held.put(t, put.section, put.body)
		if got.status != http.StatusOK {
			t.Fatalf("PUT %s = %d %s", put.section, got.status, got.body)
		}

		backup, err := os.ReadFile(filepath.Join(held.data, "settings.json.bak"))
		if err != nil {
			t.Fatal(err)
		}

		if string(backup) != original {
			t.Errorf("settings.json.bak = %s, want the original", backup)
		}
	}

	after, _ := held.document(t)

	want := filepath.Join(held.data, "settings.json.bak")
	if after.BackedUp == nil {
		t.Fatalf("backedUp = null, want %s", want)
	}

	if *after.BackedUp != want {
		t.Errorf("backedUp = %s, want %s", *after.BackedUp, want)
	}
}

func TestConcurrentPutsOnlyEverHoldOneRequestsSection(t *testing.T) {
	t.Parallel()

	const writes = 16

	held := boot(t, seeded)

	var group sync.WaitGroup

	for index := range writes {
		group.Go(func() {
			channel := "#c" + strconv.Itoa(index)
			dms := "false"

			if index%2 == 0 {
				dms = "true"
			}

			got := held.put(t, "slack", `{"enabled":true,"channels":["`+channel+`"],`+
				`"draftChannel":"`+channel+`","dms":`+dms+`}`)
			if got.status != http.StatusOK {
				t.Errorf("PUT = %d %s", got.status, got.body)
			}
		})
		group.Go(func() { consistentSlack(t, held.store.Get().Slack) })
	}

	group.Wait()

	var onDisk settings.Settings

	err := json.Unmarshal([]byte(held.file(t)), &onDisk)
	if err != nil {
		t.Fatal(err)
	}

	consistentSlack(t, onDisk.Slack)

	entries, err := os.ReadDir(held.data)
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Errorf("a temp file was left behind: %s", entry.Name())
		}
	}
}

// consistentSlack checks a Slack section holds one request's fields, never fields of two.
func consistentSlack(t *testing.T, slack settings.Slack) {
	t.Helper()

	if len(slack.Channels) == 0 {
		if slack.DraftChannel != nil {
			t.Errorf("draftChannel = %s with no channel", *slack.DraftChannel)
		}

		return
	}

	channel := slack.Channels[0]
	if slack.DraftChannel == nil {
		t.Fatalf("draftChannel = null, want %s", channel)
	}

	if *slack.DraftChannel != channel {
		t.Errorf("draftChannel = %s, want %s from the same request", *slack.DraftChannel, channel)
	}

	index, err := strconv.Atoi(strings.TrimPrefix(channel, "#c"))
	if err != nil {
		t.Fatalf("channel %s is not one a request sent", channel)
	}

	wantDMs := index%2 == 0
	if slack.DMs != wantDMs {
		t.Errorf("dms = %v for %s, want %v", slack.DMs, channel, wantDMs)
	}
}

func TestGetCarriesModuleStatusesAndRunsNoProbe(t *testing.T) {
	t.Parallel()

	held := boot(t, seeded)
	parsed, _ := held.document(t)

	want := `{"github":{"state":"unchecked","detail":"Not checked yet","at":null},` +
		`"slack":{"state":"off","detail":"Off: conn makes no calls","at":null},` +
		`"atlassian":{"state":"off","detail":"Off: conn makes no calls","at":null}}`
	if string(parsed.Modules) != want {
		t.Errorf("modules = %s, want %s", parsed.Modules, want)
	}

	if held.attempts() != 0 {
		t.Errorf("a probe ran %d times", held.attempts())
	}
}

func TestCheckingAModuleThatIsOffIsAConflict(t *testing.T) {
	t.Parallel()

	for _, module := range []string{"slack", "atlassian"} {
		t.Run(module, func(t *testing.T) {
			t.Parallel()

			held := boot(t, seeded)

			got := held.send(t, request{
				method: http.MethodPost, target: "/api/settings/check/" + module, body: "{}",
			})
			if got.status != http.StatusConflict {
				t.Errorf("POST = %d, want 409", got.status)
			}

			if !strings.Contains(got.body, " is off: save its switch on") {
				t.Errorf("POST = %s, want it to say the module is off", got.body)
			}

			if held.attempts() != 0 {
				t.Errorf("a probe ran %d times", held.attempts())
			}
		})
	}
}

func TestCheckIsBehindTheWriteGuard(t *testing.T) {
	t.Parallel()

	held := boot(t, seeded)
	for _, headers := range []map[string]string{
		{},
		{"X-Conn": "1"},
		{"Origin": ownOrigin},
		{"X-Conn": "1", "Origin": "http://evil.example"},
	} {
		got := held.send(t, request{
			method: http.MethodPost, target: "/api/settings/check/github", body: "{}",
			headers: headers,
		})
		if got.status != http.StatusForbidden {
			t.Errorf("POST with %v = %d, want 403", headers, got.status)
		}
	}

	if held.attempts() != 0 {
		t.Errorf("a probe ran %d times", held.attempts())
	}
}

// panicking is a settings source with a bug in it.
type panicking struct{ settings.Store }

func (*panicking) Get() settings.Settings { panic("a bug in the settings source") }

func TestAFailingRequestIsAServerErrorAndTheReasonIsLogged(t *testing.T) {
	t.Parallel()

	var logged strings.Builder

	handler := &server.Server{
		UI: bundle(), Secret: testKey(t), Clients: nil, Logger: log.New(&logged, "", 0),
		Settings: settingsapi.New(settingsapi.Deps{Settings: &panicking{}}),
	}

	got := get(t, handler, "/api/settings", "", "")
	if got.status != http.StatusInternalServerError {
		t.Errorf("GET = %d, want 500", got.status)
	}

	if got.body != `{"error":"the server could not handle that request"}` {
		t.Errorf("GET = %s", got.body)
	}

	if !strings.Contains(logged.String(), "request failed /api/settings a bug in the settings") {
		t.Errorf("logged %q, want the failure", logged.String())
	}
}
