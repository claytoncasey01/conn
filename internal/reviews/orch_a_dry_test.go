package reviews_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
)

const aBoom = "a dependency was used in dry mode"

// aBoomGh is a GitHub that must never be called.
type aBoomGh struct{}

func (aBoomGh) Login(context.Context) (string, error) { panic(aBoom) }

func (aBoomGh) Search(context.Context, []reviews.Query) (
	[]reviews.SearchResult, *ghbudget.RateInfo, error,
) {
	panic(aBoom)
}

func (aBoomGh) Fetch(context.Context, []reviews.KeyParts, func(*ghbudget.RateInfo)) (
	*js.Map[*reviews.PR], error,
) {
	panic(aBoom)
}

// aBoomSettings are settings that must never be read.
type aBoomSettings struct{}

func (aBoomSettings) Current() (settings.Settings, uint64) { panic(aBoom) }

func (aBoomSettings) Setup() bool { panic(aBoom) }

// aDryReviews is a dry Reviews whose every dependency panics (now too, unless one is given).
func aDryReviews(t *testing.T, file string, now func() float64) *reviews.Reviews {
	t.Helper()

	if now == nil {
		now = func() float64 { panic(aBoom) }
	}

	return reviews.New(t.Context(), reviews.Deps{
		Gh: aBoomGh{},
		Scan: func(context.Context, reviews.ScanInput) (reviews.ScanResult, error) {
			panic(aBoom)
		},
		DMScan: func(context.Context, []string, string) ([]reviews.DMFind, error) {
			panic(aBoom)
		},
		File: file, Settings: aBoomSettings{},
		CloneFor:  func(string) string { panic(aBoom) },
		OriginOf:  func(string) string { panic(aBoom) },
		SeenRepos: func() []string { panic(aBoom) }, HasClients: func() bool { panic(aBoom) },
		Sessions: func() []sessions.LiveSession { panic(aBoom) }, Bump: func() { panic(aBoom) },
		Now: now,
		Launch: func(context.Context, reviews.LaunchOpts) reviews.LaunchResult {
			panic(aBoom)
		},
		Git: func(context.Context, []string, string, time.Duration) (string, error) {
			panic(aBoom)
		},
		InputState:     func(context.Context, string) string { panic(aBoom) },
		CloseWorkspace: func(context.Context, string) (int, string) { panic(aBoom) },
		Exists:         func(string) bool { panic(aBoom) },
		WorktreeClone:  func(string) string { panic(aBoom) },
		Record:         func(string, bool, string) { panic(aBoom) }, Budget: nil, Logger: nil,
		Config: reviews.Config{
			PollMs: pollMs, FirstWindowDays: 2, DMScanMs: 600_000, ChannelScanMs: 1e12,
			CloseAfterMs: 10 * minuteMs, CloseCapMs: 60 * minuteMs, Me: "", Dry: true,
			NamePattern: names().Pattern,
		},
	})
}

// aNoPanic runs call and fails the test (rather than the whole binary) when it panics.
func aNoPanic(t *testing.T, label string, call func()) {
	t.Helper()

	defer func() {
		if found := recover(); found != nil {
			t.Errorf("%s panicked: %v", label, found)
		}
	}()

	call()
}

// aDryView is the dry snapshot, read back from its JSON.
type aDryView struct {
	Channel string         `json:"channel"`
	Enabled bool           `json:"enabled"`
	Slack   string         `json:"slack"`
	Rows    []*reviews.Row `json:"rows"`
}

func aDrySnapshot(t *testing.T, tab *reviews.Reviews) aDryView {
	t.Helper()

	var shown aDryView

	err := json.Unmarshal(js.Marshal(tab.Snapshot()), &shown)
	if err != nil {
		t.Fatal(err)
	}

	return shown
}

func aFixedNow() float64 { return worldT0 }

// CONN_REVIEWS_DRY=1 returns fixed rows and touches no dependency (the dependency part,
// every one panicking but the clock: the dry view reads the wall clock through Deps.Now, which
// the server sets to time.Now).
func TestDryTouchesNoDependency(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "never-written.json")
	tab := aDryReviews(t, file, aFixedNow)

	aNoPanic(t, "settingsChanged", tab.SettingsChanged)
	aNoPanic(t, "tick", tab.Tick)
	aNoPanic(t, "refresh", func() { aRefresh(t, tab) })
	aNoPanic(t, "snapshot", func() { aDrySnapshot(t, tab) })
	aNoPanic(t, "start", func() { aStart(t, tab, worldRepo, "4110", aSonnet) })

	_, err := os.Stat(file)
	if err == nil {
		t.Error("the dry view wrote its file")
	}
}

