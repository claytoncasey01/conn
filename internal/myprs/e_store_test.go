package myprs_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/myprs"
)

// The port of myPrsStore.test.ts: what the recorded answers (section files) never see. They
// cover a bad record dropped alone, the manual mark and both prunes; these cover a missing file,
// the modes, the rename seam, the field rules case by case and TrackOf.

var errERename = errors.New("rename refused")

// The fixtures of myPrsStore.test.ts, as JSON.
const (
	eLaunchJSON = `{"at":1000,"model":"sonnet","name":"self-repo-12","worktree":"/wt/a",` +
		`"headSha":"abc123","round":1,"armed":true,"askedSha":null,"askedAt":null,` +
		`"closedAt":null,"askTry":null}`
	eLaunchOldJSON = `{"at":1000,"model":"sonnet","name":"self-repo-12","worktree":"/wt/a",` +
		`"headSha":"abc123","round":1,"armed":true,"askedSha":null,"askedAt":null,` +
		`"closedAt":null`
	eLaunch2JSON = `{"at":2000,"model":"sonnet","name":"self-repo-12","worktree":"/wt/a",` +
		`"headSha":"abc123","round":2,"armed":false,"askedSha":"def456","askedAt":2500,` +
		`"closedAt":3000,"askTry":null}`
	eProposalJSON = `{"threadId":"T1","text":"fixed","state":"proposed","error":null,` +
		`"launchAt":2000}`
	eDraftJSON = `{"sha":"def456","text":"ready","state":"saved","error":null,"at":4000,` +
		`"tries":1}`
)

// eEntryJSON is Bun's entry() with the fields in over (raw JSON) in place of its own.
func eEntryJSON(over map[string]string) string {
	fields := []struct{ key, value string }{
		{"firstSeenAt", "500"},
		{"launches", "[" + eLaunchJSON + "," + eLaunch2JSON + "]"},
		{"proposals", "[" + eProposalJSON + "]"},
		{"draft", eDraftJSON},
		{"seenAt", "5000"},
	}
	parts := make([]string, 0, len(fields))

	for _, field := range fields {
		value := field.value
		if replaced, found := over[field.key]; found {
			value = replaced
		}

		parts = append(parts, `"`+field.key+`":`+value)
	}

	return "{" + strings.Join(parts, ",") + "}"
}

// eWithText is a fresh myprs.json holding text.
func eWithText(t *testing.T, text string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "myprs.json")

	err := os.WriteFile(path, []byte(text), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return path
}

// eLoadEntry loads a file whose only record is o/r#1 = entry; nil when it was dropped.
func eLoadEntry(t *testing.T, entry string) *myprs.Entry {
	t.Helper()

	loaded := myprs.LoadFile(eWithText(t, `{"version":1,"prs":{"o/r#1":`+entry+`}}`))
	found, _ := loaded.PRs.Get("o/r#1")

	return found
}

// eSameJSON fails unless got, as JSON, is want (JSON text, compared as JavaScript writes it).
func eSameJSON(t *testing.T, what string, got any, want string) {
	t.Helper()

	parsed, err := js.Parse([]byte(want))
	if err != nil {
		t.Fatalf("%s: the expected JSON: %v", what, err)
	}

	gotParsed, err := js.Parse(js.Marshal(got))
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}

	if js.Stringify(gotParsed) != js.Stringify(parsed) {
		t.Errorf("%s:\n got  %s\n want %s", what, js.Stringify(gotParsed), js.Stringify(parsed))
	}
}

func TestEStoreMissingOrBadFileLoadsEmpty(t *testing.T) {
	t.Parallel()

	empty := string(myprs.EmptyFile().Marshal())

	t.Run("missing", func(t *testing.T) {
		t.Parallel()

		loaded := myprs.LoadFile(filepath.Join(t.TempDir(), "none", "myprs.json"))
		if string(loaded.Marshal()) != empty {
			t.Errorf("got %s", loaded.Marshal())
		}
	})

	for _, tt := range []string{
		"", `{"version":1,"prs":{"a/b#1":`, "[]", `{"version":2}`, "null", "42",
	} {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()

			loaded := myprs.LoadFile(eWithText(t, tt))
			if string(loaded.Marshal()) != empty {
				t.Errorf("%q: got %s", tt, loaded.Marshal())
			}
		})
	}
}

