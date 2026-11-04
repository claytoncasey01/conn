package reviews_test

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
	"github.com/claytoncasey01/conn/internal/reviews"
)

// Bun's reviewScan.test.ts and reviewDm.test.ts: the sign-in cases the recorded scan runs do
// not hold, and the scan through the real claude runner (a stand-in binary that records argv).

const dScanNow = 1_761_800_000_000

var errDRunBoom = errors.New("boom")

func dScanConfig() reviews.ScanConfig {
	cfg := dConfig()

	return reviews.ScanConfig{
		Model: cfg.ReviewScanModel, WindowDays: cfg.ReviewFirstWindowDays,
		BudgetUSD: cfg.ReviewScanCostUSDMax, Timeout: cfg.ReviewScanTimeout,
	}
}

func dScanInput() reviews.ScanInput {
	return reviews.ScanInput{
		Channels: []reviews.ChannelRead{{Name: "#code-review", SinceTS: nil}},
		Allow:    []string{"org/repo"}, Unmatched: []string{"org/repo#7"}, Threads: nil,
	}
}

// dFakeRun says auth is signed out, then answers the JSON text answer, or fails with failErr.
func dFakeRun(t *testing.T, answer string, auth []string, failErr error) reviews.Run {
	t.Helper()

	var structured any

	if answer != "" {
		parsed, err := js.Parse([]byte(answer))
		dMust(t, err)

		structured = parsed
	}

	return func(_ context.Context, opts claude.Opts) (claude.Answer, error) {
		if len(auth) > 0 {
			opts.OnAuth(auth)
		}

		if failErr != nil {
			return claude.Answer{}, failErr
		}

		return claude.Answer{Text: "", Structured: structured}, nil
	}
}

func dNeedsAuth(err error) ([]string, bool) {
	var scanErr *reviews.ScanError
	if !errors.As(err, &scanErr) {
		return nil, false
	}

	return scanErr.NeedsAuth, true
}

// TestDScanAnotherConnectorIsNotASignedOutSlack: on a result or on a failure.
func TestDScanAnotherConnectorIsNotASignedOutSlack(t *testing.T) {
	t.Parallel()

	empty := `{"posts":[],"asks":[]}`
	tests := []struct {
		name    string
		auth    []string
		failErr error
		want    []string
	}{
		{"Atlassian alone", []string{"claude.ai Atlassian"}, nil, []string{}},
		{
			"Atlassian and Slack",
			[]string{"Atlassian", "claude.ai Slack"},
			nil,
			[]string{"claude.ai Slack"},
		},
		{"Atlassian, then a failure", []string{"Atlassian"}, errDRunBoom, []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := reviews.RunScan(t.Context(), dScanInput(),
				dFakeRun(t, empty, tt.auth, tt.failErr), dScanConfig(), dScanNow)

			needing := got.NeedsAuth

			if tt.failErr != nil {
				var isScanErr bool

				needing, isScanErr = dNeedsAuth(err)
				if !isScanErr {
					t.Fatalf("error %v is not a ScanError", err)
				}
			}

			if !slices.Equal(needing, tt.want) {
				t.Errorf("needsAuth %q, want %q", needing, tt.want)
			}
		})
	}
}

// TestDDMScanSignInNames: another connector being signed out is a clean read; every name Slack
// goes by is a ScanError carrying it; a run that reported something is a success all the same.
func TestDDMScanSignInNames(t *testing.T) {
	t.Parallel()

	none := `{"posts":[]}`
	one := `{"posts":[{"ts":"1761760000.123456",` +
		`"permalink":"https://acme.slack.com/archives/D000/p1761760000123456",` +
		`"prUrl":"https://github.com/org/repo/pull/7","fromMe":false}]}`
	tests := []struct {
		name, answer string
		auth         []string
		failErr      error
		posts        int
		needing      []string
	}{
		{"Atlassian", none, []string{"Atlassian"}, nil, 0, nil},
		{"claude.ai Atlassian", none, []string{"claude.ai Atlassian"}, nil, 0, nil},
		{"Notion", none, []string{"Notion"}, nil, 0, nil},
		{"Slackbot", none, []string{"Slackbot"}, nil, 0, nil},
		{"Slack", none, []string{"Slack"}, nil, 0, []string{"Slack"}},
		{"claude.ai Slack", none, []string{"claude.ai Slack"}, nil, 0, []string{"claude.ai Slack"}},
		{"slack", none, []string{"slack"}, nil, 0, []string{"slack"}},
		{"CLAUDE.AI SLACK", none, []string{"CLAUDE.AI SLACK"}, nil, 0, []string{"CLAUDE.AI SLACK"}},
		{
			"Atlassian and Slack", none,
			[]string{"Atlassian", "claude.ai Slack"},
			nil, 0,
			[]string{"claude.ai Slack"},
		},
		{"Atlassian, then a failure", none, []string{"Atlassian"}, errDRunBoom, 0, []string{}},
		{"reported something", one, []string{"Slack"}, nil, 1, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			posts, err := reviews.RunDMScan(t.Context(), []string{"org/repo"},
				"1761700000.000000", dFakeRun(t, tt.answer, tt.auth, tt.failErr), dScanConfig(),
				dScanNow)

			needing, isScanErr := dNeedsAuth(err)
			if isScanErr != (tt.needing != nil) {
				t.Fatalf("error %v, want needsAuth %q", err, tt.needing)
			}

			if !slices.Equal(needing, tt.needing) {
				t.Errorf("needsAuth %q, want %q", needing, tt.needing)
			}

			if len(posts) != tt.posts {
				t.Errorf("posts %+v", posts)
			}
		})
	}
}