// CONN_REVIEWS_DRY=1 returns fixed rows and touches no dependency (the rows).
func TestDryReturnsFixedRows(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "never-written.json")
	tab := aDryReviews(t, file, aFixedNow)
	shown := aDrySnapshot(t, tab)

	aEqual(t, "enabled", shown.Enabled, true)
	aEqual(t, "slack", shown.Slack, "ok")

	states := map[string]bool{}
	for _, row := range shown.Rows {
		states[row.State] = true
	}

	for _, state := range []string{"new", "reviewing", "waiting", "rereview", "done"} {
		if !states[state] {
			t.Errorf("no %s row", state)
		}
	}

	for label, has := range aDryChecks() {
		if !aAnyRow(shown.Rows, has) {
			t.Errorf("no row with %s", label)
		}
	}

	aDryShape(t, shown.Rows)

	_, err := os.Stat(file)
	if err == nil {
		t.Error("the dry view wrote its file")
	}
}

func aDryChecks() map[string]func(row *reviews.Row) bool {
	isBot := regexp.MustCompile(`\[bot\]$`)

	return map[string]func(row *reviews.Row) bool{
		"a GitHub re-ask": func(row *reviews.Row) bool { return aText(row.ReAsked) == "github" },
		"a Slack re-ask":  func(row *reviews.Row) bool { return aText(row.ReAsked) == "slack" },
		"a merge":         func(row *reviews.Row) bool { return aText(row.Ended) == "merged" },
		"an update":       func(row *reviews.Row) bool { return row.Updated },
		"a session":       func(row *reviews.Row) bool { return row.Session != nil },
		"a script title": func(row *reviews.Row) bool {
			return strings.Contains(row.Title, "<script>alert(1)</script>")
		},
		"a bot":    func(row *reviews.Row) bool { return isBot.MatchString(row.Author) },
		"no clone": func(row *reviews.Row) bool { return row.NoClone != nil },
	}
}

func aAnyRow(rows []*reviews.Row, has func(row *reviews.Row) bool) bool {
	return slices.ContainsFunc(rows, has)
}

// aDryShape checks the keys are unique and the rows come from more than one repo.
func aDryShape(t *testing.T, rows []*reviews.Row) {
	t.Helper()

	keys := map[string]bool{}
	repos := map[string]bool{}

	for _, row := range rows {
		keys[row.Key] = true
		repos[row.Repo] = true
	}

	aEqual(t, "unique keys", len(keys), len(rows))

	if len(repos) < 2 {
		t.Errorf("rows from %d repos", len(repos))
	}
}

// CONN_REVIEWS_DRY=1: start validates against the fixed rows, then answers ok and touches
// nothing (the clock is given: the dry rows are built at Now).
func TestDryStartValidatesAgainstFixedRows(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "never-written-2.json")
	tab := aDryReviews(t, file, aFixedNow)
	rows := aDrySnapshot(t, tab).Rows

	var open *reviews.Row

	for _, row := range rows {
		if row.Ended == nil {
			if row.NoClone == nil {
				open = row

				break
			}
		}
	}

	if open == nil {
		t.Fatal("no open row with a clone")
	}

	aDryStartOpen(t, tab, open)
	aDryStartRefusals(t, tab, rows)

	_, err := os.Stat(file)
	if err == nil {
		t.Error("the dry view wrote its file")
	}
}

func aDryStartOpen(t *testing.T, tab *reviews.Reviews, row *reviews.Row) {
	t.Helper()

	name := fmt.Sprintf("pr-%d", row.Number)
	if row.Launches > 0 {
		name += "." + strconv.Itoa(row.Launches+1)
	}

	name += "-" + strings.Replace(row.Repo, "/", "--", 1)
	number := strconv.FormatInt(row.Number, 10)
	first := aStart(t, tab, row.Repo, number, aSonnet)

	aEqual(t, "start", first.Code, 200)
	aEqual(t, "start body", js.Stringify(first.Body), `{"ok":true,"name":"`+name+`"}`)
	// Twice: nothing is remembered.
	aEqual(t, "again", aStart(t, tab, row.Repo, number, aOpus).Code, 200)
	aEqual(t, "haiku", aStart(t, tab, row.Repo, number, "haiku").Code, 400)
	aEqual(t, "bad repo", aStart(t, tab, "../x", number, aSonnet).Code, 400)
}

func aDryStartRefusals(t *testing.T, tab *reviews.Reviews, rows []*reviews.Row) {
	t.Helper()

	for _, bad := range []string{"12abc", "0", "-1", "1e9999", "1.5"} {
		aEqual(t, "number "+bad, aStart(t, tab, worldRepo, bad, aSonnet).Code, 400)
	}

	aEqual(t, "not listed", aStart(t, tab, worldRepo, "999999", aSonnet).Code, 404)

	twins := 0

	for _, row := range rows {
		number := strconv.FormatInt(row.Number, 10)

		if row.Ended != nil {
			aEqual(t, "ended", aStart(t, tab, row.Repo, number, aSonnet).Code, 404)
		}

		if row.NoClone != nil {
			aEqual(t, "no clone", aStart(t, tab, row.Repo, number, aSonnet).Code, 409)
		}

		if row.Number == 4110 {
			twins++
		}
	}

	// The same number in another repo is another PR.
	aEqual(t, "twins", twins, 2)

	legacy, err := tab.PlaceLegacy(float64(1), worldRepo)
	if err != nil {
		t.Fatal(err)
	}

	aEqual(t, "legacy", legacy.Code, 409)
}
