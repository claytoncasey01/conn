package reviews_test

// The world the Reviews orchestrator tests run in, as Bun's reviews.test.ts setup(): every
// dependency is a fake (gh, the scans, the clock, the sessions, the settings, a temp file), so
// nothing here reaches the real gh, claude, Slack or ~/.conn. Every fake is safe to call from
// the goroutines Reviews starts; a test changes the world through its methods.

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
	"github.com/claytoncasey01/conn/internal/testtime"
)

const (
	worldT0   = 1_800_000_000_000
	minuteMs  = 60_000
	hourMs    = 3_600_000
	worldMe   = "jane"
	worldRepo = "acme/widgets"
	pollMs    = 180_000
	gateWait  = 5 * time.Second
	gateDepth = 64
)

// errGhHiccup is a read by number that fails once, as gh can.
var errGhHiccup = errors.New("gh hiccup")

// keyOf is the key of PR n of acme/widgets (or of repo).
func keyOf(number int64, repo ...string) string {
	name := worldRepo
	if len(repo) > 0 {
		name = repo[0]
	}

	return strings.ToLower(name) + "#" + strconv.FormatInt(number, 10)
}

// prOf is a PR as GitHub reports it, with what the test changes.
func prOf(change func(pr *reviews.PR)) *reviews.PR {
	pull := &reviews.PR{
		Repo: worldRepo, Number: 7, Title: "Add a thing", Author: "sam", AuthorIsBot: false,
		URL: "https://github.com/acme/widgets/pull/7", BaseRef: "development", HeadSha: "aaa",
		IsDraft: false, CreatedAt: worldT0 - 5*hourMs, UpdatedAt: worldT0 - hourMs,
		Checks: "passing", Requested: []string{}, Reviews: []reviews.Review{}, Ended: nil,
		EndedAt: nil,
	}

	if change != nil {
		change(pull)
	}

	return pull
}

// myReview is a review of mine, with what the test changes.
func myReview(change func(review *reviews.Review)) reviews.Review {
	review := reviews.Review{
		Author: worldMe, State: "COMMENTED", Commit: "aaa", SubmittedAt: worldT0 - 3*hourMs,
	}

	if change != nil {
		change(&review)
	}

	return review
}

// settingsOpts are the settings a test runs with: acme/* allowed, GitHub and Slack on with one
// channel and the DM scan, "Requests me" on.
type settingsOpts struct {
	allow     []string
	setup     bool
	github    *bool
	slack     *bool
	channels  []string
	dms       *bool
	requested *bool
	allOpen   bool
}

func settingsOf(opts settingsOpts) settings.Settings {
	current := settings.Defaults()
	current.Repos.Allow = []string{"acme/*"}

	if opts.allow != nil {
		current.Repos.Allow = opts.allow
	}

	if opts.setup {
		current.Repos.Allow = nil
	}

	current.GitHub.Enabled = trueUnless(opts.github)
	current.Slack.Enabled = trueUnless(opts.slack)
	current.Slack.Channels = []string{"#code-review"}

	if opts.channels != nil {
		current.Slack.Channels = opts.channels
	}

	current.Slack.DMs = trueUnless(opts.dms)
	current.Reviews.Requested = trueUnless(opts.requested)
	current.Reviews.AllOpen = opts.allOpen

	return current
}

// trueUnless is the setting a test left out (nil) as on, else what the test set.
func trueUnless(value *bool) bool {
	if value == nil {
		return true
	}

	return *value
}

// fakeSettings holds the settings; set replaces them, as a saved update does.
type fakeSettings struct {
	mu      sync.Mutex
	current settings.Settings
	version uint64
}

func (f *fakeSettings) Current() (settings.Settings, uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.current.Clone(), f.version
}

func (f *fakeSettings) Setup() bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.current.Repos.Allow) == 0
}

func (f *fakeSettings) set(current settings.Settings) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.current = current
	f.version++
}

// gate holds a fake until the test opens it, and says when a call reached it.
type gate struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGate() *gate {
	return &gate{
		entered: make(chan struct{}, gateDepth), release: make(chan struct{}), once: sync.Once{},
	}
}

