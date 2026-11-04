package reviews_test

// Helpers for the REVIEW, auto-close and sweep tests, as Bun's ready(), launched(), mine(),
// closedLaunch(), leaves() and seededLaunch() in reviews.test.ts.

import (
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/testtime"
)

const (
	// bAfterMin and bCapMin are the close delay and the close cap the world runs with.
	bAfterMin = 10
	bCapMin   = 60
	bClone    = "/nowhere/clones/widgets"
	bPid      = 501
	bPidText  = "501"
	bSession  = "s-1"
	bIdle     = "idle"
	bBusy     = "busy"
	bModel    = "sonnet"
	bStatus   = "status --porcelain"
	bGitLimit = 30 * time.Second
	bWaitStep = time.Millisecond
	bPR       = 7
)

// bName is the session name Conn gives the first review of PR number of acme/widgets.
func bName(number int64) string {
	return "pr-" + strconv.FormatInt(number, 10) + "-acme--widgets"
}

// bMine is the launch's own session: idle in PR number's worktree under dir, with what the test
// changes.
func bMine(dir string, number int64, change func(session *sessions.LiveSession),
) sessions.LiveSession {
	session := sessions.LiveSession{
		Name: bName(number), Cwd: worktreeOf(dir, worldRepo, number), Status: bIdle,
		SessionID: bSession, PID: bPid,
	}

	if change != nil {
		change(&session)
	}

	return session
}

// bListed is PR number, asking me.
func bListed(number int64) *reviews.PR {
	return prOf(func(pr *reviews.PR) { pr.Number, pr.Requested = number, []string{worldMe} })
}

// bReady is Reviews with its rows built (PR 7 listed unless opts say otherwise), and no live
// session.
func bReady(t *testing.T, opts setupOpts) (*reviews.Reviews, *world) {
	t.Helper()

	if opts.open == nil {
		opts.open = []*reviews.PR{bListed(bPR)}
	}

	tab, fakes := setup(t, opts)
	tickAndSettle(tab)

	return tab, fakes
}

// bLaunchOpts are what bLaunched starts from.
type bLaunchOpts struct {
	// number is the PR (0: 7).
	number int64
	// sessions are the live sessions after the launch (nil: the launch's own, idle).
	sessions func(dir string) []sessions.LiveSession
}

// bHarness is a PR launched at worldT0, the clock moved with at(minutes since the launch).
type bHarness struct {
	t      *testing.T
	tab    *reviews.Reviews
	fakes  *world
	number int64
}

// bLaunched is PR 7 (or opts.number) REVIEWed at worldT0 while no session is live; the session
// shows up afterwards.
func bLaunched(t *testing.T, opts bLaunchOpts) *bHarness {
	t.Helper()

	number := opts.number
	if number == 0 {
		number = bPR
	}

	tab, fakes := bReady(t, setupOpts{open: []*reviews.PR{bListed(number)}})
	harness := &bHarness{t: t, tab: tab, fakes: fakes, number: number}

	reply := harness.start(bModel)
	if reply.Code != 200 {
		t.Fatalf("the launch answered %d %s", reply.Code, reply.Body)
	}

	live := []sessions.LiveSession{bMine(fakes.dir, number, nil)}
	if opts.sessions != nil {
		live = opts.sessions(fakes.dir)
	}

	fakes.with(func(fakes *world) { fakes.sessions = live })

	return harness
}

// start is REVIEW pressed for the harness's PR.
func (run *bHarness) start(model string) reviews.Reply {
	run.t.Helper()

	return bStart(run.t, run.tab, strconv.FormatInt(run.number, 10), model)
}

// bStart is REVIEW pressed for PR number of acme/widgets.
func bStart(t *testing.T, tab *reviews.Reviews, number, model string) reviews.Reply {
	t.Helper()

	reply, err := tab.Start(t.Context(), worldRepo, number, model)
	if err != nil {
		t.Fatal(err)
	}

	return reply
}

// at moves the clock to min minutes after the launch and ticks once.
func (run *bHarness) at(minutes float64) {
	run.fakes.with(func(fakes *world) { fakes.now = worldT0 + minutes*minuteMs })
	run.tab.Tick()
	run.tab.Settle()
}

// review is my review landing on GitHub min minutes after the launch.
func (run *bHarness) review(minutes float64) {
	run.t.Helper()

	number := run.number

	run.fakes.with(func(fakes *world) {
		fakes.open = []*reviews.PR{prOf(func(pr *reviews.PR) {
			pr.Number, pr.Requested = number, []string{worldMe}
			pr.Reviews = []reviews.Review{myReview(func(review *reviews.Review) {
				review.SubmittedAt = worldT0 + minutes*minuteMs
			})}
		})}
	})
	run.refresh()
	run.at(minutes)
}

// refresh is REFRESH pressed.
func (run *bHarness) refresh() {
	run.t.Helper()

	_, err := run.tab.Refresh()
	if err != nil {
		run.t.Fatal(err)
	}
}

// launch is the PR's first saved launch.
func (run *bHarness) launch() *reviews.Launch {
	run.t.Helper()

	launches := bLaunches(run.fakes.file, run.number)
	if len(launches) == 0 {
		run.t.Fatal("no launch is saved")
	}

	return launches[0]
}

// bLaunches are the saved launches of PR number of acme/widgets.
func bLaunches(file string, number int64) []*reviews.Launch {
	entry, found := reviews.LoadFile(file).PRs.Get(keyOf(number))
	if !found {
		return nil
	}

	return entry.Launches
}

// bInputs, bCloses and bGits are the screen reads, closes and git calls so far.
func bInputs(fakes *world) []string {
	fakes.mu.Lock()
	defer fakes.mu.Unlock()

	return append([]string{}, fakes.inputCalls...)
}

