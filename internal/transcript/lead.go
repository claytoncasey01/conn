package transcript

import (
	"slices"
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
)

// Consult is one call to the advisor. The answer is encrypted in the transcript, so only its
// shape is known: when, who asked, and what the asker said or did either side of it.
type Consult struct {
	ID      string  `json:"id"`
	At      js.Num  `json:"at"`
	EndedAt *js.Num `json:"endedAt"`
	By      string  `json:"by"`
	ByLabel string  `json:"byLabel"`
	Before  string  `json:"before"`
	After   string  `json:"after"`
}

// ConsultTracker follows the advisor calls of one asker.
type ConsultTracker struct {
	List    []*Consult
	byID    map[string]*Consult
	thought string
	waiting []*Consult
	by      string
	byLabel string
}

// NewConsultTracker tracks the calls of the asker by, shown as byLabel.
func NewConsultTracker(by, byLabel string) *ConsultTracker {
	return &ConsultTracker{
		List: []*Consult{}, byID: map[string]*Consult{}, thought: "", waiting: nil,
		by: by, byLabel: byLabel,
	}
}

// Step records that the asker said or did something: the context of the next call, and the
// follow-up of the last answer.
func (c *ConsultTracker) Step(desc string) {
	c.thought = js.Clip(desc, thoughtUnits)
	for _, waiting := range c.waiting {
		waiting.After = c.thought
	}

	c.waiting = nil
}

// Call records a call made at at.
func (c *ConsultTracker) Call(callID string, when float64) {
	consult := &Consult{
		ID: callID, At: js.Num(when), EndedAt: nil, By: c.by, ByLabel: c.byLabel,
		Before: c.thought, After: "",
	}
	c.List = append(c.List, consult)
	c.byID[callID] = consult
}

// Result records the answer to a call, come at at.
func (c *ConsultTracker) Result(id string, when float64) {
	consult, ok := c.byID[id]
	if !ok {
		return
	}

	ended := js.Num(when)
	consult.EndedAt = &ended
	c.waiting = append(c.waiting, consult)
}

// LeadState is what the lead's own transcript tells.
type LeadState struct {
	Mode, Branch, Model string
	Calls, Errors       int
	AdvisorCalls        int
	CtxTokens           float64
	Activity            string
	LastAt, StartedAt   float64
	// Tools counts calls by tool name, in the order each was first used.
	Tools     *js.Object
	Task      string
	Meter     *UsageMeter
	ToolCalls []*ToolCall
	Consults  *ConsultTracker
	// ResultIDs are the tool_use ids whose results have arrived, to tell foreground agents are
	// back.
	ResultIDs map[string]bool
	// OwesReply is set when the model owes a reply: the last line of the conversation is a tool
	// result, a prompt or an advisor call, not the lead's own text. A local or bash-mode command
	// run at the prompt, meta lines and an interrupt are not a turn.
	OwesReply bool
	// Title is the session's name: a custom-title line, else an agent-name line.
	Title string
	// FinalText is the last non-empty assistant text, clipped to 4000 UTF-16 units.
	FinalText string
	Cwd       string
	// Teams is every team this session spawned a teammate into, in the order first seen.
	Teams []string

	byID           map[string]*ToolCall
	hasCustomTitle bool
	toolCallsMax   int
}

// NewLeadState is an empty lead that keeps its last toolCallsMax tool calls (0 for the live
// default of 600; a negative number keeps every call, for archiving).
func NewLeadState(toolCallsMax int) *LeadState {
	if toolCallsMax == 0 {
		toolCallsMax = toolCallsKept
	}

	return &LeadState{
		Mode: "default", Branch: "", Model: "", Calls: 0, Errors: 0, AdvisorCalls: 0,
		CtxTokens: 0, Activity: "", LastAt: 0, StartedAt: 0, Tools: js.NewObject(), Task: "",
		Meter: NewUsageMeter(), ToolCalls: []*ToolCall{},
		Consults: NewConsultTracker("lead", "LEAD"), ResultIDs: map[string]bool{},
		OwesReply: false, Title: "", FinalText: "", Cwd: "", Teams: []string{},
		byID: map[string]*ToolCall{}, hasCustomTitle: false, toolCallsMax: toolCallsMax,
	}
}

// TokensOut is the output tokens of the lead's model.
func (l *LeadState) TokensOut() float64 { return l.Meter.Output }

// InCall is whether a tool call of the lead has no result yet (judged by the result lines, not
// by a timestamp they may lack).
func (l *LeadState) InCall() bool {
	return slices.ContainsFunc(l.ToolCalls, func(call *ToolCall) bool {
		return !l.ResultIDs[call.ID]
	})
}

// Feed reads one transcript line.
func (l *LeadState) Feed(raw any) {
	line, ok := raw.(*js.Object)
	if !ok {
		return
	}

	when := timeOf(line)
	if js.Truthy(when) {
		l.LastAt = max(l.LastAt, when)
		if l.StartedAt == 0 {
			l.StartedAt = when
		}
	}

	l.readContext(line)

	if truthy(line, "isSidechain") {
		return
	}

	switch kind, _ := text(line, "type"); kind {
	case typeAssistant:
		l.assistant(line, when)
	case typeUser:
		l.user(line, when)
	}
}

