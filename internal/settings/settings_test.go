package settings_test

import (
	"bytes"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/answers"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/settings"
)

const defaultsJSON = `{"version":1,"repos":{"allow":null,"clones":{},"cloneRoot":null},` +
	`"github":{"enabled":true},` +
	`"slack":{"enabled":false,"channels":[],"dms":false,"draftChannel":null},` +
	`"atlassian":{"enabled":false,"jira":{"projects":[],"buildRepo":null,"baseBranch":null,` +
	`"baseBranches":{},"reviewMove":"all","sessionLayout":"shared"}},` +
	`"reviews":{"requested":true,"allOpen":false},` +
	`"sessions":{"where":"inside","terminalCommand":[],"resumeOnLaunch":true}}`

var errDiskFull = errors.New("disk full")

func parse(t *testing.T, text string) any {
	t.Helper()

	parsed, err := js.Parse([]byte(text))
	if err != nil {
		t.Fatalf("%s: %v", text, err)
	}

	return parsed
}

func quiet() *log.Logger { return log.New(io.Discard, "", 0) }

// folder is a data folder that does not exist yet.
func folder(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "d")
}

// withFile is a data folder holding a settings.json of text.
func withFile(t *testing.T, text string) string {
	t.Helper()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(text), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return dir
}

func readText(t *testing.T, path string) string {
	t.Helper()

	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(text)
}

func withAllow() settings.Settings {
	value := settings.Defaults()
	value.Repos.Allow = []string{"acme/*", "jane/widgets"}

	return value
}

func newStore(t *testing.T) (*settings.Store, string) {
	t.Helper()

	store := settings.NewStore(quiet())
	dir := folder(t)
	store.Reload(dir)

	return store, dir
}

func mustUpdate(t *testing.T, store *settings.Store, name, body string) {
	t.Helper()

	result := store.Update(name, parse(t, body))
	if !result.OK {
		t.Fatalf("%s %s: %s", name, body, result.Error)
	}
}

// The recorded answers: the page's own checks and the settings reader's cases
// (testdata/answers/settings.json).
func TestRecordedParseBranchAnswers(t *testing.T) {
	t.Parallel()

	for _, recorded := range answers.Load(t, "settings.json", "parseBranch") {
		branch, ok := settings.ParseBranch(recorded.Args[0])

		var got any
		if ok {
			got = branch
		}

		answers.Check(t, recorded, got)
	}
}

func TestRecordedValidateSectionAnswers(t *testing.T) {
	t.Parallel()

	for _, recorded := range answers.Load(t, "settings.json", "validateSection") {
		answers.Check(t, recorded, settings.ValidateSection(recorded.Args[0], recorded.Args[1]))
	}
}

func TestRecordedParseAnswers(t *testing.T) {
	t.Parallel()

	for _, recorded := range answers.Load(t, "settings.json", "parseSettings") {
		answers.Check(t, recorded, settings.Parse(recorded.Args[0], true))
	}

	for _, recorded := range answers.Load(t, "settings.json", "parseSettingsDetailed") {
		answers.Check(t, recorded, settings.ParseDetailed(recorded.Args[0], true))
	}
}

func TestDefaults(t *testing.T) {
	t.Parallel()

	if got := js.Stringify(settings.Defaults()); got != defaultsJSON {
		t.Fatalf("got %s", got)
	}

	if got := js.Stringify(settings.Parse(nil, false)); got != defaultsJSON {
		t.Fatalf("undefined: %s", got)
	}
}

func TestAFileThatIsNotAnObjectIsTheDefaults(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{`null`, `5`, `"x"`, `[]`, `true`, `{}`} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			if got := js.Stringify(settings.Parse(parse(t, raw), true)); got != defaultsJSON {
				t.Fatalf("got %s", got)
			}
		})
	}
}

