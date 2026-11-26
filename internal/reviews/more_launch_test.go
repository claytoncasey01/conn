package reviews_test

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/argv"
	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// The launch of a review: every dependency is a fake that records its calls in order, and
// nothing touches the disk.

const (
	dWorktree = "/data/conn/reviews/pr-412-acme--widget-app/widget-app"
	dRepo     = "acme/widget-app"
	dName     = "pr-412-acme--widget-app"
	dClone    = "/work/code/widget-app"
)

var (
	errDBoom     = errors.New("fatal: boom\nhint: second line")
	errDWrite    = errors.New("EACCES: permission denied\n    at somewhere")
	errDPathspec = errors.New("error: pathspec nope")
)

type dGitCall struct {
	args []string
	dir  string
}

// dUnlinkCall is one unlink and how many writes had been made when it ran.
type dUnlinkCall struct {
	folder, name string
	writesBefore int
}

type dOpenCall struct {
	name, cwd, command string
}

type dWriteCall struct {
	path, content string
	shared        bool
}

// dLaunchRig records every call Open makes. fail makes the git call whose joined args start with
// it fail with failErr; origin is what the worktree's origin reads as.
type dLaunchRig struct {
	calls    []string
	gits     []dGitCall
	opens    []dOpenCall
	writes   []dWriteCall
	unlinks  []dUnlinkCall
	files    map[string]string
	existing map[string]bool
	origin   string
	fail     string
	failErr  error
	revParse string
	writeErr error
	opened   cmux.Opened
}

func dNewRig(existing ...string) *dLaunchRig {
	rig := &dLaunchRig{
		files: map[string]string{}, existing: map[string]bool{}, origin: dRepo,
		failErr: errDBoom, revParse: ".git/info/exclude\n",
		opened: cmux.Opened{OK: true, Error: "", ManualCommand: ""},
	}
	for _, path := range existing {
		rig.existing[path] = true
	}

	return rig
}

func (r *dLaunchRig) git(_ context.Context, args []string, dir string) (string, error) {
	r.calls = append(r.calls, "git")
	r.gits = append(r.gits, dGitCall{args: args, dir: dir})

	if r.fail != "" {
		if strings.HasPrefix(strings.Join(args, " "), r.fail) {
			return "", r.failErr
		}
	}

	if args[0] == "rev-parse" {
		return r.revParse, nil
	}

	return "", nil
}

func (r *dLaunchRig) open(_ context.Context, name, cwd, command string) cmux.Opened {
	r.calls = append(r.calls, "open")
	r.opens = append(r.opens, dOpenCall{name: name, cwd: cwd, command: command})

	return r.opened
}

func (r *dLaunchRig) write(path, content string, shared bool) error {
	r.calls = append(r.calls, "write")
	r.writes = append(r.writes, dWriteCall{path: path, content: content, shared: shared})

	if r.writeErr != nil {
		return r.writeErr
	}

	r.files[path] = content

	return nil
}

// unlink removes the file from the rig's files, as UnlinkInOwnFolder does; a missing one is
// nothing to do.
func (r *dLaunchRig) unlink(folder, name string) error {
	r.calls = append(r.calls, "unlink")
	r.unlinks = append(r.unlinks, dUnlinkCall{
		folder: folder, name: name, writesBefore: len(r.writes),
	})
	delete(r.files, filepath.Join(folder, name))

	return nil
}

func (r *dLaunchRig) exists(path string) bool {
	_, written := r.files[path]
	if written {
		return true
	}

	return r.existing[path]
}

func (r *dLaunchRig) deps() reviews.LaunchDeps {
	cfg := dConfig()

	return reviews.LaunchDeps{
		Git: r.git, Open: asCmux(r.open), Claude: "claude", Write: r.write,
		Unlink: r.unlink,
		Exists: r.exists,
		Read:   func(path string) (string, error) { return r.files[path], nil },
		Origin: func(string) string { return r.origin },
		Names:  reviews.Names{Pattern: cfg.ReviewNamePattern},
		Prompts: reviews.Prompts{
			Review: cfg.ReviewPrompt, ReReview: cfg.ReReviewLine,
		},
	}
}