// readContext reads the lines that set the branch, folder, mode and title.
func (l *LeadState) readContext(line *js.Object) {
	if truthy(line, "gitBranch") {
		l.Branch = coalesce(line, "gitBranch")
	}

	cwd, isText := text(line, "cwd")
	if isText {
		if cwd != "" {
			l.Cwd = cwd
		}
	}

	l.readTitle(line)
}

// readTitle reads the lines that set the mode and the title: a custom title wins over an agent
// name.
func (l *LeadState) readTitle(line *js.Object) {
	switch kind, _ := text(line, "type"); kind {
	case "permission-mode":
		if truthy(line, "permissionMode") {
			l.Mode = coalesce(line, "permissionMode")
		}
	case "custom-title":
		title, _ := text(line, "customTitle")
		if js.Trim(title) != "" {
			l.Title = js.Trim(title)
			l.hasCustomTitle = true
		}
	case "agent-name":
		name, _ := text(line, "agentName")
		if js.Trim(name) != "" {
			if !l.hasCustomTitle {
				l.Title = js.Trim(name)
			}
		}
	}
}

func (l *LeadState) assistant(line *js.Object, when float64) {
	message, _ := line.Get(keyMessage)

	model, ok := modelOf(message)
	if ok {
		l.Model = model
	}

	meterAdd(l.Meter, message)

	usage, _ := field(message, "usage")
	if js.Truthy(usage) {
		object, _ := usage.(*js.Object)
		l.CtxTokens = number(object, "input_tokens") +
			number(object, "cache_creation_input_tokens") +
			number(object, "cache_read_input_tokens")
	}

	list := blocks(line)
	for _, block := range list {
		l.block(block, when)
	}

	if hasKind(list, typeToolUse, typeServerUse) {
		l.OwesReply = true
	} else if hasKind(list, typeText) {
		l.OwesReply = false
	}
}

func (l *LeadState) block(block *js.Object, when float64) {
	switch kindOf(block) {
	case typeToolUse:
		l.toolUse(block, when)
	case typeServerUse:
		name, _ := text(block, "name")
		if name == nameAdvisor {
			l.AdvisorCalls++
			l.Consults.Call(idOf(block, "id"), when)
		}
	case typeAdvisor:
		l.Consults.Result(idOf(block, "tool_use_id"), when)
	case typeText:
		said, _ := text(block, typeText)
		if js.Trim(said) != "" {
			l.Activity = js.Clip(said, activityUnits)
			l.Consults.Step(said)
			l.FinalText = js.Cut(js.Trim(said), finalUnits)
		}
	}
}

func (l *LeadState) toolUse(block *js.Object, when float64) {
	l.Calls++
	name := toolName(block)
	count(l.Tools, name)

	input, _ := block.Get("input")
	summary := Summarize(name, input)
	l.Activity = js.Trim(name + "  " + summary)
	l.Consults.Step(l.Activity)

	call := newCall(block, name, summary, when)
	l.ToolCalls = append(l.ToolCalls, call)
	l.byID[call.ID] = call

	if l.toolCallsMax > 0 {
		if len(l.ToolCalls) > l.toolCallsMax {
			delete(l.byID, l.ToolCalls[0].ID)
			l.ToolCalls = l.ToolCalls[1:]
		}
	}
}

func (l *LeadState) user(line *js.Object, when float64) {
	if l.Task == "" {
		l.Task = js.Cut(promptText(line), taskUnits)
	}

	list := blocks(line)
	l.OwesReply = owesAfterUser(line, list, l.OwesReply)

	spawn, spawned := SpawnOf(line)
	if spawned {
		if len(l.Teams) < teamsMax {
			if !slices.Contains(l.Teams, spawn.Team) {
				l.Teams = append(l.Teams, spawn.Team)
			}
		}
	}

	for _, block := range list {
		if kindOf(block) != typeToolRes {
			continue
		}

		id := idOf(block, "tool_use_id")
		l.ResultIDs[id] = true

		call, found := l.byID[id]
		if found {
			settle(call, block, when)
		}

		if truthy(block, "is_error") {
			l.Errors++
		}
	}
}

// toolResults counts a line's tool results, and says whether one is the result of a call the
// user cut short.
func toolResults(list []*js.Object) (int, bool) {
	results := 0

	for _, block := range list {
		if kindOf(block) != typeToolRes {
			continue
		}

		results++

		value, _ := block.Get(keyContent)
		if interrupted(resultText(value)) {
			return results, true
		}
	}

	return results, false
}

// owesAfterUser is whether the model owes a reply after a user line.
func owesAfterUser(line *js.Object, list []*js.Object, owes bool) bool {
	message, _ := line.Get(keyMessage)
	content, _ := field(message, keyContent)

	said, isText := content.(string)
	if !isText {
		said = joinText(list, "")
	}

	said = strings.TrimLeftFunc(said, js.IsSpace)

	// The user stopped the turn (its marker line, or the result of the call it cut short).
	if interrupted(said) {
		return false
	}

	results, cut := toolResults(list)
	if cut {
		return false
	}

	if results > 0 {
		return true
	}

	// A local command or a bash-mode command (!) run at the prompt, and its output.
	if localCommand(said) {
		return false
	}

	if truthy(line, "isMeta") {
		return owes
	}

	if said != "" {
		return true
	}

	if rawBlocks(line) > 0 {
		return true
	}

	return owes
}
