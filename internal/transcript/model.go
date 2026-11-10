package transcript

import (
	"strings"

	"github.com/claytoncasey01/conn/internal/js"
)

// What one transcript line says, read the way the server has always read it. A line or a
// content block that is not an object says nothing, and a null block is skipped.

const (
	// summaryUnits is how much of a tool call's input its one-line summary keeps.
	summaryUnits = 140
	// resultUnits is how much of a tool call's result its preview keeps.
	resultUnits = 240
	// activityUnits is how much of the lead's or an agent's text its activity line keeps.
	activityUnits = 120
	// thoughtUnits is how much of what an asker said or did a consult keeps.
	thoughtUnits = 280
	// taskUnits is how much of the first prompt a task keeps.
	taskUnits = 1500
	// finalUnits is how much of the lead's last text is kept.
	finalUnits = 4000
	// toolCallsKept is how many of the lead's tool calls the live view keeps.
	toolCallsKept = 600
	// maxTeams is how many teams a lead's spawn record keeps, as many as the spawn index does.
	maxTeams = 100
	// stallMs is how long an agent may be quiet before it shows as stalled.
	stallMs = 180_000

	typeAssistant = "assistant"
	typeUser      = "user"
	typeText      = "text"
	typeToolUse   = "tool_use"
	typeToolRes   = "tool_result"
	typeServerUse = "server_tool_use"
	typeAdvisor   = "advisor_tool_result"
	nameAdvisor   = "advisor"
	nameHandback  = "SubagentHandback"
	keyTimestamp  = "timestamp"
	keyContent    = "content"
	keyMessage    = "message"
	// keyAdvisorModel is a reply line's own key (beside its message) naming the session's advisor.
	keyAdvisorModel = "advisorModel"

	interruptedMark = "[Request interrupted by user"
	commandOpen     = "<command-name>"
	commandClose    = "</command-name>"
	argsOpen        = "<command-args>"
	argsClose       = "</command-args>"
)

// ToolCall is one tool call and, once it has come, its result.
type ToolCall struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Summary string  `json:"summary"`
	At      js.Num  `json:"at"`
	Ms      *js.Num `json:"ms"`
	Error   bool    `json:"error"`
	Result  string  `json:"result"`
}

// field is o[key] when o is an object.
func field(value any, key string) (any, bool) {
	object, ok := value.(*js.Object)
	if !ok {
		return nil, false
	}

	return object.Get(key)
}

// nullish is whether a value is null or undefined, what ?? looks past.
func nullish(value any, present bool) bool {
	if !present {
		return true
	}

	return value == nil
}

// coalesce is a[k1] ?? a[k2] ?? ... ?? ” as a string.
func coalesce(object any, keys ...string) string {
	for _, key := range keys {
		value, present := field(object, key)
		if !nullish(value, present) {
			return js.String(value)
		}
	}

	return ""
}

// text is a[key] when it is a string.
func text(object any, key string) (string, bool) {
	value, _ := field(object, key)
	typed, ok := value.(string)

	return typed, ok
}

func truthy(object any, key string) bool {
	value, _ := field(object, key)

	return js.Truthy(value)
}

// blocks is o.message.content when it is an array: its objects.
func blocks(line *js.Object) []*js.Object {
	message, _ := line.Get(keyMessage)
	content, _ := field(message, keyContent)
	list, _ := content.([]any)

	out := make([]*js.Object, 0, len(list))

	for _, item := range list {
		object, ok := item.(*js.Object)
		if ok {
			out = append(out, object)
		}
	}

	return out
}

// rawBlocks is o.message.content.length when it is an array, objects or not.
func rawBlocks(line *js.Object) int {
	message, _ := line.Get(keyMessage)
	content, _ := field(message, keyContent)
	list, _ := content.([]any)

	return len(list)
}

func kindOf(block *js.Object) string {
	kind, _ := text(block, "type")

	return kind
}

