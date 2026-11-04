package transcript

import (
	"slices"

	"github.com/claytoncasey01/conn/internal/js"
)

// Agent statuses.
const (
	StatusRunning = "running"
	StatusDone    = "done"
	StatusStalled = "stalled"
)

// AgentMeta is what a subagent's meta file says. A nil field is one the file leaves out.
type AgentMeta struct {
	AgentType   *string
	Description *string
	ToolUseID   *string
	SpawnDepth  float64
	// RequestShape is "background" for an agent the lead does not wait on.
	RequestShape *string
	// ParentID is the agent that owns this one, when its meta file cannot say (a teammate's own
	// subagent).
	ParentID *string
}

// Agent is a subagent (or the lead) as the page draws it.
type Agent struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	Description string     `json:"description"`
	Model       string     `json:"model"`
	Status      string     `json:"status"`
	Depth       float64    `json:"depth"`
	ParentID    *string    `json:"parentId"`
	StartedAt   float64    `json:"startedAt"`
	LastAt      float64    `json:"lastAt"`
	EndedAt     *float64   `json:"endedAt"`
	Calls       int        `json:"calls"`
	Errors      int        `json:"errors"`
	Nested      int        `json:"nested"`
	LastAction  string     `json:"lastAction"`
	Tools       *js.Object `json:"tools"`
	TokensOut   float64    `json:"tokensOut"`
	CostUSD     float64    `json:"costUsd"`
	Task        string     `json:"task"`
	Result      string     `json:"result"`
	InCall      *bool      `json:"inCall,omitempty"`
}

// AgentState is what one subagent transcript tells.
type AgentState struct {
	ID         string
	Meta       AgentMeta
	Model      string
	Task       string
	StartedAt  float64
	LastAt     float64
	Meter      *UsageMeter
	ToolCalls  []*ToolCall
	ToolUseIDs map[string]bool
	Tools      *js.Object
	Errors     int
	Handback   bool
	Result     string
	// EndedTurn is set when the last assistant message ended its turn with no tool call.
	EndedTurn bool
	// OwesReply is set when the agent's last line asks the model to go on (a tool call, a tool
	// result or a prompt): quiet after one is generating, not finished.
	OwesReply  bool
	LastAction string
	Consults   *ConsultTracker

	byID map[string]*ToolCall
	// resultIDs are the calls that have their result (a result line may carry no timestamp, so
	// Ms alone cannot tell).
	resultIDs map[string]bool
}

// NewAgentState is an empty agent.
func NewAgentState(agentID string, meta AgentMeta) *AgentState {
	label := "agent"

	if meta.AgentType != nil {
		if *meta.AgentType != "" {
			label = *meta.AgentType
		}
	}

	if meta.Description != nil {
		if *meta.Description != "" {
			label = *meta.Description
		}
	}

	return &AgentState{
		ID: agentID, Meta: meta, Model: "", Task: "", StartedAt: 0, LastAt: 0,
		Meter: NewUsageMeter(), ToolCalls: []*ToolCall{}, ToolUseIDs: map[string]bool{},
		Tools: js.NewObject(), Errors: 0, Handback: false, Result: "", EndedTurn: false,
		OwesReply: false, LastAction: "", Consults: NewConsultTracker(agentID, label),
		byID: map[string]*ToolCall{}, resultIDs: map[string]bool{},
	}
}

// TokensOut is the agent's output tokens.
func (a *AgentState) TokensOut() float64 { return a.Meter.Output }

// InCall is whether a tool call of the agent has no result yet: it is inside a command, which
// writes nothing until it returns.
func (a *AgentState) InCall() bool {
	return slices.ContainsFunc(a.ToolCalls, func(call *ToolCall) bool {
		return !a.resultIDs[call.ID]
	})
}

// Holds is whether the agent may still be working while quiet: inside a call, or its last line
// asks the model to go on (thinking, writing one big block, an advisor consult).
func (a *AgentState) Holds() bool {
	if a.InCall() {
		return true
	}

	return a.OwesReply
}

// Feed reads one transcript line.
func (a *AgentState) Feed(raw any) {
	line, ok := raw.(*js.Object)
	if !ok {
		return
	}

	when := timeOf(line)
	if js.Truthy(when) {
		if a.StartedAt == 0 {
			a.StartedAt = when
		}

		a.LastAt = max(a.LastAt, when)
	}

	switch kind, _ := text(line, "type"); kind {
	case typeAssistant:
		a.assistant(line, when)
	case typeUser:
		a.user(line, when)
	}
}

