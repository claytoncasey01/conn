package openwork_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/openwork"
)

// A run's file as the version before legs wrote it: every fact about the repository at the top,
// the session's counts inside the session, and a key a later version added (futureKey) in the
// middle.
const legacyRunText = `{"key":"PROJ-1","title":"Fix the export","base":"main","prBase":"main",` +
	`"model":"sonnet","branch":"proj-1-fix","worktree":"/x/widgets-proj-1","state":"failed",` +
	`"stage":"pr","startedAt":1,"updatedAt":2,"jiraStatus":"In Review","agents":[],` +
	`"answered":[],"questions":[],"log":[],"session":{"name":"PROJ-1","sessionId":"77",` +
	`"pid":77,"status":"idle","commitsAhead":3,"dirty":1},"pr":{"url":` +
	`"https://github.com/acme/widgets/pull/9","number":9},"error":null,"manualCommand":"cd x",` +
	`"brief":"b","desc":"d","worktreeReady":true,"ticketStatus":"In Review",` +
	`"futureKey":{"kept":true},"ticketCategory":"indeterminate","repo":"acme/widgets",` +
	`"clone":"/x/widgets","claudeSessionId":"` + sessionUUIDText + `","cleaned":false}`

// legacyWorld is a world whose runs folder holds the legacy run.
func legacyWorld(t *testing.T) *world {
	t.Helper()

	return newWorld(t, worldOpts{
		settings: workSettings(t.TempDir(), ""), runs: []any{legacyRunText},
	})
}

// jsonOf is JSON text, parsed.
func jsonOf(t *testing.T, text string) any {
	t.Helper()

	parsed, err := js.Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}

	return parsed
}

// keysOf are an object's keys in order, as one line.
func keysOf(value any) string { return strings.Join(objectOf(value).Keys(), ",") }

// fileOf is a run's file, parsed.
func (w *world) fileOf(key string) any {
	data, err := os.ReadFile(filepath.Join(w.dir, "runs", key+".json"))
	if err != nil {
		w.t.Fatal(err)
	}

	parsed, err := js.Parse(data)
	if err != nil {
		w.t.Fatal(err)
	}

	return parsed
}

func TestALegacyRunIsShownAsOneLeg(t *testing.T) {
	t.Parallel()

	run := listOf(runsOf(t, legacyWorld(t).work))[0]

	same(t, "keys", keysOf(run), "key,title,model,state,stage,startedAt,updatedAt,jiraStatus,"+
		"agents,answered,questions,log,error,futureKey,repos,layout")
	same(t, "repos", get(run, "repos"), jsonOf(t, `[{"repo":"acme/widgets","base":"main",`+
		`"prBase":"main","branch":"proj-1-fix","worktree":"/x/widgets-proj-1","session":`+
		`{"name":"PROJ-1","sessionId":"77","pid":77,"status":"idle"},"claudeSessionId":"`+
		sessionUUIDText+`","manualCommand":"cd x","ahead":3,"dirty":1,"pr":{"url":`+
		`"https://github.com/acme/widgets/pull/9","number":9},"cleaned":false}]`))
	same(t, "layout", get(run, "layout"), "shared")
}

func TestALegacyRunIsWrittenBackInTheNewShape(t *testing.T) {
	t.Parallel()

	made := legacyWorld(t)
	openwork.TouchAll(made.work)

	file := made.fileOf("PROJ-1")

	same(t, "keys", keysOf(file), "key,title,model,state,stage,startedAt,updatedAt,jiraStatus,"+
		"agents,answered,questions,log,error,brief,desc,ticketStatus,futureKey,ticketCategory,"+
		"repos,layout")
	same(t, "kept", get(file, "futureKey"), body(t, `{"kept":true}`))
	same(t, "repos", get(file, "repos"), jsonOf(t, `[{"repo":"acme/widgets","clone":`+
		`"/x/widgets","base":"main","prBase":"main","branch":"proj-1-fix","worktree":`+
		`"/x/widgets-proj-1","worktreeReady":true,"session":{"name":"PROJ-1","sessionId":"77",`+
		`"pid":77,"status":"idle"},"claudeSessionId":"`+sessionUUIDText+`","manualCommand":`+
		`"cd x","ahead":3,"dirty":1,"pr":{"url":"https://github.com/acme/widgets/pull/9",`+
		`"number":9},"cleaned":false}]`))
	same(t, "layout", get(file, "layout"), "shared")
}

func TestARunWrittenInTheNewShapeReadsBackTheSame(t *testing.T) {
	t.Parallel()

	first := legacyWorld(t)
	openwork.TouchAll(first.work)

	written := js.Stringify(first.fileOf("PROJ-1"))
	again := newWorld(t, worldOpts{settings: workSettings(t.TempDir(), ""), runs: []any{written}})
	openwork.TouchAll(again.work)

	same(t, "file", js.Stringify(again.fileOf("PROJ-1")), written)
	same(t, "snapshot", runsOf(t, again.work), runsOf(t, first.work))
}

