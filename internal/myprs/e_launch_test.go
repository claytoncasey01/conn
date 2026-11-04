package myprs_test

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/cmux"
	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// The port of myPrsLaunch.test.ts. Every dependency is a fake: no real git, cmux or claude runs.
// The recorded answers (selfName, selfFolder, selfPrompt, launches) cover the prompt's text, the
// verb sequences of a new, reused and leftover worktree, the refusal texts for the git answers
// they pick, the claude command, a failed write and a failed cmux; here are the refusal lists,
// the git answers they never pick, the folder of each repo and the command as a shell runs it.
// A self name being a selfreview session is internal/sessions' kind_test.go.

const (
	// eSha is the commit the fake worktree is at.
	eSha  = "c0ffee0000000000000000000000000000000000"
	eRepo = "/work/code/widget-app"
	// eWorktree is acme/web#12's worktree; c52e5cc5 is the sha1 of acme/web, worked out apart.
	eWorktree = "/data/conn/myprs/acme-web-c52e5cc5-12/widget-app"
	eBoom     = "fatal: boom\nhint: second line"
)

// eOut is one git answer: the call whose joined args start with prefix answers out.
type eOut struct{ prefix, out string }

// eOwn is what git says of a worktree that belongs to the clone at eRepo.
func eOwn() eOut {
	return eOut{"-C " + eWorktree + " rev-parse --git-common-dir", eRepo + "/.git\n"}
}

func eOnBranch() eOut { return eOut{"-C " + eWorktree + " branch", "conn-self-12\n"} }

type eGitCall struct {
	args []string
	dir  string
}

type eWrite struct {
	path, text string
	shared     bool
}

// eRig records every call in order. fail makes the git call whose joined args start with it
// fail with stderr; outs answer by prefix; opened is what open answers.
type eRig struct {
	calls    []string
	gits     []eGitCall
	opens    [][3]string
	writes   []eWrite
	files    map[string]string
	existing map[string]bool
	fail     string
	stderr   string
	outs     []eOut
	opened   cmux.Opened
}

func eNewRig(existing []string, outs ...eOut) *eRig {
	return &eRig{
		calls: nil, gits: nil, opens: nil, writes: nil, files: map[string]string{},
		existing: eSet(existing...), fail: "", stderr: eBoom, outs: outs,
		opened: cmux.Opened{OK: true, Error: "", ManualCommand: ""},
	}
}

func (r *eRig) git(_ context.Context, args []string, dir string) (string, error) {
	r.calls = append(r.calls, "git")
	r.gits = append(r.gits, eGitCall{args: args, dir: dir})
	joined := strings.Join(args, " ")

	if r.fail != "" {
		if strings.HasPrefix(joined, r.fail) {
			return "", &myprs.FailureError{Message: r.stderr}
		}
	}

	for _, out := range r.outs {
		if strings.HasPrefix(joined, out.prefix) {
			return out.out, nil
		}
	}

	return eDefaultAnswer(args), nil
}

// eDefaultAnswer is the fake's answer to a call no out names: the commit for HEAD or an origin
// branch read in a folder, the exclude file's path, else nothing.
func eDefaultAnswer(args []string) string {
	if args[0] == "rev-parse" {
		return ".git/info/exclude\n"
	}

	if len(args) < 4 {
		return ""
	}

	if args[2] != "rev-parse" {
		return ""
	}

	switch {
	case args[3] == "HEAD", strings.HasPrefix(args[3], "origin/"):
		return eSha + "\n"
	}

	return ""
}

func (r *eRig) deps() reviews.LaunchDeps {
	return reviews.LaunchDeps{
		Git: r.git,
		Open: func(_ context.Context, name, cwd, command string) cmux.Opened {
			r.calls = append(r.calls, "open")
			r.opens = append(r.opens, [3]string{name, cwd, command})

			return r.opened
		},
		Write: func(path, content string, shared bool) error {
			r.calls = append(r.calls, "write")
			r.writes = append(r.writes, eWrite{path: path, text: content, shared: shared})
			r.files[path] = content

			return nil
		},
		Exists: func(path string) bool {
			if r.existing[path] {
				return true
			}

			_, found := r.files[path]

			return found
		},
		Read:   func(path string) (string, error) { return r.files[path], nil },
		Origin: nil, Names: reviews.Names{Pattern: nil},
		Prompts: reviews.Prompts{Review: "", ReReview: ""},
	}
}

