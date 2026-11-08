package app

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/hosted"
	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// The review kinds' checks as the app composes them, from a real configuration and settings:
// each row differs from a resumable world in one thing.

const (
	ownerClaude    = "/opt/conn-test/bin/claude"
	ownerSessionID = "0b6c4f7e-2d1a-4c3b-9e8f-112233445566"
	ownerModel     = "opus"
	reviewName     = "pr-12-widgets"
	selfName       = "self-widgets-12"
)

// ownerWorld is a data folder with a review worktree and a self-review worktree of acme/widgets,
// each launched by its module, and the settings and environment the checks are built from.
type ownerWorld struct {
	data, settings string
	env            map[string]string
	beside         bool
}

func newOwnerWorld(t *testing.T) *ownerWorld {
	t.Helper()

	data := t.TempDir()

	for _, kind := range []string{"reviews", "myprs"} {
		writeOwnerFile(t, filepath.Join(data, kind, "acme-widgets-12", "widgets", ".git",
			"config"), "[remote \"origin\"]\n\turl = git@github.com:acme/widgets.git\n")
	}

	return &ownerWorld{
		data: data,
		settings: `{"version":1,"repos":{"allow":["acme/*"],"clones":{},"cloneRoot":null},` +
			`"github":{"enabled":true}}`,
		env:    map[string]string{"CONN_DATA": data, "CONN_CLAUDE": ownerClaude},
		beside: false,
	}
}

func writeOwnerFile(t *testing.T, path, text string) {
	t.Helper()

	err := os.MkdirAll(filepath.Dir(path), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(path, []byte(text), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

// worktree is kind's worktree in the world.
func (w *ownerWorld) worktree(kind string) string {
	return filepath.Join(w.data, kind, "acme-widgets-12", "widgets")
}

// checks are the owners' checks the app builds over the world.
func (w *ownerWorld) checks(t *testing.T) hosted.Checks {
	t.Helper()

	writeOwnerFile(t, filepath.Join(w.data, "settings.json"), w.settings)

	lookup := func(key string) (string, bool) {
		value, found := w.env[key]

		return value, found
	}

	return ownerChecks(ownerParts{
		cfg: config.FromEnv(lookup), store: loadSettings(w.data, log.New(io.Discard, "", 0),
			io.Discard),
		reader: origin.NewReader(), work: nil, history: nil,
		beside: func(string, string) bool { return w.beside },
		stores: w.stores(),
	})
}

// stores are each module's open launch of its session in its worktree.
func (w *ownerWorld) stores() launchStores {
	pinned := ownerSessionID
	reviewsFile := reviews.EmptyFile()
	reviewsFile.PRs.Set("acme/widgets#12", &reviews.Entry{Launches: []*reviews.Launch{{
		At: 1, Model: ownerModel, Name: reviewName, HeadSha: "abc",
		Worktree: w.worktree("reviews"), SessionID: &pinned, ClosedAt: nil, Clone: "",
	}}})

	selfFile := myprs.EmptyFile()
	selfFile.PRs.Set("acme/widgets#12", &myprs.Entry{Launches: []*myprs.Launch{{
		At: 1, Model: ownerModel, Name: selfName, Worktree: w.worktree("myprs"),
		HeadSha: "abc", Round: 1, Armed: false, AskedSha: nil, AskedAt: nil, ClosedAt: nil,
		AskTry: nil, Manual: false,
	}}})

	return launchStores{reviews: reviewsFile, myPrs: selfFile}
}

// record is the record of owner's session in folder's worktree.
func (w *ownerWorld) record(owner, folder string) hosted.Record {
	name := reviewName
	if owner == hosted.OwnerMyPrs {
		name = selfName
	}

	return hosted.Record{
		ID: "000000000000000000000000000000aa", Owner: owner, Name: name,
		Dir: w.worktree(folder), ClaudeSessionID: ownerSessionID,
	}
}

// The checks resume each kind's own session, on its model, a self-review under its rules.
func TestOwnerChecksResumeEachKindAsItLaunched(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, owner, folder string
		wantRules           []string
	}{
		{"a review", hosted.OwnerReviews, "reviews", nil},
		{"a self-review", hosted.OwnerMyPrs, "myprs", myprs.SelfDisallowed()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := newOwnerWorld(t)
			record := world.record(tt.owner, tt.folder)

			spec, ok := world.checks(t)[tt.owner](t.Context(), record)
			if !ok {
				t.Fatal("not resumed")
			}

			want := slices.Concat([]string{ownerClaude}, ruleArgsOf(tt.wantRules),
				[]string{
					"--resume", ownerSessionID, "--name", record.Name, "--model", ownerModel,
				})
			if !slices.Equal(spec.Argv, want) {
				t.Errorf("argv %q, want %q", spec.Argv, want)
			}
		})
	}
}

// ruleArgsOf are the arguments that pass rules to claude.
func ruleArgsOf(rules []string) []string {
	if len(rules) == 0 {
		return nil
	}

	return slices.Concat([]string{"--disallowedTools"}, rules)
}

// ownerRefusal changes a resumable world in one thing, and is the record to check.
type ownerRefusal struct {
	name    string
	arrange func(world *ownerWorld) hosted.Record
}

func ownerRefusals() []ownerRefusal {
	return slices.Concat([]ownerRefusal{
		{"a self-review in the reviews folder", func(world *ownerWorld) hosted.Record {
			return world.record(hosted.OwnerMyPrs, "reviews")
		}},
		{"a review in the My PRs folder", func(world *ownerWorld) hosted.Record {
			return world.record(hosted.OwnerReviews, "myprs")
		}},
	},
		kindRefusals(hosted.OwnerReviews, "reviews", "CONN_REVIEWS_DRY"),
		kindRefusals(hosted.OwnerMyPrs, "myprs", "CONN_MYPRS_DRY"))
}

// kindRefusals are owner's records refused for the module, the allowlist or a session beside.
func kindRefusals(owner, folder, dry string) []ownerRefusal {
	return []ownerRefusal{
		{owner + " dry", func(world *ownerWorld) hosted.Record {
			world.env[dry] = "1"

			return world.record(owner, folder)
		}},
		{owner + " with GitHub off", func(world *ownerWorld) hosted.Record {
			world.settings = `{"version":1,"repos":{"allow":["acme/*"],"clones":{},` +
				`"cloneRoot":null},"github":{"enabled":false}}`

			return world.record(owner, folder)
		}},
		{owner + " off the allowlist", func(world *ownerWorld) hosted.Record {
			world.settings = `{"version":1,"repos":{"allow":["other/*"],"clones":{},` +
				`"cloneRoot":null},"github":{"enabled":true}}`

			return world.record(owner, folder)
		}},
		{owner + " beside a session", func(world *ownerWorld) hosted.Record {
			world.beside = true

			return world.record(owner, folder)
		}},
	}
}

func TestOwnerChecksRefuse(t *testing.T) {
	t.Parallel()

	for _, tt := range ownerRefusals() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := newOwnerWorld(t)
			record := tt.arrange(world)

			spec, ok := world.checks(t)[record.Owner](t.Context(), record)
			if ok {
				t.Errorf("resumed %+v", spec)
			}
		})
	}
}
