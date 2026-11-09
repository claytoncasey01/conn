package myprs_test

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// The start tests: Self-Review and Re-Review.

const aMinuteMs = 60_000

var (
	errANotARepo   = errors.New("fatal: not a git repository")
	errABadGateway = errors.New("gh: HTTP 502 \u202ebad gateway")
)

// aStart is a press of the round button.
func aStart(poller *myprs.MyPrs, repo, number, model any) reviews.Reply {
	return poller.Start(context.Background(), repo, number, model, nil)
}

// aStart7 is a press for org/repo#7 with sonnet.
func aStart7(poller *myprs.MyPrs) reviews.Reply {
	return aStart(poller, "org/repo", 7.0, "sonnet")
}

// aError is the error a reply gives, "" when it gives none.
func aError(reply reviews.Reply) string {
	if reply.Body == nil {
		return ""
	}

	value, _ := reply.Body.Get("error")
	text, _ := value.(string)

	return text
}

// aLaunched are the launches the world has seen.
func aLaunched(world *World) []myprs.LaunchOpts {
	var launched []myprs.LaunchOpts

	world.Change(func(w *World) { launched = slices.Clone(w.Launched) })

	return launched
}

// aMatched are the origin checks the world has seen, as "repo path".
func aMatched(world *World) []string {
	var matched []string

	world.Change(func(w *World) { matched = slices.Clone(w.Matched) })

	return matched
}

// aNothingCalled checks that no origin was read and nothing launched.
func aNothingCalled(t *testing.T, world *World) {
	t.Helper()

	if launched := aLaunched(world); len(launched) != 0 {
		t.Errorf("launched = %v, want none", launched)
	}

	if matched := aMatched(world); len(matched) != 0 {
		t.Errorf("matched = %v, want none", matched)
	}
}

// aLaunches are the launches of org/repo#7 in the file on disk, as JSON.
func aLaunches(t *testing.T, file string) string {
	t.Helper()

	entry, found := myprs.LoadFile(file).PRs.Get(myprs.EntryKey("org/repo", 7))
	if !found {
		t.Fatal("org/repo#7 is not in the file")
	}

	return string(js.Marshal(entry.Launches))
}

// aLive makes the live sessions one busy session named self-repo-7, in cwd.
func aLive(world *World, cwd string) {
	world.Change(func(w *World) {
		w.Sessions = []sessions.LiveSession{
			{Name: "self-repo-7", Cwd: cwd, Status: "busy", SessionID: "s1", PID: 1},
		}
	})
}

// aPollOnce moves the clock on and polls at once.
func aPollOnce(world *World, poller *myprs.MyPrs, advanceMs float64) {
	world.Advance(advanceMs)
	poller.Refresh()
	tick(poller)
}

// aCheckReply checks a reply's code and that its error matches pattern.
func aCheckReply(t *testing.T, reply reviews.Reply, code int, pattern string) {
	t.Helper()

	if reply.Code != code {
		t.Errorf("code = %d (%s), want %d", reply.Code, aError(reply), code)
	}

	if !regexp.MustCompile(pattern).MatchString(aError(reply)) {
		t.Errorf("error = %q, want one matching %s", aError(reply), pattern)
	}
}

func TestAStartRefusesBadInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		repo, number, model any
	}{
		{name: "no repo", repo: nil, number: 7.0, model: "sonnet"},
		{name: "a number repo", repo: 7.0, number: 7.0, model: "sonnet"},
		{name: "an empty repo", repo: "", number: 7.0, model: "sonnet"},
		{name: "an owner alone", repo: "org", number: 7.0, model: "sonnet"},
		{name: "a repo with a number", repo: "org/repo#7", number: 7.0, model: "sonnet"},
		{name: "a path", repo: "../x/y", number: 7.0, model: "sonnet"},
		{name: "a space", repo: "org/re po", number: 7.0, model: "sonnet"},
		{name: "a text number", repo: "org/repo", number: "7", model: "sonnet"},
		{name: "zero", repo: "org/repo", number: 0.0, model: "sonnet"},
		{name: "negative", repo: "org/repo", number: -1.0, model: "sonnet"},
		{name: "a fraction", repo: "org/repo", number: 1.5, model: "sonnet"},
		{name: "NaN", repo: "org/repo", number: math.NaN(), model: "sonnet"},
		{name: "no number", repo: "org/repo", number: nil, model: "sonnet"},
		{name: "no model", repo: "org/repo", number: 7.0, model: nil},
		{name: "haiku", repo: "org/repo", number: 7.0, model: "haiku"},
		{name: "an empty model", repo: "org/repo", number: 7.0, model: ""},
		{name: "Opus", repo: "org/repo", number: 7.0, model: "Opus"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world, poller, _ := aPolled(t, pr())
			aCheckReply(t, aStart(poller, tt.repo, tt.number, tt.model), 400, `.`)
			aNothingCalled(t, world)
		})
	}
}