// dStandIn writes a claude that records its argv and answers a clean scan.
func dStandIn(t *testing.T) (string, string) {
	t.Helper()

	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	argv := filepath.Join(dir, "argv.txt")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argv + "'\ncat > /dev/null\n" +
		`echo '{"type":"system","subtype":"init","mcp_servers":[]}'` + "\n" +
		`echo '{"type":"result","is_error":false,"result":"",` +
		`"structured_output":{"posts":[],"asks":[]}}'` + "\n"
	dMust(t, os.WriteFile(bin, []byte(script), 0o755))

	return bin, argv
}

// TestDScanThroughTheRealRunnerTurnsBuiltinsOff: --tools is empty and comes before the variadic
// lists; only Slack's read tools are allowed; its write tools are denied, before the allow list,
// and none of them is also allowed.
func TestDScanThroughTheRealRunnerTurnsBuiltinsOff(t *testing.T) {
	t.Parallel()

	bin, argvFile := dStandIn(t)
	input := dScanInput()
	input.Unmatched = nil

	result, err := reviews.RunScan(t.Context(), input, claude.Runner{Bin: bin}.Run, dScanConfig(),
		dScanNow)
	dMust(t, err)

	if len(result.Posts) != 0 {
		t.Errorf("posts %+v", result.Posts)
	}

	argv := strings.Split(dRead(t, argvFile), "\n")
	if slices.Contains(argv, "--strict-mcp-config") {
		t.Error("the scan runs without the connectors")
	}

	tools := slices.Index(argv, "--tools")
	allowed := slices.Index(argv, "--allowedTools")
	denied := slices.Index(argv, "--disallowedTools")

	if tools < 0 {
		t.Fatalf("no --tools in %q", argv)
	}

	if argv[tools+1] != "" {
		t.Errorf("--tools %q", argv[tools+1])
	}

	if !dInOrder(tools, denied, allowed) {
		t.Fatalf("--tools %d, --disallowedTools %d, --allowedTools %d", tools, denied, allowed)
	}

	dCheckToolLists(t, dListAfter(argv, allowed), dListAfter(argv, denied))
}

// dCheckToolLists: only Slack tools are allowed, its write tools are denied, and none is both.
func dCheckToolLists(t *testing.T, listed, refused []string) {
	t.Helper()

	for _, tool := range listed {
		if !strings.HasPrefix(tool, "mcp__claude_ai_Slack__") {
			t.Errorf("allowed %q", tool)
		}
	}

	if len(listed) == 0 {
		t.Error("nothing allowed")
	}

	if !slices.Equal(refused, claude.SlackWrite()) {
		t.Errorf("denied %q", refused)
	}

	for _, tool := range refused {
		if slices.Contains(listed, tool) {
			t.Errorf("%s is denied and allowed", tool)
		}
	}
}

func dInOrder(first, second, third int) bool {
	if first >= second {
		return false
	}

	return second < third
}

// dListAfter is the variadic list after the flag at index: up to the next flag or the end.
func dListAfter(argv []string, index int) []string {
	out := []string{}

	for _, arg := range argv[index+1:] {
		if strings.HasPrefix(arg, "--") {
			break
		}

		if arg != "" {
			out = append(out, arg)
		}
	}

	return out
}