// verbs are the git calls, each joined.
func (r *eRig) verbs() []string {
	out := make([]string, 0, len(r.gits))
	for _, call := range r.gits {
		out = append(out, strings.Join(call.args, " "))
	}

	return out
}

func (r *eRig) ran(part string) bool {
	return slices.ContainsFunc(r.verbs(), func(verb string) bool {
		return strings.Contains(verb, part)
	})
}

// eOpts are the launch options of myPrsLaunch.test.ts, change editing them.
func eOpts(change ...func(opts *myprs.LaunchOpts)) myprs.LaunchOpts {
	out := myprs.LaunchOpts{
		Repo: "acme/web", Number: 12, HeadRef: "feature/login-fix", Model: "sonnet",
		Name: "self-web-12", RepoPath: eRepo, DataDir: "/data/conn", Round: 1,
	}

	for _, one := range change {
		one(&out)
	}

	return out
}

func eLaunchWith(rig *eRig, opts myprs.LaunchOpts) myprs.LaunchResult {
	return selfOf().Launch(context.Background(), rig.deps(), opts)
}

// eNothingDone fails when the rig wrote or opened anything.
func eNothingDone(t *testing.T, rig *eRig) {
	t.Helper()

	if len(rig.writes) != 0 {
		t.Errorf("wrote %v", rig.writes)
	}

	if len(rig.opens) != 0 {
		t.Errorf("opened %v", rig.opens)
	}
}

func TestESelfName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		repo          string
		number, round float64
		want          string
	}{
		{"acme/web", 12, 1, "self-web-12"},
		{"acme/web", 12, 2, "self-web-12-2"},
		{"acme/web", 12, 3, "self-web-12-3"},
		{"acme/api", 12, 1, "self-api-12"},
		{"acme/Widget-App", 5, 1, "self-widget-app-5"},
		{"acme/my_repo", 5, 1, "self-my_repo-5"},
		{"acme/widgets.core", 5, 1, "self-widgets.core-5"},
		{"acme/web", 7, 4, "self-web-7-4"},
		{"Acme/Widgets.Core_2", 7, 1, "self-widgets.core_2-7"},
		{"a/b", 7, 1, "self-b-7"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			got, err := selfOf().Name(tt.repo, tt.number, tt.round)
			if err != nil {
				t.Fatal(err)
			}

			if got != tt.want {
				t.Errorf("got %q", got)
			}

			if !defaults().SelfNamePattern.MatchString(got) {
				t.Errorf("%q is not a self name", got)
			}
		})
	}
}

func TestESelfNameRefuses(t *testing.T) {
	t.Parallel()

	type row struct {
		repo          string
		number, round float64
		wantErr       error
	}

	tests := make([]row, 0, 21)
	tests = append(tests, row{"a/" + strings.Repeat("x", 61), 1, 1, myprs.ErrENameTooLong})

	for _, repo := range []string{
		"", "web", "a/", "/b", "a/b/c", "a b/c", "a/b\n", "a/b c", "a/b;id", "a/$(id)", "-x/y",
	} {
		tests = append(tests, row{repo, 1, 1, myprs.ErrENotRepoName})
	}

	for _, number := range []float64{0, -1, 1.5, math.NaN(), math.Inf(1)} {
		tests = append(tests, row{"a/b", number, 1, myprs.ErrENotNumber})
	}

	for _, round := range []float64{0, -1, 1.5, math.NaN()} {
		tests = append(tests, row{"a/b", 1, round, myprs.ErrENotARound})
	}

	for _, tt := range tests {
		t.Run(tt.repo, func(t *testing.T) {
			t.Parallel()

			_, err := selfOf().Name(tt.repo, tt.number, tt.round)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("%q %v %v: got %v", tt.repo, tt.number, tt.round, err)
			}
		})
	}
}

func ePrompt() myprs.PromptOpts {
	return myprs.PromptOpts{
		Repo: "acme/web", Number: 12, Round: 1,
		Worktree: "/data/conn/myprs/acme-web-12/widget-app", HeadRef: "feature/login-fix",
	}
}

