package reviews_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/origin"
	"github.com/claytoncasey01/conn/internal/reviews"
)

// Bun's reviewStore.test.ts, the migration on a real-shaped data folder: worktrees whose .git is
// a pointer file, read for their origin by the real reader; the backups openFile keeps.

// dCloneWithWorktree makes a clone at <root>/projects/<folder> whose origin is url, and a
// worktree of it at worktree (its .git a pointer file, the gitdir with a commondir).
func dCloneWithWorktree(t *testing.T, root, folder, url, worktree string) {
	t.Helper()

	clone := filepath.Join(root, "projects", folder)
	gitDir := filepath.Join(clone, ".git", "worktrees", filepath.Base(worktree)+"-"+folder)
	dMust(t, os.MkdirAll(gitDir, 0o755))
	dMust(t, os.WriteFile(filepath.Join(clone, ".git", "config"), []byte("[core]\n\tbare = false\n"+
		"[remote \"origin\"]\n\turl = "+url+"\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"),
		0o644))
	dMust(t, os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../..\n"), 0o644))
	dMust(t, os.MkdirAll(worktree, 0o755))
	dMust(t, os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644))
}

// dShaped is a real-shaped data folder: version 1 reviewed acme/widgets from a clone folder
// named widgets; #12's worktree is still there, the others were removed long ago.
type dShaped struct {
	path, v1 string
	ctx      reviews.MigrateContext
}

func dRealShaped(t *testing.T) dShaped {
	t.Helper()

	root := t.TempDir()
	data := filepath.Join(root, "data")
	dCloneWithWorktree(t, root, "widgets", "git@github.com:Acme/Widgets.git",
		filepath.Join(data, "reviews", "pr-12", "widgets"))

	launch := func(number, when string) string {
		return dLaunchJSON(dNumber(when), "pr-"+number,
			filepath.Join(data, "reviews", "pr-"+number, "widgets"), "null")
	}
	version1 := `{"version":1,"cursorTs":"1700000000.000100","dmCursorTs":"1700000500.000000",` +
		`"prs":{"12":` + dV1EntryJSON("["+launch("12", "1000")+"]") + `,` +
		`"13":` + dV1EntryJSON("["+launch("13", "5")+","+launch("13", "6")+"]") + `,` +
		`"16":` + dV1EntryJSON("[]") + `}}`
	path := filepath.Join(data, "reviews.json")
	dMust(t, os.WriteFile(path, []byte(version1), 0o644))

	return dShaped{path: path, v1: version1, ctx: reviews.MigrateContext{
		Allow: []string{"acme/*"}, Seen: []string{}, OriginOf: origin.NewReader().Of,
		Exists: nil,
	}}
}

func dNumber(text string) float64 {
	switch text {
	case "5":
		return 5
	case "6":
		return 6
	}

	return 1000
}

func dOpen(t *testing.T, path string, ctx reviews.MigrateContext) reviews.Opened {
	t.Helper()

	opened, err := reviews.OpenFile(path, ctx)
	dMust(t, err)

	return opened
}

func dSortedKeys(keys []string) []string {
	slices.Sort(keys)

	return keys
}

// TestDMigratePlacesTheWholeFileUnderTheLiveOrigin: records with no evidence of their own
// included; nothing is lost, nothing counts as requested; the DM cursor carries over and the
// channel one does not; the old file is kept, private, before the new one is written.
func TestDMigratePlacesTheWholeFileUnderTheLiveOrigin(t *testing.T) {
	t.Parallel()

	shaped := dRealShaped(t)
	opened := dOpen(t, shaped.path, shaped.ctx)

	if !opened.Writable {
		t.Fatalf("not writable: %s", opened.Error)
	}

	if opened.Migrated == nil {
		t.Fatal("nothing migrated")
	}

	if *opened.Migrated != 3 {
		t.Errorf("migrated %d", *opened.Migrated)
	}

	dCheckMigratedRecords(t, opened.File, shaped.v1)
	dCursors(t, opened.File)

	backup := reviews.V1Backup(shaped.path)
	if dRead(t, backup) != shaped.v1 {
		t.Errorf("backup %s", dRead(t, backup))
	}

	if dMode(t, backup) != 0o600 {
		t.Errorf("backup mode %o", dMode(t, backup))
	}

	if dMarshal(reviews.LoadFile(shaped.path)) != dMarshal(opened.File) {
		t.Error("the migrated file on disk is not the one opened")
	}
}

// dCheckMigratedRecords: every record under the one repo, its launches and post carried over,
// none of them requested.
func dCheckMigratedRecords(t *testing.T, file *reviews.File, version1 string) {
	t.Helper()

	want := []string{"acme/widgets#12", "acme/widgets#13", "acme/widgets#16"}

	if !slices.Equal(dSortedKeys(file.PRs.Keys()), want) {
		t.Errorf("keys %v", file.PRs.Keys())
	}

	if file.Legacy.Len() != 0 {
		t.Errorf("legacy %v", file.Legacy.Keys())
	}

	read := reviews.ReadFile(writeText(t, version1))
	oldThirteen, _ := read.V1.PRs.Get("13")
	thirteen, _ := file.PRs.Get("acme/widgets#13")

	if dMarshal(thirteen.Launches) != dMarshal(oldThirteen.Launches) {
		t.Errorf("launches %s", dMarshal(thirteen.Launches))
	}

	for _, key := range file.PRs.Keys() {
		entry, _ := file.PRs.Get(key)
		if entry.Requested {
			t.Errorf("%s migrated as requested", key)
		}
	}

	twelve, _ := file.PRs.Get("acme/widgets#12")
	if twelve.Slack.Channel != nil {
		t.Errorf("channel %q", *twelve.Slack.Channel)
	}
}

