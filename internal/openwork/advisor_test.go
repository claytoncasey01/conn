package openwork_test

import (
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
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

// A START with a bad layout and a bad advisor is told of both at once.
func TestAStartWithABadLayoutAndAdvisorIsToldOfBoth(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	made := newWorld(t, worldOpts{
		base: base, settings: workSettings(makeClone(t, base), ""), dry: true,
	})

	made.work.Tickets()
	until(t, "the dry list", func() bool { return len(made.work.Tickets().Tickets) == 6 })

	reply := made.work.Start("PROJ-2",
		body(t, `{"model":"haiku","repos":[3],"layout":"tiled","advisor":"gpt"}`))
	same(t, "code", reply.Code, 400)
	same(t, "error", js.Stringify(reply.Body), `{"error":"repos must be a list of repository `+
		`names; layout must be shared or split; advisor must be claude-code, fable, opus `+
		`or sonnet"}`)
	same(t, "state", made.state("PROJ-2"), "")
}

// A run file whose advisor is not a choice (edited by hand) is read with none, so its sessions take
// the one Settings has, and nothing from the file reaches a command line. The next save writes
// none, so the file heals.
func TestARunFilesAdvisorThatIsNoChoiceIsNone(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name, kept, want string
		wantSaved        any
	}{
		{name: "a choice is kept", kept: `"fable"`, want: "fable", wantSaved: "fable"},
		{name: "a model id is none", kept: `"claude-opus-5-5"`, want: "opus", wantSaved: nil},
		{
			name: "settings text is none", kept: `"opus\",\"permissions\":{}"`, want: "opus",
			wantSaved: nil,
		},
		{name: "empty text is none", kept: `""`, want: "opus", wantSaved: nil},
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

			// The file starts with the hand-edited advisor, so a nil here is the save's.
			openwork.TouchAll(made.work)
			until(t, "the saved advisor", func() bool {
				saved, isSaved := savedAdvisor(made, "PROJ-7")

				return isSaved && saved == row.wantSaved
			})
		})
	}
}

// savedAdvisor is the advisor key's value in key's run file, nil when it has none, and false when
// there is no such file to read.
func savedAdvisor(made *world, key string) (any, bool) {
	text, isText := made.files().Get(key + ".json")
	if !isText {
		return nil, false
	}

	parsed, err := js.Parse([]byte(js.String(text)))
	if err != nil {
		return nil, false
	}

	object, isObject := parsed.(*js.Object)
	if !isObject {
		return nil, false
	}

	value, _ := object.Get("advisor")

	return value, true
}

// A START's advisor is written to the run's file and read back from it, so a restart between
// START and the build keeps it.
func TestARunsAdvisorOutlastsARestart(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	made := newWorld(t, worldOpts{
		base: base, settings: workSettings(makeClone(t, base), ""), dry: true,
	})

	made.work.Tickets()
	until(t, "the dry list", func() bool { return len(made.work.Tickets().Tickets) == 6 })
	same(t, "start", made.work.Start("PROJ-2",
		body(t, `{"model":"haiku","advisor":"fable"}`)).Code, 200)

	var saved any

	until(t, "the run file", func() bool {
		text, isText := made.files().Get("PROJ-2.json")
		if !isText {
			return false
		}

		parsed, err := js.Parse([]byte(js.String(text)))
		if err != nil {
			return false
		}

		saved = parsed

		return get(parsed, "advisor") == "fable"
	})

	restarted := newWorld(t, worldOpts{
		base: realTemp(t), runs: []any{saved},
		settings: workSettings(t.TempDir(),
			`,"sessions":{"where":"inside","resumeOnLaunch":true,"advisor":"opus"}`),
	})
	same(t, "advisor after a restart", openwork.AdvisorOf(restarted.work, "PROJ-2"), "fable")
}
