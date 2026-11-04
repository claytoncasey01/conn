package modules_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/claude"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/modules"
)

var (
	errPermission = errors.New("permission denied")
	errTimedOut   = errors.New("timed out after 60s")
)

// fakeRun answers a probe's run with structured (a JSON text) or err, after reporting the
// connectors in auth as signed out. It keeps the options it was given.
type fakeRun struct {
	structured string
	err        error
	auth       []string
	seen       claude.Opts
}

func (f *fakeRun) run(_ context.Context, opts claude.Opts) (claude.Answer, error) {
	f.seen = opts
	if len(f.auth) > 0 {
		opts.OnAuth(f.auth)
	}

	if f.err != nil {
		return claude.Answer{}, f.err
	}

	value, err := js.Parse([]byte(f.structured))
	if err != nil {
		panic(err)
	}

	return claude.Answer{Text: "", Structured: value}, nil
}

func probeConnector(
	t *testing.T, connector modules.Connector, fake *fakeRun,
) modules.ProbeResult {
	t.Helper()

	result, err := modules.ConnectorProbe(connector, fake.run)(t.Context())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	return result
}

func TestConnectorProbe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		connector modules.Connector
		fake      *fakeRun
		ok        bool
		detail    string
	}{
		{
			name: "Slack works", connector: modules.Slack,
			fake: &fakeRun{structured: `{"ok":true,"name":"Jane Doe"}`},
			ok:   true, detail: "Slack connector works (Jane Doe)",
		},
		{
			name: "Atlassian works with no name", connector: modules.Atlassian,
			fake: &fakeRun{structured: `{"ok":true}`},
			ok:   true, detail: "Atlassian connector works",
		},
		{
			name: "an answer that is not ok", connector: modules.Slack,
			fake:   &fakeRun{structured: `{"ok":false,"error":"invalid_auth"}`},
			detail: "Slack did not answer: invalid_auth",
		},
		{
			name: "not ok with no reason", connector: modules.Slack,
			fake: &fakeRun{structured: `{"ok":false}`}, detail: "Slack did not answer",
		},
		{
			name: "signed out", connector: modules.Atlassian,
			fake:   &fakeRun{structured: `{"ok":true}`, auth: []string{"Atlassian"}},
			detail: "Atlassian is not signed in: connect it in claude.ai",
		},
		{
			name: "another connector signed out is not this one's problem",
			fake: &fakeRun{structured: `{"ok":true}`, auth: []string{"Atlassian"}},
			ok:   true, connector: modules.Slack, detail: "Slack connector works",
		},
		{
			name: "a failed run after reporting the connector signed out", connector: modules.Slack,
			fake:   &fakeRun{err: errPermission, auth: []string{"Slack"}},
			detail: "Slack could not be checked (not signed in: connect it in claude.ai)",
		},
		{
			name: "a failed run", connector: modules.Slack, fake: &fakeRun{err: errTimedOut},
			detail: "Slack could not be checked (timed out after 60s)",
		},
		{
			name: "an answer that is not an object", connector: modules.Slack,
			fake: &fakeRun{structured: `"not an object"`}, detail: "Slack did not answer",
		},
		{
			name: "only the boolean true is ok", connector: modules.Slack,
			fake: &fakeRun{structured: `{"ok":"yes"}`}, detail: "Slack did not answer",
		},
		{
			name: "null", connector: modules.Slack,
			fake: &fakeRun{structured: `null`}, detail: "Slack did not answer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := probeConnector(t, tt.connector, tt.fake)
			if got.OK != tt.ok {
				t.Errorf("OK = %v, want %v", got.OK, tt.ok)
			}

			if got.Detail != tt.detail {
				t.Errorf("Detail = %q, want %q", got.Detail, tt.detail)
			}
		})
	}
}

func TestConnectorProbeCleansWhatTheModelSaid(t *testing.T) {
	t.Parallel()

	got := probeConnector(t, modules.Slack, &fakeRun{
		structured: `{"ok":true,"name":"Jane\u202e\nDoe` + strings.Repeat("x", 200) + `"}`,
	})
	if !got.OK {
		t.Fatal("OK = false, want true")
	}

	if js.Len(got.Detail) >= 100 {
		t.Errorf("Detail is %d units long, want under 100", js.Len(got.Detail))
	}

	if strings.ContainsAny(got.Detail, "\u202e\n") {
		t.Errorf("Detail = %q keeps a control or bidi character", got.Detail)
	}
}

func TestConnectorProbeRunsOneReadTool(t *testing.T) {
	t.Parallel()

	fake := &fakeRun{structured: `{"ok":true}`}
	probeConnector(t, modules.Slack, fake)

	if !slices.Equal(fake.seen.DisallowedTools, claude.SlackWrite()) {
		t.Errorf("DisallowedTools = %q, want the Slack write tools", fake.seen.DisallowedTools)
	}

	if fake.seen.NoMCP {
		t.Error("NoMCP = true, but the probe needs its connector")
	}

	if fake.seen.Lenient {
		t.Error("Lenient = true, but a denied tool must fail the probe")
	}
}

// TestConnectorProbeCommandLine holds each probe's whole command line and prompt in a golden
// file. CONN_UPDATE_GOLDEN=1 rewrites them.
func TestConnectorProbeCommandLine(t *testing.T) {
	t.Parallel()

	for _, connector := range []modules.Connector{modules.Slack, modules.Atlassian} {
		t.Run(string(connector), func(t *testing.T) {
			t.Parallel()

			opts := modules.ConnectorOpts(connector)
			got := strings.Join(claude.Args(opts), "\n") + "\n\n" + opts.Prompt + "\n"
			path := filepath.Join("testdata", "check-"+string(connector)+".txt")

			if os.Getenv("CONN_UPDATE_GOLDEN") == "1" {
				err := os.WriteFile(path, []byte(got), 0o600)
				if err != nil {
					t.Fatal(err)
				}
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if got != string(want) {
				t.Errorf("the %s probe changed:\n%s\nwant:\n%s", connector, got, want)
			}
		})
	}
}
