package myprs_test

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/settings"
)

// The scope as My PRs reads it, through the real settings holder
// over a temp data folder, and the hook that reaches SettingsChanged (the holder's OnChange,
// which cmd/conn/wiring.go hands MyPrs.SettingsChanged). The recorded answers (section scope)
// cover ScopeOf over plain settings. The clone lookup is internal/origin's Reader.CloneFor,
// tested there (TestCloneFor*: a wrong origin, a bad name). The wiring is cmd/conn/wiring.go,
// which compiles or not, so no test reads its source.

// eStore is a settings holder over a fresh data folder holding file as settings.json ("" for
// no file), and that folder.
func eStore(t *testing.T, file string) (*settings.Store, string) {
	t.Helper()

	dir := t.TempDir()

	if file != "" {
		err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(file), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	store := settings.NewStore(log.New(io.Discard, "", 0))
	store.Reload(dir)

	return store, dir
}

func TestEScopeInSetup(t *testing.T) {
	t.Parallel()

	store, _ := eStore(t, `{"version":1,"slack":{"enabled":true,"channels":["#code-review"]}}`)
	scope := myprs.ScopeNow(store)()
	eSameJSON(t, "setup", scopeOut{
		Setup: scope.Setup, Allow: scope.Allow, GitHub: scope.GitHub, Slack: scope.Slack,
		DraftChannel: scope.DraftChannel,
	},
		`{"setup":true,"allow":[],"github":false,"slack":false,"draftChannel":"#code-review"}`)

	empty, _ := eStore(t, "")
	if !myprs.ScopeNow(empty)().Setup {
		t.Error("no file is not setup")
	}
}

func TestEScopeDraftChannel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, slack string
		want        *string
	}{
		{"the setting", `"channels":["#one","#two"],"draftChannel":"#two"`, new("#two")},
		{"the first channel", `"channels":["#one","#two"]`, new("#one")},
		{"no channels", `"channels":[]`, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, _ := eStore(t, `{"version":1,"repos":{"allow":["acme/*"]},`+
				`"slack":{"enabled":true,`+tt.slack+`}}`)
			eSameJSON(t, tt.name, myprs.ScopeNow(store)().DraftChannel, js.Stringify(tt.want))
		})
	}
}

func TestEScopeSwitches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, file    string
		github, slack bool
	}{
		{"the defaults", `{"version":1,"repos":{"allow":["acme/*"]}}`, true, false},
		{
			"each its own flag", `{"version":1,"repos":{"allow":["acme/*"]},` +
				`"github":{"enabled":false},"slack":{"enabled":true,"channels":["#c"]}}`,
			false, true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, _ := eStore(t, tt.file)
			scope := myprs.ScopeNow(store)()

			if scope.Setup {
				t.Error("setup")
			}

			if !slices.Equal(scope.Allow, []string{"acme/*"}) {
				t.Errorf("allow %v", scope.Allow)
			}

			if scope.GitHub != tt.github {
				t.Errorf("github %v", scope.GitHub)
			}

			if scope.Slack != tt.slack {
				t.Errorf("slack %v", scope.Slack)
			}
		})
	}

	t.Run("the holder's switch, not the setting", func(t *testing.T) {
		t.Parallel()

		current := settings.Defaults()
		current.Repos.Allow = []string{"a/b"}
		current.Slack.Enabled = true

		if myprs.ScopeOf(current, false, func(string) bool { return false }).Slack {
			t.Error("slack is on")
		}
	})
}

func TestEScopeFollowsASave(t *testing.T) {
	t.Parallel()

	store, dir := eStore(t, `{"version":1,"repos":{"allow":["acme/*"]}}`)
	scope := myprs.ScopeNow(store)
	told := 0
	stop := store.OnChange(func(settings.Settings) { told++ })

	update := func(section, body string) bool {
		parsed, err := js.Parse([]byte(body))
		if err != nil {
			t.Fatal(err)
		}

		return store.Update(section, parsed).OK
	}

	if !update("repos", `{"allow":["acme/*","jane/tools"],"clones":{},"cloneRoot":null}`) {
		t.Fatal("the save was refused")
	}

	if told != 1 {
		t.Errorf("told %d times", told)
	}

	if !slices.Equal(scope().Allow, []string{"acme/*", "jane/tools"}) {
		t.Errorf("allow %v", scope().Allow)
	}

	eCheckSaved(t, dir, scope().Allow)

	if update("repos", `{"allow":["*/*"],"clones":{},"cloneRoot":null}`) {
		t.Error("*/* was saved")
	}

	if told != 1 {
		t.Errorf("a refused save told: %d", told)
	}

	stop()
	update("github", `{"enabled":false}`)

	if told != 1 {
		t.Errorf("told after stop: %d", told)
	}
}

// eCheckSaved fails unless settings.json in dir holds allow.
func eCheckSaved(t *testing.T, dir string, allow []string) {
	t.Helper()

	saved, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}

	onDisk, err := js.Parse(saved)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(texts(get(get(onDisk, "repos"), "allow")), allow) {
		t.Errorf("the file is %s", saved)
	}
}
