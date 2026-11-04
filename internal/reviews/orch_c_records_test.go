package reviews_test

// What the Settings cards learn from Reviews (Bun's moduleStatus section), and a reviews.json
// that could not be opened for writing.

import (
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/ghbudget"
	"github.com/claytoncasey01/conn/internal/reviews"
)

const cSignedOutLine = "slack failing: Slack is signed out: sign in to the claude.ai Slack " +
	"connector"

// cLast is the last record.
func cLast(fakes *world) string { return cLastRecord(fakes, func(string) bool { return true }) }

func TestOrchCPollRecordsHowGitHubAnswered(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{
		noClient: true, settings: cSettings(settingsOpts{slack: new(false)}),
	})
	tickAndSettle(tab)
	cSame(t, "records", cRecords(fakes), []string{"github ok: Reviews read 1 open PR"})

	fakes.setSettings(settingsOf(settingsOpts{
		slack: new(false), allow: []string{"acme/*", cJaneTools},
	}))
	fakes.with(func(state *world) {
		state.searchError = cFailsFor("repo:jane/tools", "SAML enforcement")
	})
	fakes.advance(pollMs)
	tickAndSettle(tab)
	cIs(t, "a partial list", cLast(fakes),
		"github ok: Reviews read GitHub, but the search failed for 1 of 2 patterns")

	fakes.with(func(state *world) {
		state.searchError = func(string) string { return "" }
		state.listErr = errCHost
	})
	fakes.advance(2 * pollMs)
	tickAndSettle(tab)
	cIs(t, "a failure", cLast(fakes), "github failing: Reviews could not read GitHub")

	fakes.with(func(state *world) { state.listErr = &ghbudget.RateLimitError{ResetAt: nil} })
	fakes.advance(2 * pollMs)
	tickAndSettle(tab)
	cIs(t, "a rate limit", cLast(fakes),
		"github failing: Reviews is paused: GitHub's rate limit is used up")

	for _, line := range cRecords(fakes) {
		cIs(t, "Slack is off: no Slack line: "+line, strings.HasPrefix(line, "github"), true)
	}
}

func TestOrchCNoGitHubCallRecordsNothing(t *testing.T) {
	t.Parallel()

	tab, fakes := setup(t, setupOpts{
		noClient: true,
		settings: cSettings(settingsOpts{slack: new(false), requested: new(false)}),
	})
	tickAndSettle(tab)
	cIs(t, "no source on, nothing tracked: records", len(cRecords(fakes)), 0)
}

func TestOrchCChannelScanRecordsHowSlackAnswered(t *testing.T) {
	t.Parallel()

	signedOut := func() (reviews.ScanResult, error) {
		result := cEmptyScan()
		result.NeedsAuth = []string{cConnector}

		return result, nil
	}

	tests := []struct {
		name   string
		answer func() (reviews.ScanResult, error)
		want   string
	}{
		{
			name: "scanned", answer: cPosts(cPost(7)),
			want: "slack ok: Reviews scanned the Slack channels",
		},
		{
			name:   "signed out, failed",
			answer: cScanFails(cSlackOut("Slack scan failed: not signed in")), want: cSignedOutLine,
		},
		{name: "signed out, answered", answer: signedOut, want: cSignedOutLine},
		{
			name: "failed", answer: cScanFails(cScanErr("Slack scan failed: timed out")),
			want: "slack failing: Reviews could not scan the Slack channels",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tab, fakes := setup(t, setupOpts{settings: cSettings(settingsOpts{dms: new(false)})})
			fakes.with(func(state *world) { state.scanResult = tt.answer })
			tickAndSettle(tab)
			cIs(t, "scans", cScans(fakes), 1)
			cIs(t, "the last Slack line", cLastRecord(fakes, cSlackLine), tt.want)
		})
	}
}

func TestOrchCDMScanRecordsHowSlackAnswered(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		answer func() ([]reviews.DMFind, error)
		want   string
	}{
		{
			name: "scanned", answer: cDMOnly(cDM(7, cTS, cTS)),
			want: "slack ok: Reviews scanned your Slack DMs",
		},
		{name: "signed out", answer: cDMFails(cSlackOut(cSignedOut)), want: cSignedOutLine},
		{
			name: "failed", answer: cDMFails(errCBoom),
			want: "slack failing: Reviews could not scan your Slack DMs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tab, fakes := setup(t, setupOpts{
				settings: cSettings(settingsOpts{channels: []string{}}),
			})
			fakes.with(func(state *world) { state.dmResult = tt.answer })
			tickAndSettle(tab)
			cIs(t, "DM scans", len(cDMs(fakes)), 1)
			cIs(t, "no channel: only the DM scan read Slack", cScans(fakes), 0)
			cIs(t, "the last Slack line", cLastRecord(fakes, cSlackLine), tt.want)
		})
	}
}

func TestOrchCReadingWhatAChannelScanFoundRecordsHowGitHubAnswered(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		failures int
		want     string
	}{
		{name: "read", failures: 0, want: "github ok: Reviews read the PRs a Slack scan found"},
		{
			name: "failed", failures: 1,
			want: "github failing: Reviews could not read the PRs a Slack scan found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tab, fakes := setup(t, setupOpts{open: []*reviews.PR{prOf(nil)}, discovery: true})
			reads := cGated(t)

			fakes.with(func(state *world) {
				state.scanResult = cPosts(cPost(7))
				state.endedFailures = tt.failures
				state.endedGate = reads
			})
			tab.Tick()
			tab.Settle()
			// As in Bun, the DM scan of the same tick ends while the scan's read is out.
			tab.Tick()
			reads.reached(t)
			cWaitFor(t, "the DM scan", cRecorded(fakes, "slack ok: Reviews scanned your Slack DMs"))
			reads.open()
			tab.Settle()
			cIs(t, "only the scan find was read by number", cJSON(fakes.snapshotCalls().fetch),
				cJSON([][]string{{keyOf(7)}}))
			cIs(t, "the last line", cLastRecord(fakes, func(line string) bool {
				return strings.Contains(line, "a Slack scan found")
			}), tt.want)
		})
	}
}

// cRecorded is whether Reviews told the Settings cards line.
func cRecorded(fakes *world, line string) func() bool {
	return func() bool { return slices.Contains(cRecords(fakes), line) }
}