// pass is a fake reaching the gate: it waits there until the gate opens.
func (g *gate) pass() {
	if g == nil {
		return
	}

	g.entered <- struct{}{}

	<-g.release
}

// reached waits until a call has reached the gate.
func (g *gate) reached(t *testing.T) {
	t.Helper()

	select {
	case <-g.entered:
	case <-time.After(testtime.Scaled(gateWait)):
		t.Fatal("nothing reached the gate")
	}
}

// open lets every call through, now and later.
func (g *gate) open() { g.once.Do(func() { close(g.release) }) }

// gitCall is one git call.
type gitCall struct {
	args    []string
	dir     string
	timeout time.Duration
}

// calls are what Reviews asked of the fakes.
type calls struct {
	login  int
	list   int
	search [][]string
	fetch  [][]string
	ended  []int64
	scan   []reviews.ScanInput
	dm     []dmCall
	bump   int
	clone  []string
}

// dmCall is one DM scan.
type dmCall struct {
	allow []string
	since string
}

// world is the fakes' state. Every field is read and written holding mu.
type world struct {
	mu       sync.Mutex
	settings *fakeSettings
	// open is what GitHub has open now.
	open     []*reviews.PR
	clients  bool
	now      float64
	sessions []sessions.LiveSession
	// listErr fails every search while it is set.
	listErr error
	// searchError is why GitHub cannot run one search (by its query), "" when it can.
	searchError func(q string) string
	openCapped  bool
	// rate and fetchRate are what one call of searches, and of reads by number, cost.
	rate, fetchRate *ghbudget.RateInfo
	// listGate holds every search; endedGate every read by number; closeGate every close.
	listGate, endedGate, closeGate *gate
	scanResult                     func() (reviews.ScanResult, error)
	dmResult                       func() ([]reviews.DMFind, error)
	// endedAs is how a PR that is not open any more ended, when it is read by number (nil: it
	// reads as it was).
	endedAs *string
	endedAt float64
	// gone are keys GitHub has no PR for; readFails keys whose read is left out.
	gone, readFails map[string]bool
	// endedFailures is how many reads by number fail before one answers.
	endedFailures int
	calls         calls
	// records are what Reviews told the Settings cards, as "<module> ok|failing: <detail>".
	records      []string
	launches     []reviews.LaunchOpts
	launchResult func(opts reviews.LaunchOpts) reviews.LaunchResult
	clone        func(repo string) string
	inputState   string
	onInputState func()
	inputCalls   []string
	closeCalls   []string
	closeResult  func() (int, string)
	git          []gitCall
	onGit        func(args []string)
	porcelain    string
	porcelainOf  map[string]string
	exists       func(path string) bool
	originOf     func(path string) string
	worktreeOf   func(path string) string
	// known is every PR GitHub has had open in this test, by key.
	known map[string]*reviews.PR
	dir   string
	file  string
}

// setupOpts are what a test starts from.
type setupOpts struct {
	open     []*reviews.PR
	noClient bool
	sessions []sessions.LiveSession
	settings *settings.Settings
	// discovery lets the channel scan's look for new posts alone run as it is due.
	discovery bool
	// file is reviews.json's path ("" for a new one in a temp folder).
	file   string
	budget *ghbudget.Budget
	// channelScanMs is the channel scan's interval (0: never due on its own).
	channelScanMs float64
	// me is CONN_REVIEW_ME.
	me string
}

// setup is Reviews over a world of fakes.
func setup(t *testing.T, opts setupOpts) (*reviews.Reviews, *world) {
	t.Helper()

	current := settingsOf(settingsOpts{})
	if opts.settings != nil {
		current = *opts.settings
	}

	open := opts.open
	if open == nil {
		open = []*reviews.PR{prOf(func(pr *reviews.PR) { pr.Requested = []string{worldMe} })}
	}

	dir := t.TempDir()
	file := opts.file

	if file == "" {
		file = filepath.Join(dir, "reviews.json")
	}

	w := newWorld(dir, file, current, open, opts)

	return newReviews(t, w, opts), w
}

