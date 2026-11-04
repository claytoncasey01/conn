package themes_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/testtime"
	"github.com/claytoncasey01/conn/internal/themes"
)

// lockedBuffer is a log destination the test reads while the library writes.
type lockedBuffer struct {
	guard  sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.guard.Lock()
	defer b.guard.Unlock()

	count, err := b.buffer.Write(data)
	if err != nil {
		return count, fmt.Errorf("log buffer: %w", err)
	}

	return count, nil
}

func (b *lockedBuffer) lines() []string {
	b.guard.Lock()
	defer b.guard.Unlock()

	text := strings.TrimSpace(b.buffer.String())
	if text == "" {
		return nil
	}

	return strings.Split(text, "\n")
}

// rig is a built-in folder holding only the real Default, and an empty user folder.
type rig struct {
	root, builtin, user string
	logged              *lockedBuffer
}

func defaultText(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile("../../themes/default/theme.json")
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func newRig(t *testing.T) *rig {
	t.Helper()

	root := t.TempDir()
	built := &rig{
		root:    root,
		builtin: filepath.Join(root, "builtin"),
		user:    filepath.Join(root, "user"),
		logged:  &lockedBuffer{},
	}

	write(t, filepath.Join(built.builtin, "default", "theme.json"), defaultText(t))
	mkdir(t, built.user)

	return built
}

func (bed *rig) library() *themes.Library {
	return themes.New(themes.Disk(bed.builtin), themes.Disk(bed.user),
		log.New(bed.logged, "", 0))
}

func (bed *rig) list() []themes.Entry { return bed.library().List() }

func (bed *rig) css(id, version string) (string, bool) { return bed.library().CSS(id, version) }

func mkdir(t *testing.T, dir string) {
	t.Helper()

	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, name, text string) {
	t.Helper()
	mkdir(t, filepath.Dir(name))

	err := os.WriteFile(name, []byte(text), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

func link(t *testing.T, target, name string) {
	t.Helper()

	err := os.Symlink(target, name)
	if err != nil {
		t.Fatal(err)
	}
}

// manifest is Default with another id and name, and any fields replaced.
func manifest(t *testing.T, themeID string, fields map[string]any) string {
	t.Helper()

	parsed, err := js.Parse([]byte(defaultText(t)))
	if err != nil {
		t.Fatal(err)
	}

	object, _ := parsed.(*js.Object)
	object.Set("id", themeID)
	object.Set("name", themeID)

	for key, value := range fields {
		object.Set(key, value)
	}

	return js.Stringify(object)
}

// put writes a theme folder in dir: theme.json, and theme.css when css is not nil.
func put(t *testing.T, dir, folder, json string, css *string) {
	t.Helper()
	write(t, filepath.Join(dir, folder, "theme.json"), json)

	if css != nil {
		write(t, filepath.Join(dir, folder, "theme.css"), *css)
	}
}

func sha(text string) string {
	sum := sha256.Sum256([]byte(text))

	return hex.EncodeToString(sum[:])
}

func find(entries []themes.Entry, id string) (themes.Entry, bool) {
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true
		}
	}

	return themes.Entry{}, false
}

func invalidOf(t *testing.T, entries []themes.Entry, themeID string) themes.Entry {
	t.Helper()

	entry, found := find(entries, themeID)
	if !found {
		t.Fatalf("%s is not listed", themeID)
	}

	if entry.Invalid == "" {
		t.Fatalf("%s is valid, want invalid", themeID)
	}

	return entry
}

func matches(t *testing.T, got, pattern string) {
	t.Helper()

	if !regexp.MustCompile(pattern).MatchString(got) {
		t.Errorf("%q does not match %s", got, pattern)
	}
}

func ids(entries []themes.Entry) string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, string(entry.Source)+":"+entry.ID)
	}

	return strings.Join(names, " ")
}

