package myprs_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
)

// The port of myPrs.test.ts's poll tests (lines 1 to 900): the list, the rows and the file.

const aDayMs = 86_400_000

var (
	errAGhDown      = errors.New("gh down")
	errABoom        = errors.New("boom")
	errANotLoggedIn = errors.New("gh: not logged in")
	errAGhMissing   = errors.New("gh missing")
	errADirty       = errors.New("gh: \u001b[31mnot logged in\u202e\u2028\u0085")
	// aUnsafe are the characters a shown error never holds.
	aUnsafe = regexp.MustCompile(
		`[\x00-\x1f\x7f-\x9f\x{200b}-\x{200f}\x{2028}-\x{202e}\x{2066}-\x{2069}\x{feff}]`)
)

// aFile is a fresh myprs.json in a temp folder.
func aFile(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "myprs.json")
}

// aNew is My PRs over the world with myprs.json at file.
func aNew(world *World, file string) *myprs.MyPrs {
	return myprs.New(context.Background(), world.Deps(file))
}

// aFailing is a search that fails with err.
func aFailing(err error) func() (myprs.Search, error) {
	return func() (myprs.Search, error) {
		return myprs.Search{PRs: nil, Capped: false, Failed: nil, Rate: nil}, err
	}
}

// aListing is a search that answers prs.
func aListing(capped bool, prs ...*myprs.PR) func() (myprs.Search, error) {
	return func() (myprs.Search, error) {
		return myprs.Search{PRs: prs, Capped: capped, Failed: nil, Rate: nil}, nil
	}
}

// aSetSource makes the world's search answer source.
func aSetSource(world *World, source func() (myprs.Search, error)) {
	world.Change(func(w *World) { w.Source = source })
}

