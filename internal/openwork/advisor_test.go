package openwork_test

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/openwork"
)

// A START's advisor is the run's; with none, the build takes the one Settings has when it starts.
func TestARunsAdvisorIsItsStartsElseTheSettings(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name  string
		saved string
		sent  string
		want  string
	}{
		{
			name: "none chosen: the saved one", saved: "opus", sent: `{"model":"haiku"}`,
			want: "opus",
		},
		{
			name: "a choice over the saved one", saved: "opus",
			sent: `{"model":"haiku","advisor":"fable"}`, want: "fable",
		},
		{
			name: "Claude Code's own over the saved one", saved: "sonnet",
			sent: `{"model":"haiku","advisor":"claude-code"}`, want: "claude-code",
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			base := realTemp(t)
			saved := `,"sessions":{"where":"inside","resumeOnLaunch":true,"advisor":"` +
				row.saved + `"}`
			made := newWorld(t, worldOpts{
				base: base, dry: true, settings: workSettings(makeClone(t, base), saved),
			})

			made.work.Tickets()
			until(t, "the dry list", func() bool { return len(made.work.Tickets().Tickets) == 6 })
			same(t, "start", made.work.Start("PROJ-2", body(t, row.sent)).Code, 200)
			same(t, "advisor", openwork.AdvisorOf(made.work, "PROJ-2"), row.want)
		})
	}
}

// A START whose advisor is not a choice is refused, and no run starts.
func TestAStartWithAnAdvisorThatIsNoChoiceIsRefused(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	made := newWorld(t, worldOpts{
		base: base, settings: workSettings(makeClone(t, base), ""), dry: true,
	})

	made.work.Tickets()
	until(t, "the dry list", func() bool { return len(made.work.Tickets().Tickets) == 6 })

	for _, sent := range []string{
		`{"model":"haiku","advisor":"claude-opus-5-5"}`, `{"model":"haiku","advisor":""}`,
		`{"model":"haiku","advisor":7}`,
	} {
		reply := made.work.Start("PROJ-2", body(t, sent))
		same(t, sent+" code", reply.Code, 400)
		same(t, sent+" state", made.state("PROJ-2"), "")
	}
}

// A run file whose advisor is not a choice (edited by hand) is read with none, so its sessions take
// the one Settings has, and nothing from the file reaches a command line.
func TestARunFilesAdvisorThatIsNoChoiceIsNone(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name, kept, want string
	}{
		{name: "a choice is kept", kept: `"fable"`, want: "fable"},
		{name: "a model id is none", kept: `"claude-opus-5-5"`, want: "opus"},
		{name: "settings text is none", kept: `"opus\",\"permissions\":{}"`, want: "opus"},
		{name: "empty text is none", kept: `""`, want: "opus"},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			base := realTemp(t)
			made := newWorld(t, worldOpts{
				base: base,
				settings: workSettings(makeClone(t, base),
					`,"sessions":{"where":"inside","resumeOnLaunch":true,"advisor":"opus"}`),
				runs: []any{runRecord(t, "PROJ-7", `{"state":"failed","advisor":`+row.kept+`}`)},
			})

			same(t, "advisor", openwork.AdvisorOf(made.work, "PROJ-7"), row.want)
		})
	}
}
