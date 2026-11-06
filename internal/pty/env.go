package pty

import "strings"

// What a hosted program's environment leaves out, and why. Every name is matched in upper case,
// whatever case the entry has; values are never looked at.
//
//   - Conn's own variables (CONN_*): its data folder, its port, and any secret one may ever carry.
//   - cmux's (CMUX_*): they would make a hosted session look like a cmux pane, so a reply could be
//     sent to Conn's own terminal.
//   - The parent terminal's identity (TERM_PROGRAM, GHOSTTY_*, KITTY_*, COLUMNS, TERMINFO, ...): a
//     hosted program runs on Conn's terminal, which the page draws with xterm.js. A program that
//     believes it runs in the parent's terminal turns on what that terminal supports; Claude Code
//     told it is in Ghostty pushes the kitty keyboard protocol, which xterm.js never sends.
//     COLUMNS and LINES would override the hosted terminal's own size.
//   - A parent multiplexer or editor (TMUX, STY, ZELLIJ*, NVIM, ...): they name the parent's
//     control sockets, which a hosted program could use to drive the parent's panes.
//   - A parent Claude Code session (CLAUDECODE, CLAUDE_CODE_SESSION_*, the messaging socket and
//     token, the bridge and background-session variables, ...): Claude Code sets these for the
//     programs it runs, so they are there when Conn itself was started from inside Claude Code.
//     They would make a hosted claude act as that session's child and hand it that session's
//     tokens and sockets. The names were taken from what a real claude (2.1.318) sets for its
//     children and from the variable names in its binary. The person's own Claude configuration
//     is kept: ANTHROPIC_*, CLAUDE_CONFIG_DIR, CLAUDE_CODE_OAUTH_TOKEN, the provider switches
//     (CLAUDE_CODE_USE_BEDROCK and the like) and every other CLAUDE_* setting not listed.
//
// TERM and COLORTERM are replaced by the hosted terminal's own (replacedVariables). To drop
// another variable, add its exact name to deniedNames, or a prefix to deniedPrefixes; a name a
// prefix would drop but that must stay goes in keptNames. The tables are read on every Start.

// replacedVariables are what the host sets itself, after the parent's own are dropped: what
// xterm.js is.
func replacedVariables() []string {
	return []string{"TERM=xterm-256color", "COLORTERM=truecolor"}
}

// deniedNames are the variables a hosted program never gets, by exact name.
func deniedNames() []string {
	return []string{
		// The parent terminal's identity.
		"TERM_PROGRAM", "TERM_PROGRAM_VERSION", "TERM_SESSION_ID", "TERM_FEATURES", "TERMINFO",
		"TERMCAP", "COLORFGBG", "COLUMNS", "LINES", "LC_TERMINAL", "LC_TERMINAL_VERSION",
		"VTE_VERSION", "WINDOWID", "TERMINAL_EMULATOR", "TILIX_ID", "TERMINATOR_UUID",
		"WT_SESSION", "WT_PROFILE_ID", "SSH_TTY",
		// A parent multiplexer or editor.
		"TMUX", "STY", "WINDOW", "ZELLIJ", "NVIM", "NVIM_LISTEN_ADDRESS", "VIM_TERMINAL",
		"INSIDE_EMACS",
		// A parent Claude Code session, seen in a real claude's children.
		"CLAUDECODE", "CLAUDE_PID", "CLAUDE_EFFORT", "CLAUDE_CODE_ENTRYPOINT",
		"CLAUDE_CODE_EXECPATH", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_MESSAGING_SOCKET",
		"CLAUDE_CODE_MESSAGING_TOKEN",
		// A parent Claude Code session, from its binary: the IDE connection, remote, cloud and
		// runner session markers, its tool call, its teammates.
		"CLAUDE_CODE_SSE_PORT", "CLAUDE_CODE_TOOL_USE_ID", "CLAUDE_CODE_REMOTE",
		"CLAUDE_CODE_REMOTE_SESSION_ID", "CLAUDE_CODE_REMOTE_SESSION_UUID",
		"CLAUDE_CODE_REMOTE_SESSION_ORIGIN", "CLAUDE_CODE_CLOUD_SESSION_ID",
		"CLAUDE_CODE_PROJECTS_SESSION", "CLAUDE_CODE_RESUME_FROM_SESSION",
		"CLAUDE_CODE_TMUX_SESSION", "CLAUDE_CODE_CONTAINER_ID", "CLAUDE_CODE_CCR_WORKER_SOCKET",
		"CLAUDE_CODE_TEAMMATE_COMMAND", "CLAUDE_SESSION_ID", "CLAUDE_SESSION_INGRESS_TOKEN_FILE",
		"CLAUDE_SESSION_SURFACE", "CLAUDE_RUNNER_SESSION_ID", "CLAUDE_RUNNER_SESSION_UUID",
		"CLAUDE_SSH_DAEMON_CHILD", "CLAUDE_INTERNAL_SESSION_TASK_LIST_ID",
		"CLAUDE_INTERNAL_ASSISTANT_TEAM_NAME", "CLAUDE_AGENT_SDK_VERSION",
		"CLAUDE_AGENT_SDK_CLIENT_APP",
		// A parent's tokens: its MCP server and sessions tokens, and descriptors only it holds.
		"CLAUDE_CODE_MCP_SERVE_AUTH_TOKEN", "CLAUDE_CODE_SESSIONS_OAUTH_TOKEN",
		"CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", "CLAUDE_CODE_GATEWAY_TOKEN_FILE_DESCRIPTOR",
		"CLAUDE_CODE_WEBSOCKET_AUTH_FILE_DESCRIPTOR",
	}
}

// deniedPrefixes drop every variable whose name starts with one, unless keptNames has it.
func deniedPrefixes() []string {
	return []string{
		// Conn's own, and cmux's.
		"CONN_", "CMUX_",
		// The parent terminal's identity, and VS Code's terminal and its IPC handles.
		"GHOSTTY_", "KITTY_", "ITERM_", "WEZTERM_", "ALACRITTY_", "KONSOLE_", "XTERM_", "WARP_",
		"VSCODE_",
		// A parent multiplexer.
		"TMUX_", "ZELLIJ_",
		// A parent Claude Code session: its own session, its bridge, its background sessions,
		// and the host that runs it.
		"CLAUDE_CODE_SESSION_", "CLAUDE_CODE_BRIDGE_", "CLAUDE_BRIDGE_", "CLAUDE_BG_",
		"CLAUDE_CODE_HOST_",
	}
}

// keptNames are the person's own settings that a denied prefix would otherwise drop.
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
