package reviews_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

// The launch file: writeLaunchFile on a temp folder, and the name tests the recorded answers do
// not hold (the one-to-one sweep, the name pattern, the folder).

func dMode(t *testing.T, path string) fs.FileMode {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	return info.Mode().Perm()
}

func dRead(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func dMust(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

// dLoose writes a file open to others (0644 whatever the umask).
func dLoose(t *testing.T, path, content string) {
	t.Helper()

	dMust(t, os.WriteFile(path, []byte(content), 0o644))
	dMust(t, os.Chmod(path, 0o644))
}

// TestDWriteLaunchFileMakesAPrivateFile: a new file with its folder, and an existing one that was
// open to others, end up 0600 with the new text.
func TestDWriteLaunchFileMakesAPrivateFile(t *testing.T) {
	t.Parallel()

	fresh := filepath.Join(t.TempDir(), "new", ".conn", "prompt.md")
	dMust(t, reviews.WriteLaunchFile(fresh, "hello", false))

	if dRead(t, fresh) != "hello" {
		t.Errorf("new file %q", dRead(t, fresh))
	}

	if dMode(t, fresh) != 0o600 {
		t.Errorf("new file mode %o", dMode(t, fresh))
	}

	old := filepath.Join(t.TempDir(), "prompt.md")
	dLoose(t, old, "an earlier, longer prompt")
	dMust(t, reviews.WriteLaunchFile(old, "next", false))

	if dRead(t, old) != "next" {
		t.Errorf("replaced file %q", dRead(t, old))
	}

	if dMode(t, old) != 0o600 {
		t.Errorf("replaced file mode %o", dMode(t, old))
	}
}

// TestDWriteLaunchFileReplacesALink: a link left at the path, live or dangling, is replaced by a
// file; what it pointed at is never written.
func TestDWriteLaunchFileReplacesALink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere.txt")
	dLoose(t, target, "not yours to write")

	for _, link := range []struct{ path, to string }{
		{filepath.Join(dir, "prompt.md"), target},
		{filepath.Join(dir, "dangling.md"), filepath.Join(dir, "nothing-here")},
	} {
		dMust(t, os.Symlink(link.to, link.path))
		dMust(t, reviews.WriteLaunchFile(link.path, "prompt", false))

		info, err := os.Lstat(link.path)
		dMust(t, err)

		if !info.Mode().IsRegular() {
			t.Errorf("%s is %v, not a file", link.path, info.Mode())
		}

		if dRead(t, link.path) != "prompt" {
			t.Errorf("%s holds %q", link.path, dRead(t, link.path))
		}

		if dMode(t, link.path) != 0o600 {
			t.Errorf("%s mode %o", link.path, dMode(t, link.path))
		}
	}

	if dRead(t, target) != "not yours to write" {
		t.Errorf("the link's target was written: %q", dRead(t, target))
	}

	if dMode(t, target) != 0o644 {
		t.Errorf("the link's target mode %o", dMode(t, target))
	}
}

// TestDWriteLaunchFileWritesASharedFileInPlace: the clone's exclude keeps its mode and its link.
func TestDWriteLaunchFileWritesASharedFileInPlace(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	exclude := filepath.Join(dir, "exclude")
	dLoose(t, exclude, "# mine\n")
	dMust(t, reviews.WriteLaunchFile(exclude, "# mine\n.conn/\n", true))

	if dRead(t, exclude) != "# mine\n.conn/\n" {
		t.Errorf("exclude %q", dRead(t, exclude))
	}

	if dMode(t, exclude) != 0o644 {
		t.Errorf("exclude mode %o", dMode(t, exclude))
	}

	target := filepath.Join(dir, "real-exclude")
	linked := filepath.Join(dir, "linked-exclude")

	dLoose(t, target, "")
	dMust(t, os.Symlink(target, linked))
	dMust(t, reviews.WriteLaunchFile(linked, ".conn/\n", true))

	info, err := os.Lstat(linked)
	dMust(t, err)

	if info.Mode()&fs.ModeSymlink == 0 {
		t.Error("the link was replaced")
	}

	if dRead(t, target) != ".conn/\n" {
		t.Errorf("target %q", dRead(t, target))
	}
}

func dNames() reviews.Names { return reviews.Names{Pattern: dConfig().ReviewNamePattern} }

// TestDReviewNamesAreOneToOne: no two (owner, repo, number, launch) share a name, whatever dashes,
// dots and digits they hold, and every name matches the pattern.
func TestDReviewNamesAreOneToOne(t *testing.T) {
	t.Parallel()

	names := dNames()
	owners := []string{"a", "b", "a-b", "ab", "1", "a1", "a-1", "a_b"}
	repos := []string{
		"b", "c", "b-c", "a-b", "b--c", "b-5", "b-5-2", "5", "5-2", ".b", "b.c", "b_c", "c-1",
	}
	seen := map[string]string{}

	for _, owner := range owners {
		for _, repo := range repos {
			for _, number := range []int64{1, 2, 5, 12} {
				for _, previous := range []int{0, 1, 4} {
					who := owner + "/" + repo + "#" + strconv.FormatInt(number, 10) + " launch " +
						strconv.Itoa(previous+1)

					name, err := names.ReviewName(owner+"/"+repo, number, previous)
					if err != nil {
						t.Fatalf("%s: %v", who, err)
					}

					if seen[name] != "" {
						t.Errorf("%s and %s share %s", who, seen[name], name)
					}

					seen[name] = who
				}
			}
		}
	}

	if len(seen) != len(owners)*len(repos)*4*3 {
		t.Errorf("%d names", len(seen))
	}

	pairs := [][2]string{
		{dName5(t, "acme/widgets", 5, 1), dName5(t, "acme/widgets-5", 2, 0)},
		{dName5(t, "a-b/c", 5, 0), dName5(t, "a/b-c", 5, 0)},
	}
	for _, pair := range pairs {
		if pair[0] == pair[1] {
			t.Errorf("collision %s", pair[0])
		}
	}
}

func dName5(t *testing.T, repo string, number int64, previous int) string {
	t.Helper()

	name, err := dNames().ReviewName(repo, number, previous)
	dMust(t, err)

	return name
}

// TestDReviewNamePatternIsSyntactic: the new names and the old pr-<n> ones, nothing a shell would
// read.
func TestDReviewNamePatternIsSyntactic(t *testing.T) {
	t.Parallel()

	pattern := dConfig().ReviewNamePattern
	good := []string{
		"pr-12", "pr-12-2", "pr-12-acme--widgets", "pr-12.3-acme--widgets", "pr-3-jane--.github",
		"pr-7-a.b--c_d-e", "pr-5-x0123456789ab",
	}
	bad := []string{
		"pr-", "pr-x", "PR-1", "pr-1-Acme--w", "pr-1-acme/widgets", "pr-1;id", "pr-1-a b",
		"pr-1\n", "-pr-1", "pr-acme--widgets", "pr-$(id)-1", "pr-1.x-a--b",
	}

	for _, name := range good {
		if !pattern.MatchString(name) {
			t.Errorf("%q does not match", name)
		}
	}

	for _, name := range bad {
		if pattern.MatchString(name) {
			t.Errorf("%q matches", name)
		}
	}
}

// TestDReviewFolderIsTheFirstLaunchsName: so folders are one-to-one too.
func TestDReviewFolderIsTheFirstLaunchsName(t *testing.T) {
	t.Parallel()

	names := dNames()
	folder := func(repo string, number int64) string {
		got, err := names.ReviewFolder(repo, number)
		dMust(t, err)

		return got
	}

	if folder("Acme/Widgets", 12) != "pr-12-acme--widgets" {
		t.Errorf("folder %q", folder("Acme/Widgets", 12))
	}

	if folder("a-b/c", 12) == folder("a/b-c", 12) {
		t.Error("a-b/c and a/b-c share a folder")
	}

	second := strings.Replace(dName5(t, "acme/widgets", 5, 1), ".", "-", 1)
	if folder("acme/widgets-5", 2) == second {
		t.Errorf("folder of acme/widgets-5#2 is %s", second)
	}

	shape := regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	for _, repo := range []string{"acme/widgets", "a-b/c", "jane/.github"} {
		if !shape.MatchString(folder(repo, 3)) {
			t.Errorf("folder %q", folder(repo, 3))
		}
	}

	worktree, err := names.ReviewWorktree("/data/conn", "Acme/Widgets", 12)
	dMust(t, err)

	if worktree != "/data/conn/reviews/pr-12-acme--widgets/widgets" {
		t.Errorf("worktree %q", worktree)
	}

	_, err = names.ReviewFolder("../x", 1)
	if err == nil {
		t.Error("a folder for ../x")
	}
}
