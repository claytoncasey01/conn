package reviews_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// Bun's reviewStore.test.ts, the file system: the save, its modes and its failures, what loads
// as empty, and the records the recorded answers do not hold.

const (
	dKey   = "acme/widgets#12"
	dDayMs = 86_400_000
)

var errDSimulated = errors.New("EXDEV: simulated")

// dLaunchJSON is a launch as Go writes one.
func dLaunchJSON(at float64, name, worktree, closedAt string) string {
	return fmt.Sprintf(`{"at":%s,"model":"sonnet","name":%s,"headSha":"abc123",`+
		`"worktree":%s,"sessionId":null,"closedAt":%s}`,
		js.Number(at), js.Stringify(name), js.Stringify(worktree), closedAt)
}

// dV1EntryJSON is a record as version 1 wrote it: no channel on the post, none of the version 2
// fields.
func dV1EntryJSON(launches string) string {
	return `{"slack":{"ts":"1700000000.000100","permalink":"https://x.slack.com/archives/C1/p1",` +
		`"postedAt":1700000000000},"slackAskAt":null,"dm":null,"dmLastAt":null,` +
		`"firstSeenAt":500,"launches":` + launches + `,"scan":{"tries":2,"lastAt":900},` +
		`"threadScan":{"tries":1,"lastAt":800,"headSha":"abc123"}}`
}

// dEntryJSON is a version 2 record with the given launches and these fields over the rest.
func dEntryJSON(launches, over string) string {
	return `{"slack":null,"slackAskAt":null,"dm":null,"dmLastAt":null,"firstSeenAt":1,` +
		`"launches":` + launches + `,"scan":{"tries":0,"lastAt":0},` +
		`"threadScan":{"tries":0,"lastAt":0,"headSha":""},"requested":false,` + over + `}`
}

func dMarshal(value any) string { return string(js.Marshal(value)) }

func dFileIn(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "d", "reviews.json")
}