func TestAStartRefusesAPRNotInTheList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		repo   string
		number float64
	}{
		{repo: "org/repo", number: 8},
		{repo: "org/other", number: 7},
	}

	for _, tt := range tests {
		t.Run(myprs.EntryKey(tt.repo, tt.number), func(t *testing.T) {
			t.Parallel()

			world, poller, _ := aPolled(t, pr())
			aCheckReply(t, aStart(poller, tt.repo, tt.number, "sonnet"), 404, `.`)
			aNothingCalled(t, world)
		})
	}
}

func TestAStartBeforeTheFirstPollIs404(t *testing.T) {
	t.Parallel()

	world := newWorld(t, pr())
	poller, _ := world.New()

	aCheckReply(t, aStart7(poller), 404, `.`)
	aNothingCalled(t, world)
}

func TestAStartRefusesBeforeTheOriginIsRead(t *testing.T) {
	t.Parallel()

	head := func(headRef, defaultBranch string) func(p *myprs.PR) {
		return func(p *myprs.PR) {
			p.HeadRef = headRef
			p.DefaultBranch = &defaultBranch
		}
	}

	tests := []struct {
		name   string
		change func(p *myprs.PR)
		clones map[string]string
		want   string
	}{
		{
			name: "a fork", change: func(p *myprs.PR) { p.Fork = true }, clones: nil,
			want: `(?i)fork`,
		},
		{
			name: "no clone", change: func(*myprs.PR) {}, clones: map[string]string{},
			want: `No local clone of org/repo: set a folder in Settings`,
		},
		{name: "develop", change: head("develop", "main"), clones: nil, want: `develop`},
		{name: "main", change: head("main", "main"), clones: nil, want: `main`},
		{name: "trunk", change: head("trunk", "trunk"), clones: nil, want: `trunk`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := newWorld(t, pr(tt.change))
			if tt.clones != nil {
				world.Clones = tt.clones
			}

			poller, _ := world.New()
			tick(poller)

			aCheckReply(t, aStart7(poller), 409, tt.want)
			aNothingCalled(t, world)
		})
	}
}

func TestAStartRefusesAWrongOrUnreadableOrigin(t *testing.T) {
	t.Parallel()

	world, poller, file := aPolled(t, pr())
	world.Change(func(w *World) { w.OriginOK = false })
	aCheckReply(t, aStart7(poller), 409, `origin`)

	if matched := aMatched(world); !slices.Equal(matched, []string{"org/repo /clone"}) {
		t.Errorf("matched = %v, want org/repo /clone", matched)
	}

	world.Change(func(w *World) { w.OriginErr = errANotARepo })
	aCheckReply(t, aStart7(poller), 409, `not a git repository`)

	if launched := aLaunched(world); len(launched) != 0 {
		t.Errorf("launched = %v, want none", launched)
	}

	if got := aLaunches(t, file); got != "[]" {
		t.Errorf("launches = %s, want []", got)
	}

	// A refusal records nothing and starts no lock: the next press goes through once the
	// origin is right.
	world.Change(func(w *World) {
		w.OriginOK = true
		w.OriginErr = nil
	})

	if code := aStart7(poller).Code; code != 200 {
		t.Errorf("code = %d once the origin is right, want 200", code)
	}
}