// aText is a nullable text, "" for nil.
func aText(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

// aSeed writes a file holding an entry for each key, seen at seenAt.
func aSeed(t *testing.T, file string, seenAt float64, keys ...string) {
	t.Helper()

	seeded := myprs.LoadFile(file)
	for _, key := range keys {
		myprs.TrackOf(seeded, key, seenAt)
	}

	err := myprs.SaveFile(file, seeded, nil)
	if err != nil {
		t.Fatalf("seeding %s: %v", file, err)
	}
}

// aCounters are the searches and the logins the world has seen.
func aCounters(world *World) (int, int) {
	var calls, logins int

	world.Change(func(w *World) { calls, logins = w.Calls, w.Logins })

	return calls, logins
}

// aAuthors are the logins the searches were for.
func aAuthors(world *World) []string {
	var authors []string

	world.Change(func(w *World) { authors = slices.Clone(w.Authors) })

	return authors
}

// aHas is whether the file on disk has an entry for key.
func aHas(file, key string) bool { return myprs.LoadFile(file).PRs.Has(key) }

// aRow is the first row of the snapshot.
func aRow(t *testing.T, poller *myprs.MyPrs) *myprs.Row {
	t.Helper()

	rows := poller.Snapshot().Rows
	if len(rows) == 0 {
		t.Fatal("no rows")
	}

	return rows[0]
}

// aPolled is My PRs over a world listing prs, polled once.
func aPolled(t *testing.T, prs ...*myprs.PR) (*World, *myprs.MyPrs, string) {
	t.Helper()

	world := newWorld(t, prs...)
	poller, file := world.New()
	tick(poller)

	return world, poller, file
}

func TestASnapshotBeforeTheFirstPoll(t *testing.T) {
	t.Parallel()

	poller, _ := newWorld(t, pr()).New()
	view := poller.Snapshot()

	if got := string(js.Marshal(view.Rows)); got != "[]" {
		t.Errorf("rows = %s, want []", got)
	}

	if view.Error != nil {
		t.Errorf("error = %q, want nil", *view.Error)
	}

	if view.Me != nil {
		t.Errorf("me = %q, want nil", *view.Me)
	}

	if view.Capped {
		t.Error("capped, want not")
	}

	if view.Loaded {
		t.Error("loaded, want not")
	}

	if view.Refresh != (myprs.Refresh{Seq: 0, Done: 0}) {
		t.Errorf("refresh = %+v, want 0 0", view.Refresh)
	}

	if view.DraftMaxTries != myprs.DraftTriesMax {
		t.Errorf("draftMaxTries = %d, want %d", view.DraftMaxTries, myprs.DraftTriesMax)
	}
}

func TestALoadedStaysAfterAFailedPoll(t *testing.T) {
	t.Parallel()

	world, poller, _ := aPolled(t, pr())
	if !poller.Snapshot().Loaded {
		t.Fatal("not loaded after a good poll")
	}

	aSetSource(world, aFailing(errAGhDown))
	poller.Refresh()
	tick(poller)

	view := poller.Snapshot()
	if view.Error == nil {
		t.Error("no error after a failed poll")
	}

	if !view.Loaded {
		t.Error("not loaded after a failed poll that followed a good one")
	}
}

func TestAFailedFirstPollIsNotLoaded(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	aSetSource(world, aFailing(errAGhDown))
	poller, _ := world.New()
	tick(poller)

	if poller.Snapshot().Loaded {
		t.Error("loaded after a failed first poll")
	}
}

func TestALoadedTurnsTrueWithTheFirstGoodPoll(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	aSetSource(world, aFailing(errABoom))
	poller, _ := world.New()
	tick(poller)

	if poller.Snapshot().Loaded {
		t.Fatal("loaded after a failed first poll")
	}

	world.SetList()
	world.Advance(pollMs)
	tick(poller)

	if !poller.Snapshot().Loaded {
		t.Fatal("not loaded after a good poll")
	}

	// A later failure keeps the list it had, and that list is still a loaded one.
	aSetSource(world, aFailing(errABoom))
	world.Advance(pollMs)
	tick(poller)

	view := poller.Snapshot()
	if !view.Loaded {
		t.Error("not loaded after a later failure")
	}

	if aText(view.Error) != "boom" {
		t.Errorf("error = %q, want boom", aText(view.Error))
	}
}

func TestALoginOfTheList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		me         string
		wantAuthor string
		wantLogins int
	}{
		{name: "gh's login", me: "", wantAuthor: "jane", wantLogins: 1},
		// Config trims CONN_REVIEW_ME; gh is then not asked.
		{name: "CONN_REVIEW_ME", me: "someone", wantAuthor: "someone", wantLogins: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := newWorld(t, pr())
			deps := world.Deps(aFile(t))
			deps.Config.Me = tt.me
			poller := myprs.New(context.Background(), deps)
			tick(poller)

			if got := aText(poller.Snapshot().Me); got != tt.wantAuthor {
				t.Errorf("me = %q, want %q", got, tt.wantAuthor)
			}

			if got := aAuthors(world); !slices.Equal(got, []string{tt.wantAuthor}) {
				t.Errorf("authors = %v, want [%s]", got, tt.wantAuthor)
			}

			if _, logins := aCounters(world); logins != tt.wantLogins {
				t.Errorf("logins = %d, want %d", logins, tt.wantLogins)
			}
		})
	}
}