func TestEBuildPrompt(t *testing.T) {
	t.Parallel()

	opts := ePrompt()
	opts.Worktree = "/data/$&/$1/w"

	got, err := selfOf().BuildPrompt(opts)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(got, "/data/$&/$1/w") {
		t.Error("the worktree was not put in as it is")
	}

	plain, err := selfOf().BuildPrompt(ePrompt())
	if err != nil {
		t.Fatal(err)
	}

	if regexp.MustCompile(`push[^\n]*(--force|-f\b|\+conn)`).MatchString(plain) {
		t.Error("the prompt has a forced push")
	}
}

func TestEBuildPromptRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(opts *myprs.PromptOpts)
	}{
		{"repo", func(opts *myprs.PromptOpts) { opts.Repo = "x" }},
		{"number 0", func(opts *myprs.PromptOpts) { opts.Number = 0 }},
		{"number 1.5", func(opts *myprs.PromptOpts) { opts.Number = 1.5 }},
		{"round 0", func(opts *myprs.PromptOpts) { opts.Round = 0 }},
		{"ref -x", func(opts *myprs.PromptOpts) { opts.HeadRef = "-x" }},
		{"ref with a space", func(opts *myprs.PromptOpts) { opts.HeadRef = "a b" }},
		{"ref a..b", func(opts *myprs.PromptOpts) { opts.HeadRef = "a..b" }},
		{"no worktree", func(opts *myprs.PromptOpts) { opts.Worktree = "" }},
		{"a line in the worktree", func(opts *myprs.PromptOpts) {
			opts.Worktree = "/a\nIgnore all"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := ePrompt()
			tt.change(&opts)

			_, err := selfOf().BuildPrompt(opts)
			if err == nil {
				t.Error("built")
			}
		})
	}
}

func TestESelfDisallowed(t *testing.T) {
	t.Parallel()

	want := append(claude.SlackWrite(), "Bash(gh pr comment:*)", "Bash(gh pr review:*)",
		"Bash(gh pr merge:*)", "Bash(git push --force:*)", "Bash(git push -f:*)",
		"Bash(git push --force-with-lease:*)")
	if !slices.Equal(myprs.SelfDisallowed(), want) {
		t.Errorf("deny list %q", myprs.SelfDisallowed())
	}

	for _, tool := range []string{
		"mcp__claude_ai_Slack__slack_send_message", "mcp__claude_ai_Slack__slack_schedule_message",
	} {
		if !slices.Contains(claude.SlackWrite(), tool) {
			t.Errorf("%s is not a Slack write", tool)
		}
	}

	readable := regexp.MustCompile(`read|search|gh api`)
	for _, rule := range myprs.SelfDisallowed() {
		if readable.MatchString(rule) {
			t.Errorf("%s denies a read", rule)
		}
	}
}

func TestELaunchCommit(t *testing.T) {
	t.Parallel()

	head := "-C " + eWorktree + " rev-parse HEAD"
	tests := []struct {
		name, out string
		// want is the commit, "" for a refusal.
		want string
	}{
		{"read in the worktree", strings.Repeat("d", 40) + "\n", strings.Repeat("d", 40)},
		{"sha-256", strings.Repeat("e", 64), strings.Repeat("e", 64)},
		{"nothing", "", ""},
		{"a newline", "\n", ""},
		{"HEAD", "HEAD\n", ""},
		{"short", "abc123\n", ""},
		{"not hex", strings.Repeat("g", 40) + "\n", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := eNewRig(nil, eOut{head, tt.out})
			got := eLaunchWith(rig, eOpts())

			if tt.want != "" {
				if got.HeadSha != tt.want {
					t.Errorf("%+v", got)
				}

				return
			}

			if got.OK {
				t.Fatal("ok")
			}

			if !strings.Contains(got.Error, eWorktree) {
				t.Errorf("error %q", got.Error)
			}

			if got.ManualCommand != "" {
				t.Errorf("manual %q", got.ManualCommand)
			}

			eNothingDone(t, rig)
		})
	}

	t.Run("git fails", func(t *testing.T) {
		t.Parallel()

		rig := eNewRig(nil)
		rig.fail = head
		rig.stderr = "fatal: bad HEAD\nhint: x"

		if got := eLaunchWith(rig, eOpts()); got.Error != "fatal: bad HEAD" {
			t.Errorf("%+v", got)
		}

		eNothingDone(t, rig)
	})
}