func TestAPressLaunchesRoundAfterRound(t *testing.T) {
	t.Parallel()

	world, poller, file := aPolled(t, pr(func(p *myprs.PR) { p.HeadSha = "aaa111" }))
	first := aStart(poller, "org/repo", 7.0, "opus")

	if got := js.Stringify(first.Body); first.Code != 200 {
		t.Errorf("code = %d (%s), want 200", first.Code, got)
	}

	if got := js.Stringify(first.Body); got != `{"ok":true,"name":"self-repo-7"}` {
		t.Errorf("body = %s, want ok, self-repo-7", got)
	}

	want := myprs.LaunchOpts{
		Repo: "org/repo", Number: 7, HeadRef: "feature/a", Model: "opus",
		Advisor: "claude-code", Name: "self-repo-7", RepoPath: "/clone", DataDir: "", Round: 1,
	}
	if launched := aLaunched(world); !slices.Equal(launched, []myprs.LaunchOpts{want}) {
		t.Errorf("launched = %+v, want %+v", launched, want)
	}

	if matched := aMatched(world); !slices.Equal(matched, []string{"org/repo /clone"}) {
		t.Errorf("matched = %v, want org/repo /clone", matched)
	}

	wantSaved := `[{"at":1800000000000,"model":"opus","name":"self-repo-7",` +
		`"worktree":"/wt/self-repo-7","headSha":"aaa111","round":1,"armed":true,` +
		`"askedSha":null,"askedAt":null,"closedAt":null,"askTry":null}]`
	if got := aLaunches(t, file); got != wantSaved {
		t.Errorf("launches = %s, want %s", got, wantSaved)
	}

	world.Advance(6 * aMinuteMs)
	aPressRoundTwo(t, world, poller, file)
}

func aPressRoundTwo(t *testing.T, world *World, poller *myprs.MyPrs, file string) {
	t.Helper()

	second := aStart7(poller)
	if got := js.Stringify(second.Body); got != `{"ok":true,"name":"self-repo-7-2"}` {
		t.Errorf("second body = %s, want ok, self-repo-7-2", got)
	}

	launched := aLaunched(world)
	if len(launched) != 2 {
		t.Fatalf("launched = %d, want 2", len(launched))
	}

	got := []any{launched[1].Name, launched[1].Round, launched[1].Model}
	if !slices.Equal(got, []any{"self-repo-7-2", 2.0, "sonnet"}) {
		t.Errorf("second launch = %v, want self-repo-7-2 2 sonnet", got)
	}

	// The newer press disarms the older round: only the newest is ever asked for.
	entry, _ := myprs.LoadFile(file).PRs.Get(myprs.EntryKey("org/repo", 7))
	rounds := make([]any, 0, 3*len(entry.Launches))

	for _, launch := range entry.Launches {
		rounds = append(rounds, launch.Round, launch.Armed, launch.At)
	}

	want := []any{1.0, false, float64(worldT0), 2.0, true, float64(worldT0 + 6*aMinuteMs)}
	if !slices.Equal(rounds, want) {
		t.Errorf("rounds = %v, want %v", rounds, want)
	}

	world.Change(func(w *World) {
		if w.Bumps == 0 {
			t.Error("no bump")
		}
	})
}

func TestALaunchRecordsTheWorktreesCommit(t *testing.T) {
	t.Parallel()

	// A push made since the last poll: the worktree is at it, and the list still says the
	// earlier head.
	world, poller, file := aPolled(t, pr(func(p *myprs.PR) { p.HeadSha = "aaa111" }))
	world.Change(func(w *World) {
		w.LaunchResult = func(myprs.LaunchOpts) myprs.LaunchResult {
			return myprs.LaunchResult{
				OK: true, Worktree: "/wt/self-repo-7", HeadSha: "bbb222", Error: "",
				ManualCommand: "",
			}
		}
	})

	if code := aStart7(poller).Code; code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}

	entry, _ := myprs.LoadFile(file).PRs.Get(myprs.EntryKey("org/repo", 7))
	if got := entry.Launches[0].HeadSha; got != "bbb222" {
		t.Errorf("headSha = %s, want bbb222", got)
	}
}

func TestADraftPRCanStart(t *testing.T) {
	t.Parallel()

	world, poller, _ := aPolled(t, pr(func(p *myprs.PR) { p.Draft = true }))

	if code := aStart7(poller).Code; code != 200 {
		t.Errorf("code = %d, want 200", code)
	}

	if launched := aLaunched(world); len(launched) != 1 {
		t.Errorf("launched = %d, want 1", len(launched))
	}
}

