// Package snapshot builds the session picture from its state objects alone: no gh, ps, cmux or
// clock. The live store passes in what only the machine can tell (registry status, the PR, the
// terminal, shells); the archive passes fixed values.
package snapshot

import (
	"strconv"
	"strings"

	"github.com/claytoncasey01/conn/internal/ask"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/transcript"
)

// AdvisorModel is the model the advisor tool runs on, shown with every session.
const AdvisorModel = "claude-opus-5-5"

// Session statuses.
const (
	StatusBusy    = "busy"
	StatusWaiting = "waiting"
	StatusIdle    = "idle"
)

const (
	// IdleQuietMs is how long a session marked busy may go without a transcript line before it
	// is shown as idle. Claude keeps the registry at busy while background agents it started have
	// not reported back, so a session sitting at its prompt for hours would show as active for
	// ever. An agent that is not done holds busy as long after its own last line. The switcher
	// folds a stalled agent at the same quiet time (STALLED_MAX_MS in src/lib/format.ts).
	IdleQuietMs = 600_000
	// OpenCallMaxMs is how long something plausibly still working keeps a quiet busy session
	// busy: an agent inside a tool call, or a lead that owes a reply. It is capped because one
	// killed mid-call or mid-turn never writes again (STALLED_IN_CALL_MAX_MS in format.ts).
	OpenCallMaxMs = 30 * 60_000

	resultPreview = 400
	consultsKept  = 100
	logKept       = 80
	windowSmall   = 200_000
	windowLarge   = 1_000_000

	leadID = "lead"
)

// Parts is a session's transcripts, read: the lead and its subagents in the order they were found.
type Parts struct {
	Lead   *transcript.LeadState
	Agents []*transcript.AgentState
}

// PR is a session's pull request.
type PR struct {
	Number float64 `json:"number"`
	URL    string  `json:"url"`
}

// Team is set on a live session that is a teammate of a lead whose transcript is on disk.
type Team struct {
	Name          string  `json:"name"`
	LeadSessionID string  `json:"leadSessionId"`
	LeadID        *string `json:"leadId"`
}

// Live is what the transcripts cannot say: the registry, the process table, cmux and GitHub.
type Live struct {
	ID        string
	PID       float64
	SessionID string
	Name      string
	Cwd       string
	Repo      *string
	Status    string
	// WaitingFor says why a session waits; nil otherwise.
	WaitingFor *string
	CanReply   bool
	Ask        *ask.Ask
	Shells     float64
	PR         *PR
	StartedAt  float64
	// UpdatedAt is the registry's last update; the detail reports the later of this and the
	// lead's last line.
	UpdatedAt float64
	Team      *Team
}

// Opts are how a detail is built.
type Opts struct {
	// Trim is the live push: agent results clipped to a preview, only the latest consults and
	// log lines.
	Trim         bool
	Kind         string
	AdvisorModel string
	// Ended is set when the session is over: nothing is running or stalled any more.
	Ended bool
	// EndedAt is when it ended; the lead's last line when nil.
	EndedAt *float64
}

// LogLine is one line of a session's log.
type LogLine struct {
	At    js.Num `json:"at"`
	Who   string `json:"who"`
	Label string `json:"label"`
	Text  string `json:"text"`
}

// Detail is one session as the page shows it.
type Detail struct {
	ID            string                `json:"id"`
	PID           float64               `json:"pid"`
	SessionID     string                `json:"sessionId"`
	Name          string                `json:"name"`
	Kind          string                `json:"kind"`
	Cwd           string                `json:"cwd"`
	Project       string                `json:"project"`
	Repo          *string               `json:"repo"`
	Branch        string                `json:"branch"`
	Status        string                `json:"status"`
	CanReply      bool                  `json:"canReply"`
	Ask           *ask.Ask              `json:"ask"`
	WaitingFor    *string               `json:"waitingFor"`
	StartedAt     float64               `json:"startedAt"`
	UpdatedAt     float64               `json:"updatedAt"`
	Model         string                `json:"model"`
	Mode          string                `json:"mode"`
	PR            *PR                   `json:"pr"`
	Shells        float64               `json:"shells"`
	CtxTokens     float64               `json:"ctxTokens"`
	CtxWindow     float64               `json:"ctxWindow"`
	LeadCalls     int                   `json:"leadCalls"`
	AdvisorCalls  int                   `json:"advisorCalls"`
	AdvisorModel  string                `json:"advisorModel"`
	AgentsTotal   int                   `json:"agentsTotal"`
	AgentsRunning int                   `json:"agentsRunning"`
	Activity      string                `json:"activity"`
	CostUSD       float64               `json:"costUsd"`
	CostPartial   bool                  `json:"costPartial"`
	Lead          transcript.Agent      `json:"lead"`
	Consults      []*transcript.Consult `json:"consults"`
	Agents        []transcript.Agent    `json:"agents"`
	Log           []LogLine             `json:"log"`
	Tools         *js.Object            `json:"tools"`
	Errors        int                   `json:"errors"`
	Team          *Team                 `json:"team"`
	// Archived is set only on an archived session.
	Archived *Archived `json:"archived,omitempty"`
}

