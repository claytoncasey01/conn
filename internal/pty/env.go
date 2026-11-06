package pty

import (
	"slices"
	"strings"
)

// What a hosted program's environment leaves out, and why. Every name is matched in upper case,
// whatever case the entry has; values are never looked at. Each table below lists its entries
// in its own doc comment too, and env_internal_test.go fails if the two differ, so the
// documentation cannot drift from the table.
//
// The names were taken from what a real claude (2.1.318) sets for the programs it runs, and from
// the variable names in its binary. The person's own Claude configuration is kept: ANTHROPIC_*,
// CLAUDE_CONFIG_DIR, CLAUDE_CODE_OAUTH_TOKEN, the provider switches (CLAUDE_CODE_USE_BEDROCK and
// the like) and every other CLAUDE_* setting not listed.
//
// To drop another variable, add its exact name to the group of deniedNames it belongs to, or a
// prefix to deniedPrefixes, and to that function's doc comment; a name a prefix would drop but
// that must stay goes in keptNames. TERM and COLORTERM are replaced by the hosted terminal's own
// (replacedVariables). The tables are read on every Start.

// replacedVariables are what the host sets itself, after the parent's own are dropped: what
// xterm.js is. Lists: TERM=xterm-256color COLORTERM=truecolor.
func replacedVariables() []string {
	return []string{"TERM=xterm-256color", "COLORTERM=truecolor"}
}

// deniedNames are the variables a hosted program never gets, by exact name: every group below.
func deniedNames() []string {
	return slices.Concat(terminalNames(), multiplexerNames(), parentSessionNames(),
		parentPolicyNames(), parentTokenNames())
}

// terminalNames are the parent terminal's identity. A hosted program runs on Conn's terminal,
// which the page draws with xterm.js; one that believes it runs in the parent's terminal turns on
// what that terminal supports (Claude Code told it is in Ghostty pushes the kitty keyboard
// protocol, which xterm.js never sends), and COLUMNS and LINES would override the hosted
// terminal's own size. Lists: TERM_PROGRAM TERM_PROGRAM_VERSION TERM_SESSION_ID TERM_FEATURES
// TERMINFO TERMCAP COLORFGBG COLUMNS LINES LC_TERMINAL LC_TERMINAL_VERSION VTE_VERSION WINDOWID
// TERMINAL_EMULATOR TILIX_ID TERMINATOR_UUID WT_SESSION WT_PROFILE_ID SSH_TTY.
func terminalNames() []string {
	return []string{
		"TERM_PROGRAM", "TERM_PROGRAM_VERSION", "TERM_SESSION_ID", "TERM_FEATURES", "TERMINFO",
		"TERMCAP", "COLORFGBG", "COLUMNS", "LINES", "LC_TERMINAL", "LC_TERMINAL_VERSION",
		"VTE_VERSION", "WINDOWID", "TERMINAL_EMULATOR", "TILIX_ID", "TERMINATOR_UUID",
		"WT_SESSION", "WT_PROFILE_ID", "SSH_TTY",
	}
}

// multiplexerNames are a parent multiplexer's or editor's: they name its control sockets, which
// a hosted program could use to drive the parent's panes. Lists: TMUX STY WINDOW ZELLIJ NVIM
// NVIM_LISTEN_ADDRESS VIM_TERMINAL INSIDE_EMACS.
func multiplexerNames() []string {
	return []string{
		"TMUX", "STY", "WINDOW", "ZELLIJ", "NVIM", "NVIM_LISTEN_ADDRESS", "VIM_TERMINAL",
		"INSIDE_EMACS",
	}
}