func TestELaunchLongRefusalLeadsWithTheAdvice(t *testing.T) {
	t.Parallel()

	dataDir := "/Users/some-long-user-name/.conn"
	repoPath := "/Users/some-long-user-name/projects/acme-platform-services"
	worktree := dataDir + "/myprs/acme-corp-labs-acme-platform-services-13540a8c-1234/" +
		"acme-platform-services"
	rig := eNewRig([]string{worktree, worktree + "/.git"},
		eOut{"-C " + worktree + " rev-parse --git-common-dir", repoPath + "/.git\n"},
		eOut{"-C " + worktree + " branch --show-current", "conn-self-1234\n"},
		eOut{"-C " + worktree + " rev-parse HEAD", strings.Repeat("b", 40) + "\n"})
	got := eLaunchWith(rig, eOpts(func(opts *myprs.LaunchOpts) {
		opts.Repo = "acme-corp-labs/acme-platform-services"
		opts.Number = 1234
		opts.HeadRef = "feature/" + strings.Repeat("x", 160)
		opts.RepoPath = repoPath
		opts.DataDir = dataDir
		opts.Round = 2
		opts.Name = "self-acme-platform-services-1234-2"
	}))

	if got.OK {
		t.Fatal("ok")
	}

	if len(got.Error) <= 300 {
		t.Errorf("%d long: the case does not reach the cut", len(got.Error))
	}

	if !strings.Contains(got.Error[:300], "then press Re-review again") {
		t.Errorf("the advice is past the cut: %s", got.Error)
	}
}

func TestELaunchRefusesARefBeforeGit(t *testing.T) {
	t.Parallel()

	for _, tt := range []string{"HEAD", "refs/heads/main", "refs/remotes/origin/HEAD"} {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()

			rig := eNewRig(nil)
			got := eLaunchWith(rig, eOpts(func(opts *myprs.LaunchOpts) { opts.HeadRef = tt }))

			if got.Error != "not a safe branch name" {
				t.Errorf("%+v", got)
			}

			if len(rig.calls) != 0 {
				t.Errorf("calls %v", rig.calls)
			}
		})
	}

	got := eLaunchWith(eNewRig(nil), eOpts(func(opts *myprs.LaunchOpts) {
		opts.HeadRef = "HEAD-fix"
	}))
	if !got.OK {
		t.Errorf("HEAD-fix: %+v", got)
	}
}

func TestELaunchCannotFastForward(t *testing.T) {
	t.Parallel()

	rig := eNewRig([]string{eWorktree, eWorktree + "/.git"}, eOwn(), eOnBranch())
	rig.fail = "-C " + eWorktree + " merge"
	rig.stderr = "fatal: Not possible to fast-forward, aborting.\nhint: x"

	got := eLaunchWith(rig, eOpts())
	if got.Error != "fatal: Not possible to fast-forward, aborting." {
		t.Errorf("%+v", got)
	}

	eNothingDone(t, rig)
}

func TestELaunchLeftoverBranchWithWork(t *testing.T) {
	t.Parallel()

	count := "-C " + eRepo + " rev-list --count origin/feature/login-fix..conn-self-12"

	for _, tt := range []string{"3\n", "1", ""} {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()

			rig := eNewRig(nil, eOut{"-C " + eRepo + " branch --list", "  conn-self-12\n"},
				eOut{count, tt})
			got := eLaunchWith(rig, eOpts())
			want := "the branch conn-self-12 has work that is not on origin/feature/login-fix: " +
				"delete it (or push it, only if it belongs on the PR), then press Self-review again"

			if got.Error != want {
				t.Errorf("%+v", got)
			}

			if rig.ran("worktree add") {
				t.Error("a worktree was added")
			}

			eNothingDone(t, rig)
		})
	}
}