func TestAPollFillsRows(t *testing.T) {
	t.Parallel()

	bot := func(id string, change ...func(t *myprs.Thread)) myprs.Thread {
		return thread(append([]func(t *myprs.Thread){func(t *myprs.Thread) {
			t.ID = id
			t.By = findingBot
		}}, change...)...)
	}
	world, poller, _ := aPolled(t, pr(func(p *myprs.PR) {
		p.LastCommentAt = worldT0 - 2*hourMs
		p.Threads = []myprs.Thread{
			bot("1"), bot("2"), thread(func(t *myprs.Thread) { t.ID = "3" }),
			bot("4", func(t *myprs.Thread) { t.Outdated = true }),
			bot("5", func(t *myprs.Thread) { t.Resolved = true }),
		}
	}))

	view := poller.Snapshot()
	if aText(view.Me) != "jane" {
		t.Errorf("me = %q, want jane", aText(view.Me))
	}

	if view.Error != nil {
		t.Errorf("error = %q, want nil", *view.Error)
	}

	if len(view.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(view.Rows))
	}

	row := view.Rows[0]

	got := fmt.Sprint(row.Repo, row.Number, row.Title, row.URL, row.Draft, row.CreatedAt,
		row.LastCommentAt)
	want := fmt.Sprint("org/repo", 7.0, "Add a thing", "https://github.com/org/repo/pull/7",
		false, float64(worldT0-5*hourMs), float64(worldT0-2*hourMs))

	if got != want {
		t.Errorf("row = %s, want %s", got, want)
	}

	counts := fmt.Sprint(row.OpenBot, row.OpenHuman, row.Outdated, row.Partial, row.Round)
	if counts != fmt.Sprint(2, 1, 1, false, 0.0) {
		t.Errorf("counts = %s, want 2 1 1 false 0", counts)
	}

	state := fmt.Sprint(row.Status, row.CanStart, row.Reason, row.Label, row.SlackDraft)
	if state != fmt.Sprint("idle", true, (*string)(nil), "Self-review", (*myprs.SlackDraft)(nil)) {
		t.Errorf("state = %s, want idle true nil Self-review nil", state)
	}

	world.Change(func(w *World) {
		if w.Bumps == 0 {
			t.Error("no bump")
		}
	})
}

func TestAStateFileRecordsTheListedPR(t *testing.T) {
	t.Parallel()

	_, _, file := aPolled(t, pr())

	if !aHas(file, myprs.EntryKey("org/repo", 7)) {
		t.Error("org/repo#7 is not in the file")
	}
}

func TestANoPollWithoutAClient(t *testing.T) {
	t.Parallel()

	world := newWorld(t, pr())
	world.Clients = false
	poller, _ := world.New()
	tick(poller)

	calls, logins := aCounters(world)
	if calls != 0 {
		t.Errorf("searches = %d, want 0", calls)
	}

	if logins != 0 {
		t.Errorf("logins = %d, want 0", logins)
	}

	// A poll runs as soon as a client connects.
	world.Change(func(w *World) { w.Clients = true })
	tick(poller)

	if calls, _ := aCounters(world); calls != 1 {
		t.Errorf("searches = %d, want 1", calls)
	}
}

func TestAPollRepeatsEveryPollMs(t *testing.T) {
	t.Parallel()

	world, poller, _ := aPolled(t, pr())
	calls := func(want int) {
		t.Helper()

		if got, _ := aCounters(world); got != want {
			t.Errorf("searches = %d, want %d", got, want)
		}
	}

	world.Advance(pollMs - 1)
	tick(poller)
	calls(1)
	world.Advance(1)
	tick(poller)
	calls(2)

	// The login is asked once.
	if _, logins := aCounters(world); logins != 1 {
		t.Errorf("logins = %d, want 1", logins)
	}
}

func TestAFailedPollKeepsTheRows(t *testing.T) {
	t.Parallel()

	world, poller, _ := aPolled(t, pr(), pr(func(p *myprs.PR) { p.Number = 8 }))
	if got := len(poller.Snapshot().Rows); got != 2 {
		t.Fatalf("rows = %d, want 2", got)
	}

	aSetSource(world, aFailing(errANotLoggedIn))
	world.Advance(pollMs)
	tick(poller)

	view := poller.Snapshot()
	if len(view.Rows) != 2 {
		t.Errorf("rows after a failure = %d, want 2", len(view.Rows))
	}

	if !strings.Contains(aText(view.Error), "not logged in") {
		t.Errorf("error = %q, want not logged in", aText(view.Error))
	}

	// The next good poll clears the error and replaces the rows.
	world.SetList(pr())
	world.Advance(pollMs)
	tick(poller)

	view = poller.Snapshot()
	if view.Error != nil {
		t.Errorf("error = %q, want nil", *view.Error)
	}

	if len(view.Rows) != 1 {
		t.Errorf("rows after a good poll = %d, want 1", len(view.Rows))
	}
}