func TestATwoPressesWithin30SecondsLaunchOnce(t *testing.T) {
	t.Parallel()

	world, poller, _ := aPolled(t, pr())
	if code := aStart7(poller).Code; code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}

	world.Advance(29_999)
	aCheckReply(t, aStart7(poller), 409, `just started`)

	if launched := aLaunched(world); len(launched) != 1 {
		t.Errorf("launched = %d, want 1", len(launched))
	}
}

func TestATwoPressesAtOnceLaunchOnce(t *testing.T) {
	t.Parallel()

	world, poller, _ := aPolled(t, pr())
	entered := make(chan struct{})
	gate := make(chan struct{})

	var calls atomic.Int32

	// The first launch waits at the gate, so the second press comes while it is in flight and
	// before any round is recorded: only the in-flight lock can refuse it.
	world.Change(func(w *World) {
		w.LaunchResult = func(opts myprs.LaunchOpts) myprs.LaunchResult {
			if calls.Add(1) == 1 {
				close(entered)
				<-gate
			}

			return myprs.LaunchResult{
				OK: true, Worktree: "/wt/" + opts.Name, HeadSha: "abc", Error: "",
				ManualCommand: "",
			}
		}
	})

	first := make(chan int, 1)

	go func() { first <- aStart7(poller).Code }()

	select {
	case <-entered:
	case <-time.After(testtime.Scaled(5 * time.Second)):
		t.Fatal("the first press did not launch")
	}

	if code := aStart7(poller).Code; code != 409 {
		t.Errorf("code of the press in flight = %d, want 409", code)
	}

	close(gate)

	if code := <-first; code != 200 {
		t.Errorf("code of the first press = %d, want 200", code)
	}

	if launched := aLaunched(world); len(launched) != 1 {
		t.Errorf("launched = %d, want 1", len(launched))
	}
}

func TestALaunchNotYetASessionBlocksFor5Minutes(t *testing.T) {
	t.Parallel()

	world, poller, _ := aPolled(t, pr())
	aStart7(poller)
	world.Advance(5*aMinuteMs - 1)
	aCheckReply(t, aStart7(poller), 409, `has not appeared`)

	if launched := aLaunched(world); len(launched) != 1 {
		t.Errorf("launched = %d, want 1", len(launched))
	}

	world.Advance(1)

	if code := aStart7(poller).Code; code != 200 {
		t.Errorf("code after 5 minutes = %d, want 200", code)
	}

	if launched := aLaunched(world); len(launched) != 2 {
		t.Errorf("launched = %d, want 2", len(launched))
	}
}

func TestAEndedRoundWaitsFor30SecondsAndThePoll(t *testing.T) {
	t.Parallel()

	world, poller, _ := aPolled(t, pr())
	aStart7(poller)
	world.Advance(5_000)
	aLive(world, "/wt/self-repo-7")
	tick(poller)
	world.Change(func(w *World) { w.Sessions = nil })
	world.Advance(20_000)
	aCheckReply(t, aStart7(poller), 409, `just started`)
	world.Advance(6_000)

	// Past the 30 s, but the poll has not settled the round whose session was seen: its push,
	// if it made one, is still to be asked for.
	if code := aStart7(poller).Code; code != 409 {
		t.Errorf("code before the poll = %d, want 409", code)
	}

	aPollOnce(world, poller, 2*aMinuteMs)

	next := aStart7(poller)
	if got := js.Stringify(next.Body); got != `{"ok":true,"name":"self-repo-7-2"}` {
		t.Errorf("body after the poll = %s (%d), want ok, self-repo-7-2", got, next.Code)
	}

	if launched := aLaunched(world); len(launched) != 2 {
		t.Errorf("launched = %d, want 2", len(launched))
	}
}

