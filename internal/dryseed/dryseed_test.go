package dryseed_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/dryseed"
	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/settings"
)

// Bun's workDry.test.ts: when the dry seed happens, and what each dry switch seeds, through a real
// settings store in a temp folder.

// factsOf are a dry server in setup with no file and a scratch data folder, changed by change.
func factsOf(change func(facts *dryseed.Facts)) dryseed.Facts {
	facts := dryseed.Facts{
		Dry: true, SeedFlag: "", Setup: true, HasFile: false, DataEnv: "/scratch/data",
		DataDir: "/scratch/data", DataDirDefault: "/home/jane/.config/conn",
	}
	if change != nil {
		change(&facts)
	}

	return facts
}

// TestTheSeedHappensOnlyForADryServerInSetup: each condition on its own.
func TestTheSeedHappensOnlyForADryServerInSetup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(facts *dryseed.Facts)
		want   dryseed.Decision
	}{
		{"every condition holds", nil, dryseed.Decision{Seed: true, Why: ""}},
		{"not dry", func(f *dryseed.Facts) { f.Dry = false }, dryseed.Decision{}},
		{"CONN_DRY_SEED=0", func(f *dryseed.Facts) { f.SeedFlag = "0" }, dryseed.Decision{}},
		{"not in setup", func(f *dryseed.Facts) { f.Setup = false }, dryseed.Decision{}},
		{"a file is there", func(f *dryseed.Facts) { f.HasFile = true }, dryseed.Decision{}},
		{
			"CONN_DRY_SEED=1", func(f *dryseed.Facts) { f.SeedFlag = "1" },
			dryseed.Decision{Seed: true, Why: ""},
		},
		{
			"the dev server's own folder",
			func(f *dryseed.Facts) {
				dev := "/home/jane/.config/conn-dev"
				f.DataEnv, f.DataDir = dev, dev
			},
			dryseed.Decision{Seed: true, Why: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := dryseed.Decide(factsOf(tt.change))
			if got != tt.want {
				t.Errorf("Decide = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestTheSeedNeverWritesIntoTheRealDataFolder: an unset CONN_DATA, or one set to the real folder
// spelled any way, is refused, and says why.
func TestTheSeedNeverWritesIntoTheRealDataFolder(t *testing.T) {
	t.Parallel()

	const (
		realDir = "/home/jane/.config/conn"
		why     = "real data dir"
	)

	tests := []struct {
		name, dataEnv, dataDir, why string
	}{
		{"CONN_DATA unset", "", "/home/jane/.config/conn", "CONN_DATA is not set"},
		{"the real folder", realDir, realDir, "real data dir"},
		{"with a trailing slash", realDir + "/", realDir + "/", "real data dir"},
		{"through ..", "/home/jane/x/../.config/conn", "/home/jane/x/../.config/conn", why},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := dryseed.Decide(factsOf(func(f *dryseed.Facts) {
				f.DataEnv, f.DataDir = tt.dataEnv, tt.dataDir
			}))
			if got.Seed {
				t.Fatal("seeded the real data folder")
			}

			if !strings.Contains(got.Why, tt.why) {
				t.Errorf("why = %q, want it to say %q", got.Why, tt.why)
			}
		})
	}
}

// TestALinkToTheRealFolderIsTheRealFolder: CONN_DATA a link to ~/.config/conn, or ~/.config/conn
// reached through a linked home, is refused; the dev folder still seeds.
func TestALinkToTheRealFolderIsTheRealFolder(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	home := filepath.Join(base, "home")
	link := filepath.Join(base, "data-link")
	linkedHome := filepath.Join(base, "home-link")

	realDir := filepath.Join(home, ".config", "conn")
	devDir := filepath.Join(home, ".config", "conn-dev")

	for _, folder := range []string{realDir, devDir} {
		mustDo(t, os.MkdirAll(folder, 0o755))
	}

	mustDo(t, os.Symlink(realDir, link))
	mustDo(t, os.Symlink(home, linkedHome))

	tests := []struct {
		name, home, data string
		seed             bool
	}{
		{"CONN_DATA links to the real folder", home, link, false},
		{"the home is a link", linkedHome, realDir, false},
		{"the dev folder", home, devDir, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := dryseed.Decide(factsOf(func(f *dryseed.Facts) {
				f.DataDirDefault = filepath.Join(tt.home, ".config", "conn")
				f.DataEnv, f.DataDir = tt.data, tt.data
			}))
			if got.Seed != tt.seed {
				t.Fatalf("Decide = %+v, want seed %v", got, tt.seed)
			}

			if !tt.seed {
				if !strings.Contains(got.Why, "real data dir") {
					t.Errorf("why = %q", got.Why)
				}
			}
		})
	}
}

func mustDo(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

// seededStore is a settings store loaded from dir, then seeded for flags.
func seededStore(t *testing.T, flags config.Dry, dir string) *settings.Store {
	t.Helper()

	store := settings.NewStore(nil)
	store.Reload(dir)

	why := dryseed.Seed(flags, dir, store)
	if why != "" {
		t.Fatalf("Seed: %s", why)
	}

	return store
}

// seededView is what Bun's test compares after a seed.
type seededView struct {
	allow                                          string
	github, requested, allOpen, slack, atlassianOn bool
}

// TestTheSeedTurnsOnOnlyTheModulesOfItsSwitches: Work alone has no GitHub; My PRs has GitHub;
// Reviews has GitHub and "Requests me"; Slack is never on.
func TestTheSeedTurnsOnOnlyTheModulesOfItsSwitches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		flags config.Dry
		want  seededView
	}{
		{"work", config.Dry{Work: true}, seededView{"acme/*", false, false, false, false, true}},
		{"my prs", config.Dry{MyPrs: true}, seededView{"acme/*", true, false, false, false, false}},
		{
			"reviews",
			config.Dry{Reviews: true},
			seededView{"acme/*", true, true, false, false, false},
		},
		{
			"all",
			config.Dry{MyPrs: true, Reviews: true, Work: true},
			seededView{"acme/*", true, true, false, false, true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := seededStore(t, tt.flags, t.TempDir()).Get()
			view := seededView{
				allow: strings.Join(got.Repos.Allow, ","), github: got.GitHub.Enabled,
				requested: got.Reviews.Requested, allOpen: got.Reviews.AllOpen,
				slack: got.Slack.Enabled, atlassianOn: got.Atlassian.Enabled,
			}

			if view != tt.want {
				t.Errorf("seeded %+v, want %+v", view, tt.want)
			}
		})
	}
}

// TestTheWorkSeedGivesTheBuildRepoACloneTheLookupFinds: Atlassian on for PROJ with acme/widgets
// as the build repo, and a stand-in clone the real clone lookup finds, whose origin/HEAD names
// main (Go has no default-branch reader yet: the file is checked as written).
func TestTheWorkSeedGivesTheBuildRepoACloneTheLookupFinds(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	got := seededStore(t, config.Dry{Work: true}, dir).Get()
	clone := filepath.Join(dir, "dry-clone")

	if !slices.Equal(got.Atlassian.Jira.Projects, []string{"PROJ"}) {
		t.Errorf("projects %v", got.Atlassian.Jira.Projects)
	}

	if got.Atlassian.Jira.BuildRepo == nil {
		t.Fatal("no build repo")
	}

	if *got.Atlassian.Jira.BuildRepo != "acme/widgets" {
		t.Errorf("build repo %q", *got.Atlassian.Jira.BuildRepo)
	}

	if got.Atlassian.Jira.BaseBranch != nil {
		t.Errorf("base branch %q", *got.Atlassian.Jira.BaseBranch)
	}

	found, ok := origin.NewReader().CloneFor("acme/widgets", got, t.TempDir())
	if !ok {
		t.Fatal("the clone lookup did not find the stand-in clone")
	}

	if found != (origin.Clone{Path: clone, How: origin.HowSettings}) {
		t.Errorf("CloneFor = %+v, want %s from settings", found, clone)
	}

	head, err := os.ReadFile(filepath.Join(clone, ".git", "refs", "remotes", "origin", "HEAD"))
	mustDo(t, err)

	if string(head) != "ref: refs/remotes/origin/main\n" {
		t.Errorf("origin/HEAD = %q", head)
	}
}

// refusing is a holder that refuses every update.
type refusing struct{}

func (refusing) Update(string, any) settings.UpdateResult {
	return settings.UpdateResult{OK: false, Error: "no way", SaveFailed: false}
}

// TestARefusedUpdateIsReported: the seed says why, not swallowing it, for every switch.
func TestARefusedUpdateIsReported(t *testing.T) {
	t.Parallel()

	for _, flags := range []config.Dry{{Work: true}, {MyPrs: true}, {Reviews: true}} {
		got := dryseed.Seed(flags, filepath.Join(t.TempDir(), "refused"), refusing{})
		if got != "no way" {
			t.Errorf("Seed(%+v) = %q, want the refusal", flags, got)
		}
	}
}

// TestAnySwitchCountsAsDry: a seeded run makes every module dry, the switches are read as "1"
// only, and any one of them makes the facts dry.
func TestAnySwitchCountsAsDry(t *testing.T) {
	t.Parallel()

	if dryseed.SeededDry() != (config.Dry{MyPrs: true, Reviews: true, Work: true}) {
		t.Errorf("SeededDry = %+v", dryseed.SeededDry())
	}

	tests := []struct {
		name string
		env  map[string]string
		want config.Dry
	}{
		{
			"work on, my prs 0",
			map[string]string{"CONN_WORK_DRY": "1", "CONN_MYPRS_DRY": "0"},
			config.Dry{Work: true},
		},
		{"my prs", map[string]string{"CONN_MYPRS_DRY": "1"}, config.Dry{MyPrs: true}},
		{"reviews", map[string]string{"CONN_REVIEWS_DRY": "1"}, config.Dry{Reviews: true}},
		{"work 0", map[string]string{"CONN_WORK_DRY": "0"}, config.Dry{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.env["CONN_DATA"] = "/scratch/data"
			tt.env["HOME"] = "/home/jane"
			cfg := config.FromEnv(func(name string) (string, bool) {
				value, found := tt.env[name]

				return value, found
			})

			if cfg.Dry != tt.want {
				t.Errorf("Dry = %+v, want %+v", cfg.Dry, tt.want)
			}

			facts := dryseed.FactsOf(cfg, settings.NewStore(nil))
			if facts.Dry != tt.want.Any() {
				t.Errorf("FactsOf dry = %v", facts.Dry)
			}
		})
	}
}