func TestAErrorTextIsCleanedAndCut(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	aSetSource(world, aFailing(fmt.Errorf("%w!%s", errADirty, strings.Repeat("z", 500))))
	poller, _ := world.New()
	tick(poller)

	shown := aText(poller.Snapshot().Error)
	if !strings.HasPrefix(shown, "gh: [31mnot logged in !") {
		t.Errorf("error = %q, want it to start gh: [31mnot logged in !", shown)
	}

	if len(shown) > 300 {
		t.Errorf("error is %d long, want at most 300", len(shown))
	}

	if aUnsafe.MatchString(shown) {
		t.Errorf("error %q holds an unsafe character", shown)
	}
}

// aFailLogin is gh whose login fails.
type aFailLogin struct{ myprs.GitHub }

func (aFailLogin) Login(context.Context) (string, error) { return "", errAGhMissing }

func TestAFailedLoginLeavesNoRows(t *testing.T) {
	t.Parallel()

	world := newWorld(t, pr())
	deps := world.Deps(aFile(t))
	deps.Gh = aFailLogin{deps.Gh}
	poller := myprs.New(context.Background(), deps)
	tick(poller)

	view := poller.Snapshot()
	if got := string(js.Marshal(view.Rows)); got != "[]" {
		t.Errorf("rows = %s, want []", got)
	}

	if !strings.Contains(aText(view.Error), "gh missing") {
		t.Errorf("error = %q, want gh missing", aText(view.Error))
	}
}

func TestACappedListPrunesNothing(t *testing.T) {
	t.Parallel()

	// A PR that left the list long ago is only forgotten by a list that proves it is gone.
	file := aFile(t)
	aSeed(t, file, worldT0-30*aDayMs, myprs.EntryKey("org/repo", 99))

	world := newWorld(t)
	aSetSource(world, aListing(true, pr(), pr(func(p *myprs.PR) { p.Number = 8 })))
	poller := aNew(world, file)
	tick(poller)

	view := poller.Snapshot()
	if !view.Capped {
		t.Error("not capped")
	}

	if len(view.Rows) != 2 {
		t.Errorf("rows = %d, want 2", len(view.Rows))
	}

	if !aHas(file, myprs.EntryKey("org/repo", 99)) {
		t.Error("a capped list pruned org/repo#99")
	}

	if !aHas(file, myprs.EntryKey("org/repo", 7)) {
		t.Error("org/repo#7 is not in the file")
	}
}

func TestAUncappedListPrunesWhatIsGoneFor14Days(t *testing.T) {
	t.Parallel()

	file := aFile(t)
	aSeed(t, file, worldT0-30*aDayMs, myprs.EntryKey("org/repo", 99))

	world := newWorld(t)
	aSetSource(world, aFailing(errABoom))
	poller := aNew(world, file)
	tick(poller)

	if !aHas(file, myprs.EntryKey("org/repo", 99)) {
		t.Fatal("a failed poll pruned org/repo#99")
	}

	world.SetList(pr())
	world.Advance(pollMs)
	tick(poller)

	if aHas(file, myprs.EntryKey("org/repo", 99)) {
		t.Error("org/repo#99 was not pruned")
	}

	if !aHas(file, myprs.EntryKey("org/repo", 7)) {
		t.Error("org/repo#7 is not in the file")
	}
}

