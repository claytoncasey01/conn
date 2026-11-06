package server_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/openwork"
	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/server"
	"github.com/claytoncasey01/conn/internal/settings"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// Open work through the server, and the 413 for its routes, through the
// handler: a dry Open work, so no Jira, git or claude is ever reached. It is not there while
// the Atlassian module is off, lists no project or the server is in setup; a change in Settings
// takes effect with no restart; a run needs a clone of the build repository.

const offText = `{"error":"Atlassian is off. Enable it in Settings."}`

type workRig struct {
	reviewsRig

	store *settings.Store
	work  *openwork.Work
	data  string
	clone string
}

// newWorkRig is a dry Open work over settings (JSON with CLONE standing for a stand-in clone of
// acme/widgets whose default branch is trunk; "" is no settings file).
func newWorkRig(t *testing.T, current string) workRig {
	t.Helper()

	base := t.TempDir()
	clone := filepath.Join(base, "scratch", "widgets")
	heads := filepath.Join(clone, ".git", "refs", "remotes", "origin")

	err := os.MkdirAll(heads, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(clone, ".git", "config"),
		"[remote \"origin\"]\n\turl = git@github.com:acme/widgets.git\n")
	writeFile(t, filepath.Join(heads, "HEAD"), "ref: refs/remotes/origin/trunk\n")

	settingsDir := filepath.Join(base, "settings")
	data := filepath.Join(base, "data")

	err = os.MkdirAll(settingsDir, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	if current != "" {
		writeFile(t, filepath.Join(settingsDir, "settings.json"),
			strings.ReplaceAll(current, "CLONE", js.Stringify(clone)))
	}

	store := settings.NewStore(nil)
	store.Reload(settingsDir)

	work := newDryWork(t, store, data, base)
	store.OnChange(func(settings.Settings) { work.SettingsChanged() })

	return workRig{
		reviewsRig: reviewsRig{
			handler: &server.Server{UI: bundle(), Clients: func() int { return 0 }, Work: work},
			tab:     nil, data: data,
		},
		store: store, work: work, data: data, clone: clone,
	}
}

func newDryWork(t *testing.T, store *settings.Store, data, home string) *openwork.Work {
	t.Helper()

	reader := origin.NewReader()
	ctx, stop := context.WithCancel(context.Background())
	work := openwork.New(ctx, openwork.Deps{
		DataDir: data, Dry: true, Settings: store.Get,
		CloneFor: func(repo string, current settings.Settings) (string, bool) {
			clone, found := reader.CloneFor(repo, current, home)

			return clone.Path, found
		},
		OriginOf: reader.Of, Sh: nil, Helper: nil, FetchSprint: nil, Transition: nil,
		Open: nil, Record: nil, FindSession: nil, Reply: nil, Bump: nil,
		Now: func() float64 { return float64(time.Now().UnixMilli()) },
		Sleep: func(ctx context.Context, wait time.Duration) {
			select {
			case <-ctx.Done():
			case <-time.After(wait / 40):
			}
		},
		Logger: nil,
	})

	t.Cleanup(func() {
		stop()
		work.Settle()
	})

	return work
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	err := os.WriteFile(path, []byte(content), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

func workSettings(atlassian, repos string) string {
	if repos == "" {
		repos = `{"allow":["acme/*"],"clones":{"acme/widgets":CLONE}}`
	}

	return `{"version":1,"repos":` + repos + `,"atlassian":` + atlassian + `}`
}

const (
	workOn = `{"enabled":true,"jira":{"projects":["PROJ"],"buildRepo":"acme/widgets"}}`
	// workOffSwitch is the module switched off; workNoProject lists no project.
	workOffSwitch = `{"enabled":false,"jira":{"projects":["PROJ"],"buildRepo":"acme/widgets"}}`
	workNoProject = `{"enabled":true,"jira":{"projects":[],"buildRepo":null}}`
)

func TestOpenWorkIsNotThereWhileItIsOff(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"default":      `{"version":1,"repos":{"allow":["acme/*"]}}`,
		"switched off": workSettings(workOffSwitch, ""),
		"no project":   workSettings(workNoProject, ""),
		"setup":        "",
	}

	for name, current := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rig := newWorkRig(t, current)
			code, answer := rig.send(t, reviewsCall{method: http.MethodGet, path: "/api/work"})
			same(t, "list", []any{code, answer}, []any{404, offText})

			// The guard comes first (403 without the header), then the module (404).
			code, _ = rig.send(t, reviewsCall{
				method: http.MethodPost, path: "/api/work/PROJ-1/start", body: "{}",
			})
			same(t, "guard", code, 403)

			for _, path := range []string{
				"/api/work/refresh", "/api/work/PROJ-1/start", "/api/runs/PROJ-1/answers",
				"/api/runs/PROJ-1/pr", "/api/runs/PROJ-1/jira", "/api/runs/PROJ-1/cleanup",
			} {
				code, answer = rig.send(t, postOwn(path, `{"status":"Done","answers":{}}`))
				same(t, path, []any{code, answer}, []any{404, offText})
			}

			same(t, "runs", len(rig.work.Runs()), 0)
			same(t, "cache", fileThere(filepath.Join(rig.data, "work-cache.json")), false)
		})
	}
}

