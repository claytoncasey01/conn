package openwork_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/jira"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/openwork"
	"github.com/claytoncasey01/conn/internal/settings"
)

// The recorded answers in testdata/answers/openwork/pure.json.gz: slugs, the build target, the
// default branch, PR URLs, linked worktrees, the reader prompts, the brief, the research with fakes
// (every helper run's options, the records, the run file, the brief and the exclude file), the PR
// polls, the dry PR, run records read and saved, the list API and START.

const (
	clock0 = 1_800_000_000_000
	// dryPolls is how many dry PR polls the recorder took.
	dryPolls = 6
)

// answersFor are the answers with the recorder's temp folder replaced by base.
func answersFor(t *testing.T, base string) *js.Object {
	t.Helper()

	answersText := readAnswers(t)

	parsed, err := js.Parse(answersText)
	if err != nil {
		t.Fatal(err)
	}

	recorded := text(get(parsed, "base"))
	if base != "" {
		parsed, err = js.Parse([]byte(strings.ReplaceAll(string(answersText), recorded, base)))
		if err != nil {
			t.Fatal(err)
		}
	}

	object, _ := parsed.(*js.Object)

	return object
}

func readAnswers(t *testing.T) []byte {
	t.Helper()

	file, err := os.Open("../../testdata/answers/openwork/pure.json.gz")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = file.Close() }()

	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

func section(t *testing.T, base, name string) []any {
	t.Helper()

	list, _ := get(answersFor(t, base), name).([]any)
	if len(list) == 0 {
		t.Fatalf("no %s answers", name)
	}

	return list
}

func recordOf(t *testing.T, value any) *openwork.Record {
	t.Helper()

	record := &openwork.Record{}

	err := json.Unmarshal([]byte(js.Stringify(value)), record)
	if err != nil {
		t.Fatal(err)
	}

	return record
}

func nilOr(value string, present bool) any {
	if !present {
		return nil
	}

	return value
}

func TestSlugsMatchTheRecordedAnswers(t *testing.T) {
	t.Parallel()

	for index, item := range section(t, "", "slug") {
		same(t, strconv.Itoa(index), openwork.Slug(text(get(item, "title"))), get(item, "out"))
	}
}

func TestBuildTargetsMatchTheRecordedAnswers(t *testing.T) {
	t.Parallel()

	for index, item := range section(t, "", "target") {
		current := settings.Defaults()
		jiraSettings := get(get(get(item, "settings"), "atlassian"), "jira")

		if repo, isText := get(jiraSettings, "buildRepo").(string); isText {
			current.Atlassian.Jira.BuildRepo = &repo
		}

		if base, isText := get(jiraSettings, "baseBranch").(string); isText {
			current.Atlassian.Jira.BaseBranch = &base
		}

		current.Repos.Allow = []string{}
		for _, pattern := range listOf(get(get(get(item, "settings"), "repos"), "allow")) {
			current.Repos.Allow = append(current.Repos.Allow, text(pattern))
		}

		target := openwork.BuildTarget(current,
			func(repo string, _ settings.Settings) (string, bool) {
				if get(item, "found") != true {
					return "", false
				}

				if js.Lower(repo) != "acme/widgets" {
					return "", false
				}

				return text(get(item, "clonePath")), true
			},
			func(string) (string, bool) {
				branch, known := get(item, "def").(string)

				return branch, known
			})

		got := js.NewObject()
		got.Set("repo", nilOr(target.Repo, target.Repo != ""))
		got.Set("clone", nilOr(target.Clone, target.Clone != ""))
		got.Set("base", nilOr(target.Base, target.Base != ""))
		got.Set("reason", nilOr(target.Reason, target.Reason != ""))
		same(t, strconv.Itoa(index), got, get(item, "out"))
	}
}

func TestDefaultBranchMatchesTheRecordedAnswers(t *testing.T) {
	t.Parallel()

	clone := t.TempDir()
	heads := filepath.Join(clone, ".git", "refs", "remotes", "origin")

	err := os.MkdirAll(heads, 0o755)
	if err != nil {
		t.Fatal(err)
	}

	for index, item := range section(t, "", "defaultBranch") {
		writeJSON(t, filepath.Join(heads, "HEAD"), text(get(item, "head")))

		branch, known := openwork.DefaultBranch(clone)
		same(t, strconv.Itoa(index), nilOr(branch, known), get(item, "out"))
	}

	_, known := openwork.DefaultBranch(filepath.Join(clone, "nope"))
	same(t, "missing", known, false)
}

func TestPRURLsMatchTheRecordedAnswers(t *testing.T) {
	t.Parallel()

	for _, item := range section(t, "", "prUrl") {
		repo := openwork.PRURLRepo(text(get(item, "url")))
		same(t, text(get(item, "url")), nilOr(repo, repo != ""), get(item, "out"))
	}
}

