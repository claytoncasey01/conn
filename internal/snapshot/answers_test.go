package snapshot_test

import (
	"compress/gzip"
	"io"
	"os"
	"strconv"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/snapshot"
	"github.com/claytoncasey01/conn/internal/transcript"
)

// The answers in testdata/answers/sessions/model.json.gz are recorded: seeded synthetic sessions
// (a lead transcript and its agents) fed through LeadState and AgentState, and the details,
// statuses and agent pages built from them at three clocks.

func readModel(t *testing.T) *js.Object {
	t.Helper()

	file, err := os.Open("../../testdata/answers/sessions/model.json.gz")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = file.Close() }()

	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := js.Parse(data)
	if err != nil {
		t.Fatal(err)
	}

	object, _ := parsed.(*js.Object)

	return object
}

func get(value any, key string) any {
	object, _ := value.(*js.Object)
	got, _ := object.Get(key)

	return got
}

func items(value any) []any {
	list, _ := value.([]any)

	return list
}

func number(value any) float64 {
	typed, _ := value.(float64)

	return typed
}

func str(value any) string {
	typed, _ := value.(string)

	return typed
}

func optional(value any) *string {
	typed, ok := value.(string)
	if !ok {
		return nil
	}

	return &typed
}

// normal is a value's JSON as JSON.parse then JSON.stringify would give it: what the code
// marshals, read back, so both sides compare as the same text.
func normal(t *testing.T, value any) string {
	t.Helper()

	parsed, err := js.Parse(js.Marshal(value))
	if err != nil {
		t.Fatalf("Go wrote JSON it cannot read: %v", err)
	}

	return js.Stringify(parsed)
}

// check compares got with the recorded answer and reports where they first part.
func check(t *testing.T, what string, got, want any) {
	t.Helper()

	gotText := normal(t, got)
	wantText := js.Stringify(want)

	if gotText == wantText {
		return
	}

	parted := 0
	for parted < min(len(gotText), len(wantText)) {
		if gotText[parted] != wantText[parted] {
			break
		}

		parted++
	}

	from := max(0, parted-120)
	t.Errorf("%s differs at byte %d:\n got  …%s\n want …%s", what, parted,
		gotText[from:min(len(gotText), parted+80)], wantText[from:min(len(wantText), parted+80)])
}

func meta(value any) transcript.AgentMeta {
	return transcript.AgentMeta{
		AgentType:    optional(get(value, "agentType")),
		Description:  optional(get(value, "description")),
		ToolUseID:    optional(get(value, "toolUseId")),
		SpawnDepth:   number(get(value, "spawnDepth")),
		RequestShape: optional(get(value, "requestShape")),
		ParentID:     optional(get(value, "parentId")),
	}
}

func live(value any) snapshot.Live {
	out := snapshot.Live{
		ID: str(get(value, "id")), PID: number(get(value, "pid")),
		SessionID: str(get(value, "sessionId")), Name: str(get(value, "name")),
		Cwd: str(get(value, "cwd")), Repo: optional(get(value, "repo")),
		Status: str(get(value, "status")), WaitingFor: nil,
		CanReply: get(value, "canReply") == true, Ask: nil,
		Shells: number(get(value, "shells")), PR: nil,
		StartedAt: number(get(value, "startedAt")), UpdatedAt: number(get(value, "updatedAt")),
		Team: nil,
	}

	pr := get(value, "pr")
	if pr != nil {
		out.PR = &snapshot.PR{Number: number(get(pr, "number")), URL: str(get(pr, "url"))}
	}

	team := get(value, "team")
	if team != nil {
		out.Team = &snapshot.Team{
			Name: str(get(team, "name")), LeadSessionID: str(get(team, "leadSessionId")),
			LeadID: optional(get(team, "leadId")),
		}
	}

	return out
}

