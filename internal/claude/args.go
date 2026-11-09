// Package claude runs headless Claude Code (`claude -p`) for Conn's helpers: one run that must
// answer, in a JSON schema when one is given, with nothing but the tools the caller names. Nobody
// is there to approve a tool, so every other tool is denied, and a denial fails the run unless
// the caller says a partial answer is fine.
//
// The parts are pure (Args builds the command line, a stream reads its output a line at a time,
// outcome decides what the run answered) around one function that starts the process.
package claude

import (
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/claytoncasey01/conn/internal/js"
)

// HelperPrefix starts the name of every helper run, so the session list and the archive can hide
// them.
const HelperPrefix = "conn:"

const (
	// budgetUSDDefault is the cost cap of a run whose Opts set none.
	budgetUSDDefault = 1.0

	// defaultTimeout is how long a run whose Opts set no limit may take.
	defaultTimeout = 10 * time.Minute

	// maxStepArgument is the most of a tool's argument a progress line shows, in UTF-16 units.
	maxStepArgument = 70

	mcpToolPrefix = "mcp__"
)

// connectorTool splits a claude.ai connector's tool name into the connector and the tool:
// mcp__claude_ai_Atlassian_MCP__searchJiraIssuesUsingJql.
var connectorTool = regexp.MustCompile(`^mcp__claude_ai_(.+?)__(.+)$`)

// Opts describe one run.
type Opts struct {
	Prompt string
	Model  string
	// Name is the run's name after HelperPrefix.
	Name string
	// AllowedTools are exact tool names; anything else is denied.
	AllowedTools []string
	// DisallowedTools must never run, whatever a permission rule allows (deny beats allow). A run
	// that reads untrusted text and uses connectors names the connector's write tools here, so a
	// rule added to the user's settings later cannot make them usable.
	DisallowedTools []string
	// Dir is the folder the run starts in; "" is the server's.
	Dir string
	// AddDirs are folders besides Dir the run's tools may reach (--add-dir); none adds nothing.
	AddDirs []string
	// Schema is the JSON schema the answer must follow, as JSON text; "" asks for plain text.
	Schema string
	// BudgetUSD caps the run's cost; 0 is 1 USD.
	BudgetUSD float64
	// Timeout stops the run; 0 is 10 minutes.
	Timeout time.Duration
	// Lenient lets a run that had a tool denied answer anyway. Readers set it: a model poking at
	// an unlisted tool and then answering is fine for them.
	Lenient bool
	// NoMCP starts the run with no MCP servers at all: no claude.ai connectors, no user or project
	// servers. A run that reads untrusted text and needs no connector sets it, so a prompt
	// injection has nothing to call even if a permission rule were added later.
	NoMCP bool
	// OnDenied is given the denied tool names when a Lenient run answered anyway, so the caller
	// can mark a "found nothing" as unreliable.
	OnDenied func(tools []string)
	// OnProgress is given a short line for each step the run takes, as it takes it.
	OnProgress func(line string)
	// OnAuth is given the connectors the run started without being signed in to ("Atlassian").
	OnAuth func(needing []string)
}

func (o Opts) budgetUSD() float64 {
	if o.BudgetUSD == 0 {
		return budgetUSDDefault
	}

	return o.BudgetUSD
}

func (o Opts) timeout() time.Duration {
	if o.Timeout == 0 {
		return defaultTimeout
	}

	return o.Timeout
}

// Args is the command line for a run, after the program name. The prompt goes in on stdin,
// because the tool lists are variadic and would swallow a trailing argument; for the same reason
// each list sits before any flag that must follow it, and --allowedTools comes last.
func Args(opts Opts) []string {
	args := []string{
		"-p",
		"--model", opts.Model,
		"--name", HelperPrefix + opts.Name,
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "dontAsk",
		"--no-session-persistence",
		"--disable-slash-commands",
		"--max-budget-usd", js.Number(opts.budgetUSD()),
	}

	if opts.NoMCP {
		args = append(args, "--strict-mcp-config")
	}

	// --add-dir takes every folder up to the next flag, so --tools must follow it.
	if len(opts.AddDirs) > 0 {
		args = append(args, "--add-dir")
		args = append(args, opts.AddDirs...)
	}

	// Only the built-ins the caller lists exist at all (none: every built-in is off).
	// --allowedTools alone is not enough: dontAsk still honours the user's own permissions.allow
	// rules, so a helper given only MCP tools could still run Bash. One comma-joined value is one
	// argument, so it can sit before the variadic lists.
	args = append(args, "--tools", strings.Join(BuiltinTools(opts.AllowedTools), ","))

	if len(opts.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools")
		args = append(args, opts.DisallowedTools...)
	}

	if opts.Schema != "" {
		args = append(args, "--json-schema", opts.Schema)
	}

	if len(opts.AllowedTools) > 0 {
		args = append(args, "--allowedTools")
		args = append(args, opts.AllowedTools...)
	}

	return args
}

// BuiltinTools are the built-in tool names an allow list asks for, in order of first appearance:
// "Bash(git log:*)" is Bash, and MCP tools are left out.
func BuiltinTools(allowed []string) []string {
	names := []string{}

	for _, tool := range allowed {
		if strings.HasPrefix(tool, mcpToolPrefix) {
			continue
		}

		name, _, _ := strings.Cut(tool, "(")

		name = js.Trim(name)
		if name == "" {
			continue
		}

		if slices.Contains(names, name) {
			continue
		}

		names = append(names, name)
	}

	return names
}

// StepLabel is the progress line for one tool call: "Atlassian: searchJiraIssuesUsingJql" for a
// connector's tool, else the tool and the start of its file, pattern, path or command.
func StepLabel(tool string, input any) string {
	match := connectorTool.FindStringSubmatch(tool)
	if match != nil {
		return strings.TrimSuffix(match[1], "_MCP") + ": " + match[2]
	}

	argument := js.Slice(js.CollapseSpace(stepArgument(input)), maxStepArgument)
	if argument == "" {
		return tool
	}

	return tool + " " + argument
}

// stepArgument is the first of the input's file_path, pattern, path and command that is there and
// not null, as text.
func stepArgument(input any) string {
	object, isObject := input.(*js.Object)
	if !isObject {
		return ""
	}

	for _, key := range []string{"file_path", "pattern", "path", "command"} {
		value, found := object.Get(key)
		if !found {
			continue
		}

		if value == nil {
			continue
		}

		return js.String(value)
	}

	return ""
}
