package history_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/history"
	"github.com/claytoncasey01/conn/internal/js"
)

func open(t *testing.T, path string) *history.DB {
	t.Helper()

	archive, err := history.Open(t.Context(), path, history.Options{
		OriginOf: func(string) string { return "" }, Now: func() float64 { return 1000 },
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = archive.Close() })

	return archive
}

func entry(sessionID, kind, outcome string, prNumber *float64) history.Entry {
	pages := js.NewObject()
	pages.Set("lead", map[string]any{"agent": "x"})

	return history.Entry{
		SessionID: sessionID, Name: "name " + sessionID, Cwd: "/work", Project: "work",
		Branch: "feature/x", Kind: kind, Model: "claude-opus-5-5", StartedAt: 100, EndedAt: 200,
		CostUSD: 1.5, Agents: 1, AdvisorCalls: 0, Errors: 0, Outcome: outcome,
		Repo: "acme/widgets", PRNumber: prNumber, TicketKey: nil, Source: "live",
		ResumedFrom: nil, TranscriptPath: "/t.jsonl", Detail: map[string]any{"id": sessionID},
		AgentDetails: pages, FirstPrompt: "fix the login", FinalMessage: "done",
		SearchText: "fix the login bug", Final: true, TranscriptSize: nil,
		Named: true,
	}
}

func TestOpenKeepsTheArchivePrivateAndRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "data")
	path := filepath.Join(dir, "history.db")
	open(t, path)

	for name, want := range map[string]os.FileMode{dir: 0o700, path: 0o600} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}

		if info.Mode().Perm() != want {
			t.Errorf("%s mode %v", name, info.Mode().Perm())
		}
	}

	garbage := filepath.Join(t.TempDir(), "history.db")

	err := os.WriteFile(garbage, []byte("not a database at all, just text"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	_, err = history.Open(t.Context(), garbage, history.Options{})
	if err == nil {
		t.Error("a text file opened as a database")
	}

	data, _ := os.ReadFile(garbage)
	if string(data) != "not a database at all, just text" {
		t.Error("the file was changed")
	}

	refusesANewerArchive(t)
}

func refusesANewerArchive(t *testing.T) {
	t.Helper()

	newer := filepath.Join(t.TempDir(), "history.db")

	conn, err := sql.Open("sqlite", newer)
	if err != nil {
		t.Fatal(err)
	}

	_, err = conn.ExecContext(t.Context(), "PRAGMA user_version = 9")
	if err != nil {
		t.Fatal(err)
	}

	_ = conn.Close()

	_, err = history.Open(t.Context(), newer, history.Options{})
	if err == nil {
		t.Fatal("a newer database opened")
	}

	if !strings.Contains(err.Error(), "version 9, this build understands up to 3") {
		t.Errorf("error %v", err)
	}
}

func TestALaterWriteKeepsWhatItCannotSay(t *testing.T) {
	t.Parallel()

	archive := open(t, filepath.Join(t.TempDir(), "history.db"))
	ctx := t.Context()
	prNumber := 42.0

	err := archive.Upsert(ctx, entry("s1", "build", "merged", &prNumber))
	if err != nil {
		t.Fatal(err)
	}

	err = archive.Upsert(ctx, entry("s1", "adhoc", "ended", nil))
	if err != nil {
		t.Fatal(err)
	}

	stored, found, err := archive.Get(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}

	if !found {
		t.Fatal("not found")
	}

	if stored.Row.Kind != "build" {
		t.Errorf("kind %s", stored.Row.Kind)
	}

	if stored.Row.Outcome != "merged" {
		t.Errorf("outcome %s", stored.Row.Outcome)
	}

	if stored.Row.PRNumber == nil {
		t.Error("the PR number was erased")
	}

	want := `{"ask":null,"sessionId":"s1","id":"s1","repo":"acme/widgets"}`
	if string(js.Marshal(stored.Detail)) != want {
		t.Errorf("detail %s", js.Marshal(stored.Detail))
	}
}

func TestListFiltersSearchAndTheAllowlist(t *testing.T) {
	t.Parallel()

	archive := open(t, filepath.Join(t.TempDir(), "history.db"))
	ctx := t.Context()

	for _, id := range []string{"s1", "s2"} {
		err := archive.Upsert(ctx, entry(id, "adhoc", "ended", nil))
		if err != nil {
			t.Fatal(err)
		}
	}

	hidden := entry("s3", "adhoc", "ended", nil)
	hidden.Repo = "other/secret"
	hidden.SearchText = "secret NEAR( * ^ \" text"

	err := archive.Upsert(ctx, hidden)
	if err != nil {
		t.Fatal(err)
	}

	allow := []string{"acme/*"}

	listed, err := archive.List(ctx, history.Query{Allow: &allow, ExcludeIDs: []string{"s2"}})
	if err != nil {
		t.Fatal(err)
	}

	if listed.Total != 1 {
		t.Errorf("total %v", listed.Total)
	}

	if strings.Join(listed.Facets.Repos, ",") != "acme/widgets" {
		t.Errorf("repos %v", listed.Facets.Repos)
	}

	searched, err := archive.List(ctx, history.Query{Text: `NEAR( * ^ "`})
	if err != nil {
		t.Fatalf("operators were not quoted: %v", err)
	}

	if searched.Total != 1 {
		t.Errorf("search total %v", searched.Total)
	}

	if history.FTSPhrase(`a "b"  c`) != `"a" """b""" "c"` {
		t.Errorf("phrase %s", history.FTSPhrase(`a "b"  c`))
	}
}

func TestOnlyACheckpointIsDeleted(t *testing.T) {
	t.Parallel()

	archive := open(t, filepath.Join(t.TempDir(), "history.db"))
	ctx := t.Context()
	checkpoint := entry("c1", "adhoc", "ended", nil)
	checkpoint.Final = false

	_ = archive.Upsert(ctx, checkpoint)
	_ = archive.Upsert(ctx, entry("f1", "adhoc", "ended", nil))

	removed, _ := archive.DeleteIfNotFinal(ctx, "c1")
	kept, _ := archive.DeleteIfNotFinal(ctx, "f1")

	if !removed {
		t.Error("the checkpoint stayed")
	}

	if kept {
		t.Error("a final row was deleted")
	}

	_ = archive.RecordFailure(ctx, "x", "boom")
	_ = archive.RecordFailure(ctx, "y", "boom")

	count, _ := archive.FailureCount(ctx)
	if count != 2 {
		t.Errorf("failures %v", count)
	}

	cleared, _ := archive.ClearFailuresExcept(ctx, []string{"x"}, 2000)
	if cleared != 1 {
		t.Errorf("cleared %d", cleared)
	}
}