func leadDump(lead *transcript.LeadState) *js.Object {
	dump := js.NewObject()

	for _, pair := range []struct {
		key   string
		value any
	}{
		{"mode", lead.Mode},
		{"branch", lead.Branch},
		{"model", lead.Model},
		{"calls", lead.Calls},
		{"errors", lead.Errors},
		{"advisorCalls", lead.AdvisorCalls},
		{"ctxTokens", lead.CtxTokens},
		{"activity", lead.Activity},
		{"lastAt", lead.LastAt},
		{"tools", lead.Tools},
		{"startedAt", lead.StartedAt},
		{"task", lead.Task},
		{"tokensOut", lead.TokensOut()},
		{"usd", lead.Meter.USD},
		{"unpriced", lead.Meter.Unpriced},
		{"toolCalls", lead.ToolCalls},
		{"consults", lead.Consults.List},
		{"resultIds", len(lead.ResultIDs)},
		{"owesReply", lead.OwesReply},
		{"inCall", lead.InCall()},
		{"title", lead.Title},
		{"finalText", lead.FinalText},
		{"cwd", lead.Cwd},
		{"teams", lead.Teams},
	} {
		dump.Set(pair.key, pair.value)
	}

	return dump
}

func agentDump(agent *transcript.AgentState, now float64) *js.Object {
	dump := js.NewObject()

	for _, pair := range []struct {
		key   string
		value any
	}{
		{"model", agent.Model},
		{"task", agent.Task},
		{"startedAt", agent.StartedAt},
		{"lastAt", agent.LastAt},
		{"tokensOut", agent.TokensOut()},
		{"usd", agent.Meter.USD},
		{"unpriced", agent.Meter.Unpriced},
		{"toolCalls", agent.ToolCalls},
		{"toolUseIds", len(agent.ToolUseIDs)},
		{"tools", agent.Tools},
		{"errors", agent.Errors},
		{"handback", agent.Handback},
		{"result", agent.Result},
		{"endedTurn", agent.EndedTurn},
		{"owesReply", agent.OwesReply},
		{"inCall", agent.InCall()},
		{"holds", agent.Holds()},
		{"lastAction", agent.LastAction},
		{"consults", agent.Consults.List},
		{"status", []string{
			agent.Status(now, false), agent.Status(now, true), agent.Status(now+200_000, false),
		}},
	} {
		dump.Set(pair.key, pair.value)
	}

	return dump
}

// unique counts a list's distinct values, as a Set's size does.
func unique(list any) int {
	seen := map[string]bool{}
	for _, item := range items(list) {
		seen[js.String(item)] = true
	}

	return len(seen)
}

// withCounts swaps the recorded id lists for their sizes, since the snapshot keeps them as sets.
func withCounts(dump any, keys ...string) any {
	object, _ := dump.(*js.Object)
	for _, key := range keys {
		value, _ := object.Get(key)
		object.Set(key, float64(unique(value)))
	}

	return object
}

func feed(recorded any) snapshot.Parts {
	lead := transcript.NewLeadState(int(number(get(recorded, "maxToolCalls"))))
	for _, line := range items(get(recorded, "lead")) {
		lead.Feed(line)
	}

	parts := snapshot.Parts{Lead: lead, Agents: nil}

	for _, raw := range items(get(recorded, "agents")) {
		agent := transcript.NewAgentState(str(get(raw, "id")), meta(get(raw, "meta")))
		for _, line := range items(get(raw, "lines")) {
			agent.Feed(line)
		}

		parts.Agents = append(parts.Agents, agent)
	}

	return parts
}

func TestSessionsMatchTheRecording(t *testing.T) {
	t.Parallel()

	answers := readModel(t)

	for index, recorded := range items(get(answers, "cases")) {
		name := "case " + strconv.Itoa(index)
		parts := feed(recorded)

		check(t, name+" lead state", leadDump(parts.Lead),
			withCounts(get(recorded, "lead_state"), "resultIds"))

		session := live(get(recorded, "live"))

		for _, out := range items(get(recorded, "outs")) {
			checkOut(t, name, parts, session, out)
		}
	}
}

