package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
)

// gitClone is a folder whose git config names a GitHub origin, as far as origin can tell.
func gitClone(t *testing.T, folder, url string) {
	t.Helper()

	err := os.MkdirAll(filepath.Join(folder, ".git"), 0o755)
	if err == nil {
		err = os.WriteFile(filepath.Join(folder, ".git", "config"),
			[]byte("[remote \"origin\"]\n\turl = "+url+"\n"), 0o644)
	}

	if err != nil {
		t.Fatal(err)
	}
}

func TestOpenWorkSeesTheClonesOnThisMachineThenSessionsThenMyPRs(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	root := t.TempDir()
	gitClone(t, filepath.Join(root, "gadgets"), "git@github.com:acme/gadgets.git")
	gitClone(t, filepath.Join(home, "projects", "widgets"), "git@github.com:acme/widgets.git")

	data := t.TempDir()

	err := os.WriteFile(filepath.Join(data, "settings.json"), []byte(`{"version":1,"repos":`+
		`{"allow":["acme/*"],"clones":{},"cloneRoot":`+`"`+root+`"}}`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	store := settings.NewStore(nil)
	store.Reload(data)

	parts := workParts{
		cfg: config.Config{Home: home}, store: store, reader: origin.NewReader(),
		myPrRepos: func() []string { return []string{"acme/pulls"} },
	}

	got := seenWorkRepos(parts, func() []string { return []string{"acme/session"} })()
	want := []string{"acme/gadgets", "acme/widgets", "acme/session", "acme/pulls"}

	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestOpenWorkFindsASessionByNameOrFolderAndByFolderAloneWithNoName(t *testing.T) {
	t.Parallel()

	live := []sessions.LiveSession{
		{Name: "", Cwd: "/work/other", Shown: "idle", SessionID: "s-60", PID: 60},
		{Name: "PROJ-1", Cwd: "/work/widgets-proj-1", Shown: "busy", SessionID: "s-70", PID: 70},
		{Name: "PROJ-1", Cwd: "/work/gadgets-proj-1", Shown: "idle", SessionID: "s-71", PID: 71},
	}

	rows := []struct {
		name, lookUp, cwd string
		// want is the session id found, "" for none.
		want string
	}{
		{"by name", "PROJ-1", "/nowhere", "s-70"},
		{"by folder", "PROJ-9", "/work/gadgets-proj-1", "s-71"},
		{"by folder alone", "", "/work/gadgets-proj-1", "s-71"},
		{"no name finds no unnamed session", "", "/nowhere", ""},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			got := ""
			if found := foundIn(live, row.lookUp, row.cwd); found != nil {
				got = found.SessionID
			}

			if got != row.want {
				t.Errorf("found %q, want %q", got, row.want)
			}
		})
	}
}