func newWorld(dir, file string, current settings.Settings, open []*reviews.PR,
	opts setupOpts,
) *world {
	empty := reviews.ScanResult{
		Posts: []reviews.Post{}, Asks: []reviews.Ask{}, NeedsAuth: []string{},
	}

	return &world{
		settings: &fakeSettings{current: current, version: 1}, open: open,
		clients: !opts.noClient, now: worldT0, sessions: opts.sessions,
		searchError: func(string) string { return "" },
		scanResult:  func() (reviews.ScanResult, error) { return empty, nil },
		dmResult:    func() ([]reviews.DMFind, error) { return []reviews.DMFind{}, nil },
		endedAs:     textOf(reviews.EndedMerged), endedAt: worldT0 - minuteMs,
		gone: map[string]bool{}, readFails: map[string]bool{},
		launchResult: func(launch reviews.LaunchOpts) reviews.LaunchResult {
			return reviews.LaunchResult{
				OK: true, Worktree: worktreeOf(dir, launch.Repo, launch.Number),
			}
		},
		clone: func(repo string) string {
			_, name, _ := strings.Cut(repo, "/")

			return "/nowhere/clones/" + name
		},
		inputState:  "empty",
		closeResult: func() (int, string) { return 200, "" },
		porcelainOf: map[string]string{}, exists: func(string) bool { return true },
		originOf:   func(string) string { return "" },
		worktreeOf: func(string) string { return "/nowhere/clones/widgets" },
		known:      map[string]*reviews.PR{}, dir: dir, file: file,
	}
}

func textOf(value string) *string { return new(value) }

// names are the review session names as Conn makes them.
func names() reviews.Names {
	return reviews.Names{Pattern: config.FromEnv(func(string) (string, bool) {
		return "", false
	}).ReviewNamePattern}
}

// worktreeOf is where a launch of PR number of repo puts its worktree under dir.
func worktreeOf(dir, repo string, number int64) string {
	path, err := names().ReviewWorktree(dir, strings.ToLower(repo), number)
	if err != nil {
		panic(err)
	}

	return path
}

func newReviews(t *testing.T, fixture *world, opts setupOpts) *reviews.Reviews {
	t.Helper()

	channelScanMs := opts.channelScanMs
	if channelScanMs == 0 {
		channelScanMs = 1e12
	}

	tab := reviews.New(t.Context(), reviews.Deps{
		Gh: worldGh{w: fixture}, Scan: fixture.scan, DMScan: fixture.dmScan, File: fixture.file,
		Settings: fixture.settings, CloneFor: fixture.cloneFor, OriginOf: fixture.origin,
		SeenRepos: func() []string { return nil }, HasClients: fixture.hasClients,
		Sessions: fixture.liveSessions, Bump: fixture.bump, Now: fixture.clock,
		Launch: fixture.launch, Git: fixture.runGit, InputState: fixture.input,
		CloseWorkspace: fixture.closeWorkspace, Exists: fixture.isThere,
		WorktreeClone: fixture.worktreeClone, Record: fixture.record, Budget: opts.budget,
		Logger: nil,
		Config: reviews.Config{
			PollMs: pollMs, FirstWindowDays: 2, DMScanMs: 600_000, ChannelScanMs: channelScanMs,
			CloseAfterMs: 10 * minuteMs, CloseCapMs: 60 * minuteMs, Me: opts.me, Dry: false,
			NamePattern: names().Pattern,
		},
	})

	// The channel scan's look for new posts alone is not due until a test asks for it.
	if !opts.discovery {
		tab.NoDiscovery()
	}

	return tab
}

// tickAndSettle is two ticks, each waited out: the first starts a poll, and the second sees its
// result (a scan is decided from the list the poll left).
func tickAndSettle(tab *reviews.Reviews) {
	tab.Tick()
	tab.Settle()
	tab.Tick()
	tab.Settle()
}

// view is the snapshot as Reviews shows it.
func view(t *testing.T, tab *reviews.Reviews) reviews.View {
	t.Helper()

	shown, isView := tab.Snapshot().(reviews.View)
	if !isView {
		t.Fatal("the snapshot is not a live view")
	}

	return shown
}

