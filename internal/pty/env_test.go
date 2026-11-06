package pty_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/pty"
)

func TestChildEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		parent []string
		want   []string
	}{
		{"nothing", nil, []string{"TERM=xterm-256color", "COLORTERM=truecolor"}},
		{
			"ordinary variables are kept in order",
			[]string{"PATH=/usr/bin", "HOME=/Users/me", "LANG=en_US.UTF-8"},
			[]string{
				"PATH=/usr/bin", "HOME=/Users/me", "LANG=en_US.UTF-8", "TERM=xterm-256color",
				"COLORTERM=truecolor",
			},
		},
		{
			"Conn's own variables go",
			[]string{"CONN_DATA=/d", "CONN_CLAUDE=/bin/claude", "CONN_SECRET=s", "CONN_=x", "A=1"},
			[]string{"A=1", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
		{
			"cmux's variables go",
			[]string{"CMUX_SOCKET_PATH=/tmp/s", "CMUX_SURFACE_ID=1", "CMUX_=", "B=2"},
			[]string{"B=2", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
		{
			"in any case",
			[]string{"conn_data=/d", "Conn_Port=1", "cmux_socket=/s", "CMux_X=1", "C=3"},
			[]string{"C=3", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
		{
			"an entry without = goes",
			[]string{"CONN_DATA", "CMUX_SOCKET", "JUSTANAME", "D=4"},
			[]string{"D=4", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
		{
			"an entry with no name goes",
			[]string{"=CONN_DATA=/d", "=", "E=5"},
			[]string{"E=5", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
		{
			"only the name is matched, not the value",
			[]string{"F=CONN_DATA", "G=CMUX_SOCKET=/s"},
			[]string{
				"F=CONN_DATA", "G=CMUX_SOCKET=/s", "TERM=xterm-256color", "COLORTERM=truecolor",
			},
		},
		{
			"a name that only starts like one is kept",
			[]string{"CONNECTION=1", "CMUXED=2", "XCONN_DATA=3"},
			[]string{
				"CONNECTION=1", "CMUXED=2", "XCONN_DATA=3", "TERM=xterm-256color",
				"COLORTERM=truecolor",
			},
		},
		{
			"the parent terminal's identity goes",
			[]string{
				"TERM_PROGRAM=ghostty", "TERM_PROGRAM_VERSION=1", "LC_TERMINAL=iTerm2",
				"LC_TERMINAL_VERSION=3", "GHOSTTY_RESOURCES_DIR=/g", "KITTY_WINDOW_ID=1",
				"ITERM_SESSION_ID=w0", "WEZTERM_PANE=1", "VTE_VERSION=7000", "WT_SESSION=x",
				"ALACRITTY_SOCKET=/a", "TERMINFO=/g/terminfo", "COLUMNS=80", "LINES=24",
				"TERM_SESSION_ID=s", "COLORFGBG=15;0", "WINDOWID=3", "KONSOLE_VERSION=2",
				"VSCODE_GIT_IPC_HANDLE=/v", "I=9",
			},
			[]string{"I=9", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
		{
			"a parent multiplexer or editor goes",
			[]string{
				"TMUX=/tmp/tmux,1,0", "TMUX_PANE=%1", "STY=1.pts", "WINDOW=0", "ZELLIJ=0",
				"ZELLIJ_SESSION_NAME=z", "NVIM=/n", "NVIM_LISTEN_ADDRESS=/n", "INSIDE_EMACS=t",
				"SSH_TTY=/dev/ttys001", "J=10",
			},
			[]string{"J=10", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
		{
			"a parent Claude Code session's variables go",
			[]string{
				"CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli", "CLAUDE_CODE_SESSION_ID=u",
				"CLAUDE_CODE_MESSAGING_TOKEN=t", "CLAUDE_CODE_MESSAGING_SOCKET=/s",
				"CLAUDE_CODE_SSE_PORT=1", "CLAUDE_CODE_BRIDGE_SESSION_ID=b",
				"CLAUDE_CODE_CHILD_SESSION=1", "CLAUDE_CODE_SESSION_ATTENDED=1",
				"CLAUDE_CODE_EXECPATH=/c", "CLAUDE_PID=1", "CLAUDE_EFFORT=high",
				"CLAUDE_BRIDGE_OAUTH_TOKEN=t", "CLAUDE_BG_PTY_AUTH=a",
				"CLAUDE_CODE_HOST_CREDS_FILE=/f", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR=3",
				"K=11",
			},
			[]string{"K=11", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
		{
			"the person's own Claude, provider and cloud settings are kept",
			[]string{
				"ANTHROPIC_API_KEY=k", "ANTHROPIC_BASE_URL=u", "CLAUDE_CODE_USE_BEDROCK=1",
				"CLAUDE_CODE_USE_VERTEX=1", "CLAUDE_CODE_USE_FOUNDRY=1",
				"CLAUDE_CODE_OAUTH_TOKEN=t", "CLAUDE_CONFIG_DIR=/c", "AWS_PROFILE=p",
				"GOOGLE_CLOUD_PROJECT=g", "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1",
				"TERMINFO_DIRS=/t", "CLAUDE_CODE_SESSIONEND_HOOKS_TIMEOUT_MS=1",
			},
			[]string{
				"ANTHROPIC_API_KEY=k", "ANTHROPIC_BASE_URL=u", "CLAUDE_CODE_USE_BEDROCK=1",
				"CLAUDE_CODE_USE_VERTEX=1", "CLAUDE_CODE_USE_FOUNDRY=1",
				"CLAUDE_CODE_OAUTH_TOKEN=t", "CLAUDE_CONFIG_DIR=/c", "AWS_PROFILE=p",
				"GOOGLE_CLOUD_PROJECT=g", "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1",
				"TERMINFO_DIRS=/t", "CLAUDE_CODE_SESSIONEND_HOOKS_TIMEOUT_MS=1",
				"TERM=xterm-256color", "COLORTERM=truecolor",
			},
		},
		{
			"a name in the keep list wins over a denied prefix",
			[]string{
				"CLAUDE_CODE_SESSION_START_ANNOUNCEMENTS_BEFORE_PROMPT=1",
				"CLAUDE_CODE_SESSION_X=1",
			},
			[]string{
				"CLAUDE_CODE_SESSION_START_ANNOUNCEMENTS_BEFORE_PROMPT=1",
				"TERM=xterm-256color", "COLORTERM=truecolor",
			},
		},
		{
			"denied names go in any case and with no = at all",
			[]string{"claudecode=1", "Term_Program=x", "TMUX", "tmux_pane", "L=12"},
			[]string{"L=12", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
		{
			"duplicates: every copy of a denied name goes, others stay as given",
			[]string{"CLAUDECODE=1", "M=1", "CLAUDECODE=2", "M=2", "COLORTERM=24bit"},
			[]string{"M=1", "M=2", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
		{
			"the terminal's own TERM replaces the parent's",
			[]string{"TERM=xterm-ghostty", "term=dumb", "H=8"},
			[]string{"H=8", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parent := slices.Clone(tt.parent)

			got := pty.ChildEnv(parent)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("ChildEnv(%q) = %q, want %q", tt.parent, got, tt.want)
			}

			if !slices.Equal(parent, tt.parent) {
				t.Fatalf("ChildEnv changed its input to %q", parent)
			}
		})
	}
}

// Whatever a Spec's environment asks for, a denied variable never reaches the program: for every
// denied name, and a name under every denied prefix, in two cases.
func TestStartNeverPassesADeniedVariable(t *testing.T) {
	t.Parallel()

	names := pty.DeniedNames()
	for _, prefix := range pty.DeniedPrefixes() {
		names = append(names, prefix+"ANYTHING")
	}

	env := make([]string, 0, 1+2*len(names))
	env = append(env, "KEEP=1")

	for _, name := range names {
		env = append(env, name+"=value", strings.ToLower(name)+"=value")
	}

	host, spawner := newFakeHost(t, pty.DefaultLimits())
	spec := testSpec(t)
	spec.Env = env

	_, err := host.Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}

	got := spawner.lastSpec(t).Env
	want := []string{"KEEP=1", "TERM=xterm-256color", "COLORTERM=truecolor"}

	if !slices.Equal(got, want) {
		// Only names: a value may be a secret.
		names := make([]string, 0, len(got))
		for _, entry := range got {
			name, _, _ := strings.Cut(entry, "=")
			names = append(names, name)
		}

		t.Fatalf("the program got the variables %q", names)
	}
}
