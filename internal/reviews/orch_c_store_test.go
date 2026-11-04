package reviews_test

// A reviews.json that could not be opened for writing: REVIEW and Place wait with the reason, and
// fixing it needs no restart.

import (
	"regexp"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

var (
	cNotMigrated = regexp.MustCompile(
		`could not be migrated: .*save Settings or press REFRESH on Reviews to try again`)
	cNotSaved = regexp.MustCompile(
		`reviews.json cannot be saved, so this would be lost at the next restart`)
)

const cOldLegacy = `{"version":1,"cursorTs":null,"prs":{"41":{"slack":null,"slackAskAt":null,` +
	`"firstSeenAt":1799999999000,"launches":[],"scan":{"tries":0,"lastAt":0},` +
	`"threadScan":{"tries":0,"lastAt":0,"headSha":""}}}}`

func TestOrchCFileThatCouldNotBeOpenedForWritingHoldsReviewAndPlace(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{settings: cSetupSettings()})
	cWriteV1(t, fakes.file, cOldLegacy)
	// The data folder is read-only: no copy can be made, so nothing is migrated or written.
	cChmod(t, cDataDir(fakes), 0o500, 0o700)
	cChanged(tab, fakes, settingsOpts{})
	tickAndSettle(tab)

	why := cText(view(t, tab).StoreError)
	cIs(t, "storeError "+why, cNotMigrated.MatchString(why), true)
	cSame(t, "the list still works", cRowKeys(t, tab), []string{keyOf(7)})

	press := cStart(t, tab, worldRepo, "7")
	cIs(t, "REVIEW", press.Code, 409)
	cIs(t, "REVIEW's reason "+cReason(press), cNotSaved.MatchString(cReason(press)), true)
	cIs(t, "nothing launched", len(cLaunches(fakes)), 0)
	cIs(t, "Place", cPlace(t, tab, "all", worldRepo).Code, 409)

	// A press that changes nothing on disk opens it again and stays as it was.
	cRefresh(t, tab)
	cIs(t, "storeError after a press", view(t, tab).StoreError != nil, true)

	cChmod(t, cDataDir(fakes), 0o700, 0o700)

	// The cause is gone: REFRESH opens it again, migrates, and REVIEW is remembered on disk.
	cRefresh(t, tab)
	cIs(t, "storeError", cText(view(t, tab).StoreError), "<null>")
	cIs(t, "version", reviews.LoadFile(fakes.file).Version, 2)

	tickAndSettle(tab)
	cIs(t, "REVIEW", cStart(t, tab, worldRepo, "7").Code, 200)

	entry := cEntry(fakes.file, keyOf(7))
	if entry == nil {
		t.Fatal("no record of 7")
	}

	cIs(t, "launches", len(entry.Launches), 1)
}

func TestOrchCSettingsSaveOpensAFileThatCouldNotBeWrittenAgain(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{settings: cSetupSettings()})
	cWriteV1(t, fakes.file, `{"version":1,"cursorTs":null,"prs":{}}`)
	cChmod(t, cDataDir(fakes), 0o500, 0o700)
	cChanged(tab, fakes, settingsOpts{})
	cIs(t, "storeError", view(t, tab).StoreError != nil, true)

	cChmod(t, cDataDir(fakes), 0o700, 0o700)
	cChanged(tab, fakes, settingsOpts{allow: []string{"acme/*", cJaneTools}})
	cIs(t, "storeError", cText(view(t, tab).StoreError), "<null>")
}
