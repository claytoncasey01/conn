package settings_test

import (
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/settings"
)

func TestJiraDefaults(t *testing.T) {
	t.Parallel()

	jira := settings.Defaults().Atlassian.Jira

	if jira.ReviewMove != settings.ReviewAll {
		t.Errorf("reviewMove = %q", jira.ReviewMove)
	}

	if jira.SessionLayout != settings.LayoutShared {
		t.Errorf("sessionLayout = %q", jira.SessionLayout)
	}

	if jira.BaseBranches == nil {
		t.Fatal("baseBranches is nil: it must be an empty map")
	}

	if len(jira.BaseBranches) != 0 {
		t.Errorf("baseBranches = %v", jira.BaseBranches)
	}
}

func TestBaseForPrefersTheRepositoryThenTheGlobalBranchThenNothing(t *testing.T) {
	t.Parallel()

	develop, release := "develop", "release/1"
	own := map[string]string{"acme/api": release}
	both := settings.Jira{BaseBranch: &develop, BaseBranches: own}

	tests := []struct {
		name string
		jira settings.Jira
		repo string
		want *string
	}{
		{"repository entry wins", both, "acme/api", &release},
		{"repository entry wins in any case", both, "Acme/Api", &release},
		{"global when the repository has none", both, "acme/web", &develop},
		{"global with no map", settings.Jira{BaseBranch: &develop}, "acme/web", &develop},
		{
			"nothing",
			settings.Jira{BaseBranches: map[string]string{"acme/api": release}},
			"acme/web", nil,
		},
		{"nothing at all", settings.Jira{}, "acme/web", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.jira.BaseFor(tt.repo)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("got %q, want nil", *got)
				}

				return
			}

			if got == nil {
				t.Fatalf("got nil, want %q", *tt.want)
			}

			if *got != *tt.want {
				t.Fatalf("got %q, want %q", *got, *tt.want)
			}
		})
	}
}

func TestBaseForReturnsACopy(t *testing.T) {
	t.Parallel()

	jira := settings.Jira{BaseBranches: map[string]string{"acme/api": "develop"}}

	got := jira.BaseFor("acme/api")
	*got = "changed"

	if jira.BaseBranches["acme/api"] != "develop" {
		t.Fatal("a caller changed the setting through BaseFor")
	}
}

const (
	reviewMoveProblem = `atlassian.jira.reviewMove "sometimes" dropped: ` +
		`it must be "first", "all" or "never"`
	layoutProblem = `atlassian.jira.sessionLayout "tabs" dropped: ` +
		`it must be "shared" or "split"`
	branchProblem = `atlassian.jira.baseBranches["acme/api"] "-x" dropped: ` +
		`not a branch name (letters, digits and . _ - /, starting with a letter or digit, ` +
		`no .., at most 100 characters)`
)