func TestAFullValidFileReadsAsItIs(t *testing.T) {
	t.Parallel()

	full := `{"version":1,"repos":{"allow":["acme/*","jane/widgets"],` +
		`"clones":{"acme/infra":"/Users/x/work/infra"},"cloneRoot":"/Users/x/work"},` +
		`"github":{"enabled":false},"slack":{"enabled":true,` +
		`"channels":["#code-review","#dev.team"],"dms":true,"draftChannel":"#code-review"},` +
		`"atlassian":{"enabled":true,"jira":{"projects":["PROJ","AB2"],"buildRepo":"acme/app",` +
		`"baseBranch":null,"baseBranches":{},"reviewMove":"all","sessionLayout":"shared"}},` +
		`"reviews":{"requested":false,"allOpen":true},` +
		`"sessions":{"where":"terminal","terminalCommand":["/bin/t","{command}"],` +
		`"resumeOnLaunch":false}}`

	if got := js.Stringify(settings.Parse(parse(t, full), true)); got != full {
		t.Fatalf("got %s", got)
	}
}

func TestClonesKeepFirstEntryPerRepoInOrder(t *testing.T) {
	t.Parallel()

	loaded := settings.ParseDetailed(parse(t, `{"repos":{"allow":["acme/*"],"clones":{`+
		`"Acme/Infra":"/work/infra","acme/rel":"work/rel","acme/*":"/work/x","../x":"/y",`+
		`"acme/num":5,"acme/nul":"/a\u0000b","acme/up":"/a/../b","ACME/INFRA":"/later"}}}`), true)

	got := js.Stringify(loaded.Settings.Repos.Clones)
	if got != `{"acme/infra":"/work/infra","acme/up":"/b"}` {
		t.Fatalf("got %s", got)
	}

	if len(loaded.Dropped) != 5 {
		t.Fatalf("dropped %q", loaded.Dropped)
	}
}

func TestLoadMissingIsDefaultsAndCreatesNothing(t *testing.T) {
	t.Parallel()

	dir := folder(t)

	want := `{"settings":` + defaultsJSON + `,"dropped":[],"unreadable":false}`
	if got := js.Stringify(settings.LoadDetailed(dir)); got != want {
		t.Fatalf("got %s", got)
	}

	_, err := os.Stat(dir)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatal("nothing is created")
	}
}

func TestLoadBadFileIsDefaultsLeftAloneAndSaysWhy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, text, want string
	}{
		{"empty", "", "settings.json is not valid JSON: using the defaults, and the file is " +
			"left as it is until you save"},
		{"cut short", `{"version":1,"repos":`, "settings.json is not valid JSON: using the " +
			"defaults, and the file is left as it is until you save"},
		{"a list", "[]", "settings.json is not a JSON object ([]): using the defaults"},
		{"null", "null", "settings.json is not a JSON object (null): using the defaults"},
		{"a number", "42", "settings.json is not a JSON object (42): using the defaults"},
		{"a string", `"x"`, `settings.json is not a JSON object ("x"): using the defaults`},
		{"version 2", `{"version":2,"repos":{"allow":["acme/*"]}}`, "settings.json is version " +
			"2: this conn reads version 1, so the defaults are used and the file is left as it is"},
		{"version as text", `{"version":"1"}`, `settings.json is version "1": this conn reads ` +
			"version 1, so the defaults are used and the file is left as it is"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := withFile(t, tt.text)

			want := `{"settings":` + defaultsJSON + `,"dropped":[` + js.Stringify(tt.want) +
				`],"unreadable":true}`
			if got := js.Stringify(settings.LoadDetailed(dir)); got != want {
				t.Fatalf("got %s", got)
			}

			if got := readText(t, filepath.Join(dir, "settings.json")); got != tt.text {
				t.Fatalf("the file was changed to %q", got)
			}
		})
	}
}

func TestLoadNoVersionIsVersionOne(t *testing.T) {
	t.Parallel()

	allow := settings.Load(withFile(t, `{"repos":{"allow":["acme/*"]}}`)).Repos.Allow
	if !slices.Equal(allow, []string{"acme/*"}) {
		t.Fatal(allow)
	}
}

