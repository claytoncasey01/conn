package main

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/settings"
)

// The one dry seed (Bun's drySeedServer.test.ts, at the wiring): the dry switches with no
// settings file boot out of setup from a temp folder, never writing into the data folder; a file
// that exists, CONN_DRY_SEED=0 and the real data folder are never seeded. That a seeded run makes
// no gh call and leaves reviews.json alone is held by the Reviews dry tests and the parity
// harness.

// seedCase is one boot.
type seedCase struct {
	name string
	env  map[string]string
	// file is a settings.json already in the data folder.
	file bool
	// data is how CONN_DATA is set: "temp", "unset" or "link" (a link to the real ~/.conn).
	data string
	// seeded is whether the seed is made; then allow, github, atlassian are what it holds.
	seeded    bool
	github    bool
	atlassian bool
	// said is what stderr says ("" for nothing).
	said string
}

func seedCases() []seedCase {
	yes := "1"

	return []seedCase{
		{"all three switches", map[string]string{
			"CONN_MYPRS_DRY": yes, "CONN_REVIEWS_DRY": yes, "CONN_WORK_DRY": yes,
		}, false, "temp", true, true, true, ""},
		{
			"Reviews alone",
			map[string]string{"CONN_REVIEWS_DRY": yes},
			false, "temp", true, true,
			false, "",
		},
		{
			"My PRs alone",
			map[string]string{"CONN_MYPRS_DRY": yes},
			false, "temp", true, true,
			false, "",
		},
		{
			"Open work alone",
			map[string]string{"CONN_WORK_DRY": yes},
			false, "temp", true, false,
			true, "",
		},
		{"no switch", map[string]string{}, false, "temp", false, false, false, ""},
		{
			"a settings file",
			map[string]string{"CONN_REVIEWS_DRY": yes},
			true, "temp", false,
			false, false, "",
		},
		{
			"CONN_DRY_SEED=0",
			map[string]string{"CONN_REVIEWS_DRY": yes, "CONN_DRY_SEED": "0"},
			false, "temp", false, false, false, "",
		},
		{
			"CONN_DATA unset",
			map[string]string{"CONN_REVIEWS_DRY": yes},
			false, "unset", false,
			false, false, "CONN_DATA is not set",
		},
		{
			"a link to the real folder",
			map[string]string{"CONN_REVIEWS_DRY": yes},
			false, "link",
			false, false, false, "is the real data dir",
		},
	}
}

// bootLookup is the environment of one boot: a temp HOME, and CONN_DATA as the case says.
func bootLookup(t *testing.T, tt seedCase) (config.LookupFunc, string) {
	t.Helper()

	home := t.TempDir()
	theReal := filepath.Join(home, ".conn")
	data := filepath.Join(t.TempDir(), "data")

	for _, folder := range []string{theReal, data} {
		err := os.MkdirAll(folder, 0o700)
		if err != nil {
			t.Fatal(err)
		}
	}

	env := map[string]string{"HOME": home}
	maps.Copy(env, tt.env)

	switch tt.data {
	case "temp":
		env["CONN_DATA"] = data
	case "link":
		data = filepath.Join(t.TempDir(), "link")

		err := os.Symlink(theReal, data)
		if err != nil {
			t.Fatal(err)
		}

		env["CONN_DATA"] = data
	default:
		data = theReal
	}

	return func(key string) (string, bool) {
		value, found := env[key]

		return value, found
	}, data
}

func TestDrySeedAtBoot(t *testing.T) {
	t.Parallel()

	for _, tt := range seedCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lookup, data := bootLookup(t, tt)
			if tt.file {
				writeAllow(t, data)
			}

			cfg := config.FromEnv(lookup)
			store := settings.NewStore(nil)
			store.Reload(cfg.DataDir)

			var stderr bytes.Buffer

			seeded, remove := seedDry(cfg, store, &stderr)
			if !tt.seeded {
				if seeded.Dry != cfg.Dry {
					t.Errorf("an unseeded run changed the dry switches: %+v", seeded.Dry)
				}
			}

			checkSeed(t, tt, seeded, store, data, stderr.String())
			checkRemoved(t, store, remove)
		})
	}
}

func writeAllow(t *testing.T, data string) {
	t.Helper()

	err := os.WriteFile(filepath.Join(data, "settings.json"),
		[]byte(`{"version":1,"repos":{"allow":["jane/widgets"]}}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

func checkSeed(t *testing.T, tt seedCase, cfg config.Config, store *settings.Store,
	data, said string,
) {
	t.Helper()

	checkSaid(t, said, tt.said)

	if !tt.seeded {
		return
	}

	current := store.Get()
	if !slices.Equal(current.Repos.Allow, []string{"acme/*"}) {
		t.Errorf("allow %v", current.Repos.Allow)
	}

	got := []bool{current.GitHub.Enabled, current.Atlassian.Enabled, current.Slack.Enabled}
	if !slices.Equal(got, []bool{tt.github, tt.atlassian, false}) {
		t.Errorf("github, atlassian, slack = %v", got)
	}

	if cfg.Dry != (config.Dry{MyPrs: true, Reviews: true, Work: true}) {
		t.Errorf("a seeded run is not dry everywhere: %+v", cfg.Dry)
	}

	// A save from the Settings page goes to the seed's folder, never the data folder.
	body, err := js.Parse([]byte(`{"requested":true,"allOpen":true}`))
	if err != nil {
		t.Fatal(err)
	}

	saved := store.Update(settings.SectionReviews, body)
	if !saved.OK {
		t.Fatalf("update refused: %s", saved.Error)
	}

	_, err = os.Stat(filepath.Join(data, "settings.json"))
	if err == nil {
		t.Error("the seed wrote settings.json into the data folder")
	}
}

// checkSaid checks stderr says want, or nothing when want is "".
func checkSaid(t *testing.T, said, want string) {
	t.Helper()

	if want == "" {
		if said != "" {
			t.Errorf("stderr: %q", said)
		}

		return
	}

	if !strings.Contains(said, want) {
		t.Errorf("stderr %q does not say %q", said, want)
	}
}

// checkRemoved checks the seed's folder (found through the work clone, when it has one) is gone
// once the server's cleanup runs.
func checkRemoved(t *testing.T, store *settings.Store, remove func()) {
	t.Helper()

	clone := store.Get().Repos.Clones.Get("acme/widgets")

	remove()

	if clone == "" {
		return
	}

	_, err := os.Stat(filepath.Dir(clone))
	if err == nil {
		t.Errorf("the seed's folder %s is still there", filepath.Dir(clone))
	}
}
