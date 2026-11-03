package modules_test

import (
	"context"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/modules"
)

const (
	header      = "github.com\n"
	goodAccount = "  ✓ Logged in to github.com account jane (keyring)\n" +
		"  - Active account: true\n  - Git operations protocol: https\n" +
		"  - Token: gho_************************************\n" +
		"  - Token scopes: 'repo', 'read:org'\n"
	staleAccount = "\n  X Failed to log in to github.com account old-me (keyring)\n" +
		"  - Active account: false\n  - The token in keyring is invalid.\n" +
		"  - To re-authenticate, run: gh auth login -h github.com\n" +
		"  - To forget about this account, run: gh auth logout -h github.com -u old-me\n"
	brokenJane = "  X Failed to log in to github.com account jane (keyring)\n" +
		"  - Active account: true\n  - The token in keyring is invalid.\n"
	brokenJaneDetail = "Failed to log in to github.com account jane (keyring): " +
		"The token in keyring is invalid."
)

// exited is a gh that ran and exited with code.
func exited(code int, out, err string) modules.AuthRun {
	return modules.AuthRun{Exited: true, Code: code, Out: out, Err: err, SpawnError: ""}
}

func probeGitHub(t *testing.T, ended modules.AuthRun) modules.ProbeResult {
	t.Helper()

	probe := modules.GitHubProbe(func(context.Context) modules.AuthRun { return ended })

	result, err := probe(t.Context())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	return result
}

func TestGitHubProbe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ended  modules.AuthRun
		ok     bool
		detail string
	}{
		{
			name: "signed in, the login from stdout",
			ended: exited(0, "github.com\n  ✓ Logged in to github.com account jane (keyring)\n"+
				"  - Active account: true\n  - Token: gho_****\n", ""),
			ok: true, detail: "Signed in as jane",
		},
		{
			name:  "signed in, the older wording on stderr",
			ended: exited(0, "", "✓ Logged in to github.com as jane-doe (oauth_token)"),
			ok:    true, detail: "Signed in as jane-doe",
		},
		{
			name: "signed in, no login printed", ended: exited(0, "", ""), ok: true,
			detail: "Signed in to GitHub",
		},
		{
			name:  "a login that is not valid is not shown",
			ended: exited(0, "Logged in to github.com account <img> (x)", ""),
			ok:    true, detail: "Signed in to GitHub",
		},
		{
			name: "not signed in",
			ended: exited(1, "", "You are not logged into any GitHub hosts. "+
				"To log in, run: gh auth login\n"),
			detail: "Not signed in: run gh auth login",
		},
		{
			name:   "gh is not installed",
			ended:  modules.AuthRun{Exited: false, Code: -1, SpawnError: "ENOENT"},
			detail: "The gh CLI was not found: install it, then run gh auth login",
		},
		{
			name:   "gh did not answer",
			ended:  modules.AuthRun{Exited: false, Code: -1, SpawnError: "killed"},
			detail: "gh did not answer in time",
		},
		{
			name: "the reason is the account line and why, never the host",
			ended: exited(1, header+brokenJane+
				"  - To re-authenticate, run: gh auth login -h github.com\n", ""),
			detail: brokenJaneDetail,
		},
		{
			name:   "the same on stderr",
			ended:  exited(1, "", header+brokenJane),
			detail: brokenJaneDetail,
		},
		{
			name:   "no account line still never shows the bare host",
			ended:  exited(1, "", header+"something went wrong with the network\n"),
			detail: "something went wrong with the network",
		},
		{
			name:  "a stale second account does not make a working login fail",
			ended: exited(1, header+goodAccount+staleAccount, ""),
			ok:    true, detail: "Signed in as jane (another account needs gh auth login)",
		},
		{
			name:  "exit 0 with a stale account",
			ended: exited(0, header+goodAccount+staleAccount, ""),
			ok:    true, detail: "Signed in as jane (another account needs gh auth login)",
		},
		{
			name:  "the stale account listed first",
			ended: exited(1, header+strings.TrimSpace(staleAccount)+"\n"+goodAccount, ""),
			ok:    true, detail: "Signed in as jane (another account needs gh auth login)",
		},
		{
			name: "an older gh with no active line: the check mark decides",
			ended: exited(1, header+"  ✓ Logged in to github.com as jane (oauth_token)\n"+
				"  X Logged in to ghe.example.com as old (oauth_token)\n", ""),
			ok: true, detail: "Signed in as jane (another account needs gh auth login)",
		},
		{
			name:   "an older gh that failed",
			ended:  exited(1, "", "X github.com: authentication failed\n"),
			detail: "github.com: authentication failed",
		},
		{
			name:   "a token in the text is replaced",
			ended:  exited(1, "", "The token ghp_abcdefghij1234567890 is bad"),
			detail: "The token *** is bad",
		},
		{
			name:   "a fine-grained token is replaced",
			ended:  exited(1, "", "github_pat_11ABCDEFG0abcdefghij_xyz is bad"),
			detail: "*** is bad",
		},
		{name: "nothing printed", ended: exited(2, "", ""), detail: "gh auth status exited with 2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := probeGitHub(t, tt.ended)
			if got.OK != tt.ok {
				t.Errorf("OK = %v, want %v", got.OK, tt.ok)
			}

			if got.Detail != tt.detail {
				t.Errorf("Detail = %q, want %q", got.Detail, tt.detail)
			}
		})
	}
}

func TestGitHubProbeWhenTheActiveAccountIsTheBrokenOne(t *testing.T) {
	t.Parallel()

	got := probeGitHub(t, exited(1, header+brokenJane+
		"\n  ✓ Logged in to github.com account other (keyring)\n  - Active account: false\n", ""))
	if got.OK {
		t.Error("OK = true, want false")
	}

	if !strings.Contains(got.Detail, "account jane") {
		t.Errorf("Detail = %q, want it to name account jane", got.Detail)
	}
}

func TestGitHubProbeNeverShowsATokenUnderAnAccount(t *testing.T) {
	t.Parallel()

	got := probeGitHub(t, exited(1, "", header+
		"  X Failed to log in to github.com account jane (keyring)\n"+
		"  - The token gho_abcdefghijklmnop in keyring is invalid.\n"))
	if strings.Contains(got.Detail, "gho_") {
		t.Errorf("Detail = %q shows the token", got.Detail)
	}
}

func TestLoginFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text string
		want string
	}{
		{text: "✓ Logged in to github.com account jane (keyring)", want: "jane"},
		{text: "✓ Logged in to github.com as jane_emu (oauth_token)", want: "jane_emu"},
		{text: "✓ Logged in to github.com account -rf (x)", want: ""},
		{text: "✓ Logged in to github.com account a<b> (x)", want: ""},
		{text: "X Logged in to github.com account jane (x)", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			t.Parallel()

			got := modules.LoginFrom(tt.text)
			if got != tt.want {
				t.Errorf("LoginFrom(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}
