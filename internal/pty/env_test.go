package pty_test

import (
	"slices"
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
		{"nothing", nil, []string{"TERM=xterm-256color"}},
		{
			"ordinary variables are kept in order",
			[]string{"PATH=/usr/bin", "HOME=/Users/me", "LANG=en_US.UTF-8"},
			[]string{"PATH=/usr/bin", "HOME=/Users/me", "LANG=en_US.UTF-8", "TERM=xterm-256color"},
		},
		{
			"Conn's own variables go",
			[]string{"CONN_DATA=/d", "CONN_CLAUDE=/bin/claude", "CONN_SECRET=s", "CONN_=x", "A=1"},
			[]string{"A=1", "TERM=xterm-256color"},
		},
		{
			"cmux's variables go",
			[]string{"CMUX_SOCKET_PATH=/tmp/s", "CMUX_SURFACE_ID=1", "CMUX_=", "B=2"},
			[]string{"B=2", "TERM=xterm-256color"},
		},
		{
			"in any case",
			[]string{"conn_data=/d", "Conn_Port=1", "cmux_socket=/s", "CMux_X=1", "C=3"},
			[]string{"C=3", "TERM=xterm-256color"},
		},
		{
			"an entry without = goes",
			[]string{"CONN_DATA", "CMUX_SOCKET", "JUSTANAME", "D=4"},
			[]string{"D=4", "TERM=xterm-256color"},
		},
		{
			"an entry with no name goes",
			[]string{"=CONN_DATA=/d", "=", "E=5"},
			[]string{"E=5", "TERM=xterm-256color"},
		},
		{
			"only the name is matched, not the value",
			[]string{"F=CONN_DATA", "G=CMUX_SOCKET=/s"},
			[]string{"F=CONN_DATA", "G=CMUX_SOCKET=/s", "TERM=xterm-256color"},
		},
		{
			"a name that only starts like one is kept",
			[]string{"CONNECTION=1", "CMUXED=2", "XCONN_DATA=3"},
			[]string{"CONNECTION=1", "CMUXED=2", "XCONN_DATA=3", "TERM=xterm-256color"},
		},
		{
			"the terminal's own TERM replaces the parent's",
			[]string{"TERM=xterm-ghostty", "term=dumb", "H=8"},
			[]string{"H=8", "TERM=xterm-256color"},
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
