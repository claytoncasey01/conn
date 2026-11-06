package openwork_test

import (
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/openwork"
	"github.com/claytoncasey01/conn/internal/settings"
)

// Where a run may build, and which of those places a START chose: plain values in, plain values
// out, with the clone lookup and the default branch read standing in for the disk.

// targetWorld is what BuildTargets is given: the settings, the clones that exist and the default
// branch each clone's git config names.
type targetWorld struct {
	allow        []string
	clones       []string
	buildRepo    string
	base         string
	baseBranches map[string]string
	// found are the clones on disk, by lowercase repository.
	found map[string]string
	// defaults are the default branches the clones name, by clone folder.
	defaults map[string]string
}

func (world targetWorld) targets() []openwork.Target {
	current := settings.Defaults()
	current.Repos.Allow = world.allow
	current.Repos.Clones = settings.NewClones(world.clones...)

	if world.buildRepo != "" {
		current.Atlassian.Jira.BuildRepo = &world.buildRepo
	}

	if world.base != "" {
		current.Atlassian.Jira.BaseBranch = &world.base
	}

	if world.baseBranches != nil {
		current.Atlassian.Jira.BaseBranches = world.baseBranches
	}

	find := func(repo string, _ settings.Settings) (string, bool) {
		clone, found := world.found[strings.ToLower(repo)]

		return clone, found
	}

	defaultOf := func(clone string) (string, bool) {
		branch, known := world.defaults[clone]

		return branch, known
	}

	return openwork.BuildTargets(current, find, defaultOf)
}

// twoRepos is acme/* allowed, with clones of widgets and gadgets set in Settings and on disk.
func twoRepos() targetWorld {
	return targetWorld{
		allow:        []string{"acme/*"},
		clones:       []string{"acme/widgets", "/x/widgets", "acme/gadgets", "/x/gadgets"},
		buildRepo:    "",
		base:         "",
		baseBranches: nil,
		found:        map[string]string{"acme/widgets": "/x/widgets", "acme/gadgets": "/x/gadgets"},
		defaults:     map[string]string{"/x/widgets": "main", "/x/gadgets": "trunk"},
	}
}

func target(repo, clone, base, reason string) openwork.Target {
	return openwork.Target{Repo: repo, Clone: clone, Base: base, Reason: reason}
}

func TestBuildTargetsListEveryAllowedRepositoryAndWhetherARunCanStartThere(t *testing.T) {
	t.Parallel()

	noDefault := twoRepos()
	noDefault.defaults = map[string]string{"/x/widgets": "main"}

	noClone := twoRepos()
	noClone.found = map[string]string{"acme/widgets": "/x/widgets"}

	bases := twoRepos()
	bases.base = "release"
	bases.baseBranches = map[string]string{"acme/gadgets": "next"}

	global := twoRepos()
	global.base = "release"

	exact := twoRepos()
	exact.allow = []string{"other/one", "acme/*", "acme/widgets"}
	exact.clones = []string{
		"acme/widgets", "/x/widgets", "other/one", "/x/one", "elsewhere/no",
		"/x/no",
	}
	exact.buildRepo = "acme/zebra"
	exact.baseBranches = map[string]string{"acme/apple": "main", "elsewhere/nope": "main"}
	exact.found = map[string]string{"acme/widgets": "/x/widgets", "other/one": "/x/one"}
	exact.defaults = map[string]string{"/x/widgets": "main", "/x/one": "main"}

	missing := "conn could not tell the default branch of gadgets: set a Base branch on the " +
		"Atlassian card in Settings."

	rows := []struct {
		name  string
		world targetWorld
		want  []openwork.Target
	}{
		{"both ready, by name within the pattern", twoRepos(), []openwork.Target{
			target("acme/gadgets", "/x/gadgets", "trunk", ""),
			target("acme/widgets", "/x/widgets", "main", ""),
		}},
		{"no default branch and no base", noDefault, []openwork.Target{
			target("acme/gadgets", "/x/gadgets", "", missing),
			target("acme/widgets", "/x/widgets", "main", ""),
		}},
		{"no clone", noClone, []openwork.Target{
			target("acme/gadgets", "", "", "No local clone of acme/gadgets: set a folder in "+
				"Settings."),
			target("acme/widgets", "/x/widgets", "main", ""),
		}},
		{"its own base beats the global one", bases, []openwork.Target{
			target("acme/gadgets", "/x/gadgets", "next", ""),
			target("acme/widgets", "/x/widgets", "release", ""),
		}},
		{"the global base beats the default branch", global, []openwork.Target{
			target("acme/gadgets", "/x/gadgets", "release", ""),
			target("acme/widgets", "/x/widgets", "release", ""),
		}},
		{
			"in allowlist order, from every place Settings names a repository", exact,
			[]openwork.Target{
				target("other/one", "/x/one", "main", ""),
				target("acme/apple", "", "", "No local clone of acme/apple: set a folder in "+
					"Settings."),
				target("acme/widgets", "/x/widgets", "main", ""),
				target("acme/zebra", "", "", "No local clone of acme/zebra: set a folder in "+
					"Settings."),
			},
		},
		{"nothing allowed", targetWorld{
			allow: []string{}, clones: []string{"acme/widgets", "/x/widgets"},
			buildRepo: "acme/widgets", base: "", baseBranches: nil,
			found: twoRepos().found, defaults: twoRepos().defaults,
		}, []openwork.Target{}},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			same(t, "targets", row.world.targets(), row.want)
		})
	}
}