func TestTheEmbeddedBuiltinsListDefaultAndNeon(t *testing.T) {
	t.Parallel()

	library := themes.New(themes.Embedded(conn.Themes()),
		themes.Disk(filepath.Join(t.TempDir(), "none")), log.New(&lockedBuffer{}, "", 0))
	list := library.List()

	want := "builtin:crt builtin:default builtin:matrix builtin:neon"
	if ids(list) != want {
		t.Errorf("list = %s, want %s", ids(list), want)
	}

	for _, entry := range list {
		if entry.Invalid != "" {
			t.Errorf("%s is invalid: %s", entry.ID, entry.Invalid)
		}
	}

	defaultEntry, _ := find(list, "default")
	if defaultEntry.HasCSS {
		t.Error("Default has a stylesheet")
	}

	neon, _ := find(list, "neon")
	if !neon.HasCSS {
		t.Fatal("Neon has no stylesheet")
	}

	css, served := library.CSS("neon", "")
	if !served {
		t.Fatal("Neon's stylesheet is not served")
	}

	if !strings.Contains(css, `[data-theme="neon"]`) {
		t.Error("Neon's stylesheet is not Neon's")
	}

	if neon.CSSSha256 != sha(css) {
		t.Error("Neon's hash is not of the stylesheet served")
	}
}

func TestUserThemes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setUp   func(t *testing.T, bed *rig)
		id      string
		invalid string // a pattern, or "" for valid
		source  string
	}{
		{
			name: "a good user theme is valid with source user",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()

				put(t, bed.user, "mine", manifest(t, "mine", nil), nil)
			},
			id:     "mine",
			source: "user",
		},
		{
			name: "a bad value names the slot",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()

				tokens := js.NewObject()
				light := js.NewObject()
				light.Set("bg", "url(http://x)")
				tokens.Set("light", light)
				tokens.Set("dark", js.NewObject())
				put(t, bed.user, "bad", manifest(t, "bad", map[string]any{"tokens": tokens}), nil)
			},
			id: "bad", invalid: `bg`, source: "user",
		},
		{
			name: "a contrast failure names the pair",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()

				tokens := js.NewObject()
				dark := js.NewObject()
				dark.Set("tx", "oklch(.3 0 0)")
				tokens.Set("dark", dark)
				put(t, bed.user, "dim", manifest(t, "dim", map[string]any{
					"modes": []any{"dark"}, "tokens": tokens,
				}), nil)
			},
			id: "dim", invalid: `tx on bg`, source: "user",
		},
		{
			name: "invalid JSON",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()
				put(t, bed.user, "broken", "{ nope", nil)
			},
			id: "broken", invalid: `not valid JSON`, source: "user",
		},
		{
			name: "no theme.json",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()
				mkdir(t, filepath.Join(bed.user, "empty"))
			},
			id: "empty", invalid: `no theme\.json`, source: "user",
		},
		{
			name: "a manifest over 64 KB",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()

				put(t, bed.user, "bigjson", manifest(t, "bigjson", map[string]any{
					"description": strings.Repeat("x", 70_000),
				}), nil)
			},
			id: "bigjson", invalid: `64 KB`, source: "user",
		},
		{
			name: "a stylesheet over 256 KB",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()

				put(t, bed.user, "bigcss", manifest(t, "bigcss", nil),
					new(strings.Repeat("a{}", 100_000)))
			},
			id: "bigcss", invalid: `256 KB`, source: "user",
		},
		{
			name: "a manifest id that is a built-in's",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()

				put(t, bed.user, "sneaky", manifest(t, "default", nil), nil)
			},
			id: "sneaky", invalid: `built-in`, source: "user",
		},
		{
			name: "a folder that is a link is never read",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()

				put(t, bed.root, "elsewhere", manifest(t, "linked", nil), nil)
				link(t, filepath.Join(bed.root, "elsewhere"), filepath.Join(bed.user, "linked"))
			},
			id: "linked", invalid: `symbolic link`, source: "user",
		},
		{
			name: "a theme.json that is a link is never read",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()

				put(t, bed.root, "real", manifest(t, "swap", nil), nil)
				mkdir(t, filepath.Join(bed.user, "swap"))
				link(t, filepath.Join(bed.root, "real", "theme.json"),
					filepath.Join(bed.user, "swap", "theme.json"))
			},
			id: "swap", invalid: `^theme\.json is not a regular file`, source: "user",
		},
		{
			name: "a theme.css that is a link is refused",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()

				write(t, filepath.Join(bed.root, "secret.css"), "a{}")
				put(t, bed.user, "sly", manifest(t, "sly", nil), nil)
				link(t, filepath.Join(bed.root, "secret.css"),
					filepath.Join(bed.user, "sly", "theme.css"))
			},
			id: "sly", invalid: `theme\.css is not a regular file`, source: "user",
		},
		{
			name: "a gallery folder is judged as gallery, so a built-in id is refused",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()

				put(t, bed.user, "default", manifest(t, "default", nil), nil)
				write(t, filepath.Join(bed.user, "default", ".source.json"), "{}")
			},
			id: "default", invalid: `built-in`, source: "gallery",
		},
		{
			name: "a .source.json that is not a regular file",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()

				put(t, bed.user, "odd", manifest(t, "odd", nil), new("a{}"))
				link(t, filepath.Join(bed.root, "nowhere"),
					filepath.Join(bed.user, "odd", ".source.json"))
			},
			id: "odd", invalid: `\.source\.json`, source: "user",
		},
		{
			name: "a folder name that is not an id is never read",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()

				put(t, bed.user, "My Theme", manifest(t, "my-theme", nil), nil)
			},
			id: "My Theme", invalid: `^folder name must be a-z, 0-9 and -$`, source: "user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bed := newRig(t)
			tt.setUp(t, bed)

			entries := bed.list()
			found := false

			for _, entry := range entries {
				if entry.ID != tt.id {
					continue
				}

				if string(entry.Source) != tt.source {
					continue
				}

				found = true

				checkEntry(t, entry, tt.invalid)
			}

			if !found {
				t.Fatalf("%s:%s is not listed in %s", tt.source, tt.id, ids(entries))
			}
		})
	}
}

