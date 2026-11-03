package sessions_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/sessions"
)

const (
	team    = "session-abcd1234"
	leadID  = "abcd1234-1111-4111-8111-aaaaaaaaaaaa"
	mateID  = "eeee5555-2222-4222-8222-bbbbbbbbbbbb"
	otherID = "99998888-1111-4111-8111-dddddddddddd"
)

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()

	err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

func at(seconds int) string {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	return base.Add(time.Duration(seconds) * time.Second).Format("2006-01-02T15:04:05.000Z")
}

func spawnLine(seconds int, name, teamName string) string {
	return `{"type":"user","timestamp":"` + at(seconds) + `","toolUseResult":{"status":` +
		`"teammate_spawned","name":"` + name + `","team_name":"` + teamName + `"}}`
}

func mateLines(seconds int, name, teamName string) []string {
	return []string{
		`{"type":"mode","mode":"default"}`,
		`{"type":"user","timestamp":"` + at(seconds) + `","teamName":"` + teamName +
			`","agentName":"` + name + `","message":{"content":"go"}}`,
	}
}

func TestReadTeamHead(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tests := []struct {
		name  string
		lines []string
		tag   string
		ok    bool
	}{
		{
			"after untagged lines", append([]string{`{"type":"x"}`}, mateLines(1, "dev", team)...),
			team + "/dev", true,
		},
		{"non-string values", []string{
			`{"teamName":5,"agentName":"x"}`, `not json`, `{"teamName":"","agentName":"x"}`,
		}, "", false},
		{"a giant line is skipped and cannot hide the next", []string{
			`{"teamName":"` + strings.Repeat("x", 5<<20) + `","agentName":"big"}`,
			mateLines(1, "dev", team)[1],
		}, team + "/dev", true},
		{"a tagged line straddling 64 KiB", []string{
			`{"pad":"` + strings.Repeat("p", 65_500) + `"}`, mateLines(1, "dev", team)[1],
		}, team + "/dev", true},
	}

	for index, tt := range tests {
		path := filepath.Join(dir, strconv.Itoa(index)+".jsonl")
		writeLines(t, path, tt.lines...)

		head := sessions.ReadTeamHead(path)

		got := ""
		if head.Tag != nil {
			got = head.Tag.Team + "/" + head.Tag.Name
		}

		if got != tt.tag {
			t.Errorf("%s: tag %q, want %q", tt.name, got, tt.tag)
		}

		if head.Decided != tt.ok {
			t.Errorf("%s: decided %v", tt.name, head.Decided)
		}
	}

	if sessions.ReadTeamHead(filepath.Join(dir, "missing.jsonl")).Tag != nil {
		t.Error("a missing file has a tag")
	}
}

func TestLeadPathFollowsTheSpawn(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mate := filepath.Join(dir, mateID+".jsonl")
	writeLines(t, mate, mateLines(10, "dev", team)...)

	leads := sessions.NewLeads()
	tag := sessions.TeamTag{Team: team, Name: "dev"}
	opts := sessions.LeadOpts{Limit: 0, FirstAt: nil, First: ""}

	if leads.Path(mate, tag, opts) != "" {
		t.Error("a lone teammate has a lead")
	}

	// A file named like the team that never spawned it is not its lead.
	named := filepath.Join(dir, leadID+".jsonl")
	writeLines(t, named, `{"type":"user","timestamp":"`+at(0)+`","message":{"content":"hi"}}`)

	if leads.Path(mate, tag, opts) != "" {
		t.Error("a file named like the team is its lead")
	}

	// A lead whose id shares nothing with the team is found by its spawn result.
	other := filepath.Join(dir, otherID+".jsonl")
	writeLines(t, other, spawnLine(5, "dev", team))

	if leads.Path(mate, tag, opts) != other {
		t.Errorf("lead %q", leads.Path(mate, tag, opts))
	}

	if leads.Path(mate, sessions.TeamTag{Team: team, Name: "team-lead"}, opts) != "" {
		t.Error("team-lead has a lead")
	}
}

func TestTheNearestSpawnBeforeTheTeammateStartedWins(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mate := filepath.Join(dir, mateID+".jsonl")
	early := filepath.Join(dir, "11112222-aaaa-4aaa-8aaa-111111111111.jsonl")
	near := filepath.Join(dir, "33334444-bbbb-4bbb-8bbb-222222222222.jsonl")
	late := filepath.Join(dir, "55556666-cccc-4ccc-8ccc-333333333333.jsonl")

	writeLines(t, mate, mateLines(100, "dev", team)...)
	writeLines(t, near, spawnLine(90, "dev", team))
	writeLines(t, early, spawnLine(10, "dev", team))
	writeLines(t, late, spawnLine(500, "dev", team))

	leads := sessions.NewLeads()

	got := leads.Path(mate, sessions.TeamTag{Team: team, Name: "dev"},
		sessions.LeadOpts{Limit: 0, FirstAt: nil, First: ""})
	if got != near {
		t.Errorf("lead %q, want the nearest spawn before the start", got)
	}
}

func TestFinderListsOnlyThisLeadsTeammates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lead := filepath.Join(dir, leadID+".jsonl")
	other := filepath.Join(dir, otherID+".jsonl")

	writeLines(t, lead, spawnLine(1, "dev", team), spawnLine(2, "qa", team))
	writeLines(t, other, spawnLine(1, "solo", "session-12345678"))
	writeLines(t, filepath.Join(dir, mateID+".jsonl"), mateLines(10, "dev", team)...)
	writeLines(t, filepath.Join(dir, "ffff6666-3333-4333-8333-cccccccccccc.jsonl"),
		mateLines(11, "qa", team)...)
	writeLines(t, filepath.Join(dir, "77776666-2222-4222-8222-eeeeeeeeeeee.jsonl"),
		mateLines(12, "solo", "session-12345678")...)
	writeLines(t, filepath.Join(dir, "00001111-2222-4222-8222-eeeeeeeeeeee.jsonl"),
		mateLines(13, "team-lead", team)...)

	finder := sessions.NewFinder(sessions.NewLeads(), lead, 0)

	found := finder.Find([]string{team, "session-12345678"})
	names := make([]string, 0, len(found))

	for _, mate := range found {
		names = append(names, mate.Name)
	}

	if strings.Join(names, ",") != "dev,qa" {
		t.Errorf("teammates %v", names)
	}
}