func dCursors(t *testing.T, file *reviews.File) {
	t.Helper()

	if file.DMCursorTS == nil {
		t.Fatal("the DM cursor did not carry over")
	}

	if *file.DMCursorTS != "1700000500.000000" {
		t.Errorf("DM cursor %q", *file.DMCursorTS)
	}

	if file.Cursors.Len() != 0 {
		t.Errorf("cursors %v", file.Cursors.Keys())
	}
}

// dTwoWorktrees is a version 1 file of #10 (its worktree under pr-10/app) and #11 (pr-11/app).
func dTwoWorktrees(t *testing.T, data string, only10 bool) *reviews.V1File {
	t.Helper()

	entry := func(number string) string {
		return dV1EntryJSON("[" + dLaunchJSON(1000, "pr-"+number,
			filepath.Join(data, "reviews", "pr-"+number, "app"), "null") + "]")
	}

	prs := `"10":` + entry("10")
	if !only10 {
		prs += `,"11":` + entry("11")
	}

	read := reviews.ReadFile(writeText(t, `{"version":1,"cursorTs":null,"prs":{`+prs+`}}`))
	if read.Kind != reviews.ReadV1 {
		t.Fatalf("read as %s", read.Kind)
	}

	return read.V1
}

// TestDMigrateTrustsALiveOriginOverAFolderName: #11's worktree was swept, jane/app is allowed and
// seen, and still no record goes to a stranger's repo.
func TestDMigrateTrustsALiveOriginOverAFolderName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	data := filepath.Join(root, "data")
	dCloneWithWorktree(t, root, "app", "https://github.com/acme/app",
		filepath.Join(data, "reviews", "pr-10", "app"))

	file, err := reviews.MigrateV1(dTwoWorktrees(t, data, false), reviews.MigrateContext{
		Allow: []string{"acme/*", "jane/app"}, Seen: []string{"jane/app"},
		OriginOf: origin.NewReader().Of, Exists: nil,
	})
	dMust(t, err)

	if !slices.Equal(dSortedKeys(file.PRs.Keys()), []string{"acme/app#10", "acme/app#11"}) {
		t.Errorf("keys %v", file.PRs.Keys())
	}
}

// TestDMigrateSendsDisagreeingOriginsToLegacy: two origins that disagree send the whole file to
// legacy, even when the folder name alone would name one allowed repo; so does an origin the
// list does not allow.
func TestDMigrateSendsDisagreeingOriginsToLegacy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	data := filepath.Join(root, "data")
	dCloneWithWorktree(t, root, "app", "https://github.com/acme/app",
		filepath.Join(data, "reviews", "pr-10", "app"))
	dCloneWithWorktree(t, root, "app2", "https://github.com/jane/app",
		filepath.Join(data, "reviews", "pr-11", "app"))

	reader := origin.NewReader()
	ctx := func(allow ...string) reviews.MigrateContext {
		return reviews.MigrateContext{
			Allow: allow, Seen: []string{}, OriginOf: reader.Of, Exists: nil,
		}
	}

	both := dTwoWorktrees(t, data, false)

	two, err := reviews.MigrateV1(both, ctx("acme/*", "jane/*"))
	dMust(t, err)

	if two.PRs.Len() != 0 {
		t.Errorf("placed %v", two.PRs.Keys())
	}

	if !slices.Equal(dSortedKeys(two.Legacy.Keys()), []string{"10", "11"}) {
		t.Errorf("legacy %v", two.Legacy.Keys())
	}

	if got := reviews.PlaceFile(both, ctx("acme/app", "jane/*")); got != "" {
		t.Errorf("PlaceFile = %q", got)
	}

	one, err := reviews.MigrateV1(dTwoWorktrees(t, data, true), ctx("jane/*"))
	dMust(t, err)

	if !slices.Equal(one.Legacy.Keys(), []string{"10"}) {
		t.Errorf("legacy %v", one.Legacy.Keys())
	}
}

// TestDMigrateTwiceChangesNothing: a second run reads version 2 and changes nothing, and an
// earlier good backup is never overwritten.
func TestDMigrateTwiceChangesNothing(t *testing.T) {
	t.Parallel()

	shaped := dRealShaped(t)
	earlier := strings.Replace(shaped.v1, `"cursorTs":"1700000000.000100"`, `"cursorTs":null`, 1)
	dMust(t, os.WriteFile(reviews.V1Backup(shaped.path), []byte(earlier), 0o600))

	first := dOpen(t, shaped.path, shaped.ctx)
	if !first.Writable {
		t.Fatalf("not writable: %s", first.Error)
	}

	text := dRead(t, shaped.path)
	second := dOpen(t, shaped.path, reviews.MigrateContext{
		Allow: []string{"other/*"}, Seen: []string{}, OriginOf: shaped.ctx.OriginOf, Exists: nil,
	})

	if second.Migrated != nil {
		t.Errorf("migrated again: %d", *second.Migrated)
	}

	if dMarshal(second.File) != dMarshal(first.File) {
		t.Error("the second open read another file")
	}

	if dRead(t, shaped.path) != text {
		t.Error("the file was rewritten")
	}

	if dRead(t, reviews.V1Backup(shaped.path)) != earlier {
		t.Error("the earlier backup was overwritten")
	}
}