// Status is the agent's status at now, given whether the lead has the result of the call that
// started it.
func (a *AgentState) Status(now float64, parentResultSeen bool) string {
	if a.Handback {
		return StatusDone
	}

	if a.EndedTurn {
		return StatusDone
	}

	if parentResultSeen {
		if !a.background() {
			return StatusDone
		}
	}

	if now-a.LastAt > stallMs {
		return StatusStalled
	}

	return StatusRunning
}

// ToAgent is the agent as the page draws it.
func (a *AgentState) ToAgent(now float64, parentResultSeen bool, parentID *string,
	nested int,
) Agent {
	status := a.Status(now, parentResultSeen)
	holds := a.Holds()

	agent := Agent{
		ID: a.ID, Type: "agent", Description: "", Model: a.Model, Status: status, Depth: 1,
		ParentID: parentID, StartedAt: a.StartedAt, LastAt: a.LastAt, EndedAt: nil,
		Calls: len(a.ToolCalls), Errors: a.Errors, Nested: nested, LastAction: a.LastAction,
		Tools: a.Tools, TokensOut: a.TokensOut(), CostUSD: a.Meter.USD, Task: a.Task,
		Result: a.Result, InCall: &holds,
	}

	if a.Meta.AgentType != nil {
		agent.Type = *a.Meta.AgentType
	}

	if a.Meta.Description != nil {
		agent.Description = *a.Meta.Description
	}

	// 0 is the lead's own depth: a named agent (an in-process teammate) is written with it, but
	// is the lead's child.
	if js.Truthy(a.Meta.SpawnDepth) {
		agent.Depth = a.Meta.SpawnDepth
	}

	if status == StatusDone {
		ended := a.LastAt
		agent.EndedAt = &ended
	}

	return agent
}

func (a *AgentState) assistant(line *js.Object, when float64) {
	message, _ := line.Get(keyMessage)

	model, ok := modelOf(message)
	if ok {
		a.Model = model
	}

	meterAdd(a.Meter, message)

	list := blocks(line)
	usedTool := a.readBlocks(list, when)

	stop, _ := field(message, "stop_reason")

	switch {
	case usedTool:
		a.EndedTurn = false
	case stop == "end_turn":
		a.EndedTurn = true
	}

	// A tool call or a consult asks the model to go on; its own text does not.
	if hasKind(list, typeToolUse, typeServerUse) {
		a.OwesReply = true
	} else if hasKind(list, typeText) {
		a.OwesReply = false
	}
}

// readBlocks reads an assistant line's blocks and says whether one was a tool call.
func (a *AgentState) readBlocks(list []*js.Object, when float64) bool {
	usedTool := false

	for _, block := range list {
		switch kindOf(block) {
		case typeToolUse:
			usedTool = true

			a.toolUse(block, when)
		case typeServerUse:
			name, _ := text(block, "name")
			if name == nameAdvisor {
				a.Consults.Call(idOf(block, "id"), when)
			}
		case typeAdvisor:
			a.Consults.Result(idOf(block, "tool_use_id"), when)
		case typeText:
			a.text(block, usedTool)
		}
	}

	return usedTool
}

func (a *AgentState) text(block *js.Object, usedTool bool) {
	said, _ := text(block, typeText)
	if js.Trim(said) == "" {
		return
	}

	if usedTool {
		return
	}

	a.LastAction = js.Clip(said, activityUnits)
	a.Consults.Step(said)
}

func (a *AgentState) toolUse(block *js.Object, when float64) {
	a.ToolUseIDs[idOf(block, "id")] = true

	name := toolName(block)
	if name == nameHandback {
		a.Handback = true

		input, _ := block.Get("input")
		message, present := field(input, "message")

		a.Result = ""
		if !nullish(message, present) {
			a.Result = js.String(message)
		}

		return
	}

	input, _ := block.Get("input")
	call := newCall(block, name, Summarize(name, input), when)
	a.ToolCalls = append(a.ToolCalls, call)
	a.byID[call.ID] = call
	count(a.Tools, name)
	a.LastAction = js.Trim(name + "  " + call.Summary)
	a.Consults.Step(a.LastAction)
}

func (a *AgentState) user(line *js.Object, when float64) {
	if a.Task == "" {
		message, _ := line.Get(keyMessage)

		content, isText := text(message, keyContent)
		if isText {
			a.Task = js.Cut(js.Trim(content), taskUnits)
		}
	}

	a.OwesReply = true

	for _, block := range blocks(line) {
		if kindOf(block) != typeToolRes {
			continue
		}

		id := idOf(block, "tool_use_id")
		a.resultIDs[id] = true

		call, found := a.byID[id]
		if !found {
			continue
		}

		settle(call, block, when)

		if call.Error {
			a.Errors++
		}
	}

	a.EndedTurn = false
}

func (a *AgentState) background() bool {
	if a.Meta.RequestShape == nil {
		return false
	}

	return *a.Meta.RequestShape == "background"
}