// Archived is where a finished session's record came from.
type Archived struct {
	EndedAt             float64 `json:"endedAt"`
	Source              string  `json:"source"`
	TranscriptAvailable bool    `json:"transcriptAvailable"`
	ResumedFrom         *string `json:"resumedFrom"`
}

// AgentDetail is one agent page: the agent with its whole result, and its last tool calls.
type AgentDetail struct {
	Agent     transcript.Agent       `json:"agent"`
	ToolCalls []*transcript.ToolCall `json:"toolCalls"`
}

// parentResultSeen is whether the lead has the result of the call that started agent.
func parentResultSeen(parts Parts, agent *transcript.AgentState) bool {
	if agent.Meta.ToolUseID == nil {
		return false
	}

	if *agent.Meta.ToolUseID == "" {
		return false
	}

	return parts.Lead.ResultIDs[*agent.Meta.ToolUseID]
}

// Agents is every subagent as an Agent, oldest first, with its parent and nested count worked
// out. Ended, none is left running or stalled.
func Agents(parts Parts, now float64, ended bool) []transcript.Agent {
	// An agent spawned by another agent: its toolUseId is one of that agent's own tool calls.
	owner := map[string]string{}

	for _, state := range parts.Agents {
		for id := range state.ToolUseIDs {
			owner[id] = state.ID
		}
	}

	nested := map[string]int{}
	parentOf := map[string]*string{}

	for _, state := range parts.Agents {
		parent := parentID(state, owner)
		parentOf[state.ID] = parent

		if parent != nil {
			if *parent != "" {
				nested[*parent]++
			}
		}
	}

	agents := make([]transcript.Agent, 0, len(parts.Agents))

	for _, state := range parts.Agents {
		agent := state.ToAgent(now, parentResultSeen(parts, state), parentOf[state.ID],
			nested[state.ID])
		if ended {
			if agent.Status != transcript.StatusDone {
				agent.Status = transcript.StatusDone
				endedAt := agent.LastAt
				agent.EndedAt = &endedAt
			}
		}

		agents = append(agents, agent)
	}

	js.Sort(agents, func(left, right transcript.Agent) float64 {
		return left.StartedAt - right.StartedAt
	})

	return agents
}

// parentID is meta.parentId ?? (meta.toolUseId ? owner.get(toolUseId) ?? null : null).
func parentID(state *transcript.AgentState, owner map[string]string) *string {
	if state.Meta.ParentID != nil {
		return state.Meta.ParentID
	}

	if state.Meta.ToolUseID == nil {
		return nil
	}

	if *state.Meta.ToolUseID == "" {
		return nil
	}

	id, ok := owner[*state.Meta.ToolUseID]
	if !ok {
		return nil
	}

	return &id
}

// ShownStatus is what a session's status shows: the registry's, except that busy becomes idle
// once nothing is plausibly working:
//   - the lead has written nothing for IdleQuietMs;
//   - no tool call of the lead is waiting for its result (however long it takes);
//   - the lead does not owe a reply, or has been quiet OpenCallMaxMs;
//   - every agent that is not done has been quiet IdleQuietMs, or OpenCallMaxMs when it is
//     inside a tool call.
//
// Waiting is never changed: someone is wanted. The store acts on this status too, so what is
// shown is what is acted on.
func ShownStatus(status string, parts Parts, now float64) string {
	if status != StatusBusy {
		return status
	}

	lead := parts.Lead
	if lead.LastAt == 0 {
		return status
	}

	if now-lead.LastAt <= IdleQuietMs {
		return status
	}

	if lead.InCall() {
		return status
	}

	if lead.OwesReply {
		if now-lead.LastAt < OpenCallMaxMs {
			return status
		}
	}

	if agentsHold(parts, now) {
		return status
	}

	return StatusIdle
}

