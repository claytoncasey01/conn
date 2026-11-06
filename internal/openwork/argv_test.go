package openwork_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/openwork"
	"github.com/claytoncasey01/conn/internal/pty"
)

const goldenClaude = "/opt/claude/bin/claude"

// hostileLeg is a leg whose branch and folder hold what an attacker who can name a branch or a
// folder would put there.
func hostileLeg(repo, worktree string) *openwork.Leg {
	return &openwork.Leg{
		Repo: repo, Branch: "proj-12-$(id)--x", Base: "main", PRBase: "main", Worktree: worktree,
	}
}

// hostileRecord is a run whose ticket title and description hold what an attacker who can edit a
// Jira ticket would put there, building in legs, laid out as layout.
func hostileRecord(layout string, legs ...*openwork.Leg) *openwork.Record {
	return &openwork.Record{
		Key:   "PROJ-12",
		Title: `"; rm -rf ~ $(id) --dangerously-skip-permissions` + "\n-x",
		Desc:  "`id`\n--permission-mode bypassPermissions",
		Repos: legs, Layout: layout,
	}
}

// legFolders makes a folder for each leg under a base of its own, and returns the base and the
// folders: --add-dir names only a worktree that is there.
func legFolders(t *testing.T, names ...string) (string, []string) {
	t.Helper()

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	folders := make([]string, 0, len(names))

	for _, name := range names {
		folder := filepath.Join(base, name)

		err := os.MkdirAll(folder, 0o700)
		if err != nil {
			t.Fatal(err)
		}

		folders = append(folders, folder)
	}

	return base, folders
}

// goldenSpec checks spec against testdata/name, with base written /work: the argv one argument a
// line as JSON, the folder and the name. CONN_UPDATE_GOLDEN=1 rewrites it.
func goldenSpec(t *testing.T, name, base string, spec pty.Spec) {
	t.Helper()

	got, err := json.MarshalIndent(map[string]any{
		"argv": spec.Argv, "dir": spec.Dir, "name": spec.Name,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	text := strings.ReplaceAll(string(got), base, "/work") + "\n"
	path := filepath.Join("testdata", name)

	if os.Getenv("CONN_UPDATE_GOLDEN") == "1" {
		err = os.WriteFile(path, []byte(text), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if text != string(want) {
		t.Errorf("%s changed:\n%s\nwant:\n%s", name, text, want)
	}
}

// TestTheBuildSessionsArgvIsGolden: the build session's program, arguments, folder and name, for
// one repository, the shared session of two (reaching the other with --add-dir) and each split
// session of two (reaching none).
func TestTheBuildSessionsArgvIsGolden(t *testing.T) {
	t.Parallel()

	base, folders := legFolders(t, "widgets-proj-12", "gadgets-proj-12")
	widgets := hostileLeg("acme/widgets", folders[0])
	gadgets := hostileLeg("acme/gadgets", folders[1])

	one := hostileRecord("shared", widgets)
	goldenSpec(t, "argv-build.json", base, openwork.LaunchSpec(goldenClaude, one, widgets))

	shared := hostileRecord("shared", widgets, gadgets)
	goldenSpec(t, "argv-build-shared.json", base,
		openwork.LaunchSpec(goldenClaude, shared, widgets))

	split := hostileRecord("split", widgets, gadgets)
	goldenSpec(t, "argv-build-split.json", base, openwork.LaunchSpec(goldenClaude, split, gadgets))
}

// TestTheResumeSessionsArgvIsGolden: resumed on its conversation when the id is a UUID, and on
// the folder's last conversation when it is not (a tampered id never reaches an argument).
func TestTheResumeSessionsArgvIsGolden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		sessionID string
		golden    string
		verb      string
	}{
		{"a session id", "0f8fad5b-d9cb-469f-a165-70867728950e", "argv-resume.json", "resumed"},
		{
			"a tampered id", "0f8fad5b; rm -rf ~", "argv-continue.json",
			"reopened on its last conversation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, folders := legFolders(t, "widgets-proj-12", "gadgets-proj-12")

			err := os.MkdirAll(filepath.Join(folders[0], ".conn"), 0o700)
			if err != nil {
				t.Fatal(err)
			}

			err = os.WriteFile(filepath.Join(folders[0], ".conn", "delegation.md"), []byte("d"),
				0o600)
			if err != nil {
				t.Fatal(err)
			}

			lead := hostileLeg("acme/widgets", folders[0])
			lead.ClaudeSessionID = &tt.sessionID
			record := hostileRecord("shared", lead, hostileLeg("acme/gadgets", folders[1]))

			spec, verb := openwork.ResumeSpec(goldenClaude, record, lead)
			goldenSpec(t, tt.golden, base, spec)

			if verb != tt.verb {
				t.Errorf("verb %q", verb)
			}
		})
	}
}

// TestNothingFromTheTicketOrTheFolderIsAnArgument: the title, description and branches are in no
// argument; a slot's own folder is only the spec's folder, the other legs' each one --add-dir
// argument; and no argument but claude's own options starts with a dash.
func TestNothingFromTheTicketOrTheFolderIsAnArgument(t *testing.T) {
	t.Parallel()

	_, folders := legFolders(t, "it's $(id)\n--x", "-gadgets")
	lead := hostileLeg("acme/widgets", folders[0])
	sessionID := "0f8fad5b-d9cb-469f-a165-70867728950e"
	lead.ClaudeSessionID = &sessionID
	other := hostileLeg("acme/gadgets", folders[1])
	record := hostileRecord("shared", lead, other)
	resumed, _ := openwork.ResumeSpec(goldenClaude, record, lead)

	for _, spec := range []pty.Spec{openwork.LaunchSpec(goldenClaude, record, lead), resumed} {
		if spec.Dir != lead.Worktree {
			t.Errorf("folder %q", spec.Dir)
		}

		checkArguments(t, spec, record, other.Worktree)
	}
}

// checkArguments fails when an argument holds the ticket's text or a branch, a folder but reach
// is one, or reads as an option claude does not have here.
func checkArguments(t *testing.T, spec pty.Spec, record *openwork.Record, reach string) {
	t.Helper()

	hostile := []string{record.Title, record.Desc, record.Repos[0].Branch, record.Repos[0].Worktree}
	options := []string{
		"--name", "--model", "--append-system-prompt-file", "--permission-mode", "--resume",
		"--add-dir", "--",
	}

	for index, argument := range spec.Argv[1:] {
		for _, text := range hostile {
			if strings.Contains(argument, text) {
				t.Errorf("argument %q holds %q", argument, text)
			}
		}

		if argument == reach {
			if spec.Argv[index] != "--add-dir" {
				t.Errorf("the other leg's folder %q is not an --add-dir argument", argument)
			}

			continue
		}

		if strings.HasPrefix(argument, "-") {
			if !slices.Contains(options, argument) {
				t.Errorf("argument %q reads as an option", argument)
			}
		}
	}
}
