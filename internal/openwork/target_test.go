package openwork_test

import (
	"strconv"
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
	// seen are the repositories found on this machine, in the order found.
	seen []string
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

	return openwork.BuildTargets(current, world.seen, find, defaultOf)
}

// twoRepos is acme/* allowed, with clones of widgets and gadgets set in Settings and on disk.
func twoRepos() targetWorld {
	return targetWorld{
		allow:        []string{"acme/*"},
		clones:       []string{"acme/gadgets", "/x/gadgets", "acme/widgets", "/x/widgets"},
		buildRepo:    "",
		base:         "",
		baseBranches: nil,
		seen:         nil,
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
		{"both ready, in the order named", twoRepos(), []openwork.Target{
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
				target("acme/zebra", "", "", "No local clone of acme/zebra: set a folder in "+
					"Settings."),
				target("acme/widgets", "/x/widgets", "main", ""),
				target("acme/apple", "", "", "No local clone of acme/apple: set a folder in "+
					"Settings."),
			},
		},
		{"nothing allowed", targetWorld{
			allow: []string{}, clones: []string{"acme/widgets", "/x/widgets"},
			buildRepo: "acme/widgets", base: "", baseBranches: nil, seen: nil,
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
		}, nil, []string{
			"acme/widgets", "acme/gadgets", "acme/a", "acme/b", "acme/c", "acme/d",
		}, []string{"Choose at most 6 repositories, not 7."}},
		{"seven, one not allowed", []string{
			"acme/widgets", "elsewhere/repo", "acme/a", "acme/b", "acme/c", "acme/d", "acme/e",
		}, nil, []string{"acme/widgets", "acme/a", "acme/b", "acme/c", "acme/d"}, []string{
			"Choose at most 6 repositories, not 7.",
			"elsewhere/repo is not an allowed repository: add it in Settings.",
		}},
		{
			"an empty name",
			[]string{"acme/widgets", ""},
			nil,
			[]string{"acme/widgets"},
			[]string{"An empty repository name was chosen."},
		},
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

// seenWorld is an allowlist of patterns only, with nothing named in Settings, and clones on disk
// of acme/a, acme/b and other/a; seen are the repositories found on this machine.
func seenWorld(allow, seen []string) targetWorld {
	return targetWorld{
		allow: allow, clones: []string{}, buildRepo: "", base: "", baseBranches: nil, seen: seen,
		found:    map[string]string{"acme/a": "/x/a", "acme/b": "/x/b", "other/a": "/y/a"},
		defaults: map[string]string{"/x/a": "main", "/x/b": "main", "/y/a": "main"},
	}
}

func TestBuildTargetsOfferTheAllowedRepositoriesFoundOnThisMachine(t *testing.T) {
	t.Parallel()

	acme := []string{"acme/*"}
	noClone := "No local clone of acme/c: set a folder in Settings."

	rows := []struct {
		name  string
		world targetWorld
		want  []string
	}{
		{"a pattern alone names none", seenWorld(acme, nil), []string{}},
		{
			"found, in the order found", seenWorld(acme, []string{"acme/b", "acme/a"}),
			[]string{"acme/b", "acme/a"},
		},
		{
			"once each in any case", seenWorld(acme, []string{"acme/a", "ACME/A", "Acme/b"}),
			[]string{"acme/a", "Acme/b"},
		},
		{"only what the allowlist covers, and only names", seenWorld(acme,
			[]string{"other/a", "acme", "acme/*", "acme/a"}), []string{"acme/a"}},
		{"allowlist order, then the order found", seenWorld([]string{"other/*", "acme/*"},
			[]string{"acme/b", "other/a", "acme/a"}), []string{"other/a", "acme/b", "acme/a"}},
		{"after what Settings names", func() targetWorld {
			world := seenWorld(acme, []string{"acme/a", "acme/b"})
			world.clones = []string{"acme/b", "/x/b"}

			return world
		}(), []string{"acme/b", "acme/a"}},
		{
			"found without a clone CloneFor finds", seenWorld(acme, []string{"acme/c"}),
			[]string{"acme/c: " + noClone},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			got := []string{}

			for _, listed := range row.world.targets() {
				if listed.Reason != "" {
					got = append(got, listed.Repo+": "+listed.Reason)

					continue
				}

				got = append(got, listed.Repo)
			}

			same(t, "listed", got, row.want)
		})
	}
}

func TestBuildTargetsListAtMostBuildReposMax(t *testing.T) {
	t.Parallel()

	seen := make([]string, 0, openwork.BuildReposMax+1)
	for index := range openwork.BuildReposMax + 1 {
		seen = append(seen, "acme/r"+strconv.Itoa(index))
	}

	same(t, "listed", len(seenWorld([]string{"acme/*"}, seen).targets()), openwork.BuildReposMax)

	// The cap keeps the allowlist's order, not the order found: other/a, found first, is under
	// the second pattern, after every acme repository.
	world := seenWorld([]string{"acme/*", "other/*"}, append([]string{"other/a"}, seen...))
	listed := world.targets()
	same(t, "capped", len(listed), openwork.BuildReposMax)
	same(t, "last", listed[len(listed)-1].Repo, "acme/r"+strconv.Itoa(openwork.BuildReposMax-1))
}