// parentSessionNames mark a parent Claude Code session, which sets them for the programs it runs,
// so they are there when Conn itself was started from inside Claude Code: they would make a
// hosted claude act as that session's child, part of its team, plugin, worker or relaunch.
// Lists: CLAUDECODE CLAUDE_PID CLAUDE_EFFORT CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_EXECPATH
// CLAUDE_CODE_CHILD_SESSION CLAUDE_CODE_MESSAGING_SOCKET CLAUDE_CODE_SSE_PORT
// CLAUDE_CODE_TOOL_USE_ID CLAUDE_CODE_REMOTE CLAUDE_CODE_REMOTE_SESSION_ID
// CLAUDE_CODE_REMOTE_SESSION_UUID CLAUDE_CODE_REMOTE_SESSION_ORIGIN CLAUDE_CODE_CLOUD_SESSION_ID
// CLAUDE_CODE_PROJECTS_SESSION CLAUDE_CODE_RESUME_FROM_SESSION CLAUDE_CODE_TMUX_SESSION
// CLAUDE_CODE_CONTAINER_ID CLAUDE_CODE_CCR_WORKER_SOCKET CLAUDE_CODE_TEAMMATE_COMMAND
// CLAUDE_CODE_AGENT CLAUDE_CODE_SPAWNED_BY_SERVER CLAUDE_CODE_INVOKED_SKILLS
// CLAUDE_CODE_TASK_LIST_ID CLAUDE_CODE_MCP_SERVE_WORKSPACE_ROOT CLAUDE_CODE_AGENT_VIEW_RELAUNCH
// CLAUDE_AGENTS_AUTO_RELAUNCHED_AT CLAUDE_RELAUNCH_SESSION_ADD_DIRS CLAUDE_PTY_HOST_EXEC
// CLAUDE_PTY_HOST_NO_STABLE_PATH CLAUDE_PLUGIN_ROOT CLAUDE_PLUGIN_DATA CLAUDE_SCRIPT_HOOK_ACK
// CLAUDE_SESSION_ID CLAUDE_SESSION_SURFACE CLAUDE_SSH_DAEMON_CHILD
// CLAUDE_INTERNAL_SESSION_TASK_LIST_ID CLAUDE_INTERNAL_ASSISTANT_TEAM_NAME
// CLAUDE_AGENT_SDK_VERSION CLAUDE_AGENT_SDK_CLIENT_APP.
func parentSessionNames() []string {
	return []string{
		"CLAUDECODE", "CLAUDE_PID", "CLAUDE_EFFORT", "CLAUDE_CODE_ENTRYPOINT",
		"CLAUDE_CODE_EXECPATH", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_MESSAGING_SOCKET",
		"CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_TOOL_USE_ID", "CLAUDE_CODE_REMOTE",
		"CLAUDE_CODE_REMOTE_SESSION_ID", "CLAUDE_CODE_REMOTE_SESSION_UUID",
		"CLAUDE_CODE_REMOTE_SESSION_ORIGIN", "CLAUDE_CODE_CLOUD_SESSION_ID",
		"CLAUDE_CODE_PROJECTS_SESSION", "CLAUDE_CODE_RESUME_FROM_SESSION",
		"CLAUDE_CODE_TMUX_SESSION", "CLAUDE_CODE_CONTAINER_ID", "CLAUDE_CODE_CCR_WORKER_SOCKET",
		"CLAUDE_CODE_TEAMMATE_COMMAND", "CLAUDE_CODE_AGENT", "CLAUDE_CODE_SPAWNED_BY_SERVER",
		"CLAUDE_CODE_INVOKED_SKILLS", "CLAUDE_CODE_TASK_LIST_ID",
		"CLAUDE_CODE_MCP_SERVE_WORKSPACE_ROOT", "CLAUDE_CODE_AGENT_VIEW_RELAUNCH",
		"CLAUDE_AGENTS_AUTO_RELAUNCHED_AT", "CLAUDE_RELAUNCH_SESSION_ADD_DIRS",
		"CLAUDE_PTY_HOST_EXEC", "CLAUDE_PTY_HOST_NO_STABLE_PATH", "CLAUDE_PLUGIN_ROOT",
		"CLAUDE_PLUGIN_DATA", "CLAUDE_SCRIPT_HOOK_ACK", "CLAUDE_SESSION_ID",
		"CLAUDE_SESSION_SURFACE", "CLAUDE_SSH_DAEMON_CHILD", "CLAUDE_INTERNAL_SESSION_TASK_LIST_ID",
		"CLAUDE_INTERNAL_ASSISTANT_TEAM_NAME", "CLAUDE_AGENT_SDK_VERSION",
		"CLAUDE_AGENT_SDK_CLIENT_APP",
	}
}