func TestLoadIgnoresAHugeFile(t *testing.T) {
	t.Parallel()

	huge := withFile(t, `{"repos":{"allow":["acme/*"]},"pad":"`+
		strings.Repeat("x", 2_000_000)+`"}`)

	loaded := settings.LoadDetailed(huge)
	if loaded.Settings.Repos.Allow != nil {
		t.Fatal("read")
	}

	if loaded.Dropped[0] != "settings.json is larger than 1000000 bytes: using the defaults" {
		t.Fatalf("got %q", loaded.Dropped)
	}
}

func TestLoadAFolderIsDefaultsAndSaysWhy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.Mkdir(filepath.Join(dir, "settings.json"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	loaded := settings.LoadDetailed(dir)
	want := "settings.json could not be read (EISDIR: illegal operation on a directory, read): " +
		"using the defaults"

	if loaded.Dropped[0] != want {
		t.Fatalf("got %q", loaded.Dropped)
	}

	if !loaded.Unreadable {
		t.Fatal("not set aside")
	}
}

func TestAnUnreadableFileSaysWhyInTheUsualWords(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}

	dir := withFile(t, "{}")
	path := filepath.Join(dir, "settings.json")

	err := os.Chmod(path, 0)
	if err != nil {
		t.Fatal(err)
	}

	want := "settings.json could not be read (EACCES: permission denied, open '" + path +
		"'): using the defaults"
	if got := settings.LoadDetailed(dir).Dropped[0]; got != want {
		t.Fatal(got)
	}
}

func TestSaveWritesWhatLoadReadsPrivately(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(folder(t), "nested")
	value := withAllow()
	value.GitHub.Enabled = false

	err := settings.Save(dir, value, nil)
	if err != nil {
		t.Fatal(err)
	}

	if js.Stringify(settings.Load(dir)) != js.Stringify(value) {
		t.Fatal("round trip")
	}

	checkMode(t, filepath.Join(dir, "settings.json"), 0o600)
	checkMode(t, dir, 0o700)
	checkOnlyTheFile(t, dir)

	text := readText(t, filepath.Join(dir, "settings.json"))
	if !strings.HasPrefix(text, "{\n  \"version\": 1,\n  \"repos\": {\n    \"allow\": [\n") {
		t.Fatalf("not written as JSON.stringify(s, null, 2):\n%s", text)
	}

	if !strings.HasSuffix(text, "}\n") {
		t.Fatalf("no final newline:\n%s", text)
	}
}

func checkMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s is %v, want %v", path, got, want)
	}
}

// checkOnlyTheFile fails when a temporary file is left beside settings.json.
func checkOnlyTheFile(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Fatalf("%d files", len(entries))
	}
}

func TestSaveIsAtomic(t *testing.T) {
	t.Parallel()

	dir := folder(t)

	err := settings.Save(dir, withAllow(), nil)
	if err != nil {
		t.Fatal(err)
	}

	before := readText(t, filepath.Join(dir, "settings.json"))
	value := withAllow()
	value.GitHub.Enabled = false

	err = settings.Save(dir, value, func(string, string) error { return errDiskFull })
	if !errors.Is(err, errDiskFull) {
		t.Fatal(err)
	}

	if readText(t, filepath.Join(dir, "settings.json")) != before {
		t.Fatal("the old file is not whole")
	}

	checkOnlyTheFile(t, dir)
}

