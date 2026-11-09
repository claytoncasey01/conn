// Package dryseed is the dry seed of every dry switch (CONN_MYPRS_DRY, CONN_REVIEWS_DRY,
// CONN_WORK_DRY) on a data folder that has no settings.json, so a dry UI is not stuck in setup.
// The server writes it into a temp folder it then reads the settings from, never into the data
// folder. It allows `acme/*` and turns on only the modules of the switches that are on, so a
// module whose own switch is off makes no real call and writes nothing: GitHub only with
// CONN_MYPRS_DRY or CONN_REVIEWS_DRY (every module runs dry in any seeded run), "Requests me"
// only with CONN_REVIEWS_DRY, Slack never, Atlassian only with CONN_WORK_DRY, which also points
// the default repository (acme/widgets) and a second one (acme/gadgets) at stand-in clones made in
// that folder: a folder whose git config names that repo as origin and whose origin/HEAD names
// main, so the real clone and default-branch lookups run and no git process is needed. Nothing
// here is real.
package dryseed

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/realpath"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	folderMode = 0o755
	fileMode   = 0o644
)

// Facts are what the seed decision is made on.
type Facts struct {
	// Dry is that a dry switch is on.
	Dry bool
	// SeedFlag is CONN_DRY_SEED: "0" turns the seed off (a test's own no-file boot).
	SeedFlag string
	// Setup is that there is no allowlist.
	Setup bool
	// HasFile is that a settings.json is already in the data folder.
	HasFile bool
	// DataEnv is the CONN_DATA the process was started with: "" means the default folder,
	// which is the real one.
	DataEnv string
	// DataDir is the data folder in use.
	DataDir string
	// DefaultDataDir is the real data folder: where Conn keeps its data when CONN_DATA is unset.
	DefaultDataDir string
}

// Decision is whether to seed, and when not why not ("" when it is simply not wanted).
type Decision struct {
	Seed bool
	Why  string
}

// Decide is whether to seed. It never seeds an unset CONN_DATA or the real data folder
// (~/.config/conn), however it is reached.
func Decide(facts Facts) Decision {
	if !facts.Dry {
		return Decision{Seed: false, Why: ""}
	}

	if facts.SeedFlag == "0" {
		return Decision{Seed: false, Why: ""}
	}

	if !facts.Setup {
		return Decision{Seed: false, Why: ""}
	}

	if facts.HasFile {
		return Decision{Seed: false, Why: ""}
	}

	if facts.DataEnv == "" {
		return Decision{Seed: false, Why: "CONN_DATA is not set, so the data dir is the real " +
			"one: the dry settings are not written (set CONN_DATA to a scratch folder)"}
	}

	theReal := realpath.Resolve(facts.DefaultDataDir)
	if realpath.Resolve(facts.DataDir) == theReal {
		return realRefusal(facts.DataDir)
	}

	if realpath.Resolve(facts.DataEnv) == theReal {
		return realRefusal(facts.DataDir)
	}

	return Decision{Seed: true, Why: ""}
}

func realRefusal(dataDir string) Decision {
	return Decision{Seed: false, Why: dataDir + " is the real data dir: the dry settings are " +
		"not written (set CONN_DATA to a scratch folder)"}
}

// FactsOf are the facts for this process.
func FactsOf(cfg config.Config, store *settings.Store) Facts {
	_, err := os.Stat(filepath.Join(cfg.DataDir, "settings.json"))

	return Facts{
		Dry: cfg.Dry.Any(), SeedFlag: cfg.DrySeed, Setup: store.Setup(), HasFile: err == nil,
		DataEnv: cfg.DataEnv, DataDir: cfg.DataDir, DefaultDataDir: cfg.DefaultDataDir,
	}
}

// Holder is where the seed goes: the settings store, validated as a PUT is.
type Holder interface {
	Update(name string, body any) settings.UpdateResult
}

func update(holder Holder, name, body string) string {
	parsed, err := js.Parse([]byte(body))
	if err != nil {
		panic(fmt.Sprintf("dryseed: the %s seed is not JSON: %v", name, err))
	}

	result := holder.Update(name, parsed)
	if result.OK {
		return ""
	}

	return result.Error
}

// cloneRepos are the repositories seedWork makes stand-in clones of, with the folder each is in.
// The first is the default repository.
func cloneRepos() [][2]string {
	return [][2]string{
		{"acme/widgets", "dry-clone"},
		{"acme/gadgets", "dry-clone-gadgets"},
	}
}

// makeClone makes the stand-in clone of repo in dir/name and returns its folder. What went wrong
// is the second result, or "".
func makeClone(dir, name, repo string) (string, string) {
	clone := filepath.Join(dir, name)
	refs := filepath.Join(clone, ".git", "refs", "remotes", "origin")

	err := os.MkdirAll(refs, folderMode)
	if err == nil {
		err = os.WriteFile(filepath.Join(clone, ".git", "config"),
			[]byte("[remote \"origin\"]\n\turl = https://github.com/"+repo+".git\n"), fileMode)
	}

	if err == nil {
		err = os.WriteFile(filepath.Join(refs, "HEAD"),
			[]byte("ref: refs/remotes/origin/main\n"), fileMode)
	}

	if err != nil {
		return "", settings.NodeError(err)
	}

	return clone, ""
}

// seedWork makes the stand-in clones and points Open work at them. What went wrong, or "".
func seedWork(dir string, holder Holder) string {
	clones := js.NewObject()

	for _, pair := range cloneRepos() {
		clone, why := makeClone(dir, pair[1], pair[0])
		if why != "" {
			return why
		}

		clones.Set(pair[0], clone)
	}

	why := update(holder, settings.SectionRepos,
		`{"allow":["acme/*"],"clones":`+js.Stringify(clones)+`,"cloneRoot":null}`)
	if why != "" {
		return why
	}

	return update(holder, settings.ModuleAtlassian,
		`{"enabled":true,"jira":{"projects":["PROJ"],"buildRepo":"acme/widgets"}}`)
}

// Seed writes the seed for the switches that are on, through holder, into the folder it reads
// from: the allowlist, and only the modules those switches fake (Open work's clone and Atlassian
// with Work; GitHub with MyPrs or Reviews; "Requests me" with Reviews). Every other module is
// off. What went wrong, or "".
func Seed(flags config.Dry, dir string, holder Holder) string {
	if flags.Work {
		if why := seedWork(dir, holder); why != "" {
			return why
		}
	} else {
		why := update(holder, settings.SectionRepos,
			`{"allow":["acme/*"],"clones":{},"cloneRoot":null}`)
		if why != "" {
			return why
		}
	}

	github := flags.MyPrs
	if flags.Reviews {
		github = true
	}

	why := update(holder, settings.ModuleGitHub, `{"enabled":`+js.Stringify(github)+`}`)
	if why != "" {
		return why
	}

	return update(holder, settings.SectionReviews, `{"requested":`+js.Stringify(flags.Reviews)+
		`,"allOpen":false}`)
}

// SeededDry is the dry switches in a seeded run: every module runs dry, whichever switches are
// on and whatever is turned on later from its Settings page.
func SeededDry() config.Dry { return config.Dry{MyPrs: true, Reviews: true, Work: true} }
