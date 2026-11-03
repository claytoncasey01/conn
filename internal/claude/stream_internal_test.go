package claude

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// readAll feeds lines to a stream and returns it.
func readAll(opts Opts, lines ...string) *stream {
	reading := &stream{opts: opts, final: nil}
	for _, line := range lines {
		reading.read([]byte(line))
	}

	return reading
}

func TestOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		opts     Opts
		lines    []string
		stderr   string
		wantText string
		wantErr  error
		wantMsg  string
	}{
		{
			name:     "text answer",
			lines:    []string{`{"type":"result","result":"done"}`},
			wantText: "done",
		},
		{
			name:     "a structured answer",
			opts:     Opts{Schema: `{}`},
			lines:    []string{`{"type":"result","structured_output":{"ok":true}}`},
			wantText: "",
		},
		{
			name:    "no result line",
			lines:   []string{`not json`, `{"type":"assistant"}`},
			stderr:  "  boom\n",
			wantErr: ErrNoResult,
			wantMsg: "claude exited 1 without a result: boom",
		},
		{
			name:    "no result and no error output",
			wantErr: ErrNoResult,
			wantMsg: "claude exited 1 without a result",
		},
		{
			name:    "an error result is its own text",
			lines:   []string{`{"type":"result","is_error":true,"result":"the model said no"}`},
			wantErr: ErrReported,
			wantMsg: "the model said no",
		},
		{
			name:    "an error result with no text",
			lines:   []string{`{"type":"result","is_error":1}`},
			wantErr: ErrReported,
			wantMsg: "claude reported an error",
		},
		{
			name: "a denial fails a strict run",
			lines: []string{
				`{"type":"result","result":"x","permission_denials":` +
					`[{"tool_name":"Bash"},{"tool_name":"Bash"},{"tool_name":"Edit"}]}`,
			},
			wantErr: ErrDenied,
			wantMsg: "tools were denied: Bash, Edit",
		},
		{
			name: "a lenient run answers anyway",
			opts: Opts{Lenient: true},
			lines: []string{
				`{"type":"result","result":"x","permission_denials":[{"tool_name":"Bash"}]}`,
			},
			wantText: "x",
		},
		{
			name:    "a schema with no structured answer",
			opts:    Opts{Schema: `{}`},
			lines:   []string{`{"type":"result","result":"x","structured_output":null}`},
			wantErr: ErrNoStructured,
			wantMsg: "no structured answer came back",
		},
		{
			name:     "the last result line wins",
			lines:    []string{`{"type":"result","result":"a"}`, `{"type":"result","result":"b"}`},
			wantText: "b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reading := readAll(tt.opts, tt.lines...)

			answer, err := outcome(reading.final, tt.opts, "1", tt.stderr)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("outcome() error = %v, want %v", err, tt.wantErr)
			}

			if err != nil {
				if err.Error() != tt.wantMsg {
					t.Errorf("outcome() error = %q, want %q", err.Error(), tt.wantMsg)
				}

				return
			}

			if answer.Text != tt.wantText {
				t.Errorf("outcome() text = %q, want %q", answer.Text, tt.wantText)
			}
		})
	}
}

func TestOutcomeCutsAReportedError(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 500)
	reading := readAll(Opts{}, `{"type":"result","is_error":true,"result":"`+long+`"}`)

	_, err := outcome(reading.final, Opts{}, "0", "")
	if len(err.Error()) != reportedShownMax {
		t.Errorf("len(error) = %d, want %d", len(err.Error()), reportedShownMax)
	}
}

func TestLenientRunReportsItsDenials(t *testing.T) {
	t.Parallel()

	var denied []string

	opts := Opts{Lenient: true, OnDenied: func(tools []string) { denied = tools }}
	reading := readAll(opts, `{"type":"result","permission_denials":[{"tool_name":"Bash"}]}`)

	_, err := outcome(reading.final, opts, "0", "")
	if err != nil {
		t.Fatalf("outcome(): %v", err)
	}

	if !slices.Equal(denied, []string{"Bash"}) {
		t.Errorf("denied = %q, want [Bash]", denied)
	}
}

func TestStreamReportsSignedOutConnectors(t *testing.T) {
	t.Parallel()

	var needing []string

	opts := Opts{OnAuth: func(names []string) { needing = names }}
	readAll(opts, `{"type":"system","subtype":"init","mcp_servers":[`+
		`{"name":"claude.ai Atlassian MCP","status":"needs-auth"},`+
		`{"name":"claude.ai Atlassian","status":"needs-auth"},`+
		`{"name":"claude.ai Slack","status":"connected"},`+
		`{"name":"claude.ai Gmail","status":"needs-auth"},`+
		`{"name":"claude.ai Slack","status":"needs-auth"}]}`)

	want := []string{"Atlassian", "Slack"}
	if !slices.Equal(needing, want) {
		t.Errorf("needing = %q, want %q", needing, want)
	}
}

func TestStreamSaysNothingWhenEveryConnectorIsSignedIn(t *testing.T) {
	t.Parallel()

	called := false
	opts := Opts{OnAuth: func([]string) { called = true }}
	readAll(opts, `{"type":"system","subtype":"init","mcp_servers":[`+
		`{"name":"claude.ai Slack","status":"connected"}]}`)

	if called {
		t.Error("OnAuth was called with nothing signed out")
	}
}

func TestStreamReportsEachStep(t *testing.T) {
	t.Parallel()

	var lines []string

	opts := Opts{OnProgress: func(line string) { lines = append(lines, line) }}
	readAll(opts, `{"type":"assistant","message":{"content":[`+
		`{"type":"text","text":"hi"},`+
		`{"type":"tool_use","name":"ToolSearch","input":{}},`+
		`{"type":"tool_use","name":"Grep","input":{"pattern":"needle"}}]}}`)

	want := []string{"Grep needle"}
	if !slices.Equal(lines, want) {
		t.Errorf("progress = %q, want %q", lines, want)
	}
}

func TestLineWriterSplitsLinesAndSkipsOverlongOnes(t *testing.T) {
	t.Parallel()

	var lines []string

	writer := &lineWriter{onLine: func(line []byte) { lines = append(lines, string(line)) }}

	_, _ = writer.Write([]byte("one\ntw"))
	_, _ = writer.Write([]byte("o\n"))
	_, _ = writer.Write([]byte(strings.Repeat("x", lineBytesMax)))
	_, _ = writer.Write([]byte("y\nthree"))
	writer.flush()

	want := []string{"one", "two", "three"}
	if !slices.Equal(lines, want) {
		t.Errorf("lines = %q, want %q", lines, want)
	}
}

func TestLineWriterDropsATrailingBlankLine(t *testing.T) {
	t.Parallel()

	var lines []string

	writer := &lineWriter{onLine: func(line []byte) { lines = append(lines, string(line)) }}

	_, _ = writer.Write([]byte("one\n \t"))
	writer.flush()

	if !slices.Equal(lines, []string{"one"}) {
		t.Errorf("lines = %q, want [one]", lines)
	}
}