func TestValidatingTheJiraKeysReportsEveryProblemAtOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			"a bad reviewMove",
			`{"enabled":true,"jira":{"reviewMove":"sometimes"}}`,
			reviewMoveProblem,
		},
		{
			"a bad sessionLayout",
			`{"enabled":true,"jira":{"sessionLayout":"tabs"}}`,
			layoutProblem,
		},
		{
			"a bad branch in baseBranches",
			`{"enabled":true,"jira":{"baseBranches":{"acme/api":"-x"}}}`,
			branchProblem,
		},
		{
			"all three at once",
			`{"enabled":true,"jira":{"reviewMove":"sometimes","sessionLayout":"tabs",` +
				`"baseBranches":{"acme/api":"-x"}}}`,
			branchProblem + "; " + reviewMoveProblem + "; " + layoutProblem,
		},
		{
			"a repository that is not owner/repo",
			`{"enabled":true,"jira":{"baseBranches":{"../x":"main"}}}`,
			`atlassian.jira.baseBranches "../x" dropped: not owner/repo`,
		},
		{
			"baseBranches that is not an object",
			`{"enabled":true,"jira":{"baseBranches":["main"]}}`,
			`atlassian.jira.baseBranches ["main"] dropped: ` +
				`not an object of repository to branch`,
		},
		{
			"a reviewMove that is not text",
			`{"enabled":true,"jira":{"reviewMove":3}}`,
			`atlassian.jira.reviewMove 3 dropped: it must be "first", "all" or "never"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checked := settings.ValidateSection("atlassian", parse(t, tt.body))
			if checked.OK {
				t.Fatalf("accepted: %s", js.Stringify(checked))
			}

			if checked.Error != tt.want {
				t.Fatalf("\n got  %s\n want %s", checked.Error, tt.want)
			}
		})
	}
}

func TestValidatingTheJiraKeysNormalisesThem(t *testing.T) {
	t.Parallel()

	body := `{"enabled":true,"jira":{"reviewMove":"first","sessionLayout":"split",` +
		`"baseBranches":{"Acme/API":"develop","acme/web":null}}}`
	want := `{"ok":true,"value":{"enabled":true,"jira":{"projects":[],"buildRepo":null,` +
		`"baseBranch":null,"baseBranches":{"acme/api":"develop"},"reviewMove":"first",` +
		`"sessionLayout":"split"}}}`

	got := js.Stringify(settings.ValidateSection("atlassian", parse(t, body)))
	if got != want {
		t.Fatalf("\n got  %s\n want %s", got, want)
	}
}

func TestTooManyBaseBranchesKeepsTheFirstOnes(t *testing.T) {
	t.Parallel()

	object := js.NewObject()
	for index := range settings.BaseBranchesMax + 3 {
		object.Set("acme/r"+js.Stringify(float64(index)), "main")
	}

	jira := js.NewObject()
	jira.Set("baseBranches", object)

	section := js.NewObject()
	section.Set("enabled", true)
	section.Set("jira", jira)

	loaded := settings.ParseDetailed(wrap("atlassian", section), true)

	if got := len(loaded.Settings.Atlassian.Jira.BaseBranches); got != settings.BaseBranchesMax {
		t.Errorf("kept %d", got)
	}

	if len(loaded.Dropped) != 1 {
		t.Errorf("dropped = %v", loaded.Dropped)
	}
}

func TestAPutOverTheBaseBranchesCapIsRefusedNotTruncated(t *testing.T) {
	t.Parallel()

	object := js.NewObject()
	for index := range settings.BaseBranchesMax + 1 {
		object.Set("acme/r"+js.Stringify(float64(index)), "main")
	}

	jira := js.NewObject()
	jira.Set("baseBranches", object)

	section := js.NewObject()
	section.Set("enabled", true)
	section.Set("jira", jira)

	checked := settings.ValidateSection("atlassian", section)
	if checked.OK {
		t.Fatalf("accepted: %s", js.Stringify(checked))
	}

	want := "atlassian.jira.baseBranches holds 101 entries: only the first 100 are read"
	if checked.Error != want {
		t.Fatalf("\n got  %s\n want %s", checked.Error, want)
	}
}

func TestAnExplicitNullChoiceResetsToTheDefaultAndARepeatedSpellingKeepsTheFirst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			"null reviewMove resets, not keeps",
			`{"enabled":true,"jira":{"reviewMove":null}}`, `"reviewMove":"all"`,
		},
		{
			"null sessionLayout resets, not keeps",
			`{"enabled":true,"jira":{"sessionLayout":null}}`, `"sessionLayout":"shared"`,
		},
		{
			"the first spelling of a repository wins",
			`{"enabled":true,"jira":{"baseBranches":{"Acme/API":"develop","acme/api":"main"}}}`,
			`"baseBranches":{"acme/api":"develop"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, _ := newStore(t)
			mustUpdate(t, store, "repos", `{"allow":["acme/*"]}`)
			mustUpdate(t, store, "atlassian", `{"enabled":true,"jira":{"reviewMove":"never",`+
				`"sessionLayout":"split","baseBranches":{"acme/db":"trunk"}}}`)
			mustUpdate(t, store, "atlassian", tt.body)

			got := js.Stringify(store.Get().Atlassian.Jira)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("got %s, want it to hold %s", got, tt.want)
			}
		})
	}
}

func wrap(key string, value any) *js.Object {
	object := js.NewObject()
	object.Set(key, value)

	return object
}