func (r *dLaunchRig) launch(
	t *testing.T, change func(opts *reviews.LaunchOpts),
) reviews.LaunchResult {
	t.Helper()

	opts := reviews.LaunchOpts{
		Repo: dRepo, Number: 412, BaseRef: "release/1.2", Model: "sonnet",
		Advisor: "claude-code", Name: dName, RepoPath: dClone, DataDir: "/data/conn",
		ReReviewSha: nil,
	}
	if change != nil {
		change(&opts)
	}

	return reviews.Open(t.Context(), r.deps(), opts)
}

func dConfig() config.Config {
	return config.FromEnv(func(string) (string, bool) { return "", false })
}

func dPrompt(t *testing.T, sha *string) string {
	t.Helper()

	cfg := dConfig()

	prompt, err := reviews.Prompts{Review: cfg.ReviewPrompt, ReReview: cfg.ReReviewLine}.
		BuildPrompt(412, sha, dRepo)
	if err != nil {
		t.Fatal(err)
	}

	return prompt
}

func dLaunched(worktree string) reviews.LaunchResult {
	return reviews.LaunchResult{OK: true, Worktree: worktree, Error: "", ManualCommand: ""}
}

func dRefused(why string) reviews.LaunchResult {
	return reviews.LaunchResult{OK: false, Worktree: "", Error: why, ManualCommand: ""}
}

func dSameArgs(t *testing.T, label string, got dGitCall, want []string, dir string) {
	t.Helper()

	if !slices.Equal(got.args, want) {
		t.Errorf("%s args %q, want %q", label, got.args, want)
	}

	if got.dir != dir {
		t.Errorf("%s dir %q, want %q", label, got.dir, dir)
	}
}

// TestDLaunchFetchesAddsWritesThenOpens: fetch, prune, worktree add, write the prompt, the exclude
// lookup and its write, then open, with these arguments.
func TestDLaunchFetchesAddsWritesThenOpens(t *testing.T) {
	t.Parallel()

	rig := dNewRig()
	got := rig.launch(t, nil)

	if got != dLaunched(dWorktree) {
		t.Fatalf("Open = %+v", got)
	}

	want := []string{"git", "git", "git", "unlink", "write", "git", "write", "open"}
	if !slices.Equal(rig.calls, want) {
		t.Fatalf("calls %v, want %v", rig.calls, want)
	}

	dSameArgs(t, "fetch", rig.gits[0], []string{"-C", dClone, "fetch", "origin", "release/1.2"}, "")
	dSameArgs(t, "prune", rig.gits[1], []string{"-C", dClone, "worktree", "prune"}, "")
	dSameArgs(t, "add", rig.gits[2], []string{
		"-C", dClone, "worktree", "add", "--detach", dWorktree, "origin/release/1.2",
	}, "")
	dSameArgs(t, "rev-parse", rig.gits[3], []string{"rev-parse", "--git-path", "info/exclude"},
		dWorktree)

	prompt := dWriteCall{
		path: dWorktree + "/.conn/prompt.md", content: dPrompt(t, nil), shared: false,
	}
	if rig.writes[0] != prompt {
		t.Errorf("prompt write %+v", rig.writes[0])
	}

	if !strings.Contains(rig.writes[0].content,
		"pull request #412 in the GitHub repository acme/widget-app") {
		t.Errorf("the prompt does not name the PR and repo: %s", rig.writes[0].content)
	}

	opened := dOpenCall{
		name: dName, cwd: dWorktree,
		command: argv.ShellLine(append(
			append([]string{"claude", "--disallowedTools"}, reviews.ReviewDisallowed()...),
			"--name", "pr-412-acme--widget-app", "--model", "sonnet", "--",
			"Read .conn/prompt.md and follow it exactly.",
		)),
	}
	if !slices.Equal(rig.opens, []dOpenCall{opened}) {
		t.Errorf("opens %+v", rig.opens)
	}
}