func TestLinkedWorktreesMatchTheRecordedAnswers(t *testing.T) {
	t.Parallel()

	base := realTemp(t)

	err := os.MkdirAll(filepath.Join(base, "linked", "wt"), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	for index, item := range section(t, base, "linked") {
		calls := []any{}
		run := func(_ context.Context, name string, args []string, dir string,
			timeout time.Duration,
		) (string, error) {
			calls = append(calls, []any{name, anyList(args), dir, float64(timeout /
				time.Millisecond)})

			answer := get(item, "listing")
			if args[0] == "rev-parse" {
				answer = get(item, "first")
			}

			if answer == nil {
				return "", fakeError("fatal: not a git repository")
			}

			return text(answer), nil
		}

		repo, linked := openwork.LinkedWorktreeRepo(context.Background(),
			text(get(item, "path")), run)

		label := strconv.Itoa(index)
		same(t, label+" out", nilOr(repo, linked), get(item, "out"))
		same(t, label+" calls", calls, get(item, "calls"))
	}
}

func TestReaderPromptsAndBriefsMatchTheRecordedAnswers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for index, item := range section(t, "", "readerPrompt") {
		path := filepath.Join(dir, "jira-site.json")
		_ = os.Remove(path)

		if site, isText := get(item, "site").(string); isText {
			writeJSON(t, path, site)
		}

		prompt := openwork.ReaderPrompt(recordOf(t, get(item, "record")), text(get(item, "id")),
			jira.ReadSite(dir))
		same(t, strconv.Itoa(index), prompt, get(item, "out"))
	}

	for index, item := range section(t, "", "brief") {
		same(t, strconv.Itoa(index), openwork.BriefText(recordOf(t, get(item, "record"))),
			get(item, "out"))
	}
}

func TestDryPRsMatchTheRecordedAnswers(t *testing.T) {
	t.Parallel()

	for _, item := range section(t, "", "dryPr") {
		made := newWorld(t, worldOpts{settings: map[string]any{}, dry: true})
		made.setClock(clock0 + 79)

		key := text(get(item, "key"))
		seq := make([]any, 0, dryPolls)

		for step := range dryPolls {
			if step == 3 {
				if key == "PROJ-3" {
					openwork.FixDry(made.work, key)
				}
			}

			seq = append(seq, parsed(t, openwork.DryStatus(made.work, key)))
		}

		same(t, key, seq, get(item, "seq"))
	}
}

func parsed(t *testing.T, value any) any {
	t.Helper()

	out, err := js.Parse(js.Marshal(value))
	if err != nil {
		t.Fatal(err)
	}

	return out
}

func TestPollsMatchTheRecordedAnswers(t *testing.T) {
	t.Parallel()

	for index, item := range section(t, "", "polls") {
		made := newWorld(t, worldOpts{
			settings: workSettings(t.TempDir(), ""), runs: []any{get(item, "record")},
		})
		made.setClock(clock0 + float64(index))
		made.sh = func(string, []string, string) (string, error) {
			return text(get(item, "view")), nil
		}

		err := openwork.PollPR(made.work, "PROJ-5")
		if err != nil {
			t.Fatal(err)
		}

		label := strconv.Itoa(index)
		same(t, label+" files", made.files(), get(item, "files"))
		same(t, label+" snapshot", runsOf(t, made.work), get(item, "snapshot"))
	}
}

// workSettings are the recorder's settings for a clone at clone: Open work on for PROJ,
// building in acme/widgets.
func workSettings(clone, extra string) any {
	text := `{"version":1,"repos":{"allow":["acme/*"],"clones":{"acme/widgets":` +
		js.Stringify(clone) + `},"cloneRoot":null},"github":{"enabled":true},"slack":` +
		`{"enabled":false,"channels":["#code-review"],"dms":false,"draftChannel":null},` +
		`"atlassian":{"enabled":true,"jira":{"projects":["PROJ"],"buildRepo":"acme/widgets"}},` +
		`"reviews":{"requested":true,"allOpen":false}` + extra + `}`

	parsed, _ := js.Parse([]byte(text))

	return parsed
}

func TestRecordsReadAndSavedMatchTheRecordedAnswers(t *testing.T) {
	t.Parallel()

	load := get(answersFor(t, ""), "load")
	made := newWorldFiles(t, listOf(get(load, "records")))

	same(t, "snapshot", runsOf(t, made.work), get(load, "snapshot"))
	made.setClock(clock0 + 99)
	openwork.TouchAll(made.work)
	same(t, "files", made.files(), get(load, "files"))
}

// newWorldFiles is a world whose runs folder holds records, plus the files the recorder added
// once the runs were read: one that is not JSON, one that is an array, and one that is not a
// run's.
func newWorldFiles(t *testing.T, records []any) *world {
	t.Helper()

	base := realTemp(t)
	made := newWorld(t, worldOpts{base: base, settings: workSettings(base, ""), runs: records})

	for name, content := range map[string]string{
		"notes.txt": "x", "PROJ-11.json": "not json", "PROJ-12.json": "[1]",
	} {
		writeJSON(t, filepath.Join(made.dir, "runs", name), content)
	}

	return made
}