func TestSaveTightensALooseDataFolder(t *testing.T) {
	t.Parallel()

	// A folder others may read: Save makes it the owner's alone.
	dir := filepath.Join(t.TempDir(), "d")

	err := os.Mkdir(dir, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = settings.Save(dir, withAllow(), nil)
	if err != nil {
		t.Fatal(err)
	}

	checkMode(t, dir, 0o700)
}

func TestValidateSectionMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, body, want string
	}{
		{"nope", `{}`, `{"ok":false,"error":"there is no settings section \"nope\""}`},
		{"__proto__", `{}`, `{"ok":false,"error":"there is no settings section \"__proto__\""}`},
		{"github", `[]`, `{"ok":false,"error":"github must be a JSON object"}`},
		{"repos", `{"allow":[]}`, `{"ok":false,"error":"choose at least one repository"}`},
		{"repos", `{}`, `{"ok":false,"error":"repos.allow must be a list of patterns: ` +
			`choose at least one repository"}`},
		{"repos", `{"allow":["acme/*"],"cloneRoot":"relative"}`, `{"ok":false,"error":` +
			`"repos.cloneRoot \"relative\" dropped: not an absolute folder path"}`},
		{"repos", `{"allow":[" ACME/* ","acme/*","Jane/Widgets"]}`, `{"ok":true,"value":` +
			`{"allow":["acme/*","jane/widgets"],"clones":{},"cloneRoot":null}}`},
		{
			"atlassian", `{"enabled":true,"jira":{"projects":["PROJ"],"buildRepo":"Acme/App"}}`,
			`{"ok":true,"value":{"enabled":true,"jira":{"projects":["PROJ"],` +
				`"buildRepo":"acme/app","baseBranch":null,"baseBranches":{},` +
				`"reviewMove":"all","sessionLayout":"shared"}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name+tt.body, func(t *testing.T) {
			t.Parallel()

			got := js.Stringify(settings.ValidateSection(tt.name, parse(t, tt.body)))
			if got != tt.want {
				t.Fatalf("\n got  %s\n want %s", got, tt.want)
			}
		})
	}
}

func TestARefusalNamesFiveProblemsAndCountsTheRest(t *testing.T) {
	t.Parallel()

	many := `{"allow":["acme/*"],"clones":{"a":"/1","b":"/2","c":"/3","d":"/4","e":"/5",` +
		`"f":"/6","g":"/7"}}`

	got := settings.ValidateSection("repos", parse(t, many)).Error
	if !strings.HasSuffix(got, "; and 2 more") {
		t.Fatal(got)
	}

	if strings.Count(got, "; ") != 5 {
		t.Fatal(got)
	}
}

func TestAStoreWithNoFileIsInSetup(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t)
	if !store.Setup() {
		t.Fatal("no file is setup")
	}

	if store.Allow() != nil {
		t.Fatal("setup allows nothing: the list is null")
	}

	if js.Stringify(store.Get()) != defaultsJSON {
		t.Fatal("not the defaults")
	}

	mustUpdate(t, store, "repos", `{"allow":["acme/*"]}`)

	if store.Setup() {
		t.Fatal("a pattern ends setup")
	}

	if !slices.Equal(store.Allow(), []string{"acme/*"}) {
		t.Fatal(store.Allow())
	}
}

func TestAnEmptyListIsSetup(t *testing.T) {
	t.Parallel()

	store := settings.NewStore(quiet())
	store.Reload(withFile(t, `{"repos":{"allow":[]}}`))

	allow := store.Get().Repos.Allow
	if allow == nil {
		t.Fatal("an empty list is not null")
	}

	if !store.Setup() {
		t.Fatal("an empty list is setup")
	}
}

func TestUpdateBeforeReloadRefuses(t *testing.T) {
	t.Parallel()

	result := settings.NewStore(quiet()).Update("github", parse(t, `{"enabled":false}`))
	if result.Error != "settings are not loaded" {
		t.Fatal(result)
	}
}

func TestModuleEnabled(t *testing.T) {
	t.Parallel()

	dir := folder(t)
	value := withAllow()
	value.Slack.Enabled = true
	value.Atlassian.Enabled = true

	err := settings.Save(dir, value, nil)
	if err != nil {
		t.Fatal(err)
	}

	store := settings.NewStore(quiet())
	store.Reload(dir)
	mustUpdate(t, store, "github", `{"enabled":false}`)

	tests := []struct {
		module string
		want   bool
	}{
		{settings.ModuleGitHub, false},
		{settings.ModuleSlack, true},
		{settings.ModuleAtlassian, true},
		{"other", false},
	}
	for _, tt := range tests {
		if got := store.ModuleEnabled(tt.module); got != tt.want {
			t.Fatalf("%s: got %v", tt.module, got)
		}
	}
}

func TestEveryModuleIsOffInSetup(t *testing.T) {
	t.Parallel()

	store := settings.NewStore(quiet())
	store.Reload(withFile(t, `{"github":{"enabled":true},"slack":{"enabled":true},`+
		`"atlassian":{"enabled":true}}`))

	modules := []string{settings.ModuleGitHub, settings.ModuleSlack, settings.ModuleAtlassian}
	for _, module := range modules {
		if store.ModuleEnabled(module) {
			t.Fatalf("%s is on", module)
		}
	}
}

func TestUpdateSavesAndNotifiesOnce(t *testing.T) {
	t.Parallel()

	store, dir := newStore(t)

	var seen []settings.Settings

	off := store.OnChange(func(value settings.Settings) { seen = append(seen, value) })
	mustUpdate(t, store, "repos", `{"allow":["ACME/*"],"clones":{"acme/infra":"/w/infra"}}`)

	if len(seen) != 1 {
		t.Fatalf("heard %d", len(seen))
	}

	if js.Stringify(store.Get()) != js.Stringify(seen[0]) {
		t.Fatal("a listener heard other settings than the store holds")
	}

	got := js.Stringify(settings.Load(dir).Repos)
	if got != `{"allow":["acme/*"],"clones":{"acme/infra":"/w/infra"},"cloneRoot":null}` {
		t.Fatal(got)
	}

	off()
	mustUpdate(t, store, "github", `{"enabled":false}`)

	if len(seen) != 1 {
		t.Fatal("an unsubscribed listener hears nothing more")
	}
}

func TestARefusedUpdateChangesNothing(t *testing.T) {
	t.Parallel()

	store, dir := newStore(t)
	mustUpdate(t, store, "repos", `{"allow":["acme/*"]}`)

	before := readText(t, filepath.Join(dir, "settings.json"))
	heard := 0

	store.OnChange(func(settings.Settings) { heard++ })

	result := store.Update("repos", parse(t, `{"allow":["acme/*","*/*"]}`))
	if !strings.Contains(result.Error, "*/*") {
		t.Fatal(result)
	}

	if store.Update("nope", parse(t, `{}`)).OK {
		t.Fatal("unknown section")
	}

	if readText(t, filepath.Join(dir, "settings.json")) != before {
		t.Fatal("the file changed")
	}

	if !slices.Equal(store.Get().Repos.Allow, []string{"acme/*"}) {
		t.Fatal("the settings changed")
	}

	if heard != 0 {
		t.Fatal("a listener was told")
	}
}

func TestAFailedSaveIsToldApartAndChangesNothing(t *testing.T) {
	t.Parallel()

	store, dir := newStore(t)
	mustUpdate(t, store, "repos", `{"allow":["acme/*"]}`)

	err := os.RemoveAll(dir)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(dir, []byte("in the way"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	heard := 0

	store.OnChange(func(settings.Settings) { heard++ })

	result := store.Update("github", parse(t, `{"enabled":false}`))
	if !result.SaveFailed {
		t.Fatal(result)
	}

	if !strings.HasPrefix(result.Error, "settings could not be saved: ENOTDIR: not a directory") {
		t.Fatal(result.Error)
	}

	if !store.Get().GitHub.Enabled {
		t.Fatal("the settings changed")
	}

	if heard != 0 {
		t.Fatal("a listener was told")
	}
}

func TestUpdateRefusesABuildRepoTheListDoesNotCover(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t)
	mustUpdate(t, store, "repos", `{"allow":["acme/*"]}`)
	mustUpdate(t, store, "atlassian",
		`{"enabled":true,"jira":{"projects":["PROJ"],"buildRepo":"acme/app"}}`)

	buildRepo := store.Get().Atlassian.Jira.BuildRepo
	if buildRepo == nil {
		t.Fatal("no build repo")
	}

	if *buildRepo != "acme/app" {
		t.Fatal(*buildRepo)
	}

	result := store.Update("atlassian", parse(t,
		`{"enabled":true,"jira":{"projects":["PROJ"],"buildRepo":"other/app"}}`))
	if result.Error != `atlassian.jira.buildRepo "other/app": it is not an allowed repository` {
		t.Fatal(result)
	}
}

func TestUpdateClearsWhatTheOtherSectionsNoLongerHold(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t)
	mustUpdate(t, store, "repos", `{"allow":["acme/*"]}`)
	mustUpdate(t, store, "atlassian",
		`{"enabled":true,"jira":{"projects":["PROJ"],"buildRepo":"acme/app"}}`)
	mustUpdate(t, store, "repos", `{"allow":["jane/widgets"]}`)

	if store.Get().Atlassian.Jira.BuildRepo != nil {
		t.Fatal("a build repo the list no longer covers is cleared")
	}

	mustUpdate(t, store, "slack", `{"enabled":true,"channels":["#a","#b"],"draftChannel":"#b"}`)
	mustUpdate(t, store, "slack", `{"enabled":true,"channels":["#a"],"dms":false}`)

	if store.Get().Slack.DraftChannel != nil {
		t.Fatal("a draft channel the channels no longer hold is cleared")
	}
}

func TestAnAtlassianSaveWithoutBaseBranchKeepsIt(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t)
	mustUpdate(t, store, "repos", `{"allow":["acme/*"]}`)
	mustUpdate(t, store, "atlassian",
		`{"enabled":true,"jira":{"projects":["PROJ"],"baseBranch":"develop"}}`)
	mustUpdate(t, store, "atlassian", `{"enabled":true,"jira":{"projects":["PROJ"]}}`)

	branch := store.Get().Atlassian.Jira.BaseBranch
	if branch == nil {
		t.Fatal("dropped")
	}

	if *branch != "develop" {
		t.Fatal(*branch)
	}

	mustUpdate(t, store, "atlassian",
		`{"enabled":true,"jira":{"projects":["PROJ"],"baseBranch":null}}`)

	if store.Get().Atlassian.Jira.BaseBranch != nil {
		t.Fatal("null clears it")
	}
}

func TestAPanickingListenerDoesNotStopTheOthers(t *testing.T) {
	t.Parallel()

	var logged bytes.Buffer

	store := settings.NewStore(log.New(&logged, "", 0))
	store.Reload(folder(t))

	heard := 0

	store.OnChange(func(settings.Settings) { panic("boom") })
	store.OnChange(func(settings.Settings) { heard++ })
	mustUpdate(t, store, "github", `{"enabled":false}`)

	if heard != 1 {
		t.Fatalf("heard %d", heard)
	}

	if logged.String() != "settings listener failed: boom\n" {
		t.Fatalf("logged %q", logged.String())
	}
}

func TestGetIsACopy(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t)
	mustUpdate(t, store, "repos", `{"allow":["acme/*"]}`)

	got := store.Get()
	got.Repos.Allow[0] = "*/*"
	got.Repos.Allow = append(got.Repos.Allow, "x/*")

	if !slices.Equal(store.Get().Repos.Allow, []string{"acme/*"}) {
		t.Fatal("a reader widened the list")
	}
}

func TestTwoUpdatesInARowAgreeWithTheFile(t *testing.T) {
	t.Parallel()

	store, dir := newStore(t)
	mustUpdate(t, store, "repos", `{"allow":["acme/*"]}`)
	mustUpdate(t, store, "slack", `{"enabled":true,"channels":["#a"]}`)
	mustUpdate(t, store, "repos", `{"allow":["jane/*"]}`)

	if js.Stringify(settings.Load(dir)) != js.Stringify(store.Get()) {
		t.Fatal("the file and the store disagree")
	}

	if !slices.Equal(store.Get().Slack.Channels, []string{"#a"}) {
		t.Fatal("channels")
	}

	if !slices.Equal(store.Get().Repos.Allow, []string{"jane/*"}) {
		t.Fatal("allow")
	}
}

func TestAFirstSaveOverAnUnreadableFileBacksItUpOnce(t *testing.T) {
	t.Parallel()

	tests := []string{
		`{"repos":{"allow":["acme/*"],},"slack":{"enabled":true}}`,
		`{"version":2,"slack":{"enabled":true,"channels":["#a"]}}`,
		"[1,2]",
		"",
	}
	for _, original := range tests {
		t.Run(original, func(t *testing.T) {
			t.Parallel()

			dir := withFile(t, original)
			backup := filepath.Join(dir, "settings.json.bak")
			store := settings.NewStore(quiet())
			store.Reload(dir)

			if store.BackedUp() != "" {
				t.Fatal("no backup yet")
			}

			mustUpdate(t, store, "repos", `{"allow":["acme/*"]}`)

			if readText(t, backup) != original {
				t.Fatal("the backup is not the original")
			}

			checkMode(t, backup, 0o600)

			if store.BackedUp() != backup {
				t.Fatal(store.BackedUp())
			}

			if !slices.Equal(settings.Load(dir).Repos.Allow, []string{"acme/*"}) {
				t.Fatal("not saved")
			}

			err := os.WriteFile(backup, []byte("kept as it is"), 0o600)
			if err != nil {
				t.Fatal(err)
			}

			mustUpdate(t, store, "github", `{"enabled":false}`)

			if readText(t, backup) != "kept as it is" {
				t.Fatal("a later save touched the backup")
			}
		})
	}
}

func TestAnExistingBackupIsNeverOverwritten(t *testing.T) {
	t.Parallel()

	dir := withFile(t, `{"repos":`)
	backup := filepath.Join(dir, "settings.json.bak")

	err := os.WriteFile(backup, []byte("an earlier rescue"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	store := settings.NewStore(quiet())
	store.Reload(dir)
	mustUpdate(t, store, "repos", `{"allow":["acme/*"]}`)

	if readText(t, backup) != "an earlier rescue" {
		t.Fatal("overwritten")
	}

	if store.BackedUp() != "" {
		t.Fatal(store.BackedUp())
	}
}

func TestAReadableFileMakesNoBackup(t *testing.T) {
	t.Parallel()

	dir := withFile(t, `{"repos":{"allow":["acme/*"]}}`)
	store := settings.NewStore(quiet())
	store.Reload(dir)
	mustUpdate(t, store, "github", `{"enabled":false}`)

	_, err := os.Stat(filepath.Join(dir, "settings.json.bak"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a backup of a readable file")
	}

	if store.BackedUp() != "" {
		t.Fatal(store.BackedUp())
	}
}

func TestAReloadForgetsABackup(t *testing.T) {
	t.Parallel()

	bad := withFile(t, `{"repos":`)
	store := settings.NewStore(quiet())
	store.Reload(bad)
	mustUpdate(t, store, "repos", `{"allow":["acme/*"]}`)

	if store.BackedUp() == "" {
		t.Fatal("not backed up")
	}

	store.Reload(bad)

	if store.BackedUp() != "" {
		t.Fatal("a reload forgets it")
	}
}

func TestASaveThatCannotBackUpIsRefused(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	err := os.Mkdir(path, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	store := settings.NewStore(quiet())
	store.Reload(dir)

	result := store.Update("repos", parse(t, `{"allow":["acme/*"]}`))
	want := "settings could not be saved: EISDIR: illegal operation on a directory, copyfile '" +
		path + "' -> '" + path + ".bak'"

	if result.Error != want {
		t.Fatal(result.Error)
	}

	if !result.SaveFailed {
		t.Fatal(result)
	}

	if !store.Setup() {
		t.Fatal("the settings changed")
	}

	checkMode(t, path, 0o700)
}