func TestESaveRoundTripsOwnerOnly(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "data", "myprs.json")
	text := `{"version":1,"prs":{"o/r#12":` + eEntryJSON(nil) + `,"o/r#13":` +
		eEntryJSON(map[string]string{"draft": "null", "proposals": "[]", "launches": "[]"}) + `}}`
	file := myprs.LoadFile(eWithText(t, text))

	err := myprs.SaveFile(path, file, nil)
	if err != nil {
		t.Fatal(err)
	}

	eSameJSON(t, "loaded again", myprs.LoadFile(path), text)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode %o", info.Mode().Perm())
	}

	folder, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}

	if folder.Mode().Perm() != 0o700 {
		t.Errorf("folder mode %o", folder.Mode().Perm())
	}
}

func TestESaveThatCannotRenameLeavesTheOldFileAndNoTemp(t *testing.T) {
	t.Parallel()

	folder := t.TempDir()
	path := filepath.Join(folder, "myprs.json")

	err := myprs.SaveFile(path, myprs.EmptyFile(), nil)
	if err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	file := myprs.EmptyFile()
	myprs.TrackOf(file, "o/r#1", 10)

	err = myprs.SaveFile(path, file, func(string, string) error { return errERename })
	if !errors.Is(err, errERename) {
		t.Errorf("got %v, want the rename's error", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if string(after) != string(before) {
		t.Errorf("the old file changed:\n%s", after)
	}

	entries, err := os.ReadDir(folder)
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if entry.Name() != "myprs.json" {
			t.Errorf("left behind: %s", entry.Name())
		}
	}
}

func TestEDraftTries(t *testing.T) {
	t.Parallel()

	old := `{"sha":"def456","text":"ready","state":"failed","error":"x","at":4000`

	t.Run("none is one", func(t *testing.T) {
		t.Parallel()

		entry := eLoadEntry(t, eEntryJSON(map[string]string{"draft": old + "}"}))
		if entry == nil {
			t.Fatal("dropped")
		}

		eSameJSON(t, "draft", entry.Draft, old+`,"tries":1}`)
	})

	t.Run("three", func(t *testing.T) {
		t.Parallel()

		entry := eLoadEntry(t, eEntryJSON(map[string]string{"draft": old + `,"tries":3}`}))
		if entry == nil {
			t.Fatal("dropped")
		}

		if entry.Draft.Tries != 3 {
			t.Errorf("tries %d", entry.Draft.Tries)
		}
	})

	// Bun's NaN is written as null by JSON.stringify: the null row is that case too.
	for _, tt := range []string{"0", "-1", "1.5", `"x"`, "null", "1000"} {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()

			entry := eLoadEntry(t, eEntryJSON(map[string]string{
				"draft": old + `,"tries":` + tt + "}",
			}))
			if entry != nil {
				t.Errorf("tries %s: the record was kept", tt)
			}
		})
	}
}

func TestEAskTry(t *testing.T) {
	t.Parallel()

	launchWith := func(askTry string) string {
		return eLaunchOldJSON + `,"askTry":` + askTry + "}"
	}

	tests := []struct {
		name, askTry, want string
	}{
		{"none", "", eLaunchJSON},
		{
			"tried", `{"sha":"abc123","at":1500,"firstAt":1200,"tries":2}`,
			launchWith(`{"sha":"abc123","at":1500,"firstAt":1200,"tries":2}`),
		},
		{
			"no firstAt", `{"sha":"abc123","at":1500,"tries":1}`,
			launchWith(`{"sha":"abc123","at":1500,"firstAt":1500,"tries":1}`),
		},
		{"text", `"x"`, eLaunchJSON},
		{"sha a number", `{"sha":1,"at":1,"tries":1}`, eLaunchJSON},
		{"at text", `{"sha":"a","at":"x","tries":1}`, eLaunchJSON},
		{"no tries", `{"sha":"a","at":1,"tries":0}`, eLaunchJSON},
		{"half a try", `{"sha":"a","at":1,"tries":1.5}`, eLaunchJSON},
		{"too many", `{"sha":"a","at":1,"tries":100}`, eLaunchJSON},
		{"firstAt text", `{"sha":"a","at":1,"firstAt":"x","tries":1}`, eLaunchJSON},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			launch := eLaunchOldJSON + "}"
			if tt.askTry != "" {
				launch = launchWith(tt.askTry)
			}

			entry := eLoadEntry(t, eEntryJSON(map[string]string{"launches": "[" + launch + "]"}))
			if entry == nil {
				t.Fatal("dropped")
			}

			eSameJSON(t, tt.name, entry.Launches, "["+tt.want+"]")
		})
	}
}