func checkEntry(t *testing.T, entry themes.Entry, invalid string) {
	t.Helper()

	if invalid == "" {
		if entry.Invalid != "" {
			t.Fatalf("invalid: %s", entry.Invalid)
		}

		if entry.Manifest == nil {
			t.Error("a valid entry has no manifest")
		}

		return
	}

	matches(t, entry.Invalid, invalid)

	if entry.Manifest != nil {
		t.Error("an invalid entry has a manifest")
	}

	if entry.HasCSS {
		t.Error("an invalid entry has a stylesheet")
	}

	if entry.CSSSha256 != "" {
		t.Error("an invalid entry has a stylesheet hash")
	}
}

func TestBuiltinsComeFirstThenUserThemesEachSorted(t *testing.T) {
	t.Parallel()

	bed := newRig(t)
	put(t, bed.builtin, "beta", manifest(t, "beta", nil), nil)
	put(t, bed.user, "zed", manifest(t, "zed", nil), nil)
	put(t, bed.user, "alpha", manifest(t, "alpha", nil), nil)

	want := "builtin:beta builtin:default user:alpha user:zed"
	if ids(bed.list()) != want {
		t.Errorf("list = %s, want %s", ids(bed.list()), want)
	}
}

func TestAUserFolderNamedLikeABuiltinIsInvalid(t *testing.T) {
	t.Parallel()

	bed := newRig(t)
	put(t, bed.user, "default", manifest(t, "default", nil), nil)

	for _, entry := range bed.list() {
		if entry.Source == "user" {
			matches(t, entry.Invalid, `is a built-in`)
		}
	}
}

func TestAFolderWhoseNameIsNotItsManifestIDIsInvalid(t *testing.T) {
	t.Parallel()

	bed := newRig(t)
	put(t, bed.user, "one", manifest(t, "two", nil), nil)

	entries := bed.list()
	entry := invalidOf(t, entries, "one")
	matches(t, entry.Invalid, `"one"`)
	matches(t, entry.Invalid, `"two"`)

	_, listed := find(entries, "two")
	if listed {
		t.Error("the manifest's id is listed")
	}
}