// TestDLaunchExcludesConnOnce: .conn/ is added to the exclude file, keeping what is there, and
// not twice; the prompt is private and the exclude file (the clone's own) is shared.
func TestDLaunchExcludesConnOnce(t *testing.T) {
	t.Parallel()

	exclude := dWorktree + "/.git/info/exclude"

	rig := dNewRig()
	rig.files[exclude] = "# mine\nnode_modules"
	rig.launch(t, nil)

	if rig.files[exclude] != "# mine\nnode_modules\n.conn/\n" {
		t.Errorf("exclude %q", rig.files[exclude])
	}

	shared := []bool{rig.writes[0].shared, rig.writes[1].shared}
	paths := []string{rig.writes[0].path, rig.writes[1].path}

	if !slices.Equal(shared, []bool{false, true}) {
		t.Errorf("shared %v", shared)
	}

	if !slices.Equal(paths, []string{dWorktree + "/.conn/prompt.md", exclude}) {
		t.Errorf("paths %v", paths)
	}

	again := dNewRig(dWorktree, dWorktree+"/.git")
	again.files[exclude] = ".conn/\n"
	again.launch(t, nil)

	if again.files[exclude] != ".conn/\n" {
		t.Errorf("exclude written again: %q", again.files[exclude])
	}

	if len(again.writes) != 1 {
		t.Errorf("writes %+v, want the prompt only", again.writes)
	}
}

// TestDLaunchUsesAnAbsoluteExcludePath: git's absolute answer is used as it is.
func TestDLaunchUsesAnAbsoluteExcludePath(t *testing.T) {
	t.Parallel()

	rig := dNewRig()
	rig.revParse = "/repo/.git/info/exclude\n"
	rig.launch(t, nil)

	if rig.files["/repo/.git/info/exclude"] != ".conn/\n" {
		t.Errorf("files %v", rig.files)
	}
}

// TestDLaunchMovesAnExistingWorktree: checkout in the worktree, no add and no prune, and the
// re-review prompt and the launch's own name.
func TestDLaunchMovesAnExistingWorktree(t *testing.T) {
	t.Parallel()

	sha := "abc1234"
	rig := dNewRig(dWorktree, dWorktree+"/.git")
	got := rig.launch(t, func(opts *reviews.LaunchOpts) {
		opts.Name, opts.ReReviewSha = "pr-412.2-acme--widget-app", &sha
	})

	if got != dLaunched(dWorktree) {
		t.Fatalf("Open = %+v", got)
	}

	dSameArgs(t, "fetch", rig.gits[0], []string{"-C", dClone, "fetch", "origin", "release/1.2"}, "")
	dSameArgs(t, "checkout", rig.gits[1],
		[]string{"-C", dWorktree, "checkout", "--detach", "origin/release/1.2"}, "")

	for _, call := range rig.gits {
		if slices.Contains(call.args, "add") {
			t.Errorf("added again: %q", call.args)
		}

		if slices.Contains(call.args, "prune") {
			t.Errorf("pruned: %q", call.args)
		}
	}

	if rig.writes[0].content != dPrompt(t, &sha) {
		t.Errorf("prompt %q", rig.writes[0].content)
	}

	if rig.opens[0].name != "pr-412.2-acme--widget-app" {
		t.Errorf("name %q", rig.opens[0].name)
	}

	if !strings.Contains(rig.opens[0].command, " --name pr-412.2-acme--widget-app ") {
		t.Errorf("command %q", rig.opens[0].command)
	}
}