func TestELaunchRefusesHostileOptsBeforeAnyCall(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(opts *myprs.LaunchOpts)
	}{
		{"ref -x", func(opts *myprs.LaunchOpts) { opts.HeadRef = "-x" }},
		{"ref upload-pack", func(opts *myprs.LaunchOpts) { opts.HeadRef = "--upload-pack=x" }},
		{"ref a..b", func(opts *myprs.LaunchOpts) { opts.HeadRef = "a..b" }},
		{"ref with a space", func(opts *myprs.LaunchOpts) { opts.HeadRef = "a b" }},
		{"ref substitution", func(opts *myprs.LaunchOpts) { opts.HeadRef = "$(touch pwn)" }},
		{"no ref", func(opts *myprs.LaunchOpts) { opts.HeadRef = "" }},
		{"number 0", func(opts *myprs.LaunchOpts) { opts.Number = 0 }},
		{"number -3", func(opts *myprs.LaunchOpts) { opts.Number = -3 }},
		{"number 1.5", func(opts *myprs.LaunchOpts) { opts.Number = 1.5 }},
		{"round 0", func(opts *myprs.LaunchOpts) { opts.Round = 0 }},
		{"round 1.5", func(opts *myprs.LaunchOpts) { opts.Round = 1.5 }},
		{"name with a command", func(opts *myprs.LaunchOpts) { opts.Name = "self-web-12; id" }},
		{"another number's name", func(opts *myprs.LaunchOpts) { opts.Name = "self-web-13" }},
		{"another repo's name", func(opts *myprs.LaunchOpts) { opts.Name = "self-api-12" }},
		{"round 2's name", func(opts *myprs.LaunchOpts) { opts.Name = "self-web-12-2" }},
		{"a review's name", func(opts *myprs.LaunchOpts) { opts.Name = "pr-12" }},
		{"another name", func(opts *myprs.LaunchOpts) { opts.Name = "other" }},
		{"haiku", func(opts *myprs.LaunchOpts) { opts.Model = "haiku" }},
		{"repo", func(opts *myprs.LaunchOpts) { opts.Repo = "web" }},
		{"repo with a command", func(opts *myprs.LaunchOpts) {
			opts.Repo = "acme/web;id"
			opts.Name = "self-web;id-12"
		}},
		{"no clone", func(opts *myprs.LaunchOpts) { opts.RepoPath = "" }},
		{"clone ..", func(opts *myprs.LaunchOpts) { opts.RepoPath = "/work/code/.." }},
		{"clone /", func(opts *myprs.LaunchOpts) { opts.RepoPath = "/" }},
		{"no data folder", func(opts *myprs.LaunchOpts) { opts.DataDir = "" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := eNewRig(nil)
			got := eLaunchWith(rig, eOpts(tt.change))

			if got.OK {
				t.Fatal("ok")
			}

			if got.Error == "" {
				t.Error("no reason")
			}

			if len(rig.calls) != 0 {
				t.Errorf("calls %v", rig.calls)
			}
		})
	}
}

func TestELaunchGitFailureGivesTheFirstLine(t *testing.T) {
	t.Parallel()

	for _, tt := range []string{
		"-C " + eRepo + " fetch", "-C " + eRepo + " worktree add",
		"-C " + eRepo + " worktree prune",
	} {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()

			rig := eNewRig(nil)
			rig.fail = tt

			got := eLaunchWith(rig, eOpts())
			if got.Error != "fatal: boom" {
				t.Errorf("%+v", got)
			}

			eNothingDone(t, rig)
		})
	}
}

func TestELaunchRefusesAnotherClonesWorktree(t *testing.T) {
	t.Parallel()

	for _, tt := range []string{
		"/b/web/.git\n", "/a/web-other/.git\n", "/a/web/.git/worktrees/x\n", "\n",
	} {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()

			rig := eNewRig([]string{eWorktree, eWorktree + "/.git"},
				eOut{"-C " + eWorktree + " rev-parse --git-common-dir", tt}, eOnBranch())
			got := eLaunchWith(rig, eOpts())

			if got.OK {
				t.Fatal("ok")
			}

			for _, part := range []string{eWorktree, eRepo, "clone"} {
				if !strings.Contains(got.Error, part) {
					t.Errorf("%q is not in %q", part, got.Error)
				}
			}

			if rig.ran("merge") {
				t.Error("merged")
			}

			if rig.ran("worktree add") {
				t.Error("a worktree was added")
			}

			eNothingDone(t, rig)
		})
	}
}