func TestTheStylesheetsThatAreNeverServed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setUp func(t *testing.T, bed *rig)
		id    string
	}{
		{
			name: "over 256 KB",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()

				put(t, bed.user, "bigcss", manifest(t, "bigcss", nil),
					new(strings.Repeat("a{}", 100_000)))
			},
			id: "bigcss",
		},
		{
			name: "a link",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()

				write(t, filepath.Join(bed.root, "secret.css"), "a{}")
				put(t, bed.user, "sly", manifest(t, "sly", nil), nil)
				link(t, filepath.Join(bed.root, "secret.css"),
					filepath.Join(bed.user, "sly", "theme.css"))
			},
			id: "sly",
		},
		{
			name: "in a gallery folder",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()

				put(t, bed.user, "gal", manifest(t, "gal", nil), new("a{}"))
				write(t, filepath.Join(bed.user, "gal", ".source.json"), "{}")
			},
			id: "gal",
		},
		{
			name: "beside a .source.json that is not a file",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()

				put(t, bed.user, "odd", manifest(t, "odd", nil), new("a{}"))
				link(t, filepath.Join(bed.root, "nowhere"),
					filepath.Join(bed.user, "odd", ".source.json"))
			},
			id: "odd",
		},
		{
			name: "in a linked folder",
			setUp: func(t *testing.T, bed *rig) {
				t.Helper()

				put(t, bed.root, "elsewhere", manifest(t, "linked", nil), new("a{}"))
				link(t, filepath.Join(bed.root, "elsewhere"), filepath.Join(bed.user, "linked"))
			},
			id: "linked",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bed := newRig(t)
			tt.setUp(t, bed)

			for _, version := range []string{"", sha("a{}"), sha(strings.Repeat("a{}", 100_000))} {
				_, served := bed.css(tt.id, version)
				if served {
					t.Errorf("served with v=%q", version)
				}
			}
		})
	}
}

func TestAGalleryFolderIsListedAsGalleryWithoutItsStylesheet(t *testing.T) {
	t.Parallel()

	bed := newRig(t)
	put(t, bed.user, "gal", manifest(t, "gal", nil), new("a{}"))
	write(t, filepath.Join(bed.user, "gal", ".source.json"), "{}")

	entry, _ := find(bed.list(), "gal")
	checkEntry(t, entry, "")

	if entry.Source != "gallery" {
		t.Errorf("source = %s, want gallery", entry.Source)
	}

	if entry.HasCSS {
		t.Error("a gallery theme has a stylesheet")
	}
}

func TestTheLimitOfUserThemes(t *testing.T) {
	t.Parallel()

	bed := newRig(t)

	for index := range 101 {
		id := fmt.Sprintf("t%03d", index)
		put(t, bed.user, id, manifest(t, id, nil), nil)
	}

	user := []themes.Entry{}

	for _, entry := range bed.list() {
		if entry.Source == "user" {
			user = append(user, entry)
		}
	}

	if len(user) != 101 {
		t.Fatalf("user entries = %d, want 100 themes and the note", len(user))
	}

	_, listed := find(user, "t100")
	if listed {
		t.Error("the 101st theme is listed")
	}

	note := invalidOf(t, user, "_limit")
	if note.Invalid != "limit of 100 user themes reached, 1 folder not listed" {
		t.Errorf("note = %q", note.Invalid)
	}

	_, served := bed.css("t100", "")
	if served {
		t.Error("the 101st theme's stylesheet is served")
	}
}

func TestUnderscoreAndDotFoldersAreSkipped(t *testing.T) {
	t.Parallel()

	bed := newRig(t)
	put(t, bed.user, "_draft", manifest(t, "draft", nil), nil)
	write(t, filepath.Join(bed.builtin, "_template", "theme.json"), "{}")
	mkdir(t, filepath.Join(bed.user, ".git"))
	put(t, bed.user, ".hidden", manifest(t, "hidden", nil), nil)
	mkdir(t, filepath.Join(bed.builtin, ".cache"))
	put(t, bed.user, "mine", manifest(t, "mine", nil), nil)
	write(t, filepath.Join(bed.user, "stray.txt"), "x")

	if ids(bed.list()) != "builtin:default user:mine" {
		t.Errorf("list = %s", ids(bed.list()))
	}
}

func TestAStylesheetIsHashedAndServedForThatHashOnly(t *testing.T) {
	t.Parallel()

	bed := newRig(t)
	css := ":root { --x: 1 }\n"
	put(t, bed.user, "styled", manifest(t, "styled", nil), &css)

	entry, _ := find(bed.list(), "styled")
	if !entry.HasCSS {
		t.Fatal("no stylesheet")
	}

	if entry.CSSSha256 != sha(css) {
		t.Errorf("hash = %s, want %s", entry.CSSSha256, sha(css))
	}

	got, served := bed.css("styled", entry.CSSSha256)
	if !served {
		t.Fatal("not served for its own hash")
	}

	if got != css {
		t.Errorf("served %q", got)
	}

	// Edited after the list was read: the old hash no longer serves anything.
	write(t, filepath.Join(bed.user, "styled", "theme.css"), "b{}")

	_, served = bed.css("styled", entry.CSSSha256)
	if served {
		t.Error("served the edited file for the old hash")
	}

	for _, version := range []string{"", strings.Repeat("B", 64)} {
		_, served = bed.css("styled", version)
		if served {
			t.Errorf("served with v=%q", version)
		}
	}

	got, _ = bed.css("styled", sha("b{}"))
	if got != "b{}" {
		t.Errorf("served %q for the new hash", got)
	}
}