// agentsHold is whether an agent that is not done keeps its session busy: quiet less than
// IdleQuietMs, or less than OpenCallMaxMs while it may still be working.
func agentsHold(parts Parts, now float64) bool {
	for _, agent := range parts.Agents {
		if agent.Status(now, parentResultSeen(parts, agent)) == transcript.StatusDone {
			continue
		}

		quiet := now - agent.LastAt
		if quiet < IdleQuietMs {
			return true
		}

		if agent.Holds() {
			if quiet < OpenCallMaxMs {
				return true
			}
		}
	}

	return false
}

// leadAgent is the lead as an agent-shaped record. Live, it runs while the session is busy or
// waiting and has no end time; once the session has ended it is done, ended at endedAt (its
// last line when nil).
func leadAgent(parts Parts, name, status string, ended bool, endedAt *float64) transcript.Agent {
	lead := parts.Lead

	agent := transcript.Agent{
		ID: leadID, Type: leadID, Description: name, Model: lead.Model,
		Status: transcript.StatusDone, Depth: 0, ParentID: nil, StartedAt: lead.StartedAt,
		LastAt: lead.LastAt, EndedAt: nil, Calls: lead.Calls, Errors: lead.Errors,
		Nested: len(parts.Agents), LastAction: lead.Activity, Tools: lead.Tools,
		TokensOut: lead.TokensOut(), CostUSD: lead.Meter.USD, Task: lead.Task, Result: "",
		InCall: nil,
	}

	if !ended {
		if status != StatusIdle {
			agent.Status = transcript.StatusRunning
		}

		return agent
	}

	end := lead.LastAt
	if endedAt != nil {
		end = *endedAt
	}

	agent.EndedAt = &end

	return agent
}

// basename is node's path.basename for a POSIX path.
func basename(path string) string {
	trimmed := strings.TrimRight(path, "/")

	return trimmed[strings.LastIndexByte(trimmed, '/')+1:]
}

// Build is one session's detail at now.
func Build(parts Parts, live Live, now float64, opts Opts) Detail {
	lead := parts.Lead

	status := live.Status
	if !opts.Ended {
		status = ShownStatus(live.Status, parts, now)
	}

	agents := Agents(parts, now, opts.Ended)
	running := 0

	for index := range agents {
		// Live, the full report rides only on the agent page; the push carries a preview.
		if opts.Trim {
			if js.Len(agents[index].Result) > resultPreview {
				agents[index].Result = js.Cut(agents[index].Result, resultPreview) + "…"
			}
		}

		if agents[index].Status == transcript.StatusRunning {
			running++
		}
	}

	consults := allConsults(parts)
	log := sessionLog(agents, consults)

	window := float64(windowSmall)
	if lead.CtxTokens > windowSmall {
		window = windowLarge
	}

	detail := Detail{
		ID: live.ID, PID: live.PID, SessionID: live.SessionID, Name: live.Name, Kind: opts.Kind,
		Cwd: live.Cwd, Project: basename(live.Cwd), Repo: live.Repo, Branch: lead.Branch,
		Status: status, CanReply: live.CanReply, Ask: live.Ask, WaitingFor: live.WaitingFor,
		StartedAt: live.StartedAt, UpdatedAt: max(live.UpdatedAt, lead.LastAt),
		Model: lead.Model, Mode: lead.Mode, PR: live.PR, Shells: live.Shells,
		CtxTokens: lead.CtxTokens, CtxWindow: window, LeadCalls: lead.Calls,
		AdvisorCalls: len(consults), AdvisorModel: opts.AdvisorModel,
		AgentsTotal: len(agents), AgentsRunning: running, Activity: lead.Activity,
		CostUSD: lead.Meter.USD + agentsUSD(parts), CostPartial: unpriced(parts) > 0,
		Lead:     leadAgent(parts, live.Name, status, opts.Ended, opts.EndedAt),
		Consults: consults, Agents: agents, Log: log, Tools: allTools(lead.Tools, agents),
		Errors: lead.Errors + agentErrors(agents), Team: live.Team, Archived: nil,
	}

	if opts.Trim {
		detail.Consults = last(consults, consultsKept)
		detail.Log = last(log, logKept)
	}

	return detail
}

func last[T any](items []T, count int) []T {
	if len(items) <= count {
		return items
	}

	return items[len(items)-count:]
}

// agentsUSD is parts.agents.reduce((n, a) => n + a.meter.usd, 0), in that order.
func agentsUSD(parts Parts) float64 {
	total := 0.0
	for _, agent := range parts.Agents {
		total += agent.Meter.USD
	}

	return total
}