func TestALegacyRunFromBeforeSettingsAsksItsWorktreesOrigin(t *testing.T) {
	t.Parallel()

	base := realTemp(t)
	worktree := gitClone(t, base, "main")
	made := newWorld(t, worldOpts{base: base, settings: workSettings(worktree, ""), runs: []any{
		withoutRepo(runRecord(t, "PROJ-1", `{"worktree":`+js.Stringify(worktree)+`}`)),
	}})

	run := listOf(runsOf(t, made.work))[0]
	leg := listOf(get(run, "repos"))[0]

	same(t, "recorded", get(leg, "repo"), "")
	same(t, "origin", openwork.RepoOf(made.work, "PROJ-1"), "acme/widgets")
}

func TestARunWithoutAUsableLegIsNotRead(t *testing.T) {
	t.Parallel()

	leg := `{"repo":"acme/widgets","clone":"/x/widgets","base":"main","prBase":"main",` +
		`"branch":"proj-1-fix","worktree":"/x/widgets-proj-1","worktreeReady":true,` +
		`"session":null,"claudeSessionId":null,"manualCommand":null,"ahead":0,"dirty":0,` +
		`"pr":null,"cleaned":null}`
	seven := strings.TrimSuffix(strings.Repeat(leg+",", 7), ",")
	six := strings.TrimSuffix(strings.Repeat(leg+",", 6), ",")

	rows := []struct {
		name, repos, layout string
		read                bool
	}{
		{"one leg", `[` + leg + `]`, `,"layout":"shared"`, true},
		{"six legs", `[` + six + `]`, `,"layout":"shared"`, true},
		{"no legs", `[]`, `,"layout":"shared"`, false},
		{"a null leg", `[` + leg + `,null]`, `,"layout":"shared"`, false},
		{"seven legs", `[` + seven + `]`, `,"layout":"shared"`, false},
		{"not a list", `{}`, `,"layout":"shared"`, false},
		{"split", `[` + leg + `]`, `,"layout":"split"`, true},
		{"no layout", `[` + leg + `]`, ``, true},
		{"an unknown layout", `[` + leg + `]`, `,"layout":"sideways"`, false},
		{"another case", `[` + leg + `]`, `,"layout":"Split"`, false},
		{"an empty layout", `[` + leg + `]`, `,"layout":""`, false},
		{"a layout that is not text", `[` + leg + `]`, `,"layout":1`, false},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			runsDir := t.TempDir()
			writeJSON(t, filepath.Join(runsDir, "PROJ-1.json"), `{"key":"PROJ-1","title":"t",`+
				`"model":"haiku","state":"failed","stage":"pr","startedAt":1,"updatedAt":1,`+
				`"jiraStatus":"Doing","agents":[],"answered":[],"questions":[],"log":[],`+
				`"error":null,"brief":"","desc":"","ticketStatus":"Doing","repos":`+row.repos+
				row.layout+`}`)

			read := len(openwork.LoadedKeys(runsDir)) == 1
			same(t, strconv.Quote(row.repos+row.layout), read, row.read)
		})
	}
}

func TestLegForFindsARunsRepositoryInAnyCase(t *testing.T) {
	t.Parallel()

	leg := func(repo string) string {
		return `{"repo":"` + repo + `","clone":"","base":"main","prBase":"main","branch":"b",` +
			`"worktree":"/w/` + repo + `","worktreeReady":false,"session":null,` +
			`"claudeSessionId":null,"manualCommand":null,"ahead":0,"dirty":0,"pr":null,` +
			`"cleaned":null}`
	}

	record, ok := openwork.DecodeRecord([]byte(`{"key":"PROJ-1","repos":[` + leg("acme/widgets") +
		`,` + leg("acme/gadgets") + `,` + leg("") + `],"layout":"split"}`))
	same(t, "read", ok, true)

	rows := []struct {
		name, repo, worktree string
		found                bool
	}{
		{"the first", "acme/widgets", "/w/acme/widgets", true},
		{"the second", "acme/gadgets", "/w/acme/gadgets", true},
		{"another case", "Acme/Gadgets", "/w/acme/gadgets", true},
		{"none", "acme/other", "", false},
		{"nothing, with a leg from before Settings", "", "", false},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			found, present := openwork.LegFor(record, row.repo)
			same(t, "found", present, row.found)

			worktree := ""
			if present {
				worktree = found.Worktree
			}

			same(t, "worktree", worktree, row.worktree)
		})
	}
}
