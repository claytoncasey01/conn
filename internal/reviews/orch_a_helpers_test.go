package reviews_test

// Helpers for the orchestrator tests from the view's chosen channels up to auto-close. Every
// name starts with `a` so it cannot clash with the helpers of the other orchestrator files.

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
	"github.com/claytoncasey01/conn/internal/testtime"
)

const (
	aCodeReview = "#code-review"
	aShipIt     = "#ship-it"
	aGadgets    = "acme/gadgets"
	aTools      = "jane/tools"
	aSonnet     = "sonnet"
	aOpus       = "opus"
	aTS         = "1799999000.000100"
	aSignedOut  = "claude.ai Slack"
	aThreadTS   = "1799990000.000200"
	aThreadLink = "https://acme.slack.com/archives/C1/p7"
)

// The failures the fakes answer.
var (
	errANotLoggedIn = errors.New("gh: not logged in")
	errANoGh        = errors.New("gh: command not found")
	errATimedOut    = errors.New("helper timed out")
	errASearch      = errors.New("search failed")
	errABoom        = errors.New("boom")
)

// aRequested is PR number of acme/widgets (or of repo), requesting me.
func aRequested(number int64, repo ...string) *reviews.PR {
	return prOf(func(pr *reviews.PR) {
		pr.Number, pr.Requested = number, []string{worldMe}
		if len(repo) > 0 {
			pr.Repo = repo[0]
		}
	})
}

// aNumbered is PR number of acme/widgets (or of repo), requesting nobody.
func aNumbered(number int64, repo ...string) *reviews.PR {
	return prOf(func(pr *reviews.PR) {
		pr.Number = number
		if len(repo) > 0 {
			pr.Repo = repo[0]
		}
	})
}

// aReviewedPR is PR 7 requesting me, with my review on it.
func aReviewedPR() *reviews.PR {
	return prOf(func(pr *reviews.PR) {
		pr.Requested, pr.Reviews = []string{worldMe}, []reviews.Review{myReview(nil)}
	})
}

// aSettingsOf is the settings as a setup option.
func aSettingsOf(opts settingsOpts) *settings.Settings {
	current := settingsOf(opts)

	return &current
}

// aPost is a channel post as the scan reports it (in #code-review, about acme/widgets).
func aPost(number int64, stamp string, repo ...string) reviews.Post {
	name := worldRepo
	if len(repo) > 0 {
		name = repo[0]
	}

	digits := strconv.FormatInt(number, 10)

	return reviews.Post{
		Repo: name, Number: number, Key: keyOf(number, name), Channel: aCodeReview, TS: stamp,
		Permalink: "https://acme.slack.com/archives/C1/p" + digits,
	}
}

// aDM is a DM as the DM scan reports it.
func aDM(number int64, firstTS, lastTS string) reviews.DMFind {
	return reviews.DMFind{
		Repo: worldRepo, Number: number, Key: keyOf(number), FirstTS: firstTS,
		Permalink: "https://acme.slack.com/archives/D000/p" + strings.ReplaceAll(firstTS, ".", ""),
		LastTS:    lastTS,
	}
}

// aEmpty is a scan that found nothing.
func aEmpty() reviews.ScanResult {
	return reviews.ScanResult{Posts: []reviews.Post{}, Asks: []reviews.Ask{}, NeedsAuth: []string{}}
}

// aPosts is a scan answer with these posts.
func aPosts(posts ...reviews.Post) func() (reviews.ScanResult, error) {
	return func() (reviews.ScanResult, error) {
		result := aEmpty()
		result.Posts = posts

		return result, nil
	}
}

// aNeedsAuth is a scan answer that finds Slack signed out.
func aNeedsAuth() (reviews.ScanResult, error) {
	result := aEmpty()
	result.NeedsAuth = []string{aSignedOut}

	return result, nil
}

// aEmptyScan is a scan answer that found nothing.
func aEmptyScan() (reviews.ScanResult, error) { return aEmpty(), nil }

// aDMs is a DM scan answer with these DMs.
func aDMs(found ...reviews.DMFind) func() ([]reviews.DMFind, error) {
	return func() ([]reviews.DMFind, error) { return found, nil }
}

// aSince is where a scan read channel from ("<nil>" for the first-scan window, "<none>" when
// it did not read the channel).
func aSince(input reviews.ScanInput, channel string) string {
	for _, read := range input.Channels {
		if read.Name == channel {
			return aText(read.SinceTS)
		}
	}

	return "<none>"
}