func TestChooseTargetsReturnsTheChosenOnesOrEveryProblemAtOnce(t *testing.T) {
	t.Parallel()

	ready := []openwork.Target{
		target("acme/widgets", "/x/widgets", "main", ""),
		target("acme/gadgets", "/x/gadgets", "trunk", ""),
		target("acme/copy", "/x/widgets/", "main", ""),
		target("acme/broken", "", "", "No local clone of acme/broken: set a folder in Settings."),
		target("acme/a", "/x/a", "main", ""),
		target("acme/b", "/x/b", "main", ""),
		target("acme/c", "/x/c", "main", ""),
		target("acme/d", "/x/d", "main", ""),
		target("acme/e", "/x/e", "main", ""),
	}
	widgets := "acme/widgets"
	elsewhere := "elsewhere/repo"

	rows := []struct {
		name     string
		chosen   []string
		fallback *string
		want     []string
		problems []string
	}{
		{
			"two in the order chosen",
			[]string{"acme/gadgets", "acme/widgets"},
			nil,
			[]string{"acme/gadgets", "acme/widgets"},
			[]string{},
		},
		{
			"in another case",
			[]string{"ACME/Gadgets"},
			nil,
			[]string{"acme/gadgets"},
			[]string{},
		},
		{"none, with a default", []string{}, &widgets, []string{"acme/widgets"}, []string{}},
		{
			"none, without a default",
			[]string{},
			nil,
			[]string{},
			[]string{"Choose at least one repository."},
		},
		{
			"a default not allowed",
			[]string{},
			&elsewhere,
			[]string{},
			[]string{"elsewhere/repo is not an allowed repository: add it in Settings."},
		},
		{
			"the same one in another case",
			[]string{"acme/widgets", "Acme/Widgets"},
			nil,
			[]string{"acme/widgets"},
			[]string{"Acme/Widgets is chosen twice."},
		},
		{
			"the same clone twice",
			[]string{"acme/widgets", "acme/copy"},
			nil,
			[]string{"acme/widgets"},
			[]string{"acme/copy is the same clone as acme/widgets: /x/widgets."},
		},
		{"seven", []string{
			"acme/widgets", "acme/gadgets", "acme/a", "acme/b", "acme/c", "acme/d", "acme/e",
		}, nil, []string{}, []string{"Choose at most 6 repositories, not 7."}},
		{"every problem at once", []string{
			"acme/broken", "elsewhere/repo", "acme/gadgets", "ACME/GADGETS", "acme/copy",
			"acme/widgets",
		}, nil, []string{"acme/gadgets", "acme/copy"}, []string{
			"No local clone of acme/broken: set a folder in Settings.",
			"elsewhere/repo is not an allowed repository: add it in Settings.",
			"ACME/GADGETS is chosen twice.",
			"acme/widgets is the same clone as acme/copy: /x/widgets.",
		}},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			chosen, problems := openwork.ChooseTargets(ready, row.chosen, row.fallback)

			names := []string{}
			for _, picked := range chosen {
				names = append(names, picked.Repo)
			}

			same(t, "chosen", names, row.want)
			same(t, "problems", problems, row.problems)
		})
	}
}