// TestDLaunchRefusesAFolderItCannotReuse: a folder that is not a worktree, or a worktree of
// another repo's clone or with no origin, is refused naming the path, and nothing is run.
func TestDLaunchRefusesAFolderItCannotReuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		existing []string
		origin   string
		says     []string
	}{
		{"not a worktree", []string{dWorktree}, dRepo, []string{dWorktree, "remove"}},
		{
			"another repo's",
			[]string{dWorktree, dWorktree + "/.git"},
			"other/widget-app",
			[]string{dWorktree, dRepo},
		},
		{"no origin", []string{dWorktree, dWorktree + "/.git"}, "", []string{dWorktree, dRepo}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := dNewRig(tt.existing...)
			rig.origin = tt.origin
			got := rig.launch(t, nil)

			if got.OK {
				t.Fatal("launched")
			}

			for _, part := range tt.says {
				if !strings.Contains(got.Error, part) {
					t.Errorf("error %q does not say %q", got.Error, part)
				}
			}

			if len(rig.calls) != 0 {
				t.Errorf("calls %v", rig.calls)
			}
		})
	}
}

// TestDLaunchWriteFailureIsItsFirstLine: a failing write gives the first line of its message and
// never opens cmux.
func TestDLaunchWriteFailureIsItsFirstLine(t *testing.T) {
	t.Parallel()

	rig := dNewRig()
	rig.writeErr = errDWrite

	got := rig.launch(t, nil)
	if got != dRefused("EACCES: permission denied") {
		t.Errorf("Open = %+v", got)
	}

	if slices.Contains(rig.calls, "open") {
		t.Error("cmux was opened")
	}
}

// TestALaunchRemovesAStaleDraft: a draft a reused worktree still holds is not this launch's, so
// it is gone before the prompt is written and the session starts.
func TestALaunchRemovesAStaleDraft(t *testing.T) {
	t.Parallel()

	draft := filepath.Join(dWorktree, ".conn", reviews.DraftFile)
	rig := dNewRig(dWorktree, dWorktree+"/.git")
	rig.files[draft] = `{"commit":"stale"}`

	got := rig.launch(t, nil)
	if got != dLaunched(dWorktree) {
		t.Fatalf("Open = %+v", got)
	}

	_, found := rig.files[draft]
	if found {
		t.Error("the stale draft is still there")
	}

	if len(rig.unlinks) != 1 {
		t.Fatalf("unlinks %v, want one", rig.unlinks)
	}

	unlink := rig.unlinks[0]
	if unlink.folder != filepath.Join(dWorktree, ".conn") || unlink.name != "review.json" {
		t.Errorf("unlinked %q in %q, want review.json in .conn", unlink.name, unlink.folder)
	}

	promptWrite := slices.IndexFunc(rig.writes, func(call dWriteCall) bool {
		return strings.HasSuffix(call.path, "/prompt.md")
	})
	if promptWrite < 0 {
		t.Fatalf("no prompt was written: %v", rig.writes)
	}

	if unlink.writesBefore > promptWrite {
		t.Errorf("the unlink came after %d writes, the prompt was write %d",
			unlink.writesBefore, promptWrite)
	}
}

// TestAFailedUnlinkOfTheDraftOnlyStopsALaunchThatCannotTrustTheFolder: a worktree with no .conn
// folder yet has no stale draft, but a folder that is not the person's own stops the launch
// before anything is written.
func TestAFailedUnlinkOfTheDraftOnlyStopsALaunchThatCannotTrustTheFolder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		unlinkErr error
		wantOK    bool
	}{
		{"no folder yet", &fs.PathError{Op: "open", Path: "x", Err: fs.ErrNotExist}, true},
		{"a link as the folder", reviews.ErrNotOwnFolder, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := dNewRig()
			deps := rig.deps()
			deps.Unlink = func(string, string) error { return tt.unlinkErr }

			got := reviews.Open(t.Context(), deps, reviews.LaunchOpts{
				Repo: dRepo, Number: 412, BaseRef: "release/1.2", Model: "sonnet",
				Advisor: "claude-code", Name: dName, RepoPath: dClone, DataDir: "/data/conn",
				ReReviewSha: nil,
			})

			if got.OK != tt.wantOK {
				t.Errorf("OK = %t, want %t (%+v)", got.OK, tt.wantOK, got)
			}

			if !tt.wantOK && len(rig.writes) != 0 {
				t.Errorf("wrote %v after a refused folder", rig.writes)
			}
		})
	}
}