func TestABuiltinStylesheetIgnoresTheVersionAndAUserFolderNeverShadowsIt(t *testing.T) {
	t.Parallel()

	bed := newRig(t)
	write(t, filepath.Join(bed.builtin, "default", "theme.css"), "c{}")
	put(t, bed.user, "default", manifest(t, "default", nil), new("evil{}"))

	for _, version := range []string{"", sha("old"), sha("c{}"), sha("evil{}")} {
		got, served := bed.css("default", version)
		if !served {
			t.Fatalf("not served with v=%q", version)
		}

		if got != "c{}" {
			t.Errorf("served %q with v=%q", got, version)
		}
	}
}

func TestCSSIsNotFoundForAnythingButAnID(t *testing.T) {
	t.Parallel()

	bed := newRig(t)
	put(t, bed.root, "x", manifest(t, "x", nil), new("a{}"))
	put(t, bed.user, "ok", manifest(t, "ok", nil), new("a{}"))

	bad := []string{
		"../x", "a/b", "..", ".", "", "OK", "ok/../ok", "%2e%2e", "ok\x00", "../user/ok", "missing",
	}
	for _, id := range bad {
		_, served := bed.css(id, sha("a{}"))
		if served {
			t.Errorf("served %q", id)
		}
	}

	got, _ := bed.css("ok", sha("a{}"))
	if got != "a{}" {
		t.Errorf("ok served %q", got)
	}
}

func TestABrokenBuiltinIsListedInvalidAndLoggedOnce(t *testing.T) {
	t.Parallel()

	bed := newRig(t)
	put(t, bed.builtin, "oops", manifest(t, "oops", map[string]any{"modes": []any{}}), nil)

	library := bed.library()
	library.List()
	entries := library.List()

	entry := invalidOf(t, entries, "oops")
	if entry.Source != "builtin" {
		t.Errorf("source = %s", entry.Source)
	}

	matches(t, entry.Invalid, `modes`)

	lines := bed.logged.lines()
	if len(lines) != 1 {
		t.Fatalf("logged %q, want one line", lines)
	}

	matches(t, lines[0], `^built-in theme "oops" is invalid: modes`)
}

func TestALongFolderNameIsClippedInTheEntryAndTheReason(t *testing.T) {
	t.Parallel()

	bed := newRig(t)
	odd := "X" + strings.Repeat("a", 249)
	lower := strings.Repeat("b", 250)

	put(t, bed.user, odd, manifest(t, "mine", nil), nil)
	put(t, bed.user, lower, manifest(t, "mine", nil), nil)

	data := js.Stringify(bed.list())

	wantOdd := `{"id":"X` + strings.Repeat("a", 39) + `...","source":"user","hasCss":false,` +
		`"cssSha256":null,"invalid":"folder name must be a-z, 0-9 and -"}`
	if !strings.Contains(data, wantOdd) {
		t.Errorf("no %s in %s", wantOdd, data)
	}

	clipped := strings.Repeat("b", 40) + "..."
	wantLower := `{"id":"` + clipped + `","source":"user","hasCss":false,"cssSha256":null,` +
		`"invalid":"folder \"` + clipped + `\" does not match the manifest id \"mine\""}`

	if !strings.Contains(data, wantLower) {
		t.Errorf("no %s in %s", wantLower, data)
	}
}