// with changes the world holding its lock.
func (w *world) with(change func(w *world)) {
	w.mu.Lock()
	defer w.mu.Unlock()

	change(w)
}

// advance moves the clock on.
func (w *world) advance(ms float64) { w.with(func(w *world) { w.now += ms }) }

// setSettings replaces the settings, as a saved update does (the caller tells Reviews).
func (w *world) setSettings(current settings.Settings) { w.settings.set(current) }

// snapshotCalls is a copy of the calls so far.
func (w *world) snapshotCalls() calls {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.calls
}

func (w *world) clock() float64 {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.now
}

func (w *world) hasClients() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.clients
}

func (w *world) liveSessions() []sessions.LiveSession {
	w.mu.Lock()
	defer w.mu.Unlock()

	return append([]sessions.LiveSession{}, w.sessions...)
}

func (w *world) bump() { w.with(func(w *world) { w.calls.bump++ }) }

func (w *world) cloneFor(repo string) string {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.calls.clone = append(w.calls.clone, repo)

	return w.clone(repo)
}

func (w *world) origin(path string) string {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.originOf(path)
}

func (w *world) isThere(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.exists(path)
}

func (w *world) worktreeClone(path string) string {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.worktreeOf(path)
}

func (w *world) record(name string, ok bool, detail string) {
	state := "failing"
	if ok {
		state = "ok"
	}

	w.with(func(w *world) { w.records = append(w.records, name+" "+state+": "+detail) })
}

func (w *world) scan(_ context.Context, input reviews.ScanInput) (reviews.ScanResult, error) {
	w.mu.Lock()
	w.calls.scan = append(w.calls.scan, input)
	answer := w.scanResult
	w.mu.Unlock()

	return answer()
}

func (w *world) dmScan(_ context.Context, allow []string, since string) ([]reviews.DMFind, error) {
	w.mu.Lock()
	w.calls.dm = append(w.calls.dm, dmCall{allow: allow, since: since})
	answer := w.dmResult
	w.mu.Unlock()

	return answer()
}

func (w *world) launch(_ context.Context, opts reviews.LaunchOpts) reviews.LaunchResult {
	w.mu.Lock()
	w.launches = append(w.launches, opts)
	answer := w.launchResult
	w.mu.Unlock()

	return answer(opts)
}

func (w *world) runGit(_ context.Context, args []string, dir string, timeout time.Duration) (
	string, error,
) {
	w.mu.Lock()
	w.git = append(w.git, gitCall{args: args, dir: dir, timeout: timeout})
	onGit := w.onGit
	porcelain, found := w.porcelainOf[dir]

	if !found {
		porcelain = w.porcelain
	}

	w.mu.Unlock()

	if onGit != nil {
		onGit(args)
	}

	if args[0] == "status" {
		return porcelain, nil
	}

	return "", nil
}

