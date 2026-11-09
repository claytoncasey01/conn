package origin_test

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/settings"
)

func mkdir(t *testing.T, path string) {
	t.Helper()

	err := os.MkdirAll(path, 0o750)
	if err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()

	mkdir(t, filepath.Dir(path))

	err := os.WriteFile(path, []byte(text), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

func config(url, extra string) string {
	text := "[core]\n\trepositoryformatversion = 0\n" + extra
	if url != "" {
		text += "[remote \"origin\"]\n\turl = " + url +
			"\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"
	}

	return text + "[branch \"main\"]\n\tremote = origin\n"
}

// clone is a plain clone: <folder>/.git/config.
func clone(t *testing.T, url, extra string) string {
	t.Helper()

	dir := t.TempDir()
	write(t, filepath.Join(dir, ".git", "config"), config(url, extra))

	return dir
}

// withConfig is a folder whose .git/config is text.
func withConfig(t *testing.T, text string) string {
	t.Helper()

	dir := t.TempDir()
	write(t, filepath.Join(dir, ".git", "config"), text)

	return dir
}

func TestEveryGitHubRemoteForm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url, want string
	}{
		{"git@github.com:Acme/Widgets.git", "acme/widgets"},
		{"https://github.com/acme/widgets", "acme/widgets"},
		{"https://github.com/acme/widgets.git", "acme/widgets"},
		{"ssh://git@github.com/acme/widgets.git", "acme/widgets"},
		{"", ""},
		{"git@gitlab.com:acme/widgets.git", ""},
		{"/srv/git/widgets.git", ""},
		{"https://github.com/acme/widgets?x=1", ""},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			t.Parallel()

			if got := origin.NewReader().Of(clone(t, tt.url, "")); got != tt.want {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestAFolderInsideACloneIsInItsRepo(t *testing.T) {
	t.Parallel()

	dir := clone(t, "git@github.com:acme/widgets.git", "")
	inside := filepath.Join(dir, "packages", "ui")
	mkdir(t, inside)

	tests := []string{inside, filepath.Join(dir, "not", "made", "yet")}
	for _, folder := range tests {
		if got := origin.NewReader().Of(folder); got != "acme/widgets" {
			t.Fatalf("%s: got %q", folder, got)
		}
	}
}

func TestAWorktreeWithAnAbsoluteGitdir(t *testing.T) {
	t.Parallel()

	main := clone(t, "git@github.com:acme/widgets.git", "")
	gitdir := filepath.Join(main, ".git", "worktrees", "wt")
	write(t, filepath.Join(gitdir, "commondir"), "../..\n")

	worktree := t.TempDir()
	write(t, filepath.Join(worktree, ".git"), "gitdir: "+gitdir+"\n")

	if got := origin.NewReader().Of(worktree); got != "acme/widgets" {
		t.Fatalf("got %q", got)
	}
}

func TestAWorktreeWithARelativeGitdir(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	write(t, filepath.Join(base, "main", ".git", "config"),
		config("https://github.com/acme/widgets.git", ""))
	write(t, filepath.Join(base, "main", ".git", "worktrees", "wt", "commondir"), "../..")
	write(t, filepath.Join(base, "wt", ".git"), "gitdir: ../main/.git/worktrees/wt\n")

	if got := origin.NewReader().Of(filepath.Join(base, "wt")); got != "acme/widgets" {
		t.Fatalf("got %q", got)
	}
}

func TestASubmoduleWithNoCommondir(t *testing.T) {
	t.Parallel()

	gitdir := t.TempDir()
	write(t, filepath.Join(gitdir, "config"), config("git@github.com:acme/lib.git", ""))

	submodule := t.TempDir()
	write(t, filepath.Join(submodule, ".git"), "gitdir: "+gitdir+"\n")

	if got := origin.NewReader().Of(submodule); got != "acme/lib" {
		t.Fatalf("got %q", got)
	}
}

// A .git that is a link is not followed: a folder holding one is in no repo of its own, so a
// link to a clone's .git cannot borrow that clone's origin.
func TestAGitLinkIsNoRepo(t *testing.T) {
	t.Parallel()

	target := clone(t, "git@github.com:acme/widgets.git", "")
	folder := t.TempDir()

	err := os.Symlink(filepath.Join(target, ".git"), filepath.Join(folder, ".git"))
	if err != nil {
		t.Fatal(err)
	}

	if got := origin.NewReader().Of(folder); got != "" {
		t.Fatalf("got %q, want no repo", got)
	}
}

func TestNoRepoAndBadArguments(t *testing.T) {
	t.Parallel()

	tests := []string{t.TempDir(), "/does/not/exist/at/all", "", "relative/path", "/a\x00b"}
	for _, folder := range tests {
		if got := origin.NewReader().Of(folder); got != "" {
			t.Fatalf("%q: got %q", folder, got)
		}
	}
}

func TestConfigSyntax(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, text, want string
	}{
		{
			"words in any case", "[Remote \"origin\"]\nURL = git@github.com:acme/widgets.git\n",
			"acme/widgets",
		},
		{"comments", "# comment\n; other\n\n[remote \"origin\"]\n  # note\n" +
			"  url = git@github.com:acme/widgets.git  # trailing\n", "acme/widgets"},
		{
			"quoted", "[remote \"origin\"]\n\turl = \"git@github.com:acme/widgets.git\"\n",
			"acme/widgets",
		},
		{
			"crlf", "[remote \"origin\"]\r\n\turl = git@github.com:acme/widgets.git\r\n",
			"acme/widgets",
		},
		{
			"spacing", "[remote \"origin\"] \n\turl=git@github.com:acme/widgets.git\n",
			"acme/widgets",
		},
		{
			"subsection case counts",
			"[remote \"Origin\"]\n\turl = git@github.com:acme/widgets.git\n",
			"",
		},
		{"unclosed header", "[remote \"origin\"\n\turl = git@github.com:acme/widgets.git\n", ""},
		{
			"upstream is not origin",
			"[remote \"upstream\"]\n\turl = git@github.com:acme/widgets.git\n",
			"",
		},
		{"empty", "", ""},
		{"binary", "\x00\x00binary\x00", ""},
		{
			"only origin and its first url", "[remote \"upstream\"]\n" +
				"\turl = git@github.com:up/stream.git\n[remote \"origin\"]\n" +
				"\tpushurl = git@github.com:push/only.git\n" +
				"\turl = git@github.com:acme/widgets.git\n" +
				"\turl = git@github.com:acme/second.git\n[remote \"fork\"]\n" +
				"\turl = git@github.com:me/fork.git\n",
			"acme/widgets",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := origin.NewReader().Of(withConfig(t, tt.text)); got != tt.want {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestABadGitFileIsNoRepo(t *testing.T) {
	t.Parallel()

	tests := []string{
		"", "not a pointer\n", "gitdir:\n", "gitdir: /does/not/exist\n",
		"gitdir: " + strings.Repeat("x", 5000),
	}
	for _, text := range tests {
		dir := t.TempDir()
		write(t, filepath.Join(dir, ".git"), text)

		if got := origin.NewReader().Of(dir); got != "" {
			t.Fatalf("%.30q: got %q", text, got)
		}
	}
}

func TestCachedByModificationTime(t *testing.T) {
	t.Parallel()

	reader := origin.NewReader()
	dir := clone(t, "git@github.com:acme/widgets.git", "")
	path := filepath.Join(dir, ".git", "config")
	then := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	touch(t, path, then)

	if got := reader.Of(dir); got != "acme/widgets" {
		t.Fatalf("first read: %q", got)
	}

	write(t, path, config("git@github.com:acme/renamed.git", ""))
	touch(t, path, then)

	if got := reader.Of(dir); got != "acme/widgets" {
		t.Fatalf("same time: %q, want the cached answer", got)
	}

	touch(t, path, then.Add(24*time.Hour))

	if got := reader.Of(dir); got != "acme/renamed" {
		t.Fatalf("new time: %q, want it read again", got)
	}

	err := os.Remove(path)
	if err != nil {
		t.Fatal(err)
	}

	if got := reader.Of(dir); got != "" {
		t.Fatalf("gone: %q", got)
	}

	write(t, path, config("git@github.com:acme/back.git", ""))

	if got := reader.Of(dir); got != "acme/back" {
		t.Fatalf("back: %q", got)
	}
}

func touch(t *testing.T, path string, when time.Time) {
	t.Helper()

	err := os.Chtimes(path, when, when)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAHugeConfigIsNotRead(t *testing.T) {
	t.Parallel()

	dir := clone(t, "git@github.com:acme/widgets.git", "#"+strings.Repeat("x", 2_000_000)+"\n")
	if got := origin.NewReader().Of(dir); got != "" {
		t.Fatalf("got %q", got)
	}
}

func withClones(root string, pairs ...string) settings.Settings {
	value := settings.Defaults()
	value.Repos.Allow = []string{"acme/*"}

	value.Repos.Clones = settings.NewClones(pairs...)
	if root != "" {
		value.Repos.CloneRoot = &root
	}

	return value
}

func TestCloneForSettingsEntryFirst(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	mine := clone(t, "git@github.com:acme/widgets.git", "")
	want := origin.Clone{Path: mine, How: origin.HowSettings}

	for _, repo := range []string{"acme/widgets", "ACME/Widgets"} {
		got, found := origin.NewReader().CloneFor(repo, withClones("", "acme/widgets", mine), home)
		if !found {
			t.Fatalf("%s: not found", repo)
		}

		if got != want {
			t.Fatalf("%s: got %+v", repo, got)
		}
	}
}

func TestCloneForNeedsTheRightOrigin(t *testing.T) {
	t.Parallel()

	wrong := clone(t, "git@github.com:acme/other.git", "")

	_, found := origin.NewReader().CloneFor("acme/widgets",
		withClones("", "acme/widgets", wrong), t.TempDir())
	if found {
		t.Fatal("found a clone of another repo")
	}
}

func TestCloneForRootThenProjects(t *testing.T) {
	t.Parallel()

	home, root := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "widgets", ".git", "config"),
		config("https://github.com/acme/widgets.git", ""))

	reader := origin.NewReader()

	got, _ := reader.CloneFor("acme/widgets", withClones(root), home)
	if got != (origin.Clone{Path: filepath.Join(root, "widgets"), How: origin.HowRoot}) {
		t.Fatalf("root: %+v", got)
	}

	_, found := reader.CloneFor("acme/widgets", withClones(""), home)
	if found {
		t.Fatal("nothing in projects yet")
	}

	write(t, filepath.Join(home, "projects", "widgets", ".git", "config"),
		config("https://github.com/acme/widgets.git", ""))

	got, _ = reader.CloneFor("acme/widgets", withClones(""), home)
	if got != (origin.Clone{
		Path: filepath.Join(home, "projects", "widgets"),
		How:  origin.HowProjects,
	}) {
		t.Fatalf("projects: %+v", got)
	}

	got, _ = reader.CloneFor("acme/widgets", withClones(root), home)
	if got.How != origin.HowRoot {
		t.Fatal("root wins over projects")
	}

	// A step with the wrong origin or no folder falls through.
	wrong := clone(t, "git@github.com:acme/other.git", "")

	got, _ = reader.CloneFor("acme/widgets",
		withClones(filepath.Join(root, "nowhere"), "acme/widgets", wrong), home)
	if got.How != origin.HowProjects {
		t.Fatalf("fell through to %+v", got)
	}
}

func TestCloneForRefusesWhatIsNotARepoRoot(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	file := filepath.Join(t.TempDir(), "afile")
	write(t, file, "x")

	inside := filepath.Join(clone(t, "git@github.com:acme/widgets.git", ""), "src")
	mkdir(t, inside)

	tests := []struct {
		name  string
		clone string
	}{
		{"missing", "/gone/away"},
		{"a file", file},
		{"a folder inside a clone", inside},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, found := origin.NewReader().CloneFor("acme/widgets",
				withClones("", "acme/widgets", tt.clone), home)
			if found {
				t.Fatal("found")
			}
		})
	}
}

func TestCloneForSkipsAPlainFolder(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	mkdir(t, filepath.Join(home, "projects", "widgets"))

	_, found := origin.NewReader().CloneFor("acme/widgets", withClones(""), home)
	if found {
		t.Fatal("a plain folder")
	}
}

func TestCloneForRefusesABadName(t *testing.T) {
	t.Parallel()

	tests := []string{
		"", "acme", "../x", "acme/..", "a/b/c", "acme/*", "acme/wid gets", "acme/../trap",
	}
	for _, repo := range tests {
		_, found := origin.NewReader().CloneFor(repo, withClones(t.TempDir()), t.TempDir())
		if found {
			t.Fatalf("%q", repo)
		}
	}
}

func TestCloneForFollowsALink(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	target := clone(t, "git@github.com:acme/widgets.git", "")
	mkdir(t, filepath.Join(home, "projects"))

	err := os.Symlink(target, filepath.Join(home, "projects", "widgets"))
	if err != nil {
		t.Fatal(err)
	}

	got, _ := origin.NewReader().CloneFor("acme/widgets", withClones(""), home)
	if got.How != origin.HowProjects {
		t.Fatalf("got %+v", got)
	}
}

func TestCloneForTakesAWorktree(t *testing.T) {
	t.Parallel()

	gitdir := t.TempDir()
	write(t, filepath.Join(gitdir, "config"), config("git@github.com:acme/widgets.git", ""))

	worktree := t.TempDir()
	write(t, filepath.Join(worktree, ".git"), "gitdir: "+gitdir+"\n")

	got, _ := origin.NewReader().CloneFor("acme/widgets",
		withClones("", "acme/widgets", worktree), t.TempDir())
	if got != (origin.Clone{Path: worktree, How: origin.HowSettings}) {
		t.Fatalf("got %+v", got)
	}
}

func TestCloneForMatchesTheFolderInAnyCase(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	write(t, filepath.Join(home, "projects", "Widgets", ".git", "config"),
		config("https://github.com/Acme/Widgets.git", ""))

	want := origin.Clone{Path: filepath.Join(home, "projects", "Widgets"), How: origin.HowProjects}

	for _, repo := range []string{"acme/widgets", "Acme/Widgets"} {
		if got, _ := origin.NewReader().CloneFor(repo, withClones(""), home); got != want {
			t.Fatalf("%s: got %+v", repo, got)
		}
	}
}

func TestUpstreamOfWithRealGit(t *testing.T) {
	t.Parallel()

	main := t.TempDir()
	git(t, main, "init", "-q", "-b", "main")
	git(t, main, "remote", "add", "origin", "https://github.com/acme/web.git")
	write(t, filepath.Join(main, "a"), "a")
	git(t, main, "add", "a")
	git(t, main, "commit", "-qm", "a")
	git(t, main, "update-ref", "refs/remotes/origin/feature/login-fix", "HEAD")

	worktree := filepath.Join(t.TempDir(), "wt")
	git(t, main, "worktree", "add", "-q", "-b", "conn-self-412", worktree,
		"origin/feature/login-fix")

	if got := origin.UpstreamOf(worktree, "conn-self-412"); got != "feature/login-fix" {
		t.Fatalf("got %q", got)
	}

	git(t, main, "branch", "plain")

	for _, branch := range []string{"plain", "nope"} {
		if got := origin.UpstreamOf(main, branch); got != "" {
			t.Fatalf("%s: got %q", branch, got)
		}
	}
}

// git runs git in folder with no user or system config.
func git(t *testing.T, folder string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = folder

	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@t")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func TestUpstreamOfRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, body, want string
	}{
		{
			"tracks a branch",
			"[branch \"x\"]\n\tremote = origin\n\tmerge = refs/heads/feature/a\n",
			"feature/a",
		},
		{
			"another remote",
			"[branch \"x\"]\n\tremote = upstream\n\tmerge = refs/heads/feature/a\n",
			"",
		},
		{"a tag", "[branch \"x\"]\n\tremote = origin\n\tmerge = refs/tags/v1\n", ""},
		{
			"a refspec",
			"[branch \"x\"]\n\tremote = origin\n\tmerge = refs/heads/a:refs/heads/main\n",
			"",
		},
		{"a dash-led name", "[branch \"x\"]\n\tremote = origin\n\tmerge = refs/heads/-oops\n", ""},
		{"the last value", "[branch \"x\"]\n\tremote = origin\n\tmerge = refs/heads/a\n" +
			"[branch \"x\"]\n\tmerge = refs/heads/b\n", "b"},
		{"another branch", "[branch \"y\"]\n\tremote = origin\n\tmerge = refs/heads/a\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			folder := clone(t, "git@github.com:acme/web.git", tt.body)
			if got := origin.UpstreamOf(folder, "x"); got != tt.want {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestUpstreamOfBadInput(t *testing.T) {
	t.Parallel()

	for _, folder := range []string{"relative/path", t.TempDir()} {
		if got := origin.UpstreamOf(folder, "x"); got != "" {
			t.Fatalf("%s: got %q", folder, got)
		}
	}
}

func TestVisible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		repo  string
		allow []string
		want  bool
	}{
		{"", []string{"acme/*"}, true},
		{"", nil, true},
		{"acme/widgets", []string{"acme/*"}, true},
		{"Acme/Widgets", []string{"acme/widgets"}, true},
		{"acme/widgets", []string{"acme/gadgets"}, false},
		{"acme-labs/widgets", []string{"acme/*"}, false},
		{"acme/widgets", nil, false},
		{"acme/widgets", []string{}, false},
		{"not a repo", []string{"acme/*"}, false},
	}
	for _, tt := range tests {
		if got := origin.Visible(tt.repo, tt.allow); got != tt.want {
			t.Fatalf("%q in %q: got %v", tt.repo, tt.allow, got)
		}
	}
}

func TestFolderRepoOnlyForAFolderThatExists(t *testing.T) {
	t.Parallel()

	parent := withConfig(t, "[remote \"origin\"]\n\turl = git@github.com:jane/dotfiles.git\n")
	mkdir(t, filepath.Join(parent, "here"))
	write(t, filepath.Join(parent, "a-file"), "x")

	tests := []struct {
		folder, want string
	}{
		{filepath.Join(parent, "here"), "jane/dotfiles"},
		{parent, "jane/dotfiles"},
		{filepath.Join(parent, "gone"), ""},
		{filepath.Join(parent, "gone", "deeper"), ""},
		{filepath.Join(parent, "a-file"), ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := origin.NewReader().FolderRepo(tt.folder); got != tt.want {
			t.Fatalf("%s: got %q", tt.folder, got)
		}
	}
}

func TestClonesInFindsTheClonesCloneForWouldFind(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	projects := t.TempDir()
	write(t, filepath.Join(root, "widgets", ".git", "config"),
		config("git@github.com:acme/widgets.git", ""))
	write(t, filepath.Join(root, "Gadgets", ".git", "config"),
		config("https://github.com/Acme/Gadgets.git", ""))
	// Named for another repository: CloneFor would never look here for it.
	write(t, filepath.Join(root, "renamed", ".git", "config"),
		config("git@github.com:acme/other.git", ""))
	// Not on GitHub, and not a repository at all.
	write(t, filepath.Join(root, "local", ".git", "config"), config("/srv/git/local.git", ""))
	write(t, filepath.Join(root, "plain", "file"), "x")
	write(t, filepath.Join(projects, "tools", ".git", "config"),
		config("git@github.com:acme/tools.git", ""))

	rows := []struct {
		name       string
		parents    []string
		maxFolders int
		want       []string
	}{
		{
			"every clone, the clone folder first",
			[]string{root, projects},
			100,
			[]string{"acme/gadgets", "acme/widgets", "acme/tools"},
		},
		{
			"a parent that is not there",
			[]string{filepath.Join(root, "nope"), projects},
			100,
			[]string{"acme/tools"},
		},
		{
			"the cap reaches the next parent only when it is not spent",
			[]string{projects, root},
			1,
			[]string{"acme/tools"},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			got := origin.NewReader().ClonesIn(row.parents, row.maxFolders)
			if !slices.Equal(got, row.want) {
				t.Fatalf("got %v, want %v", got, row.want)
			}
		})
	}
}

func TestClonesInListsAtMostFoldersMaxFolders(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	folders := []string{"a", "b", "c", "d", "e"}
	every := make([]string, 0, len(folders))

	for _, name := range folders {
		write(t, filepath.Join(root, name, ".git", "config"),
			config("git@github.com:acme/"+name+".git", ""))
		every = append(every, "acme/"+name)
	}

	// Which folders a cut listing holds is the file system's order: only how many is fixed.
	for _, maxFolders := range []int{0, 1, 3, 5, 9} {
		got := origin.NewReader().ClonesIn([]string{root}, maxFolders)
		if len(got) != min(maxFolders, len(every)) {
			t.Fatalf("%d: got %v", maxFolders, got)
		}

		for _, repo := range got {
			if !slices.Contains(every, repo) {
				t.Fatalf("%d: got %v", maxFolders, got)
			}
		}
	}
}

// TestTheSearchForAFolderInAnyCaseReadsABoundedListing: at most the bound's entries are read,
// so no more than that many folders are found, however many the folder holds. It needs a file
// system that tells cases apart, and is skipped on one that does not.
func TestTheSearchForAFolderInAnyCaseReadsABoundedListing(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()

	for _, name := range []string{"widgets", "Widgets", "WIDGETS"} {
		err := os.Mkdir(filepath.Join(parent, name), 0o700)
		if errors.Is(err, fs.ErrExist) {
			t.Skip("the file system does not tell cases apart")
		}

		if err != nil {
			t.Fatal(err)
		}
	}

	if got := origin.NamedLikeWithin(parent, "widgets", 3); len(got) != 3 {
		t.Fatalf("with room for all: %q", got)
	}

	if got := origin.NamedLikeWithin(parent, "widgets", 2); len(got) != 2 {
		t.Errorf("read past the bound: %q", got)
	}
}
