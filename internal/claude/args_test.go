package claude_test

import (
	"slices"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/js"
)

// base is the command line every run starts with, before its tool lists.
func base(budget string) []string {
	return []string{
		"-p", "--model", "haiku", "--name", "conn:parity", "--output-format", "stream-json",
		"--verbose", "--permission-mode", "dontAsk", "--no-session-persistence",
		"--disable-slash-commands", "--max-budget-usd", budget,
	}
}

func joined(parts ...[]string) []string {
	all := []string{}
	for _, part := range parts {
		all = append(all, part...)
	}

	return all
}

func TestArgs(t *testing.T) {
	t.Parallel()

	slackRead := []string{
		"mcp__claude_ai_Slack__slack_read_channel", "mcp__claude_ai_Slack__slack_read_thread",
	}
	mixed := []string{
		"Read", "Grep", "Bash(git log:*)", "mcp__claude_ai_Slack__slack_read_channel", "Read",
		"Bash(git diff:*)",
	}

	tests := []struct {
		name string
		opts claude.Opts
		want []string
	}{
		{
			name: "one built-in and a schema",
			opts: claude.Opts{AllowedTools: []string{"Grep"}, Schema: `{"type":"object"}`},
			want: joined(base("1"), []string{
				"--tools", "Grep", "--json-schema", `{"type":"object"}`, "--allowedTools", "Grep",
			}),
		},
		{
			name: "noMcp comes before the tool lists, and passes no config",
			opts: claude.Opts{AllowedTools: []string{"Grep"}, NoMCP: true},
			want: joined(base("1"), []string{
				"--strict-mcp-config", "--tools", "Grep", "--allowedTools", "Grep",
			}),
		},
		{
			name: "only MCP tools switch every built-in off",
			opts: claude.Opts{AllowedTools: slackRead},
			want: joined(base("1"), []string{"--tools", "", "--allowedTools"}, slackRead),
		},
		{
			name: "a mixed list names its built-ins once each, and keeps the permission list whole",
			opts: claude.Opts{AllowedTools: mixed},
			want: joined(base("1"), []string{"--tools", "Read,Grep,Bash", "--allowedTools"}, mixed),
		},
		{
			name: "an empty list still passes --tools and no --allowedTools",
			opts: claude.Opts{AllowedTools: []string{}},
			want: joined(base("1"), []string{"--tools", ""}),
		},
		{
			name: "denied tools come before the allowed ones",
			opts: claude.Opts{
				AllowedTools:    []string{"mcp__a__read"},
				DisallowedTools: []string{"mcp__a__write", "mcp__a__delete"},
			},
			want: joined(base("1"), []string{
				"--tools", "", "--disallowedTools", "mcp__a__write", "mcp__a__delete",
				"--allowedTools", "mcp__a__read",
			}),
		},
		{
			name: "an empty deny list passes nothing",
			opts: claude.Opts{AllowedTools: []string{"Grep"}, DisallowedTools: []string{}},
			want: joined(base("1"), []string{"--tools", "Grep", "--allowedTools", "Grep"}),
		},
		{
			name: "a budget is written as JavaScript writes the number",
			opts: claude.Opts{AllowedTools: []string{"Grep"}, BudgetUSD: 0.25},
			want: joined(base("0.25"), []string{"--tools", "Grep", "--allowedTools", "Grep"}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := tt.opts
			opts.Model = "haiku"
			opts.Name = "parity"
			opts.Timeout = time.Second

			got := claude.Args(opts)
			if !slices.Equal(got, tt.want) {
				t.Errorf("Args() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuiltinTools(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		allowed []string
		want    []string
	}{
		{name: "none", allowed: nil, want: []string{}},
		{name: "MCP tools are left out", allowed: []string{"mcp__x__y"}, want: []string{}},
		{
			name:    "patterns become their tool, in order, once",
			allowed: []string{"Bash(git log:*)", "Read", "Bash(git diff:*)", " Grep (x)"},
			want:    []string{"Bash", "Read", "Grep"},
		},
		{name: "an empty name is dropped", allowed: []string{"(x)", " "}, want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := claude.BuiltinTools(tt.allowed)
			if !slices.Equal(got, tt.want) {
				t.Errorf("BuiltinTools(%q) = %q, want %q", tt.allowed, got, tt.want)
			}
		})
	}
}

func TestStepLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		tool  string
		input string
		want  string
	}{
		{
			name: "a connector's tool names the connector",
			tool: "mcp__claude_ai_Atlassian_MCP__searchJiraIssuesUsingJql", input: `{}`,
			want: "Atlassian: searchJiraIssuesUsingJql",
		},
		{
			name: "a built-in shows its file", tool: "Read",
			input: `{"file_path":"/a/b.go","pattern":"x"}`, want: "Read /a/b.go",
		},
		{
			name: "null is skipped for the next key", tool: "Grep",
			input: `{"file_path":null,"pattern":"needle"}`, want: "Grep needle",
		},
		{
			name: "whitespace runs become one space", tool: "Bash",
			input: `{"command":"git\n\tlog  -1"}`, want: "Bash git log -1",
		},
		{
			name: "a number is written as JavaScript writes it", tool: "X",
			input: `{"path":5}`, want: "X 5",
		},
		{name: "no argument is just the tool", tool: "Glob", input: `{}`, want: "Glob"},
		{name: "input that is not an object", tool: "Glob", input: `"x"`, want: "Glob"},
		{
			name: "the argument is cut at 70 UTF-16 units", tool: "Bash",
			input: `{"command":"` + string(slices.Repeat([]byte("a"), 80)) + `"}`,
			want:  "Bash " + string(slices.Repeat([]byte("a"), 70)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			input, err := js.Parse([]byte(tt.input))
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.input, err)
			}

			got := claude.StepLabel(tt.tool, input)
			if got != tt.want {
				t.Errorf("StepLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}