func TestELaunchFolders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(opts *myprs.LaunchOpts)
		want   string
	}{
		{"acme/web", func(*myprs.LaunchOpts) {}, eWorktree},
		{"acme/api", func(opts *myprs.LaunchOpts) {
			opts.Repo = "acme/api"
			opts.Name = "self-api-12"
			opts.RepoPath = "/work/code/api"
		}, "/data/conn/myprs/acme-api-f80670ab-12/api"},
		{"org1/web", func(opts *myprs.LaunchOpts) {
			opts.Repo = "org1/web"
			opts.Number = 5
			opts.Name = "self-web-5"
			opts.RepoPath = "/a/web"
		}, "/data/conn/myprs/org1-web-9e3113bb-5/web"},
		{"Org2/web", func(opts *myprs.LaunchOpts) {
			opts.Repo = "Org2/web"
			opts.Number = 5
			opts.Name = "self-web-5"
			opts.RepoPath = "/b/web"
		}, "/data/conn/myprs/org2-web-5254ba68-5/web"},
		{"a-b/c", func(opts *myprs.LaunchOpts) {
			opts.Repo = "a-b/c"
			opts.Number = 5
			opts.Name = "self-c-5"
			opts.RepoPath = "/one/app"
		}, "/data/conn/myprs/a-b-c-1c727559-5/app"},
		{"a/b-c", func(opts *myprs.LaunchOpts) {
			opts.Repo = "a/b-c"
			opts.Number = 5
			opts.Name = "self-b-c-5"
			opts.RepoPath = "/two/app"
		}, "/data/conn/myprs/a-b-c-653231eb-5/app"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := eLaunchWith(eNewRig(nil), eOpts(tt.change))
			if got.Worktree != tt.want {
				t.Errorf("%+v", got)
			}
		})
	}

	t.Run("the case of the name does not matter", func(t *testing.T) {
		t.Parallel()

		upper, _ := myprs.Folder("/d", "Org/Web", 5)
		lower, _ := myprs.Folder("/d", "org/web", 5)

		if upper != lower {
			t.Errorf("%s and %s", upper, lower)
		}
	})

	for _, tt := range []struct {
		repo   string
		number float64
	}{{"x", 5}, {"a/b", 0}, {"a/b", 1.5}} {
		t.Run("refuses "+tt.repo, func(t *testing.T) {
			t.Parallel()

			_, err := myprs.Folder("/d", tt.repo, tt.number)
			if err == nil {
				t.Error("a folder")
			}
		})
	}
}

func TestELaunchPathWithASpaceAndAQuote(t *testing.T) {
	t.Parallel()

	repoPath := "/work/my code/it's-app"
	worktree := "/data/ca c's/myprs/acme-web-c52e5cc5-12/it's-app"
	rig := eNewRig(nil)
	got := eLaunchWith(rig, eOpts(func(opts *myprs.LaunchOpts) {
		opts.RepoPath = repoPath
		opts.DataDir = "/data/ca c's"
	}))

	if got.Worktree != worktree {
		t.Fatalf("%+v", got)
	}

	fetch := []string{
		"-C", repoPath, "fetch", "origin",
		"+refs/heads/feature/login-fix:refs/remotes/origin/feature/login-fix",
	}
	if !slices.Equal(rig.gits[0].args, fetch) {
		t.Errorf("fetch %q", rig.gits[0].args)
	}

	add := []string{
		"-C", repoPath, "worktree", "add", "-b", "conn-self-12", worktree,
		"origin/feature/login-fix",
	}
	if !slices.Equal(rig.gits[3].args, add) {
		t.Errorf("add %q", rig.gits[3].args)
	}

	if rig.opens[0][1] != worktree {
		t.Errorf("opened in %q", rig.opens[0][1])
	}
}