func TestReadingAFileDropsWhatItCannotUseAndNamesIt(t *testing.T) {
	t.Parallel()

	file := `{"repos":{"allow":["acme/*"]},"atlassian":{"enabled":true,"jira":{` +
		`"reviewMove":"sometimes","sessionLayout":"split",` +
		`"baseBranches":{"acme/api":"develop","other/web":"main","acme/db":"a..b"}}}}`

	loaded := settings.ParseDetailed(parse(t, file), true)
	jira := loaded.Settings.Atlassian.Jira

	if jira.ReviewMove != settings.ReviewAll {
		t.Errorf("reviewMove = %q", jira.ReviewMove)
	}

	if jira.SessionLayout != settings.LayoutSplit {
		t.Errorf("sessionLayout = %q", jira.SessionLayout)
	}

	if got := js.Stringify(jira.BaseBranches); got != `{"acme/api":"develop"}` {
		t.Errorf("baseBranches = %s", got)
	}

	if len(loaded.Dropped) != 3 {
		t.Errorf("dropped = %v", loaded.Dropped)
	}
}

func TestAnAtlassianSaveKeepsWhatItLeavesOut(t *testing.T) {
	t.Parallel()

	store, _ := newStore(t)
	mustUpdate(t, store, "repos", `{"allow":["acme/*"]}`)
	mustUpdate(t, store, "atlassian", `{"enabled":true,"jira":{"projects":["PROJ"],`+
		`"reviewMove":"never","sessionLayout":"split","baseBranches":{"acme/api":"develop"}}}`)
	mustUpdate(t, store, "atlassian", `{"enabled":true,"jira":{"projects":["PROJ"]}}`)

	jira := store.Get().Atlassian.Jira

	if jira.ReviewMove != settings.ReviewNever {
		t.Errorf("reviewMove = %q", jira.ReviewMove)
	}

	if jira.SessionLayout != settings.LayoutSplit {
		t.Errorf("sessionLayout = %q", jira.SessionLayout)
	}

	if got := js.Stringify(jira.BaseBranches); got != `{"acme/api":"develop"}` {
		t.Errorf("baseBranches = %s", got)
	}
}

func TestAnAtlassianSaveReplacesTheWholeBaseBranchesMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{"another map", `{"acme/web":"main"}`, `{"acme/web":"main"}`},
		{"an empty map clears them", `{}`, `{}`},
		{"null clears them", `null`, `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, _ := newStore(t)
			mustUpdate(t, store, "repos", `{"allow":["acme/*"]}`)
			mustUpdate(t, store, "atlassian", `{"enabled":true,"jira":{"baseBranches":`+
				`{"acme/api":"develop","acme/db":"trunk"}}}`)
			mustUpdate(t, store, "atlassian",
				`{"enabled":true,"jira":{"baseBranches":`+tt.body+`}}`)

			got := js.Stringify(store.Get().Atlassian.Jira.BaseBranches)
			if got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestASaveDropsBaseBranchesOfRepositoriesOffTheAllowlist(t *testing.T) {
	t.Parallel()

	store, dir := newStore(t)
	mustUpdate(t, store, "repos", `{"allow":["acme/*","jane/widgets"]}`)
	mustUpdate(t, store, "atlassian", `{"enabled":true,"jira":{"baseBranches":`+
		`{"acme/api":"develop","jane/widgets":"trunk","other/web":"main"}}}`)

	want := `{"acme/api":"develop","jane/widgets":"trunk"}`
	if got := js.Stringify(store.Get().Atlassian.Jira.BaseBranches); got != want {
		t.Fatalf("on save: %s", got)
	}

	mustUpdate(t, store, "repos", `{"allow":["jane/widgets"]}`)

	want = `{"jane/widgets":"trunk"}`
	if got := js.Stringify(store.Get().Atlassian.Jira.BaseBranches); got != want {
		t.Fatalf("after the list shrank: %s", got)
	}

	if got := js.Stringify(settings.Load(dir).Atlassian.Jira.BaseBranches); got != want {
		t.Fatalf("in the file: %s", got)
	}
}

func TestACloneOfTheJiraKeysIsIndependent(t *testing.T) {
	t.Parallel()

	original := settings.Defaults()
	original.Atlassian.Jira.BaseBranches["acme/api"] = "develop"

	copied := original.Clone()
	copied.Atlassian.Jira.BaseBranches["acme/api"] = "changed"

	if original.Atlassian.Jira.BaseBranches["acme/api"] != "develop" {
		t.Fatal("the copy shares the map")
	}
}