func TestEProposalFields(t *testing.T) {
	t.Parallel()

	failed := `{"threadId":"T1","text":"x","state":"failed","error":"e","launchAt":0}`
	proposed := `{"threadId":"T1","text":"x","state":"proposed","error":null,"launchAt":2000}`
	failedTried := `{"threadId":"T1","text":"x","state":"failed","error":"e","launchAt":0,`
	tests := []struct {
		name, proposal, want string
	}{
		{"no launchAt", `{"threadId":"T1","text":"x","state":"failed","error":"e"}`, failed},
		{"launchAt", proposed, proposed},
		{
			"launchAt text",
			`{"threadId":"T1","text":"x","state":"proposed","error":null,"launchAt":"x"}`, "",
		},
		{"triedAt", failedTried + `"triedAt":5000}`, failedTried + `"triedAt":5000}`},
		{"triedAt text", failedTried + `"triedAt":"x"}`, failed},
		{"triedAt null", failedTried + `"triedAt":null}`, failed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			entry := eLoadEntry(t, eEntryJSON(map[string]string{
				"proposals": "[" + tt.proposal + "]",
			}))
			if entry == nil {
				t.Fatal("dropped")
			}

			eSameJSON(t, tt.name, entry.Proposals, "["+tt.want+"]")
		})
	}
}

func TestEDraftChannel(t *testing.T) {
	t.Parallel()

	base := `{"sha":"a","text":"t","state":"saved","error":null,"at":1,"tries":1`

	t.Run("kept", func(t *testing.T) {
		t.Parallel()

		entry := eLoadEntry(t, eEntryJSON(map[string]string{
			"draft": base + `,"channel":"#code-review"}`,
		}))
		if entry.Draft.Channel != "#code-review" {
			t.Errorf("channel %q", entry.Draft.Channel)
		}
	})

	t.Run("none", func(t *testing.T) {
		t.Parallel()

		entry := eLoadEntry(t, eEntryJSON(map[string]string{"draft": base + "}"}))
		eSameJSON(t, "draft", entry.Draft, base+"}")
	})

	for _, tt := range []string{
		`"code-review"`, `"#A"`, `"#a b"`, "7", "null", `"#` + strings.Repeat("a", 81) + `"`,
	} {
		t.Run(tt, func(t *testing.T) {
			t.Parallel()

			entry := eLoadEntry(t, eEntryJSON(map[string]string{
				"draft": base + `,"channel":` + tt + "}",
			}))
			if entry == nil {
				t.Fatal("dropped")
			}

			eSameJSON(t, tt, entry.Draft, base+"}")
		})
	}
}

func TestETrackOfKeepsTwoReposApart(t *testing.T) {
	t.Parallel()

	file := myprs.EmptyFile()
	one := myprs.TrackOf(file, myprs.EntryKey("o/one", 7), 10)
	two := myprs.TrackOf(file, myprs.EntryKey("o/two", 7), 20)

	if one == two {
		t.Error("the two repos share a record")
	}

	keys := file.PRs.Keys()
	slices.Sort(keys)

	if !slices.Equal(keys, []string{"o/one#7", "o/two#7"}) {
		t.Errorf("keys %v", keys)
	}

	if myprs.TrackOf(file, "o/one#7", 99) != one {
		t.Error("a second TrackOf made a new record")
	}

	if one.FirstSeenAt != 10 {
		t.Errorf("firstSeenAt %v", one.FirstSeenAt)
	}
}