// parentPolicyNames hand a parent's trust, permission, sandbox or managed-policy decisions to the
// programs it runs: a hosted session must make its own, and ask the person. Lists:
// CLAUDE_CODE_IGNORE_PERSISTED_TRUST CLAUDE_CODE_EVEN_MORE_DANGEROUSLY_SKIP_PERMISSIONS
// CLAUDE_CODE_DISABLE_INLINE_SHELL_RM_PROMPT CLAUDE_CODE_DISABLE_MOUNTED_DRIVE_RM_PROMPT
// CLAUDE_CODE_DISABLE_SUBSTITUTION_RM_PROMPT CLAUDE_CODE_DISABLE_DANGEROUS_RM_TIMEOUT
// CLAUDE_CODE_DISABLE_ULTRAREVIEW_TRUST_CHECK CLAUDE_CODE_MANAGED_SETTINGS_PATH
// CLAUDE_CODE_DISABLE_ADMIN_ENV_UNION CLAUDE_CODE_DISABLE_POLICY_SKILLS CLAUDE_CODE_SANDBOXED
// CLAUDE_CODE_REMOTE_TOOLS_HOST_ALLOWS_UNATTENDED CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST
// CLAUDE_CODE_AUTO_MODE_EXTERNAL_PERMISSIONS CLAUDE_CHROME_PERMISSION_MODE.
func parentPolicyNames() []string {
	return []string{
		"CLAUDE_CODE_IGNORE_PERSISTED_TRUST", "CLAUDE_CODE_EVEN_MORE_DANGEROUSLY_SKIP_PERMISSIONS",
		"CLAUDE_CODE_DISABLE_INLINE_SHELL_RM_PROMPT", "CLAUDE_CODE_DISABLE_MOUNTED_DRIVE_RM_PROMPT",
		"CLAUDE_CODE_DISABLE_SUBSTITUTION_RM_PROMPT", "CLAUDE_CODE_DISABLE_DANGEROUS_RM_TIMEOUT",
		"CLAUDE_CODE_DISABLE_ULTRAREVIEW_TRUST_CHECK", "CLAUDE_CODE_MANAGED_SETTINGS_PATH",
		"CLAUDE_CODE_DISABLE_ADMIN_ENV_UNION", "CLAUDE_CODE_DISABLE_POLICY_SKILLS",
		"CLAUDE_CODE_SANDBOXED", "CLAUDE_CODE_REMOTE_TOOLS_HOST_ALLOWS_UNATTENDED",
		"CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST", "CLAUDE_CODE_AUTO_MODE_EXTERNAL_PERMISSIONS",
		"CLAUDE_CHROME_PERMISSION_MODE",
	}
}

// parentTokenNames carry a parent's tokens, or name descriptors only the parent holds (a hosted
// program inherits only its terminal). Lists: CLAUDE_CODE_MESSAGING_TOKEN
// CLAUDE_CODE_MCP_SERVE_AUTH_TOKEN CLAUDE_CODE_SESSIONS_OAUTH_TOKEN
// CLAUDE_SESSION_INGRESS_TOKEN_FILE CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR
// CLAUDE_CODE_GATEWAY_TOKEN_FILE_DESCRIPTOR CLAUDE_CODE_WEBSOCKET_AUTH_FILE_DESCRIPTOR.
func parentTokenNames() []string {
	return []string{
		"CLAUDE_CODE_MESSAGING_TOKEN", "CLAUDE_CODE_MCP_SERVE_AUTH_TOKEN",
		"CLAUDE_CODE_SESSIONS_OAUTH_TOKEN", "CLAUDE_SESSION_INGRESS_TOKEN_FILE",
		"CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", "CLAUDE_CODE_GATEWAY_TOKEN_FILE_DESCRIPTOR",
		"CLAUDE_CODE_WEBSOCKET_AUTH_FILE_DESCRIPTOR",
	}
}