func TestARowStatus(t *testing.T) {
	t.Parallel()

	other := func(p *myprs.PR) {
		p.Repo = "org/other"
		p.URL = "https://github.com/org/other/pull/7"
	}
	fork := func(p *myprs.PR) { p.Fork = true }
	draft := func(p *myprs.PR) { p.Draft = true }

	tests := []struct {
		name         string
		change       []func(p *myprs.PR)
		wantStatus   string
		wantCanStart bool
		// wantReason is a pattern the reason matches; "" is no reason.
		wantReason string
	}{
		{
			name: "a repo without a clone", change: []func(p *myprs.PR){other},
			wantStatus: "no-clone", wantCanStart: false, wantReason: `org/other`,
		},
		{
			name: "a fork", change: []func(p *myprs.PR){fork},
			wantStatus: "fork", wantCanStart: false, wantReason: `.`,
		},
		{
			name:       "a fork without a clone is a fork first",
			change:     []func(p *myprs.PR){other, fork},
			wantStatus: "fork", wantCanStart: false, wantReason: `.`,
		},
		{
			name: "a GitHub draft is informational only", change: []func(p *myprs.PR){draft},
			wantStatus: "draft-pr", wantCanStart: true, wantReason: "",
		},
		{
			name: "a draft without a clone is no-clone", change: []func(p *myprs.PR){other, draft},
			wantStatus: "no-clone", wantCanStart: false, wantReason: `org/other`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, poller, _ := aPolled(t, pr(tt.change...))
			row := aRow(t, poller)

			if row.Status != tt.wantStatus {
				t.Errorf("status = %s, want %s", row.Status, tt.wantStatus)
			}

			if row.CanStart != tt.wantCanStart {
				t.Errorf("canStart = %t, want %t", row.CanStart, tt.wantCanStart)
			}

			aCheckReason(t, row.Reason, tt.wantReason)
		})
	}
}

func aCheckReason(t *testing.T, reason *string, want string) {
	t.Helper()

	if want == "" {
		if reason != nil {
			t.Errorf("reason = %q, want nil", *reason)
		}

		return
	}

	if reason == nil {
		t.Errorf("reason = nil, want one matching %s", want)

		return
	}

	if !regexp.MustCompile(want).MatchString(*reason) {
		t.Errorf("reason = %q, want one matching %s", *reason, want)
	}
}

func TestADraftRowShowsNoSlackDraft(t *testing.T) {
	t.Parallel()

	_, poller, _ := aPolled(t, pr(func(p *myprs.PR) { p.Draft = true }))
	row := aRow(t, poller)

	if !row.Draft {
		t.Error("draft = false, want true")
	}

	if row.SlackDraft != nil {
		t.Errorf("slackDraft = %+v, want nil", row.SlackDraft)
	}
}

func TestALongLivedBranchCannotStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		headRef, defaultBranch, wantReason string
	}{
		{headRef: "develop", defaultBranch: "main", wantReason: `develop.*long-lived`},
		{headRef: "release/2.0", defaultBranch: "main", wantReason: `release/2\.0`},
		{headRef: "trunk", defaultBranch: "trunk", wantReason: `trunk.*default branch`},
	}

	for _, tt := range tests {
		t.Run(tt.headRef, func(t *testing.T) {
			t.Parallel()

			_, poller, _ := aPolled(t, pr(func(p *myprs.PR) {
				p.HeadRef = tt.headRef
				p.DefaultBranch = &tt.defaultBranch
			}))
			row := aRow(t, poller)

			if row.Status != "idle" {
				t.Errorf("status = %s, want idle", row.Status)
			}

			if row.CanStart {
				t.Error("canStart = true, want false")
			}

			aCheckReason(t, row.Reason, tt.wantReason)
		})
	}
}

