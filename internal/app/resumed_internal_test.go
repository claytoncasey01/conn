package app

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/claytoncasey01/conn/internal/hosted"
	"github.com/claytoncasey01/conn/internal/pty"
)

// resumedGolden is the snapshot's "resumed" as the page reads it: src/lib/resumed.test.ts reads
// the same file.
const resumedGolden = "../../testdata/answers/sessions/resumed.json"

func TestTheResumedSessionsThePageListsAreGolden(t *testing.T) {
	t.Parallel()

	ended := 143
	view := resumedView([]hosted.Resumed{
		{ID: "0000000000000000000000000000000a", Name: "PROJ-1"},
		{ID: "0000000000000000000000000000000b", Name: "pr-12"},
		{ID: "0000000000000000000000000000000c", Name: "fix the widget"},
	}, []pty.Info{
		{ID: "0000000000000000000000000000000a"},
		{ID: "0000000000000000000000000000000b", ExitCode: &ended},
		{ID: "0000000000000000000000000000000c"},
		{ID: "0000000000000000000000000000000d"},
	})

	got, err := json.MarshalIndent(map[string]any{"resumed": view}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	got = append(got, '\n')

	if os.Getenv("CONN_UPDATE_GOLDEN") == "1" {
		err = os.WriteFile(resumedGolden, got, 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(resumedGolden)
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != string(want) {
		t.Errorf("the resumed sessions are\n%s\nwant\n%s", got, want)
	}
}

func TestNoResumedSessionLeavesTheFieldOut(t *testing.T) {
	t.Parallel()

	ended := 0
	resumed := []hosted.Resumed{{ID: "0000000000000000000000000000000a", Name: "PROJ-1"}}

	tests := []struct {
		name string
		live []pty.Info
	}{
		{"it ended", []pty.Info{{ID: "0000000000000000000000000000000a", ExitCode: &ended}}},
		{"the host forgot it", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if view := resumedView(resumed, tt.live); view != nil {
				t.Errorf("the snapshot lists %+v", view)
			}
		})
	}
}