// timeOf is o.timestamp ? Date.parse(o.timestamp) : 0.
func timeOf(line *js.Object) float64 {
	stamp, _ := line.Get(keyTimestamp)
	if !js.Truthy(stamp) {
		return 0
	}

	return js.DateParse(js.String(stamp))
}

// Summarize is the one-line summary of a tool call's input: the command, path or pattern.
func Summarize(name string, input any) string {
	switch input.(type) {
	case *js.Object, []any:
	default:
		return ""
	}

	switch name {
	case "Grep":
		return grepSummary(input)
	case nameHandback:
		return "final report"
	}

	// The input fields each tool's summary reads, the first that is set winning.
	paths := []string{"file_path", "notebook_path"}
	keys, known := map[string][]string{
		"Bash": {"command"}, "Read": paths, "Edit": paths, "Write": paths,
		"NotebookEdit": paths, "Glob": {"pattern"},
		"WebFetch": {"url"}, "WebSearch": {"query"}, "Agent": {"description", "prompt"},
		"Task": {"description", "prompt"},
	}[name]

	if known {
		return js.Clip(coalesce(input, keys...), summaryUnits)
	}

	return firstString(input)
}

// grepSummary is the pattern, then the path after two spaces when there is one.
func grepSummary(input any) string {
	path := ""
	if truthy(input, "path") {
		path = "  " + coalesce(input, "path")
	}

	pattern, present := field(input, "pattern")
	if nullish(pattern, present) {
		pattern = ""
	}

	return js.Clip(js.String(pattern)+path, summaryUnits)
}

// firstString is the first non-empty string among the input's values, clipped.
func firstString(input any) string {
	var values []any

	switch typed := input.(type) {
	case *js.Object:
		for _, key := range typed.Keys() {
			value, _ := typed.Get(key)
			values = append(values, value)
		}
	case []any:
		values = typed
	}

	for _, value := range values {
		typed, ok := value.(string)
		if !ok {
			continue
		}

		if typed != "" {
			return js.Clip(typed, summaryUnits)
		}
	}

	return ""
}

// resultText is a tool result's text: a string, or the text of its blocks joined by spaces.
func resultText(content any) string {
	switch typed := content.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))

		for _, item := range typed {
			value, _ := text(item, typeText)
			parts = append(parts, value)
		}

		return strings.Join(parts, " ")
	}

	return ""
}

// joinText is the text blocks' b.text ?? ” joined by sep, as Array.join writes each.
func joinText(list []*js.Object, sep string) string {
	parts := []string{}

	for _, block := range list {
		if kindOf(block) != typeText {
			continue
		}

		value, present := block.Get(typeText)
		if nullish(value, present) {
			parts = append(parts, "")

			continue
		}

		parts = append(parts, js.String(value))
	}

	return strings.Join(parts, sep)
}

// promptText is the words the person typed: a plain string or text blocks, a slash command as
// "/name args", never tool results or injected tags.
func promptText(line *js.Object) string {
	if truthy(line, "isMeta") {
		return ""
	}

	message, _ := line.Get(keyMessage)
	content, _ := field(message, keyContent)

	raw := ""

	switch typed := content.(type) {
	case string:
		raw = typed
	case []any:
		raw = joinText(blocks(line), "\n")
	}

	trimmed := js.Trim(raw)

	name, found := commandName(trimmed)
	if found {
		return js.Trim(name + " " + between(trimmed, argsOpen, argsClose))
	}

	if strings.HasPrefix(trimmed, "<") {
		return ""
	}

	if strings.HasPrefix(trimmed, "[Image") {
		return ""
	}

	return trimmed
}

// commandName is /<command-name>\s*([^<]*?)\s*<\/command-name>/: the first opening tag whose text
// up to the next '<' is followed by the closing tag.
func commandName(text string) (string, bool) {
	rest := text

	for {
		start := strings.Index(rest, commandOpen)
		if start < 0 {
			return "", false
		}

		rest = rest[start+len(commandOpen):]

		end := strings.IndexByte(rest, '<')
		if end < 0 {
			return "", false
		}

		if strings.HasPrefix(rest[end:], commandClose) {
			return js.Trim(rest[:end]), true
		}
	}
}

