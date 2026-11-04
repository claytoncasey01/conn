package reviews_test

import (
	"os"
	"slices"
	"testing"

	"github.com/claytoncasey01/conn/internal/reviews"
)

func TestFirstPollBuildsRowsFromRequestsMeAndSavesPrivately(t *testing.T) {
	t.Parallel()

	tab, fixture := setup(t, setupOpts{open: []*reviews.PR{
		prOf(func(pr *reviews.PR) { pr.Requested = []string{worldMe} }),
		prOf(func(pr *reviews.PR) { pr.Number = 8 }),
		prOf(func(pr *reviews.PR) {
			pr.Number, pr.Author, pr.Requested = 9, worldMe, []string{worldMe}
		}),
	}})

	tickAndSettle(tab)
	checkFirstView(t, tab)
	checkFirstCalls(t, fixture)
}

// checkFirstView checks the view after the first poll: #7 asks me on GitHub; #8 asks nobody;
// #9 is my own.
func checkFirstView(t *testing.T, tab *reviews.Reviews) {
	t.Helper()

	shown := view(t, tab)
	if !shown.Enabled {
		t.Error("the tab is not enabled")
	}

	if shown.Me == nil {
		t.Fatal("me is not known")
	}

	if *shown.Me != worldMe {
		t.Errorf("me = %s", *shown.Me)
	}

	if shown.LastPollAt == nil {
		t.Fatal("no poll")
	}

	keys := make([]string, 0, len(shown.Rows))
	for _, row := range shown.Rows {
		keys = append(keys, row.Key)
	}

	if !slices.Equal(keys, []string{keyOf(7)}) {
		t.Errorf("rows %v", keys)
	}
}

// checkFirstCalls checks what the first poll asked gh, and the file it saved privately.
func checkFirstCalls(t *testing.T, fixture *world) {
	t.Helper()

	got := fixture.snapshotCalls()
	if got.login != 1 {
		t.Errorf("login %d", got.login)
	}

	if len(got.search) != 1 {
		t.Fatalf("searches %v", got.search)
	}

	if got.search[0][0] != "is:pr is:open archived:false user-review-requested:@me user:acme" {
		t.Errorf("search %v", got.search)
	}

	info, err := os.Stat(fixture.file)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode())
	}

	file := reviews.LoadFile(fixture.file)
	if !slices.Equal(file.PRs.Keys(), []string{keyOf(7)}) {
		t.Errorf("records %v", file.PRs.Keys())
	}
}
