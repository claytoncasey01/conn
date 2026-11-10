package newsession_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
)

// A new session starts on the advisor its start chose, else on the one Settings saved.
func TestANewSessionStartsTheAdvisorItChoseElseTheSaved(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name, saved, body string
		want              []string
	}{
		{
			name: "none chosen: the saved one", saved: "opus", body: `{"repo":"acme/widgets"}`,
			want: []string{"--settings", `{"advisorModel":"opus"}`},
		},
		{
			name: "a choice over the saved one", saved: "opus",
			body: `{"repo":"acme/widgets","advisor":"fable"}`,
			want: []string{"--settings", `{"advisorModel":"fable"}`},
		},
		{
			name: "Claude Code's own: nothing added", saved: "sonnet",
			body: `{"repo":"acme/widgets","advisor":"claude-code"}`, want: nil,
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			setting := newRig(t)
			setting.current.Sessions.Advisor = row.saved

			reply := start(t, setting.api(), row.body)
			if reply.Code != http.StatusOK {
				t.Fatalf("code %d: %s", reply.Code, js.Marshal(reply.Body))
			}

			argv := setting.opened[0].Argv
			at := slices.Index(argv, "--settings")

			var got []string
			if at >= 0 {
				got = argv[at:]
			}

			if !slices.Equal(got, row.want) {
				t.Errorf("argv %q ends %q, want %q", argv, got, row.want)
			}
		})
	}
}