// between is /open([\s\S]*?)close/'s group, or "".
func between(text, open, closing string) string {
	_, rest, opened := strings.Cut(text, open)
	if !opened {
		return ""
	}

	inside, _, closed := strings.Cut(rest, closing)
	if !closed {
		return ""
	}

	return inside
}

func interrupted(text string) bool {
	return strings.HasPrefix(strings.TrimLeftFunc(text, js.IsSpace), interruptedMark)
}

// localCommand is /^<(local-command-std|bash-(input|stdout|stderr))/.
func localCommand(text string) bool {
	for _, prefix := range []string{
		"<local-command-std", "<bash-input", "<bash-stdout", "<bash-stderr",
	} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}

	return false
}

// Spawn is what a transcript line spawned: a teammate, into a team, at a time (ms, 0 when the
// line has no time).
type Spawn struct {
	Team string  `json:"team"`
	Name string  `json:"name"`
	At   float64 `json:"at"`
}

// SpawnOf reads a user line whose top-level toolUseResult is a teammate_spawned with a team name.
func SpawnOf(line any) (Spawn, bool) {
	kind, _ := text(line, "type")
	if kind != typeUser {
		return Spawn{}, false
	}

	result, _ := field(line, "toolUseResult")

	status, _ := text(result, "status")
	if status != "teammate_spawned" {
		return Spawn{}, false
	}

	team, _ := text(result, "team_name")
	if team == "" {
		return Spawn{}, false
	}

	name, _ := text(result, "name")
	spawn := Spawn{Team: team, Name: name, At: 0}

	stamp, isText := text(line, keyTimestamp)
	if isText {
		when := js.DateParse(stamp)
		if js.Truthy(when) {
			spawn.At = when
		}
	}

	return spawn, true
}

// newCall is a tool call made at at, with no result yet.
func newCall(block *js.Object, name, summary string, when float64) *ToolCall {
	id, _ := block.Get("id")

	return &ToolCall{
		ID: js.String(id), Name: name, Summary: summary, At: js.Num(when), Ms: nil,
		Error: false, Result: "",
	}
}

// settle attaches a result that came at when to its call.
func settle(call *ToolCall, block *js.Object, when float64) {
	call.Ms = nil

	if js.Truthy(when) {
		if js.Truthy(float64(call.At)) {
			elapsed := js.Num(max(0, when-float64(call.At)))
			call.Ms = &elapsed
		}
	}

	call.Error = truthy(block, "is_error")

	content, _ := block.Get(keyContent)
	call.Result = js.Clip(resultText(content), resultUnits)
}

// toolName is String(b.name).
func toolName(block *js.Object) string {
	name, present := block.Get("name")
	if !present {
		return "undefined"
	}

	return js.String(name)
}

// count adds one to tools[name].
func count(tools *js.Object, name string) {
	current, _ := tools.Get(name)
	value, _ := current.(float64)
	tools.Set(name, value+1)
}

// hasKind is whether any block is of one of the kinds.
func hasKind(list []*js.Object, kinds ...string) bool {
	for _, block := range list {
		for _, kind := range kinds {
			if kindOf(block) == kind {
				return true
			}
		}
	}

	return false
}

// modelOf is m.model when it is set and not a synthetic "<...>" one.
func modelOf(message any) (string, bool) {
	value, _ := field(message, "model")
	if !js.Truthy(value) {
		return "", false
	}

	model := js.String(value)

	return model, shownModel(model)
}

// shownModel is whether model names a real model: set, and not a synthetic "<...>" one.
func shownModel(model string) bool {
	if model == "" {
		return false
	}

	return !strings.HasPrefix(model, "<")
}

// meterAdd counts a message's usage.
func meterAdd(meter *UsageMeter, message any) {
	object, _ := message.(*js.Object)
	meter.Add(object)
}

// idOf is String(v) of a block's id-like key.
func idOf(block *js.Object, key string) string {
	value, _ := block.Get(key)

	return js.String(value)
}