func TestFolderProblemLooksAtTheFolderItself(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mkdir(t, filepath.Join(root, "real"))
	link(t, filepath.Join(root, "real"), filepath.Join(root, "link"))
	write(t, filepath.Join(root, "file"), "x")

	tests := []struct{ name, want string }{
		{name: "real", want: ""},
		{name: "link", want: "folder is a symbolic link, so it was not read"},
		{name: "file", want: "folder is not a directory"},
		{name: "gone", want: "folder could not be read"},
	}

	for _, tt := range tests {
		got := themes.FolderProblem(filepath.Join(root, tt.name))
		if got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestMissingFoldersListNothing(t *testing.T) {
	t.Parallel()

	library := themes.New(themes.Disk("/nonexistent/a"), themes.Disk("/nonexistent/b"),
		log.New(&lockedBuffer{}, "", 0))
	if len(library.List()) != 0 {
		t.Errorf("list = %s", ids(library.List()))
	}
}

func TestAPipeAtThemeJSONIsRefusedPromptly(t *testing.T) {
	t.Parallel()

	bed := newRig(t)
	mkdir(t, filepath.Join(bed.user, "pipe"))

	fifo := filepath.Join(bed.user, "pipe", "theme.json")

	err := exec.CommandContext(t.Context(), "mkfifo", fifo).Run()
	if err != nil {
		t.Skip("mkfifo is not available here")
	}

	began := time.Now()
	entry := invalidOf(t, bed.list(), "pipe")

	matches(t, entry.Invalid, `not a regular file`)

	if time.Since(began) > testtime.Scaled(2*time.Second) {
		t.Error("listing waited on the pipe")
	}
}

func TestTheOpenNeverWaitsOnAPipe(t *testing.T) {
	t.Parallel()

	fifo := filepath.Join(t.TempDir(), "swapped")

	err := exec.CommandContext(t.Context(), "mkfifo", fifo).Run()
	if err != nil {
		t.Skip("mkfifo is not available here")
	}

	done := make(chan themes.Why, 1)

	go func() {
		_, why := themes.ReadNoFollow(fifo, 65536)
		done <- why
	}()

	select {
	case why := <-done:
		if why != themes.NotFile {
			t.Errorf("why = %d, want NotFile", why)
		}
	case <-time.After(testtime.Scaled(4 * time.Second)):
		t.Fatal("the open waited on the pipe")
	}
}

func TestRouteOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path   string
		inside bool
		want   themes.Route
	}{
		{path: "/api/history", inside: false},
		{path: "/api/themesx", inside: false},
		{path: "/api/themes", inside: true, want: themes.Route{Kind: themes.RouteList}},
		{path: "/api/themes/", inside: true, want: themes.Route{Kind: themes.RouteList}},
		{
			path: "/api/themes/neon/theme.css", inside: true,
			want: themes.Route{Kind: themes.RouteCSS, ID: "neon"},
		},
		{
			path: "/api/themes/..%2f..%2fetc/theme.css", inside: true,
			want: themes.Route{Kind: themes.RouteCSS, ID: "../../etc"},
		},
		{path: "/api/themes/%E0%A4%A/theme.css", inside: true, want: themes.Route{}},
		{path: "/api/themes/neon", inside: true, want: themes.Route{}},
		{path: "/api/themes/neon/other.css", inside: true, want: themes.Route{}},
		{path: "/api/themes/a/b/theme.css", inside: true, want: themes.Route{}},
		{path: "/api/themes//theme.css", inside: true, want: themes.Route{}},
	}

	for _, tt := range tests {
		got, inside := themes.RouteOf(tt.path)
		if inside != tt.inside {
			t.Errorf("%s: inside = %v", tt.path, inside)
		}

		if got != tt.want {
			t.Errorf("%s: %+v, want %+v", tt.path, got, tt.want)
		}
	}
}

// brokenShelf is a user folder whose reads fail in a way nobody planned for.
type brokenShelf struct{ themes.Shelf }

func (brokenShelf) Read(string, string, int64) ([]byte, themes.Why) {
	panic("EACCES: permission denied, open '/Users/someone/.conn/themes/mine/theme.json'")
}

func TestAnUnexpectedFailureListsAFixedReasonAndLogsTheDetailOnce(t *testing.T) {
	t.Parallel()

	bed := newRig(t)
	put(t, bed.user, "mine", manifest(t, "mine", nil), nil)

	library := themes.New(themes.Disk(bed.builtin), brokenShelf{Shelf: themes.Disk(bed.user)},
		log.New(bed.logged, "", 0))
	library.List()

	entry := invalidOf(t, library.List(), "mine")
	if entry.Invalid != "unreadable theme folder" {
		t.Errorf("reason = %q", entry.Invalid)
	}

	lines := bed.logged.lines()
	if len(lines) != 1 {
		t.Fatalf("logged %q, want one line", lines)
	}

	matches(t, lines[0], `^user theme folder "mine" could not be read: .*/Users/someone`)
}