// aText is a text that may be missing, "<nil>" when it is.
func aText(value *string) string {
	if value == nil {
		return "<nil>"
	}

	return *value
}

// aNumbers are the rows' PR numbers, in the order shown.
func aNumbers(rows []*reviews.Row) []int64 {
	out := make([]int64, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Number)
	}

	return out
}

// aSortedNumbers are the rows' PR numbers, sorted.
func aSortedNumbers(rows []*reviews.Row) []int64 {
	out := aNumbers(rows)
	slices.Sort(out)

	return out
}

// aKeys are the rows' keys, in the order shown.
func aKeys(rows []*reviews.Row) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Key)
	}

	return out
}

// aSortedKeys are the rows' keys, sorted.
func aSortedKeys(rows []*reviews.Row) []string {
	out := aKeys(rows)
	slices.Sort(out)

	return out
}

// aStates are the rows' "<number> <state> <ended>", in the order shown.
func aStates(rows []*reviews.Row) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, fmt.Sprintf("%d %s %s", row.Number, row.State, aText(row.Ended)))
	}

	return out
}

// aRowsBy is what of each row, in the order shown.
func aRowsBy(rows []*reviews.Row, what func(row *reviews.Row) string) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, what(row))
	}

	return out
}

// aRow is the row of PR number, or nil.
func aRow(rows []*reviews.Row, number int64) *reviews.Row {
	for _, row := range rows {
		if row.Number == number {
			return row
		}
	}

	return nil
}

// aEqual fails the test when got is not want.
func aEqual(t *testing.T, label string, got, want any) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", label, got, want)
	}
}

// aOnce is one tick, waited out.
func aOnce(tab *reviews.Reviews) {
	tab.Tick()
	tab.Settle()
}

// aRefresh presses REFRESH.
func aRefresh(t *testing.T, tab *reviews.Reviews) int {
	t.Helper()

	seq, err := tab.Refresh()
	if err != nil {
		t.Fatal(err)
	}

	return seq
}

// aEntry is the saved record of key (a failure when there is none).
func aEntry(t *testing.T, fakes *world, key string) *reviews.Entry {
	t.Helper()

	entry, found := reviews.LoadFile(fakes.file).PRs.Get(key)
	if !found {
		t.Fatalf("no record of %s", key)
	}

	return entry
}

// aCursor is the saved cursor of #code-review ("<none>" when there is none).
func aCursor(fakes *world) string {
	cursor, found := reviews.LoadFile(fakes.file).Cursors.Get(aCodeReview)
	if !found {
		return "<none>"
	}

	return cursor
}

// aScans is how many channel scans ran.
func aScans(fakes *world) int { return len(fakes.snapshotCalls().scan) }

// aSession is a live session.
func aSession(name, cwd, status, sessionID string, pid float64) sessions.LiveSession {
	return sessions.LiveSession{
		Name: name, Cwd: cwd, Status: status, SessionID: sessionID, PID: pid,
	}
}

// aStart presses REVIEW (a failure on a program fault).
func aStart(t *testing.T, tab *reviews.Reviews, repo, number, model any) reviews.Reply {
	t.Helper()

	reply, err := tab.Start(t.Context(), repo, number, model)
	if err != nil {
		t.Fatal(err)
	}

	return reply
}

// aField is a field of a reply's body ("<none>" when it has none).
func aField(reply reviews.Reply, name string) string {
	if reply.Body == nil {
		return "<none>"
	}

	value, found := reply.Body.Get(name)
	if !found {
		return "<none>"
	}

	return fmt.Sprint(value)
}

// aMinutes is the clock at T0 plus minutes.
func aMinutes(minutes float64) float64 { return worldT0 + minutes*minuteMs }

// aSortedStrings is a sorted copy of texts.
func aSortedStrings(texts []string) []string {
	out := slices.Clone(texts)
	slices.Sort(out)

	return out
}

// aWaitRecord waits until Reviews told the Settings cards something that contains text.
func aWaitRecord(t *testing.T, fakes *world, text string) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(gateWait))

	for time.Now().Before(deadline) {
		found := false

		fakes.with(func(fakes *world) {
			found = slices.ContainsFunc(fakes.records, func(record string) bool {
				return strings.Contains(record, text)
			})
		})

		if found {
			return
		}

		time.Sleep(time.Millisecond)
	}

	t.Fatalf("no record with %q", text)
}

// aBody is a reply's body as JSON.
func aBody(reply reviews.Reply) string { return js.Stringify(reply.Body) }