func checkOut(t *testing.T, name string, parts snapshot.Parts, session snapshot.Live, out any) {
	t.Helper()

	now := number(get(out, "now"))
	name += " at " + js.Number(now)

	check(t, name+" detail", snapshot.Build(parts, session, now, snapshot.Opts{
		Trim: true, Kind: "adhoc", AdvisorModel: snapshot.AdvisorModel, Ended: false, EndedAt: nil,
	}), get(out, "detail"))

	if get(out, "full") != nil {
		check(t, name+" full", snapshot.Build(parts, session, now, snapshot.Opts{
			Trim: false, Kind: "review", AdvisorModel: "m", Ended: false, EndedAt: nil,
		}), get(out, "full"))
	}

	var endedAt *float64

	if get(out, "endedAt") != nil {
		value := number(get(out, "endedAt"))
		endedAt = &value
	}

	check(t, name+" ended", snapshot.Build(parts, session, now, snapshot.Opts{
		Trim: true, Kind: "build", AdvisorModel: "m", Ended: true, EndedAt: endedAt,
	}), get(out, "ended"))

	shown := make([]string, 0, 3)

	for _, status := range []string{"busy", "waiting", "idle"} {
		shown = append(shown, snapshot.ShownStatus(status, parts, now))
	}

	check(t, name+" shown", shown, get(out, "shown"))
	check(t, name+" agentsOf", snapshot.Agents(parts, now, false), get(out, "agentsOf"))

	recordedDumps := items(get(out, "agentDumps"))
	if len(recordedDumps) > 0 {
		dumps := make([]any, 0, len(parts.Agents))

		for index, agent := range parts.Agents {
			dumps = append(dumps, agentDump(agent, now))

			withCounts(recordedDumps[index], "toolUseIds")
		}

		check(t, name+" agent states", dumps, recordedDumps)
	}

	if len(items(get(out, "pages"))) > 0 {
		check(t, name+" pages", pages(parts, session, now), get(out, "pages"))
	}
}

func pages(parts snapshot.Parts, session snapshot.Live, now float64) []any {
	ids := make([]string, 0, len(parts.Agents)+2)
	ids = append(ids, "lead")

	for _, agent := range parts.Agents {
		ids = append(ids, agent.ID)
	}

	ids = append(ids, "missing")
	endedAt := 1_793_700_000_009.0
	named := "named"
	out := make([]any, 0, len(ids))

	for _, agentID := range ids {
		row := []any{}

		for _, opts := range []snapshot.AgentOpts{
			{ToolCallLimit: 5, Ended: false, EndedAt: nil, Name: nil, Status: nil},
			{ToolCallLimit: 0, Ended: true, EndedAt: &endedAt, Name: nil, Status: nil},
			{ToolCallLimit: 3, Ended: false, EndedAt: nil, Name: &named, Status: &session.Status},
		} {
			page, found := snapshot.BuildAgent(parts, agentID, now, opts)
			if !found {
				row = append(row, nil)

				continue
			}

			row = append(row, page)
		}

		out = append(out, row)
	}

	return out
}

func TestSummariesAndSpawnsMatchTheRecording(t *testing.T) {
	t.Parallel()

	answers := readModel(t)

	for index, row := range items(get(answers, "summaries")) {
		got := transcript.Summarize(str(get(row, "name")), get(row, "input"))
		if got != str(get(row, "out")) {
			t.Errorf("summary %d (%s): got %q, want %q", index, str(get(row, "name")), got,
				str(get(row, "out")))
		}
	}

	for index, row := range items(get(answers, "spawns")) {
		spawn, ok := transcript.SpawnOf(get(row, "line"))

		var got any
		if ok {
			got = spawn
		}

		check(t, "spawn "+strconv.Itoa(index), got, get(row, "out"))
	}
}