func fileThere(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

func same(t *testing.T, label string, got, want any) {
	t.Helper()

	if js.Stringify(got) != js.Stringify(want) {
		t.Errorf("%s:\n got %s\nwant %s", label, js.Stringify(got), js.Stringify(want))
	}
}

// waitTickets is the dry list once it has arrived.
func (r workRig) waitTickets(t *testing.T) *js.Object {
	t.Helper()

	end := time.Now().Add(testtime.Scaled(6 * time.Second))
	for time.Now().Before(end) {
		code, answer := r.send(t, reviewsCall{method: http.MethodGet, path: "/api/work"})
		list := parse(t, answer)

		if code == 200 {
			tickets, _ := field(list, "tickets").([]any)
			if len(tickets) > 0 {
				if field(list, "fetching") == false {
					return list
				}
			}
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("the dry list never arrived")

	return nil
}

func parse(t *testing.T, text string) *js.Object {
	t.Helper()

	parsed, err := js.Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}

	object, _ := parsed.(*js.Object)

	return object
}

func field(object *js.Object, key string) any {
	value, _ := object.Get(key)

	return value
}

func TestWithOpenWorkOnTheDrySprintIsSyntheticAndTheBuildRepoHasItsClone(t *testing.T) {
	t.Parallel()

	rig := newWorkRig(t, workSettings(workOn, ""))
	list := rig.waitTickets(t)

	same(t, "projects", field(list, "projects"), []any{"PROJ"})
	same(t, "build", field(list, "build"), parse(t,
		`{"repo":"acme/widgets","base":"trunk","ready":true,"reason":null}`))

	tickets, _ := field(list, "tickets").([]any)
	keys, assignees := make([]any, 0, len(tickets)), map[string]bool{}

	for _, ticket := range tickets {
		listed, _ := ticket.(*js.Object)
		keys = append(keys, field(listed, "key"))
		assignees[js.String(field(listed, "assignee"))] = true
	}

	same(t, "keys", keys, []any{"PROJ-1", "PROJ-2", "PROJ-3", "PROJ-4", "PROJ-5", "PROJ-6"})
	same(t, "assignees", len(assignees), 3)
}

func TestAChangeInSettingsTakesEffectWithNoRestart(t *testing.T) {
	t.Parallel()

	rig := newWorkRig(t, workSettings(workOn, ""))
	rig.waitTickets(t)
	rig.update(t, workOffSwitch)

	code, _ := rig.send(t, reviewsCall{method: http.MethodGet, path: "/api/work"})
	same(t, "off", code, 404)
	rig.update(t, workOn)

	code, _ = rig.send(t, reviewsCall{method: http.MethodGet, path: "/api/work"})
	same(t, "on", code, 200)
}

func (r workRig) update(t *testing.T, atlassian string) {
	t.Helper()

	parsed, err := js.Parse([]byte(atlassian))
	if err != nil {
		t.Fatal(err)
	}

	result := r.store.Update(settings.ModuleAtlassian, parsed)
	if !result.OK {
		t.Fatal(result.Error)
	}
}

func TestAnotherProjectKeyDropsTheOldList(t *testing.T) {
	t.Parallel()

	rig := newWorkRig(t, workSettings(workOn, ""))
	rig.waitTickets(t)
	rig.update(t, `{"enabled":true,"jira":{"projects":["OTHER"],"buildRepo":"acme/widgets"}}`)

	for range 6 {
		_, answer := rig.send(t, reviewsCall{method: http.MethodGet, path: "/api/work"})
		list := parse(t, answer)
		same(t, "projects", field(list, "projects"), []any{"OTHER"})
		same(t, "tickets", field(list, "tickets"), []any{})
		time.Sleep(20 * time.Millisecond)
	}

	code, answer := rig.send(t, postOwn("/api/work/PROJ-2/start",
		`{"model":"haiku","force":true}`))
	same(t, "start", code, 400)

	if !strings.Contains(answer, "not a ticket key in a Jira project listed in Settings") {
		t.Error(answer)
	}
}

func TestARunIsMadeOnlyForATicketOfTheProjectWithACloneToBuildIn(t *testing.T) {
	t.Parallel()

	rig := newWorkRig(t, workSettings(workOn, ""))
	rig.waitTickets(t)

	refusals := []struct {
		path, body, confirm string
		code                int
	}{
		{"/api/work/PROJ-2/start", `{"model":"bogus"}`, "", 400},
		{"/api/work/NOPE-2/start", `{"model":"haiku"}`, "", 400},
		{"/api/work/PROJ-4/start", `{"model":"haiku"}`, "assignee", 409},
		{"/api/work/PROJ-1/start", `{"model":"haiku"}`, "status", 409},
	}

	for _, refusal := range refusals {
		code, answer := rig.send(t, postOwn(refusal.path, refusal.body))
		same(t, refusal.path, code, refusal.code)

		if refusal.confirm != "" {
			same(t, refusal.path+" confirm", field(parse(t, answer), "needsConfirm"),
				refusal.confirm)
		}
	}

	code, _ := rig.send(t, postOwn("/api/work/PROJ-2/start", `{"model":"haiku"}`))
	same(t, "start", code, 200)

	runs := rig.work.Runs()
	same(t, "runs", len(runs), 1)

	run := parse(t, js.Stringify(runs[0]))
	repos, _ := field(run, "repos").([]any)
	same(t, "legs", len(repos), 1)

	leg, _ := repos[0].(*js.Object)
	same(t, "branch", field(leg, "branch"), "proj-2-nightly-job-skipped-when-the-batch")
	same(t, "bases", []any{field(leg, "base"), field(leg, "prBase")}, []any{"trunk", "trunk"})
	same(t, "worktree", field(leg, "worktree"),
		filepath.Join(filepath.Dir(rig.clone), "widgets-proj-2"))
	same(t, "clone", field(leg, "clone"), nil)
	same(t, "layout", field(run, "layout"), "shared")

	data, err := os.ReadFile(filepath.Join(rig.data, "runs", "PROJ-2.json"))
	if err != nil {
		t.Fatal(err)
	}

	record := parse(t, string(data))
	legs, _ := field(record, "repos").([]any)
	kept, _ := legs[0].(*js.Object)
	same(t, "on disk", []any{
		field(kept, "repo"), field(kept, "clone"),
		field(record, "ticketCategory"),
	}, []any{"acme/widgets", rig.clone, "new"})

	entries, _ := os.ReadDir(filepath.Dir(rig.clone))
	same(t, "beside the clone", len(entries), 1)
}

func TestWithoutACloneStartIsRefusedWithTheReason(t *testing.T) {
	t.Parallel()

	rig := newWorkRig(t, workSettings(workOn,
		`{"allow":["acme/*"],"clones":{"acme/widgets":"/no/such"}}`))
	reason := "No local clone of acme/widgets: set a folder in Settings."
	list := rig.waitTickets(t)
	same(t, "build", field(list, "build"), parse(t, `{"repo":"acme/widgets","base":null,`+
		`"ready":false,"reason":`+js.Stringify(reason)+`}`))

	code, answer := rig.send(t, postOwn("/api/work/PROJ-2/start", `{"model":"haiku"}`))
	same(t, "start", []any{code, field(parse(t, answer), "error")}, []any{409, reason})
	same(t, "runs", len(rig.work.Runs()), 0)
}

func TestAWorkRouteBodyOver64KBIsA413OnOrOff(t *testing.T) {
	t.Parallel()

	big := `{"text":"` + strings.Repeat("x", 70_000) + `"}`

	for _, current := range []string{
		`{"version":1,"repos":{"allow":["acme/*"]}}`,
		workSettings(workOn, ""),
	} {
		rig := newWorkRig(t, current)
		code, answer := rig.send(t, reviewsCall{
			method: http.MethodPost, path: "/api/runs/PROJ-1/answers", body: big,
			headers: map[string]string{"X-Conn": "1"},
		})
		same(t, "code", code, 413)

		if !strings.Contains(answer, "too large") {
			t.Error(answer)
		}
	}
}

func TestTheSnapshotHoldsNoRunsWhileOpenWorkIsOff(t *testing.T) {
	t.Parallel()

	rig := newWorkRig(t, workSettings(workOn, ""))
	rig.waitTickets(t)

	code, _ := rig.send(t, postOwn("/api/work/PROJ-2/start", `{"model":"haiku"}`))
	same(t, "start", code, 200)
	same(t, "on", len(rig.work.Runs()), 1)
	rig.update(t, workOffSwitch)
	same(t, "off", len(rig.work.Runs()), 0)
	rig.update(t, workOn)
	same(t, "on again", rig.work.Runs()[0].Key(), "PROJ-2")
}

func TestASaveWithoutBaseBranchKeepsIt(t *testing.T) {
	t.Parallel()

	rig := newWorkRig(t, workSettings(`{"enabled":true,"jira":{"projects":["PROJ"],`+
		`"buildRepo":"acme/widgets","baseBranch":"release/2"}}`, ""))
	baseOf := func() any {
		build, _ := field(rig.waitTickets(t), "build").(*js.Object)

		return field(build, "base")
	}

	same(t, "named", baseOf(), "release/2")
	rig.update(t, workOn)
	same(t, "kept", baseOf(), "release/2")
	rig.update(t, `{"enabled":true,"jira":{"projects":["PROJ"],"buildRepo":"acme/widgets",`+
		`"baseBranch":null}}`)
	same(t, "cleared", baseOf(), "trunk")

	parsed, _ := js.Parse([]byte(`{"enabled":true,"jira":{"projects":["PROJ"],` +
		`"buildRepo":"acme/widgets","baseBranch":"--bad"}}`))
	same(t, "bad", rig.store.Update(settings.ModuleAtlassian, parsed).OK, false)
	same(t, "off text", jira.Off, "Atlassian is off. Enable it in Settings.")
}