func bCloses(fakes *world) []string {
	fakes.mu.Lock()
	defer fakes.mu.Unlock()

	return append([]string{}, fakes.closeCalls...)
}

// bGits are the git calls so far, each as "<args> @<folder> <timeout>".
func bGits(fakes *world) []string {
	fakes.mu.Lock()
	defer fakes.mu.Unlock()

	out := make([]string, 0, len(fakes.git))
	for _, call := range fakes.git {
		out = append(out, bGitText(call.args, call.dir, call.timeout))
	}

	return out
}

func bGitText(args []string, dir string, timeout time.Duration) string {
	return strings.Join(args, " ") + " @" + dir + " " + timeout.String()
}

// bStatusOf and bRemoveOf are the git calls a sweep makes for a worktree.
func bStatusOf(worktree string) string {
	return bGitText([]string{"status", "--porcelain"}, worktree, bGitLimit)
}

func bRemoveOf(worktree string) string {
	return bGitText([]string{"worktree", "remove", worktree}, bClone, bGitLimit)
}

// bClosedLaunch is PR 7 launched, its session gone and the launch closed; the PR is still open.
func bClosedLaunch(t *testing.T) *bHarness {
	t.Helper()

	run := bLaunched(t, bLaunchOpts{number: 0, sessions: nil})
	run.at(1)
	run.fakes.with(func(fakes *world) { fakes.sessions = nil })
	run.at(2)

	if run.launch().ClosedAt == nil {
		t.Fatal("the launch is not closed")
	}

	return run
}

// leaves is the PR ending (merged) before the next poll, which reads it by number.
func (run *bHarness) leaves(minutes float64) {
	run.t.Helper()

	run.fakes.with(func(fakes *world) { fakes.open = []*reviews.PR{} })
	run.refresh()
	run.at(minutes)
	run.tab.Settle()
}

// bSeed writes a reviews.json holding one closed launch of PR 9 at worktree, as a
// run before a restart left it.
func bSeed(t *testing.T, file, worktree string) {
	t.Helper()

	loaded := reviews.LoadFile(file)
	session := "s"
	closedAt := float64(worldT0 - 1_800_000)
	loaded.PRs.Set(keyOf(bSeeded), &reviews.Entry{
		FirstSeenAt: worldT0 - hourMs, Requested: true, SeenAt: worldT0 - hourMs,
		Launches: []*reviews.Launch{{
			At: worldT0 - hourMs, Model: bModel, Name: "pr-9",
			HeadSha: "aaa", Worktree: worktree, SessionID: &session, ClosedAt: &closedAt,
		}},
		Scan: reviews.Scan{Tries: 0, LastAt: 0}, ThreadScan: reviews.ThreadScan{HeadSha: ""},
	})

	err := reviews.SaveFile(file, loaded, nil)
	if err != nil {
		t.Fatal(err)
	}
}

// bOldWorktree is where a launch put PR number's worktree before Reviews read several repos.
func bOldWorktree(dir string, number int64) string {
	return filepath.Join(dir, "reviews", "pr-"+strconv.FormatInt(number, 10), "widgets")
}

// bLog is a log Reviews writes to, safe to read while it does.
type bLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *bLog) Write(data []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.lines = append(l.lines, string(data))

	return len(data), nil
}

// count is how many lines say what.
func (l *bLog) count(what string) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	found := 0

	for _, line := range l.lines {
		if strings.Contains(line, what) {
			found++
		}
	}

	return found
}

func (l *bLog) total() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.lines)
}

// bLogTo makes Reviews log to a bLog.
func bLogTo(tab *reviews.Reviews) *bLog {
	logged := &bLog{mu: sync.Mutex{}, lines: nil}

	tab.SetDeps(func(deps *reviews.Deps) { deps.Logger = log.New(logged, "", 0) })

	return logged
}

// bWaitFor waits until done says so, failing the test after a while.
func bWaitFor(t *testing.T, what string, done func() bool) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(gateWait))
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("waited too long for %s", what)
		}

		time.Sleep(bWaitStep)
	}
}

// bPolledAt is when the last poll finished (0 before the first).
func bPolledAt(t *testing.T, tab *reviews.Reviews) float64 {
	t.Helper()

	at := view(t, tab).LastPollAt
	if at == nil {
		return 0
	}

	return *at
}

// bNewDry is a dry Reviews (CONN_REVIEWS_DRY) over the world's fakes.
func bNewDry(t *testing.T, fakes *world) *reviews.Reviews {
	t.Helper()

	return reviews.New(t.Context(), reviews.Deps{
		Gh: worldGh{w: fakes}, Scan: fakes.scan, DMScan: fakes.dmScan, File: fakes.file,
		Settings: fakes.settings, CloneFor: fakes.cloneFor, OriginOf: fakes.origin,
		SeenRepos: func() []string { return nil }, HasClients: fakes.hasClients,
		Sessions: fakes.liveSessions, Bump: fakes.bump, Now: fakes.clock, Launch: fakes.launch,
		Git: fakes.runGit, InputState: fakes.input, CloseWorkspace: fakes.closeWorkspace,
		Exists: fakes.isThere, WorktreeClone: fakes.worktreeClone, Record: fakes.record,
		Budget: nil,
		Logger: nil,
		Config: reviews.Config{
			PollMs: pollMs, FirstWindowDays: 2, DMScanMs: 600_000, ChannelScanMs: 1e12,
			CloseAfterMs: bAfterMin * minuteMs, CloseCapMs: bCapMin * minuteMs, Me: "", Dry: true,
			NamePattern: names().Pattern,
		},
	})
}