// eShellWorktree is a worktree whose path a shell would split or expand unquoted, holding a
// one-line prompt full of shell syntax, and the folder it is in.
func eShellWorktree(t *testing.T) (string, string, string) {
	t.Helper()

	folder := t.TempDir()
	worktree := filepath.Join(folder, "it's a $HOME `id` (wt)*")
	prompt := "Fix PR #12: \"quoted\" 'single' $HOME `id` $(id) (a) * ; | & > x"

	err := os.MkdirAll(filepath.Join(worktree, ".conn"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(worktree, ".conn", "prompt.md"), []byte(prompt+"\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return folder, worktree, prompt
}

// eShellArgs runs script with sh in dir and answers the lines printf printed.
func eShellArgs(t *testing.T, dir, script string) []string {
	t.Helper()

	command := exec.CommandContext(context.Background(), "sh", "-c", script)
	command.Dir = dir

	out, err := command.Output()
	if err != nil {
		t.Fatalf("sh: %v", err)
	}

	lines := strings.Split(string(out), "\n")

	return lines[:len(lines)-1]
}

func eExpectedArgs(prompt string) []string {
	out := append([]string{"--disallowedTools"}, myprs.SelfDisallowed()...)

	return append(out, "--name", "self-web-12", "--model", "sonnet", prompt)
}

func TestELaunchCommandAsAShellRunsIt(t *testing.T) {
	t.Parallel()

	folder, worktree, prompt := eShellWorktree(t)
	rig := eNewRig(nil)
	eLaunchWith(rig, eOpts())

	command := rig.opens[0][2]
	if !strings.HasPrefix(command, "claude ") {
		t.Fatalf("command %q", command)
	}

	args := eShellArgs(t, worktree, "printf '%s\\n' "+strings.TrimPrefix(command, "claude "))
	if !slices.Equal(args, eExpectedArgs(prompt)) {
		t.Errorf("args %q", args)
	}

	if args[len(myprs.SelfDisallowed())+1] != "--name" {
		t.Error("the rules are not closed by --name")
	}

	manual := cmux.Open("/usr/bin/false")(context.Background(), "self-web-12", worktree, command)
	if manual.OK {
		t.Fatal("false opened a workspace")
	}

	parts := strings.Split(manual.ManualCommand, " && claude ")
	if len(parts) != 2 {
		t.Fatalf("manual %q", manual.ManualCommand)
	}

	pasted := eShellArgs(t, folder,
		strings.Replace(manual.ManualCommand, " && claude ", " && printf '%s\\n' ", 1))
	if !slices.Equal(pasted, eExpectedArgs(prompt)) {
		t.Errorf("pasted %q", pasted)
	}
}

func TestELaunchOrder(t *testing.T) {
	t.Parallel()

	rig := eNewRig(nil)
	got := eLaunchWith(rig, eOpts())

	if !got.OK {
		t.Fatalf("%+v", got)
	}

	want := []string{"git", "git", "git", "git", "git", "write", "git", "write", "open"}
	if !slices.Equal(rig.calls, want) {
		t.Errorf("calls %v", rig.calls)
	}

	if strings.Join(rig.gits[4].args, " ") != "-C "+eWorktree+" rev-parse HEAD" {
		t.Errorf("git 5 %q", rig.gits[4].args)
	}

	exclude := rig.gits[5]
	if !slices.Equal(exclude.args, []string{"rev-parse", "--git-path", "info/exclude"}) {
		t.Errorf("git 6 %q", exclude.args)
	}

	if exclude.dir != eWorktree {
		t.Errorf("git 6 in %q", exclude.dir)
	}

	// The prompt is Conn's own file (written private), the exclude file the clone's.
	shared := []bool{rig.writes[0].shared, rig.writes[1].shared}
	if !slices.Equal(shared, []bool{false, true}) {
		t.Errorf("shared %v", shared)
	}

	if rig.writes[0].path != eWorktree+"/.conn/prompt.md" {
		t.Errorf("prompt at %s", rig.writes[0].path)
	}

	if rig.files[eWorktree+"/.git/info/exclude"] != ".conn/\n" {
		t.Errorf("exclude %q", rig.files[eWorktree+"/.git/info/exclude"])
	}
}

func TestELaunchTakesARelativeCommonDir(t *testing.T) {
	t.Parallel()

	rig := eNewRig([]string{eWorktree, eWorktree + "/.git"},
		eOut{
			"-C " + eWorktree + " rev-parse --git-common-dir",
			"../../../../../work/code/widget-app/.git\n",
		}, eOnBranch())
	got := eLaunchWith(rig, eOpts())
	want := myprs.LaunchResult{
		OK: true, Worktree: eWorktree, HeadSha: eSha, Error: "", ManualCommand: "",
	}

	if got != want {
		t.Errorf("%+v", got)
	}
}