func TestALongLivedBranchNameIsCleaned(t *testing.T) {
	t.Parallel()

	// The branch name is GitHub's text: it is cleaned before it is shown.
	world := newWorld(t, pr(func(p *myprs.PR) { p.HeadRef = "develop\u202e\u0007x" }))
	deps := world.Deps(aFile(t))
	deps.Config.ProtectedHeads = append(slices.Clone(deps.Config.ProtectedHeads), "develop*")
	poller := myprs.New(context.Background(), deps)
	tick(poller)

	reason := aText(aRow(t, poller).Reason)
	if !strings.Contains(reason, "developx") {
		t.Errorf("reason = %q, want it to name developx", reason)
	}

	if strings.ContainsAny(reason, "\u202e\u0007") {
		t.Errorf("reason = %q holds the branch name's unsafe characters", reason)
	}
}

func TestARecordedLaunchReadsReReview(t *testing.T) {
	t.Parallel()

	file := aFile(t)
	seeded := myprs.LoadFile(file)
	entry := myprs.TrackOf(seeded, myprs.EntryKey("org/repo", 7), worldT0-hourMs)
	entry.Launches = append(entry.Launches, &myprs.Launch{
		At: worldT0 - hourMs, Model: "sonnet", Name: "self-repo-7", Worktree: "/w",
		HeadSha: "abc", Round: 1, Armed: true, AskedSha: nil, AskedAt: nil, ClosedAt: nil,
		AskTry: nil, Manual: false,
	})
	entry.Draft = &myprs.SlackDraft{
		Sha: "abc", Text: "hi", State: "saved", Error: nil, At: worldT0, Tries: 1, Channel: "",
	}

	err := myprs.SaveFile(file, seeded, nil)
	if err != nil {
		t.Fatalf("seeding: %v", err)
	}

	poller := aNew(newWorld(t, pr()), file)
	tick(poller)
	row := aRow(t, poller)

	if row.Round != 1 {
		t.Errorf("round = %v, want 1", row.Round)
	}

	if row.Label != "Re-review" {
		t.Errorf("label = %s, want Re-review", row.Label)
	}

	want := `{"sha":"abc","text":"hi","state":"saved","error":null,"at":1800000000000,"tries":1}`
	if got := string(js.Marshal(row.SlackDraft)); got != want {
		t.Errorf("slackDraft = %s, want %s", got, want)
	}
}

func TestASameNumberInTwoReposStaysSeparate(t *testing.T) {
	t.Parallel()

	world := newWorld(t,
		pr(func(p *myprs.PR) { p.Title = "One" }),
		pr(func(p *myprs.PR) {
			p.Repo = "org/other"
			p.Title = "Two"
		}))
	world.Clones = map[string]string{"org/repo": "/a", "org/other": "/b"}
	poller, file := world.New()
	tick(poller)

	rows := make([]string, 0, len(poller.Snapshot().Rows))
	for _, row := range poller.Snapshot().Rows {
		rows = append(rows, row.Repo+" "+row.Title)
	}

	slices.Sort(rows)

	if !slices.Equal(rows, []string{"org/other Two", "org/repo One"}) {
		t.Errorf("rows = %v, want org/other Two, org/repo One", rows)
	}

	keys := myprs.LoadFile(file).PRs.Keys()
	slices.Sort(keys)

	if !slices.Equal(keys, []string{"org/other#7", "org/repo#7"}) {
		t.Errorf("keys = %v, want org/other#7 org/repo#7", keys)
	}
}

func TestARowsAreNewestOpenedFirst(t *testing.T) {
	t.Parallel()

	opened := func(number, hours float64) *myprs.PR {
		return pr(func(p *myprs.PR) {
			p.Number = number
			p.CreatedAt = worldT0 - hours*hourMs
		})
	}
	_, poller, _ := aPolled(t, opened(1, 9), opened(2, 1), opened(3, 4))

	numbers := make([]float64, 0, len(poller.Snapshot().Rows))
	for _, row := range poller.Snapshot().Rows {
		numbers = append(numbers, row.Number)
	}

	if !slices.Equal(numbers, []float64{2, 3, 1}) {
		t.Errorf("numbers = %v, want 2 3 1", numbers)
	}
}