func (w *world) input(_ context.Context, pid string) string {
	w.mu.Lock()
	w.inputCalls = append(w.inputCalls, pid)
	onInput := w.onInputState
	w.mu.Unlock()

	if onInput != nil {
		onInput()
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	return w.inputState
}

func (w *world) closeWorkspace(_ context.Context, pid string) (int, string) {
	w.mu.Lock()
	w.closeCalls = append(w.closeCalls, pid)
	held := w.closeGate
	w.mu.Unlock()

	held.pass()

	w.mu.Lock()
	defer w.mu.Unlock()

	return w.closeResult()
}

// worldGh is GitHub as the world has it.
type worldGh struct {
	w *world
}

func (g worldGh) Login(context.Context) (string, error) {
	g.w.with(func(w *world) { w.calls.login++ })

	return worldMe, nil
}

// inScope is whether a search's scope covers a repo.
func inScope(search, repo string) bool {
	lower := strings.ToLower(repo)
	owner, _, _ := strings.Cut(lower, "/")

	if strings.HasSuffix(search, " repo:"+lower) {
		return true
	}

	return strings.HasSuffix(search, " user:"+owner)
}

func (g worldGh) Search(_ context.Context, queries []reviews.Query) (
	[]reviews.SearchResult, *ghbudget.RateInfo, error,
) {
	fixture := g.w
	texts := make([]string, 0, len(queries))

	for _, query := range queries {
		texts = append(texts, query.Q)
	}

	fixture.mu.Lock()
	fixture.calls.list++
	fixture.calls.search = append(fixture.calls.search, texts)
	held := fixture.listGate
	fixture.mu.Unlock()

	held.pass()

	fixture.mu.Lock()
	defer fixture.mu.Unlock()

	if fixture.listErr != nil {
		return nil, nil, fixture.listErr
	}

	// As the real source: only every search failing fails the call.
	if len(texts) > 0 {
		if everyFails(texts, fixture.searchError) {
			return nil, nil, textError(fixture.searchError(texts[0]))
		}
	}

	results := make([]reviews.SearchResult, 0, len(texts))
	for _, q := range texts {
		results = append(results, fixture.searchOne(q))
	}

	return results, fixture.rate, nil
}

func everyFails(texts []string, why func(string) string) bool {
	for _, q := range texts {
		if why(q) == "" {
			return false
		}
	}

	return true
}

// searchOne is one search's answer. The caller holds mu.
func (w *world) searchOne(search string) reviews.SearchResult {
	if failed := w.searchError(search); failed != "" {
		return reviews.SearchResult{PRs: []*reviews.PR{}, Capped: false, Error: &failed}
	}

	requested := strings.Contains(search, "user-review-requested:@me")
	prs := []*reviews.PR{}

	for _, pull := range w.open {
		if !inScope(search, pull.Repo) {
			continue
		}

		if !w.listedBy(pull, requested) {
			continue
		}

		prs = append(prs, pull)
		w.known[keyOf(pull.Number, pull.Repo)] = pull
	}

	capped := false
	if !requested {
		capped = w.openCapped
	}

	return reviews.SearchResult{PRs: prs, Capped: capped, Error: nil}
}

func (w *world) listedBy(pull *reviews.PR, requested bool) bool {
	if requested {
		for _, login := range pull.Requested {
			if strings.EqualFold(login, worldMe) {
				return true
			}
		}

		return false
	}

	if pull.IsDraft {
		return false
	}

	return !strings.EqualFold(pull.Author, worldMe)
}

func (g worldGh) Fetch(_ context.Context, prs []reviews.KeyParts,
	onRate func(*ghbudget.RateInfo),
) (*js.Map[*reviews.PR], error) {
	fixture := g.w
	keys := make([]string, 0, len(prs))

	for _, pr := range prs {
		keys = append(keys, pr.Repo+"#"+strconv.FormatInt(pr.Number, 10))
	}

	fixture.mu.Lock()
	fixture.calls.fetch = append(fixture.calls.fetch, keys)
	open := map[string]*reviews.PR{}

	for _, pr := range fixture.open {
		open[keyOf(pr.Number, pr.Repo)] = pr
	}

	for index, key := range keys {
		if open[key] == nil {
			fixture.calls.ended = append(fixture.calls.ended, prs[index].Number)
		}
	}

	held := fixture.endedGate
	fixture.mu.Unlock()

	held.pass()

	fixture.mu.Lock()
	defer fixture.mu.Unlock()

	fixture.endedFailures--
	if fixture.endedFailures >= 0 {
		return nil, errGhHiccup
	}

	if fixture.fetchRate != nil {
		if onRate != nil {
			onRate(fixture.fetchRate)
		}
	}

	return fixture.readByNumber(keys, open), nil
}

// readByNumber is the reads' answer. The caller holds mu.
func (w *world) readByNumber(keys []string, open map[string]*reviews.PR) *js.Map[*reviews.PR] {
	out := js.NewMap[*reviews.PR]()

	for _, key := range keys {
		if w.readFails[key] {
			continue
		}

		if pr := open[key]; pr != nil {
			w.known[key] = pr
			out.Set(key, pr)

			continue
		}

		known := w.known[key]

		switch {
		case w.gone[key], known == nil:
			out.Set(key, nil)
		case w.endedAs == nil:
			out.Set(key, known)
		default:
			ended := *known
			ended.Ended = w.endedAs
			at := w.endedAt
			ended.EndedAt = &at
			out.Set(key, &ended)
		}
	}

	return out
}