func unpriced(parts Parts) float64 {
	total := 0.0
	for _, agent := range parts.Agents {
		total += agent.Meter.Unpriced
	}

	return parts.Lead.Meter.Unpriced + total
}

func agentErrors(agents []transcript.Agent) int {
	total := 0
	for _, agent := range agents {
		total += agent.Errors
	}

	return total
}

// allConsults is the lead's consults and every agent's, by time.
func allConsults(parts Parts) []*transcript.Consult {
	consults := append([]*transcript.Consult{}, parts.Lead.Consults.List...)
	for _, agent := range parts.Agents {
		consults = append(consults, agent.Consults.List...)
	}

	js.Sort(consults, func(left, right *transcript.Consult) float64 {
		return float64(left.At) - float64(right.At)
	})

	return consults
}

func sessionLog(agents []transcript.Agent, consults []*transcript.Consult) []LogLine {
	log := []LogLine{}

	for _, agent := range agents {
		named := agent.Description
		if named == "" {
			named = agent.Type
		}

		log = append(log, LogLine{
			At: js.Num(agent.StartedAt), Who: "agent", Label: agent.Type,
			Text: "spawned · " + named,
		})

		if agent.Status != transcript.StatusDone {
			continue
		}

		if agent.EndedAt == nil {
			continue
		}

		if *agent.EndedAt == 0 {
			continue
		}

		returned := "returned · " + strconv.Itoa(agent.Calls) + " calls"
		if agent.Errors > 0 {
			returned += ", " + strconv.Itoa(agent.Errors) + " errors"
		}

		log = append(log, LogLine{
			At: js.Num(*agent.EndedAt), Who: "agent", Label: agent.Type, Text: returned,
		})
	}

	for _, consult := range consults {
		said := "consulted"
		if consult.By != leadID {
			said = "consulted by " + consult.ByLabel
		}

		log = append(log, LogLine{At: consult.At, Who: "advisor", Label: "advisor", Text: said})
	}

	js.Sort(log, func(left, right LogLine) float64 {
		return float64(left.At) - float64(right.At)
	})

	return log
}

// allTools is the lead's tool counts plus every agent's.
func allTools(lead *js.Object, agents []transcript.Agent) *js.Object {
	tools := js.NewObject()

	for _, key := range lead.Keys() {
		value, _ := lead.Get(key)
		tools.Set(key, value)
	}

	for _, agent := range agents {
		for _, key := range agent.Tools.Keys() {
			value, _ := agent.Tools.Get(key)
			current, _ := tools.Get(key)
			sum, _ := current.(float64)
			add, _ := value.(float64)
			tools.Set(key, sum+add)
		}
	}

	return tools
}

// AgentOpts are how an agent page is built. A nil Name or Status is an archived session's: the
// lead is named by its transcript title and counts as running unless Ended.
type AgentOpts struct {
	// ToolCallLimit is how many of the last tool calls to keep; 0 or less keeps them all.
	ToolCallLimit int
	Ended         bool
	EndedAt       *float64
	Name          *string
	Status        *string
}

// BuildAgent is one agent page, or false when the session has no such agent.
func BuildAgent(parts Parts, agentID string, now float64, opts AgentOpts) (AgentDetail, bool) {
	if agentID == leadID {
		return leadPage(parts, now, opts), true
	}

	agents := Agents(parts, now, opts.Ended)

	for _, agent := range agents {
		if agent.ID != agentID {
			continue
		}

		for _, state := range parts.Agents {
			if state.ID == agentID {
				return AgentDetail{Agent: agent, ToolCalls: tail(state.ToolCalls, opts)}, true
			}
		}
	}

	return AgentDetail{}, false
}

func leadPage(parts Parts, now float64, opts AgentOpts) AgentDetail {
	name := parts.Lead.Title
	if name == "" {
		name = leadID
	}

	if opts.Name != nil {
		name = *opts.Name
	}

	status := StatusBusy
	if opts.Status != nil {
		status = *opts.Status
		if !opts.Ended {
			status = ShownStatus(*opts.Status, parts, now)
		}
	}

	agent := leadAgent(parts, name, status, opts.Ended, opts.EndedAt)

	return AgentDetail{Agent: agent, ToolCalls: tail(parts.Lead.ToolCalls, opts)}
}

// tail is calls.slice(-limit): the last limit calls, all of them for no limit (and, as slice(-0)
// does, for a limit of 0).
func tail(calls []*transcript.ToolCall, opts AgentOpts) []*transcript.ToolCall {
	if opts.ToolCallLimit <= 0 {
		return calls
	}

	return last(calls, opts.ToolCallLimit)
}
