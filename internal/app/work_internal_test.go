package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/origin"
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