// deniedPrefixes drop every variable whose name starts with one, unless keptNames has it:
// Conn's own (CONN_: its data folder, port and any secret); cmux's (CMUX_: a hosted session must
// never look like a cmux pane); a parent terminal's (GHOSTTY_ KITTY_ ITERM_ WEZTERM_ ALACRITTY_
// KONSOLE_ XTERM_ WARP_, and VSCODE_ for VS Code's terminal and its IPC handles); a parent
// multiplexer's (TMUX_ ZELLIJ_); and a parent Claude Code session's own session, bridge,
// background sessions, host, relaunch, runner, worker, eval and test harness
// (CLAUDE_CODE_SESSION_ CLAUDE_CODE_BRIDGE_ CLAUDE_BRIDGE_ CLAUDE_BG_ CLAUDE_CODE_HOST_
// CLAUDE_CODE_RELAUNCH_ CLAUDE_RUNNER_ CLAUDE_CODE_WORKER_ CLAUDE_CODE_EVAL_ CLAUDE_CODE_TEST_
// CLAUDE_TEST_).
func deniedPrefixes() []string {
	return []string{
		"CONN_", "CMUX_",
		"GHOSTTY_", "KITTY_", "ITERM_", "WEZTERM_", "ALACRITTY_", "KONSOLE_", "XTERM_", "WARP_",
		"VSCODE_",
		"TMUX_", "ZELLIJ_",
		"CLAUDE_CODE_SESSION_", "CLAUDE_CODE_BRIDGE_", "CLAUDE_BRIDGE_", "CLAUDE_BG_",
		"CLAUDE_CODE_HOST_", "CLAUDE_CODE_RELAUNCH_", "CLAUDE_RUNNER_", "CLAUDE_CODE_WORKER_",
		"CLAUDE_CODE_EVAL_", "CLAUDE_CODE_TEST_", "CLAUDE_TEST_",
	}
}

// keptNames are the person's own settings that a denied prefix would otherwise drop. Lists:
// CLAUDE_CODE_SESSION_START_ANNOUNCEMENTS_BEFORE_PROMPT.
func keptNames() []string {
	return []string{"CLAUDE_CODE_SESSION_START_ANNOUNCEMENTS_BEFORE_PROMPT"}
}

// environmentRules are the tables, as lookups.
type environmentRules struct {
	denied   map[string]bool
	kept     map[string]bool
	prefixes []string
}

func newEnvironmentRules() environmentRules {
	rules := environmentRules{
		denied: make(map[string]bool), kept: make(map[string]bool), prefixes: deniedPrefixes(),
	}

	for _, name := range deniedNames() {
		rules.denied[name] = true
	}

	for _, entry := range replacedVariables() {
		name, _, _ := strings.Cut(entry, "=")
		rules.denied[name] = true
	}

	for _, name := range keptNames() {
		rules.kept[name] = true
	}

	return rules
}

// ChildEnv is the environment a hosted program gets from parent: every entry of parent, in order,
// except those the tables above deny, in any case, and entries that are not a name, "=" and a
// value (no "=", or nothing before it); then the host's own TERM and COLORTERM. Only names are
// matched, never values. The result is never nil, so it can never stand for "the parent's
// environment" to os/exec.
func ChildEnv(parent []string) []string {
	rules := newEnvironmentRules()
	replaced := replacedVariables()
	child := make([]string, 0, len(parent)+len(replaced))

	for _, entry := range parent {
		if rules.keep(entry) {
			child = append(child, entry)
		}
	}

	return append(child, replaced...)
}

// keep says whether the environment entry may reach a hosted program.
func (rules environmentRules) keep(entry string) bool {
	name, _, found := strings.Cut(entry, "=")
	if !found {
		return false
	}

	if name == "" {
		return false
	}

	upper := strings.ToUpper(name)

	if rules.denied[upper] {
		return false
	}

	if rules.kept[upper] {
		return true
	}

	for _, prefix := range rules.prefixes {
		if strings.HasPrefix(upper, prefix) {
			return false
		}
	}

	return true
}