func TestALiveSessionOfTheLaunchBlocksAPress(t *testing.T) {
	t.Parallel()

	world, poller, _ := aPolled(t, pr())
	aStart7(poller)
	world.Advance(10 * aMinuteMs)
	aLive(world, "/wt/self-repo-7")
	aCheckReply(t, aStart7(poller), 409, `self-repo-7`)

	if launched := aLaunched(world); len(launched) != 1 {
		t.Errorf("launched = %d, want 1", len(launched))
	}

	// Gone: the poll settles the round, then the press goes through.
	world.Change(func(w *World) { w.Sessions = nil })
	aPollOnce(world, poller, 2*aMinuteMs)

	if code := aStart7(poller).Code; code != 200 {
		t.Errorf("code once the session is gone = %d, want 200", code)
	}
}

func TestASessionMustMatchNameAndFolder(t *testing.T) {
	t.Parallel()

	world, poller, _ := aPolled(t, pr())
	aStart7(poller)
	world.Advance(10 * aMinuteMs)
	world.Change(func(w *World) {
		w.Sessions = []sessions.LiveSession{
			{Name: "self-repo-7", Cwd: "/somewhere/else", Status: "busy", SessionID: "s1", PID: 1},
			{Name: "other", Cwd: "/wt/self-repo-7", Status: "busy", SessionID: "s2", PID: 2},
		}
	})

	if code := aStart7(poller).Code; code != 200 {
		t.Errorf("code = %d, want 200", code)
	}
}

func TestASessionInTheWorktreeThroughASymlinkIsThePRs(t *testing.T) {
	t.Parallel()

	folder := t.TempDir()
	realWorktree := filepath.Join(folder, "real-data", "wt")
	link := filepath.Join(folder, "linked-data")
	aMakeLinkedWorktree(t, folder, realWorktree, link)

	world, poller, _ := aPolled(t, pr())
	// Conn recorded the worktree under the linked data folder; the registry has the real path.
	world.Change(func(w *World) {
		w.LaunchResult = func(myprs.LaunchOpts) myprs.LaunchResult {
			return myprs.LaunchResult{
				OK: true, Worktree: filepath.Join(link, "wt"), HeadSha: "abc", Error: "",
				ManualCommand: "",
			}
		}
	})

	if code := aStart7(poller).Code; code != 200 {
		t.Fatalf("code = %d, want 200", code)
	}

	world.Advance(10 * aMinuteMs)

	for _, cwd := range []string{realWorktree, filepath.Join(realWorktree, "sub")} {
		aLive(world, cwd)
		aCheckReply(t, aStart7(poller), 409, `self-repo-7`)

		if status := aRow(t, poller).Status; status != "running" {
			t.Errorf("status in %s = %s, want running", cwd, status)
		}
	}

	// A folder beside it is not.
	aLive(world, filepath.Join(folder, "real-data"))

	if status := aRow(t, poller).Status; status != "idle" {
		t.Errorf("status beside the worktree = %s, want idle", status)
	}

	if launched := aLaunched(world); len(launched) != 1 {
		t.Errorf("launched = %d, want 1", len(launched))
	}
}

func aMakeLinkedWorktree(t *testing.T, folder, realWorktree, link string) {
	t.Helper()

	err := os.MkdirAll(filepath.Join(realWorktree, "sub"), 0o700)
	if err != nil {
		t.Fatalf("making the worktree: %v", err)
	}

	err = os.Symlink(filepath.Join(folder, "real-data"), link)
	if err != nil {
		t.Fatalf("linking the data folder: %v", err)
	}
}

func TestAStartIsRefusedWhileTheListCannotBeRefreshed(t *testing.T) {
	t.Parallel()

	world, poller, _ := aPolled(t, pr())
	aSetSource(world, aFailing(errABadGateway))
	poller.Refresh()
	tick(poller)

	refused := aStart7(poller)
	if refused.Code != 409 {
		t.Errorf("code = %d, want 409", refused.Code)
	}

	want := "the PR list could not be refreshed: gh: HTTP 502 bad gateway"
	if got := aError(refused); got != want {
		t.Errorf("error = %q, want %q", got, want)
	}

	aNothingCalled(t, world)

	// A good poll lets it through.
	world.SetList(pr())
	poller.Refresh()
	tick(poller)

	if code := aStart7(poller).Code; code != 200 {
		t.Errorf("code after a good poll = %d, want 200", code)
	}

	if strings.Contains(aError(refused), "\u202e") {
		t.Error("the refusal holds the error's bidi character")
	}
}
