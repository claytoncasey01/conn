package reviews_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

// Bun's reviewStore.test.ts, the copies openFile keeps before it writes over a file.

func dExists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

// dChmodBack restores a mode before t.TempDir's removal (cleanups run last in, first out).
func dChmodBack(t *testing.T, path string, mode os.FileMode) {
	t.Helper()

	t.Cleanup(func() { _ = os.Chmod(path, mode) })
}

// TestDBackupTakenGoesUnderTheNextNumber: a backup that is there but is another file is kept, the
// copy goes to .v1.2.bak, and no temp copy is left beside it.
func TestDBackupTakenGoesUnderTheNextNumber(t *testing.T) {
	t.Parallel()

	shaped := dRealShaped(t)
	broken := `{"version":1,"prs":{"12"`
	dMust(t, os.WriteFile(reviews.V1Backup(shaped.path), []byte(broken), 0o600))

	opened := dOpen(t, shaped.path, shaped.ctx)
	second := reviews.NthBackup(reviews.V1Backup(shaped.path), 2)

	if !opened.Writable {
		t.Fatalf("not writable: %s", opened.Error)
	}

	if second != shaped.path+".v1.2.bak" {
		t.Errorf("NthBackup = %s", second)
	}

	if opened.KeptAs != second {
		t.Errorf("kept as %q", opened.KeptAs)
	}

	if dRead(t, second) != shaped.v1 {
		t.Errorf("copy %s", dRead(t, second))
	}

	if dRead(t, reviews.V1Backup(shaped.path)) != broken {
		t.Error("the earlier file was written over")
	}

	if reviews.LoadFile(shaped.path).Version != 2 {
		t.Error("not migrated")
	}

	for _, name := range dEntries(t, filepath.Dir(shaped.path)) {
		if strings.HasSuffix(name, ".tmp") {
			t.Errorf("temp copy left: %s", name)
		}
	}
}

// TestDBackupASecondVersion1FileIsKeptToo: new, old, new: the second is copied beside the first,
// and a copy that is already this file is reused, with no third made.
func TestDBackupASecondVersion1FileIsKeptToo(t *testing.T) {
	t.Parallel()

	shaped := dRealShaped(t)

	if !dOpen(t, shaped.path, shaped.ctx).Writable {
		t.Fatal("the first open is not writable")
	}

	second := `{"version":1,"cursorTs":null,"prs":{"99":` + dV1EntryJSON("[]") + `}}`
	dMust(t, os.WriteFile(shaped.path, []byte(second), 0o600))

	opened := dOpen(t, shaped.path, shaped.ctx)
	if opened.KeptAs != shaped.path+".v1.2.bak" {
		t.Errorf("kept as %q", opened.KeptAs)
	}

	if dRead(t, reviews.V1Backup(shaped.path)) != shaped.v1 {
		t.Error("the first copy changed")
	}

	if dRead(t, shaped.path+".v1.2.bak") != second {
		t.Error("the second copy is not the second file")
	}

	// A run that stopped between the copy and the save: the same file again finds its copy.
	dMust(t, os.WriteFile(shaped.path, []byte(second), 0o600))

	again := dOpen(t, shaped.path, shaped.ctx)
	if again.KeptAs != shaped.path+".v1.2.bak" {
		t.Errorf("kept again as %q", again.KeptAs)
	}

	if dExists(shaped.path + ".v1.3.bak") {
		t.Error("a third copy was made")
	}
}

