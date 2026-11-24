package reviews_test

// CONN_UPDATE_GOLDEN=1 rewrites the recorded answers that are the shape of a row (the derived
// rows and the dry view's snapshots) from the code, so a deliberate change to a row shows as the
// diff of testdata/answers/reviews/pure.json.gz. The other answers are left as they were
// recorded.

import (
	"compress/gzip"
	"os"
	"testing"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/reviews"
	"github.com/claytoncasey01/conn/internal/settings"
)

const (
	answersFile     = "../../testdata/answers/reviews/pure.json.gz"
	updateGoldenEnv = "CONN_UPDATE_GOLDEN"
)

// deriveOf is the row the code derives for one recorded deriveRow case.
func deriveOf(t *testing.T, row any) *reviews.Row {
	t.Helper()

	pull := decode[reviews.PR](t, get(row, "pr"))
	tracking := decode[reviews.Entry](t, get(row, "tracking"))
	recorded := decode[ctxJSON](t, get(row, "ctx"))

	var channels []string
	if recorded.Sources.Channels != nil {
		channels = *recorded.Sources.Channels
	}

	return reviews.DeriveRow(&pull, &tracking, &reviews.DeriveContext{
		Me: recorded.Me, Session: recorded.Session, Now: recorded.Now,
		Sources: reviews.Sources{
			Requested: recorded.Sources.Requested, Channels: channels,
			DMs: recorded.Sources.DMs, AllOpen: recorded.Sources.AllOpen,
		},
	}, str(get(row, "key")))
}

// valueOf is a Go value as the answers file holds it.
func valueOf(t *testing.T, got any) any {
	t.Helper()

	parsed, err := js.Parse(js.Marshal(got))
	if err != nil {
		t.Fatal(err)
	}

	return parsed
}

func writeAnswers(t *testing.T, data *js.Object) {
	t.Helper()

	file, err := os.Create(answersFile)
	if err != nil {
		t.Fatal(err)
	}

	writer := gzip.NewWriter(file)

	_, err = writer.Write([]byte(js.Stringify(data)))
	if err != nil {
		t.Fatal(err)
	}

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}

	err = file.Close()
	if err != nil {
		t.Fatal(err)
	}
}

// TestUpdateTheRecordedRows is serial on purpose: the parallel tests wait until it is done, so
// they read the rewritten file.
func TestUpdateTheRecordedRows(t *testing.T) {
	if os.Getenv(updateGoldenEnv) != "1" {
		t.Skip(updateGoldenEnv + "=1 rewrites the recorded rows")
	}

	// Setting the variable to what it is makes this test serial: t.Setenv forbids t.Parallel.
	t.Setenv(updateGoldenEnv, "1")

	data := answers(t)

	for _, row := range section(t, data, "deriveRow") {
		recorded, _ := row.(*js.Object)
		recorded.Set("out", valueOf(t, deriveOf(t, row)))
	}

	dry := get(data, "dry")
	cfg := config.FromEnv(func(string) (string, bool) { return "", false })
	tab := reviews.New(t.Context(), reviews.Deps{
		Settings: settings.NewStore(nil), Now: func() float64 { return answersT0 + 123 },
		Config: reviews.Config{Dry: true, NamePattern: cfg.ReviewNamePattern},
	})

	recordedDry, _ := dry.(*js.Object)
	recordedDry.Set("snapshot", valueOf(t, tab.Snapshot()))

	_, err := tab.Refresh()
	if err != nil {
		t.Fatal(err)
	}

	recordedDry.Set("after", valueOf(t, tab.Snapshot()))

	writeAnswers(t, data)
}