func dEntries(t *testing.T, folder string) []string {
	t.Helper()

	entries, err := os.ReadDir(folder)
	dMust(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}

// TestDStoreSavedFileLoadsBack: posts, an ended record, a discovery and a legacy record go
// through a save and a load unchanged.
func TestDStoreSavedFileLoadsBack(t *testing.T) {
	t.Parallel()

	gadgets := `{"slack":null,"slackAskAt":77,"dm":null,"dmLastAt":null,"firstSeenAt":500,` +
		`"launches":[],"scan":{"tries":2,"lastAt":900},` +
		`"threadScan":{"tries":1,"lastAt":800,"headSha":"abc123"},"requested":true,` +
		`"ended":{"how":"merged","at":9},"seenAt":500}`
	widgets := dEntryJSON("["+dLaunchJSON(1000, "pr-12", "/nowhere/reviews/pr-12/widgets",
		"null")+"]", `"ended":null,"seenAt":500`)
	text := `{"version":2,"cursors":{"#code-review":"1700000000.000100"},"dmCursorTs":null,` +
		`"prs":{"acme/widgets#12":` + widgets + `,"acme/gadgets#34":` + gadgets + `},` +
		`"found":{"acme/widgets#40":{"at":5,"slack":{"ts":"1700000000.000200",` +
		`"permalink":"https://x.slack.com/p","channel":"#code-review"},"dm":null}},` +
		`"legacy":{"7":` + widgets + `}}`
	file := reviews.LoadFile(writeText(t, text))

	path := dFileIn(t)
	dMust(t, reviews.SaveFile(path, file, nil))

	if dMarshal(reviews.LoadFile(path)) != dMarshal(file) {
		t.Errorf("loaded back %s\nwant %s", dMarshal(reviews.LoadFile(path)), dMarshal(file))
	}

	if dMarshal(file) != text {
		t.Errorf("read %s\nwant %s", dMarshal(file), text)
	}
}

// TestDStoreSaveIsPrivate: the file is 0600 and its folder 0700, a folder that was there looser
// included. (Bun also checks the umask comes back: Go never sets it.)
func TestDStoreSaveIsPrivate(t *testing.T) {
	t.Parallel()

	path := dFileIn(t)
	dMust(t, os.MkdirAll(filepath.Dir(path), 0o755))
	dMust(t, os.Chmod(filepath.Dir(path), 0o755))
	dMust(t, reviews.SaveFile(path, reviews.EmptyFile(), nil))

	if dMode(t, path) != 0o600 {
		t.Errorf("file mode %o", dMode(t, path))
	}

	if dMode(t, filepath.Dir(path)) != 0o700 {
		t.Errorf("folder mode %o", dMode(t, filepath.Dir(path)))
	}
}

// TestDStoreLoadsWhatIsNotAFileAsEmpty: a missing path, a folder, and a version 1 file (only
// OpenFile migrates one) load as empty. (The texts Bun lists replay in the answers.)
func TestDStoreLoadsWhatIsNotAFileAsEmpty(t *testing.T) {
	t.Parallel()

	folder := t.TempDir()
	v1 := writeText(t, `{"version":1,"cursorTs":null,"prs":{"12":`+dV1EntryJSON("[]")+`}}`)
	empty := dMarshal(reviews.EmptyFile())

	for _, path := range []string{dFileIn(t), folder, v1} {
		if dMarshal(reviews.LoadFile(path)) != empty {
			t.Errorf("%s loaded as %s", path, dMarshal(reviews.LoadFile(path)))
		}
	}
}

// TestDStoreFailedRenameLeavesTheOldFile: a rename that fails, simulated or real (the target is
// a folder), leaves the old file whole and no temp file behind.
func TestDStoreFailedRenameLeavesTheOldFile(t *testing.T) {
	t.Parallel()

	path := dFileIn(t)
	old := reviews.EmptyFile()
	reviews.TrackOf(old, dKey, 5_000)
	dMust(t, reviews.SaveFile(path, old, nil))
	before := dRead(t, path)

	err := reviews.SaveFile(path, reviews.EmptyFile(), func(string, string) error {
		return errDSimulated
	})
	if err == nil {
		t.Fatal("the failed rename was not reported")
	}

	if !strings.Contains(err.Error(), "simulated") {
		t.Errorf("error %v", err)
	}

	if dRead(t, path) != before {
		t.Error("the old file changed")
	}

	if !slices.Equal(dEntries(t, filepath.Dir(path)), []string{"reviews.json"}) {
		t.Errorf("left behind: %v", dEntries(t, filepath.Dir(path)))
	}

	other := dFileIn(t)
	dMust(t, os.MkdirAll(filepath.Join(other, "keep"), 0o700))

	if reviews.SaveFile(other, reviews.EmptyFile(), nil) == nil {
		t.Error("a save over a folder succeeded")
	}

	if !slices.Equal(dEntries(t, filepath.Dir(other)), []string{"reviews.json"}) {
		t.Errorf("left behind: %v", dEntries(t, filepath.Dir(other)))
	}
}

// TestDStoreTrackOfCreatesARecordOnce: and returns the same one after.
func TestDStoreTrackOfCreatesARecordOnce(t *testing.T) {
	t.Parallel()

	file := reviews.EmptyFile()
	first := reviews.TrackOf(file, dKey, 5_000)
	want := `{"slack":null,"slackAskAt":null,"dm":null,"dmLastAt":null,"firstSeenAt":5000,` +
		`"launches":[],"scan":{"tries":0,"lastAt":0},` +
		`"threadScan":{"tries":0,"lastAt":0,"headSha":""},"requested":false,"ended":null,` +
		`"seenAt":5000}`

	if dMarshal(first) != want {
		t.Errorf("new record %s", dMarshal(first))
	}

	asked := 9.0
	first.SlackAskAt = &asked

	if reviews.TrackOf(file, dKey, 9_000) != first {
		t.Error("a second record was made")
	}

	entry, _ := file.PRs.Get(dKey)
	if entry.FirstSeenAt != 5_000 {
		t.Errorf("first seen %v", entry.FirstSeenAt)
	}
}

// TestDStorePlacingCountsAsATouch: a record long ended and with no worktree is kept 14 days from
// its placing, through a save and a load, then forgotten; one never placed goes at once.
func TestDStorePlacingCountsAsATouch(t *testing.T) {
	t.Parallel()

	now := 100.0 * dDayMs
	old := dEntryJSON("["+dLaunchJSON(1, "pr-41", "/gone/reviews/pr-41/widgets", "2")+"]",
		`"ended":{"how":"merged","at":3},"seenAt":1`)
	file := reviews.LoadFile(writeText(t, `{"version":2,"prs":{},"legacy":{"41":`+old+`}}`))

	moved, err := reviews.PlaceLegacy(file, "41", "acme/widgets", now)
	if moved != 1 {
		t.Fatalf("PlaceLegacy = %d, %v", moved, err)
	}

	path := dFileIn(t)
	dMust(t, reviews.SaveFile(path, file, nil))
	back := reviews.LoadFile(path)
	key := "acme/widgets#41"

	placed, _ := back.PRs.Get(key)
	if placed == nil {
		t.Fatal("the placed record did not load back")
	}

	if placed.PlacedAt == nil {
		t.Fatal("placedAt did not survive the save")
	}

	if *placed.PlacedAt != now {
		t.Errorf("placedAt %v", *placed.PlacedAt)
	}

	closed := func(string) bool { return false }

	reviews.Prune(back, closed, []string{"acme/*"}, now+dDayMs, nil)

	if !back.PRs.Has(key) {
		t.Error("placed a day ago, and forgotten")
	}

	reviews.Prune(back, closed, []string{"acme/*"}, now+14*dDayMs, nil)

	if back.PRs.Has(key) {
		t.Error("placed 14 days ago and nothing since, and kept")
	}

	never := reviews.LoadFile(writeText(t, `{"version":2,"prs":{"acme/widgets#42":`+old+`}}`))
	reviews.Prune(never, closed, []string{"acme/*"}, now, nil)

	if never.PRs.Len() != 0 {
		t.Errorf("a record never placed is kept: %v", never.PRs.Keys())
	}
}
