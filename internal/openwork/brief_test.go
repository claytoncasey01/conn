package openwork_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/openwork"
)

// The build session's brief, in golden files under testdata/: a one-repository run's is the text
// it always was, byte for byte; a run in several repositories lists them all, in the shared
// layout as one session's brief and in the split layout as one brief per session.

// briefRun is a run's record with fixed folders, so its brief can be a golden file.
func briefRun(repos, layout string) string {
	return `{"key":"PROJ-7","title":"Export keeps the range","model":"haiku","state":"asking",` +
		`"stage":"ask","startedAt":1,"updatedAt":1,"jiraStatus":"In Progress","agents":[],` +
		`"answered":[{"q":"Which formats?","a":"Every one.","src":"CONFLUENCE",` +
		`"where":"Export spec","conf":"HIGH"}],"questions":[{"id":"q1","text":"How far back?",` +
		`"why":"No source says.","choices":[{"id":"c1","label":"90 DAYS"}],"answer":"c1"}],` +
		`"log":[],"error":null,"brief":"Change exportRows().","desc":"The export drops rows.",` +
		`"ticketStatus":"In Progress","repos":[` + repos + `],"layout":"` + layout + `"}`
}

const (
	widgetsLeg = `{"repo":"acme/widgets","clone":"/work/widgets","base":"main",` +
		`"prBase":"main","branch":"proj-7-export-keeps-the-range",` +
		`"worktree":"/work/widgets-proj-7","worktreeReady":true,"session":null,` +
		`"claudeSessionId":null,"manualCommand":null,"ahead":0,"dirty":0,"pr":null,` +
		`"cleaned":null}`
	gadgetsLeg = `{"repo":"acme/gadgets","clone":"/work/gadgets","base":"develop",` +
		`"prBase":"develop","branch":"proj-7-export-keeps-the-range",` +
		`"worktree":"/work/gadgets-proj-7","worktreeReady":true,"session":null,` +
		`"claudeSessionId":null,"manualCommand":null,"ahead":0,"dirty":0,"pr":null,` +
		`"cleaned":null}`
)

// golden is the text of a golden file, rewritten from got first under CONN_UPDATE_GOLDEN=1.
func golden(t *testing.T, name, got string) string {
	t.Helper()

	path := filepath.Join("testdata", name)

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

	return string(want)
}

func TestTheBriefOfAOneRepositoryRunIsTheTextItAlwaysWas(t *testing.T) {
	t.Parallel()

	record := recordOf(t, body(t, briefRun(widgetsLeg, "shared")))
	got := openwork.BriefText(record, nil)

	if got != golden(t, "brief-one.md", got) {
		t.Errorf("the brief changed:\n%s", got)
	}

	if openwork.BriefText(record, record.Repos[0]) != got {
		t.Error("the one leg's own brief is not the run's")
	}
}

func TestTheBriefOfARunInSeveralRepositoriesNamesThemAll(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name, layout, file string
		// leg is the leg whose session the brief is for, -1 for the shared session.
		leg int
		// says are what the brief must say, besides its golden file.
		says []string
	}{
		{
			"shared", "shared", "brief-shared.md", -1, []string{
				"Work only in these directories, each on its own branch.",
				"write .conn/pr-body.md in each worktree with changes",
				"- acme/gadgets in /work/gadgets-proj-7, on branch " +
					"proj-7-export-keeps-the-range. The pull request will target develop.",
			},
		},
		{
			"split, the first", "split", "brief-split-widgets.md", 0, []string{
				"- acme/gadgets in /work/gadgets-proj-7",
				"Another session works there",
				"Work only in this directory, on this branch.",
			},
		},
		{
			"split, the second", "split", "brief-split-gadgets.md", 1, []string{
				"- acme/widgets in /work/widgets-proj-7",
				"Another session works there",
				"If a change is needed outside acme/gadgets, ask the person",
			},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			record := recordOf(t, body(t, briefRun(widgetsLeg+","+gadgetsLeg, row.layout)))

			var leg *openwork.Leg
			if row.leg >= 0 {
				leg = record.Repos[row.leg]
			}

			got := openwork.BriefText(record, leg)
			if got != golden(t, row.file, got) {
				t.Errorf("the brief changed:\n%s", got)
			}

			for _, said := range row.says {
				if !strings.Contains(got, said) {
					t.Errorf("the brief does not say %q", said)
				}
			}
		})
	}
}