// TestDBackupThatCannotBeMadeMigratesNothing: in a read-only data folder nothing is migrated or
// written, and the file is not writable.
func TestDBackupThatCannotBeMadeMigratesNothing(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root writes and reads any file")
	}

	shaped := dRealShaped(t)
	folder := filepath.Dir(shaped.path)
	dMust(t, os.Chmod(folder, 0o500))
	dChmodBack(t, folder, 0o700)

	opened := dOpen(t, shaped.path, shaped.ctx)

	if opened.Writable {
		t.Fatal("writable")
	}

	if dMarshal(opened.File) != dMarshal(reviews.EmptyFile()) {
		t.Errorf("file %s", dMarshal(opened.File))
	}

	if !strings.Contains(opened.Error, "could not be migrated") {
		t.Errorf("error %q", opened.Error)
	}

	if dRead(t, shaped.path) != shaped.v1 {
		t.Error("the version 1 file was touched")
	}

	if dExists(reviews.V1Backup(shaped.path)) {
		t.Error("a backup was made")
	}
}

func dNoOrigin() reviews.MigrateContext {
	return reviews.MigrateContext{
		Allow: []string{"acme/*"}, Seen: []string{}, OriginOf: func(string) string { return "" },
		Exists: nil,
	}
}

// TestDBackupKeepsAnUnusableFileOnce: a reviews.json Conn cannot use is kept as
// .unreadable.bak, private, before anything is written over it; a second bad file goes under
// the next number.
func TestDBackupKeepsAnUnusableFileOnce(t *testing.T) {
	t.Parallel()

	for _, text := range []string{`{"version":2,"prs":{`, `[1,2]`, `{"version":3,"prs":{}}`} {
		t.Run(text, func(t *testing.T) {
			t.Parallel()

			path := writeText(t, text)
			bak := reviews.UnreadableBackup(path)

			opened := dOpen(t, path, dNoOrigin())
			if !opened.Writable {
				t.Fatalf("not writable: %s", opened.Error)
			}

			if opened.KeptAs != bak {
				t.Errorf("kept as %q", opened.KeptAs)
			}

			if dRead(t, bak) != text {
				t.Errorf("copy %q", dRead(t, bak))
			}

			if dMode(t, bak) != 0o600 {
				t.Errorf("copy mode %o", dMode(t, bak))
			}

			dMust(t, os.WriteFile(path, []byte("still bad"), 0o600))

			second := dOpen(t, path, dNoOrigin())
			if second.KeptAs != path+".unreadable.2.bak" {
				t.Errorf("second kept as %q", second.KeptAs)
			}

			if dRead(t, bak) != text {
				t.Error("the first copy was replaced")
			}

			if dRead(t, path+".unreadable.2.bak") != "still bad" {
				t.Error("the second copy is not the second file")
			}
		})
	}
}

// TestDBackupOfAFileThatCannotBeReadWritesNothing: a file that cannot be read cannot be copied,
// so nothing is written over it; a missing file is simply new.
func TestDBackupOfAFileThatCannotBeReadWritesNothing(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root writes and reads any file")
	}

	version1 := `{"version":1,"cursorTs":null,"prs":{"12":` + dV1EntryJSON("[]") + `}}`
	locked := writeText(t, version1)
	dMust(t, os.Chmod(locked, 0o000))
	dChmodBack(t, locked, 0o600)

	opened := dOpen(t, locked, dNoOrigin())

	if opened.Writable {
		t.Fatal("writable")
	}

	if !strings.Contains(opened.Error, "could not be kept aside") {
		t.Errorf("error %q", opened.Error)
	}

	dMust(t, os.Chmod(locked, 0o600))

	if dRead(t, locked) != version1 {
		t.Error("the locked file was written over")
	}

	fresh := dOpen(t, dFileIn(t), reviews.MigrateContext{
		Allow: []string{}, Seen: []string{}, OriginOf: func(string) string { return "" },
		Exists: nil,
	})
	want := reviews.Opened{
		File: fresh.File, Writable: true, Migrated: nil, KeptAs: "", Error: "",
	}

	if fresh != want {
		t.Errorf("a missing file opened as %+v", fresh)
	}

	if dMarshal(fresh.File) != dMarshal(reviews.EmptyFile()) {
		t.Errorf("a missing file is %s", dMarshal(fresh.File))
	}
}
