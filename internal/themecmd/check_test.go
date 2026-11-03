package themecmd_test

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
)

func expectLines(t *testing.T, got []string, want ...string) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
}

func anyMatches(t *testing.T, got []string, pattern string) {
	t.Helper()

	compiled := regexp.MustCompile(pattern)
	if !slices.ContainsFunc(got, compiled.MatchString) {
		t.Errorf("no line matches %s in %q", pattern, got)
	}
}

func TestAGoodFolderPrintsOK(t *testing.T) {
	t.Parallel()

	box := newSandbox(t)
	got := box.check(folder(t, box.user, "mine", themeOf(t, "mine")))

	if got.code != 0 {
		t.Errorf("code = %d", got.code)
	}

	expectLines(t, got.all(), "mine: ok")
}

func TestRefusalsNameTheirField(t *testing.T) {
	t.Parallel()

	box := newSandbox(t)
	crt := parseFile(t, "../../themes/crt/theme.json")

	// The first accent variant's onac is its ac, which fails the gate.
	accent, _ := crt.Get("accent")
	accentObject, _ := accent.(*js.Object)
	variantsRaw, _ := accentObject.Get("variants")
	variants, _ := variantsRaw.([]any)
	first, _ := variants[0].(*js.Object)
	tokensRaw, _ := first.Get("tokens")
	firstTokens, _ := tokensRaw.(*js.Object)
	firstAc, _ := firstTokens.Get("ac")

	firstTokens.Set("onac", firstAc)

	firstName, _ := first.Get("name")

	crt.Set("id", "tint")
	crt.Set("name", "tint")

	variant := js.NewObject()
	odd := js.NewObject()
	odd.Set("ac", "url(x)")
	variant.Set("name", "Odd")
	variant.Set("tokens", odd)

	accentOdd := js.NewObject()
	accentOdd.Set("variants", []any{variant})

	tests := []struct {
		name     string
		manifest any
		pattern  string
	}{
		{
			name:     "flat",
			manifest: themeOf(t, "flat", "tokens", darkTokens(t, "tx", darkPanel(t))),
			pattern:  `^flat: dark: tx on panel is 1\.00:1`,
		},
		{
			name: "sneaky",
			manifest: themeOf(t, "sneaky", "tokens",
				darkTokens(t, "line", "url(https://x.test/a.png)")),
			pattern: `^sneaky: tokens\.dark\.line: contains "url\("$`,
		},
		{
			name: "tint", manifest: crt,
			pattern: `^tint: (light|dark) \(` + regexp.QuoteMeta(js.String(firstName)) + `\): onac`,
		},
		{
			name: "a1", manifest: themeOf(t, "a1", "accent", accentOdd),
			pattern: `^a1: accent variant "Odd" tokens\.ac: contains`,
		},
		{
			name: "a2", manifest: themeOf(t, "a2", "name", 5.0),
			pattern: `^a2: name: must be a string`,
		},
		{
			name: "a3", manifest: themeOf(t, "a3", "modes", []any{"neon"}),
			pattern: `^a3: modes: must be`,
		},
		{
			name: "one", manifest: themeOf(t, "two"),
			pattern: `^one: id: folder "one" does not match the manifest id "two"$`,
		},
		{
			name: "neon", manifest: themeOf(t, "neon"),
			pattern: `^neon: id: "neon" belongs to a built-in theme$`,
		},
		{
			name: "broken", manifest: "{ not json",
			pattern: `^broken: theme\.json: is not valid JSON$`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := box.check(folder(t, box.user, tt.name, tt.manifest))
			if got.code != 1 {
				t.Errorf("code = %d", got.code)
			}

			anyMatches(t, got.err, tt.pattern)

			if slices.Contains(got.out, tt.name+": ok") {
				t.Error("printed ok")
			}
		})
	}
}

func TestEveryContrastLineReadsAsItsMode(t *testing.T) {
	t.Parallel()

	box := newSandbox(t)
	flat := themeOf(t, "a4", "tokens", darkTokens(t, "tx", darkPanel(t)))
	got := box.check(folder(t, box.user, "a4", flat))

	for _, line := range got.err {
		if !regexp.MustCompile(`^a4: (dark|light): `).MatchString(line) {
			t.Errorf("line %q", line)
		}
	}
}

func TestOneLineFailures(t *testing.T) {
	t.Parallel()

	box := newSandbox(t)
	empty := folder(t, box.user, "empty", nil)
	broken := folder(t, box.user, "broken", "{ not json")

	linked := folder(t, box.user, "linked", nil)
	realJSON := filepath.Join(t.TempDir(), "real.json")
	writeText(t, realJSON, js.Stringify(themeOf(t, "linked")))
	symlink(t, realJSON, filepath.Join(linked, "theme.json"))

	huge := folder(t, box.user, "huge", nil)
	truncate(t, filepath.Join(huge, "theme.json"), 10*1024*1024)

	wide := folder(t, box.user, "wide", themeOf(t, "wide"))
	truncate(t, filepath.Join(wide, "theme.css"), 10*1024*1024)

	tests := []struct {
		path, want string
	}{
		{path: filepath.Join(box.user, "nope"), want: `^nope: folder: does not exist$`},
		{path: empty, want: `^empty: theme\.json: is missing$`},
		{path: broken, want: `^broken: theme\.json: is not valid JSON$`},
		{path: linked, want: `^linked: theme\.json: is not a regular file, so it was not read$`},
		{path: huge, want: `^huge: theme\.json: is larger than 64 KB$`},
		{path: wide, want: `^wide: theme\.css: is larger than 256 KB$`},
	}

	for _, tt := range tests {
		got := box.check(tt.path)
		if got.code != 1 {
			t.Errorf("%s: code = %d", tt.path, got.code)
		}

		if len(got.err) != 1 {
			t.Errorf("%s: lines %q", tt.path, got.err)

			continue
		}

		anyMatches(t, got.err, tt.want)
	}
}

func TestALinkedThemeFolderIsRefusedByPathAndByID(t *testing.T) {
	t.Parallel()

	box := newSandbox(t)
	realFolder := folder(t, t.TempDir(), "real", themeOf(t, "hop"))
	symlink(t, realFolder, filepath.Join(box.user, "hop"))

	hop := filepath.Join(box.user, "hop")
	for _, arg := range []string{hop, hop + "/", "hop"} {
		got := box.check(arg)
		if got.code != 1 {
			t.Errorf("%s: code = %d", arg, got.code)
		}

		anyMatches(t, got.err, `symbolic link`)
	}
}

func TestStylesheetsAndSourceMarkers(t *testing.T) {
	t.Parallel()

	box := newSandbox(t)

	gal := folder(t, box.user, "gal", themeOf(t, "gal"))
	writeText(t, filepath.Join(gal, "theme.css"), ".x { color: red }")
	writeText(t, filepath.Join(gal, ".source.json"), "{}")

	got := box.check(gal)
	if got.code != 1 {
		t.Errorf("css beside .source.json: code = %d", got.code)
	}

	anyMatches(t, got.err, `^gal: theme\.css: a gallery theme cannot carry CSS$`)

	marked := folder(t, box.user, "marked", themeOf(t, "marked"))
	writeText(t, filepath.Join(marked, ".source.json"), "{}")
	expectLines(t, box.check(marked).all(), "marked: ok")

	styled := folder(t, box.user, "styled", themeOf(t, "styled"))
	writeText(t, filepath.Join(styled, "theme.css"), ".x { color: red }")
	writeText(t, filepath.Join(styled, "notes.txt"), "x")

	got = box.check(styled)
	if got.code != 0 {
		t.Errorf("styled: code = %d", got.code)
	}

	expectLines(t, got.out, "styled: ok",
		"styled: note: theme.css: the app asks for confirmation before applying it")
	expectLines(t, got.err)
}

func TestUsage(t *testing.T) {
	t.Parallel()

	box := newSandbox(t)

	got := box.check("--waive-contrast")
	if got.code != 2 {
		t.Errorf("unknown flag: code = %d", got.code)
	}

	expectLines(t, got.out)
	anyMatches(t, got.err, `^unknown option "--waive-contrast"$`)

	if box.check("default", "-x").code != 2 {
		t.Error("-x is not bad usage")
	}

	got = box.check("--gallery")
	if got.code != 2 {
		t.Errorf("--gallery alone: code = %d", got.code)
	}

	anyMatches(t, got.err, `^--gallery needs a theme id or folder to check$`)

	help := box.check("--help")
	if help.code != 0 {
		t.Errorf("--help: code = %d", help.code)
	}

	if !strings.Contains(strings.Join(help.out, "\n"), "--gallery") {
		t.Errorf("--help = %q", help.out)
	}
}

func TestNoArgumentChecksEveryBuiltin(t *testing.T) {
	t.Parallel()

	box := newSandbox(t)

	got := box.check()
	if got.code != 0 {
		t.Errorf("code = %d: %q", got.code, got.all())
	}

	expectLines(t, got.out, "crt: ok", "default: ok", "matrix: ok", "neon: ok")

	writeText(t, filepath.Join(box.builtin, "neon", "theme.json"), "{")

	got = box.check()
	if got.code != 1 {
		t.Errorf("a broken built-in: code = %d", got.code)
	}

	anyMatches(t, got.err, `^neon: `)

	if !slices.Contains(got.out, "default: ok") {
		t.Errorf("the others were not checked: %q", got.out)
	}
}

func TestAnIDIsLookedUpInTheBuiltinsThenTheUserFolder(t *testing.T) {
	t.Parallel()

	box := newSandbox(t)
	folder(t, box.user, "mine", themeOf(t, "mine"))

	expectLines(t, box.check("neon").all(), "neon: ok")
	expectLines(t, box.check("mine").all(), "mine: ok")

	absent := box.check("absent")
	if absent.code != 1 {
		t.Errorf("absent: code = %d", absent.code)
	}

	anyMatches(t, absent.err, `^absent: folder: no built-in or user theme has that name$`)

	if box.check("Not An Id").code != 1 {
		t.Error("a name that is not an id passed")
	}

	several := box.check("default", "absent", "crt")
	if several.code != 1 {
		t.Errorf("several: code = %d", several.code)
	}

	expectLines(t, several.out, "default: ok", "crt: ok")

	if len(several.err) != 1 {
		t.Errorf("several: %q", several.err)
	}
}

func TestAFolderNameWithANewlineCannotForgeALine(t *testing.T) {
	t.Parallel()

	box := newSandbox(t)
	got := box.check(filepath.Join(box.user, "a\nb: ok"))

	if got.code != 1 {
		t.Errorf("code = %d", got.code)
	}

	expectLines(t, got.err, `"a\nb: ok": folder: does not exist`)
}

func TestGalleryMode(t *testing.T) {
	t.Parallel()

	box := newSandbox(t)

	mine := folder(t, box.user, "mine", themeOf(t, "mine"))
	expectLines(t, box.check("--gallery", mine).all(), "mine: ok")
	expectLines(t, box.check("--gallery", "mine").all(), "mine: ok")
	expectLines(t, box.check("mine", "--gallery").all(), "mine: ok")

	styled := folder(t, box.user, "styled", themeOf(t, "styled"))
	writeText(t, filepath.Join(styled, "theme.css"), ".x { color: red }")

	got := box.check("--gallery", styled)
	expectLines(t, got.err,
		"styled: theme.css: is not allowed (a gallery theme is theme.json alone)")
	expectLines(t, got.out)

	marked := folder(t, box.user, "marked", themeOf(t, "marked"))
	writeText(t, filepath.Join(marked, ".source.json"), "{}")
	anyMatches(t, box.check("--gallery", marked).err, `^marked: \.source\.json is not allowed`)

	litter := folder(t, box.user, "litter", themeOf(t, "litter"))
	writeText(t, filepath.Join(litter, ".DS_Store"), "")
	anyMatches(t, box.check("--gallery", litter).err, `^litter: \.DS_Store is not allowed`)

	anyMatches(t, box.check("--gallery", folder(t, box.user, "neon", themeOf(t, "neon"))).err,
		`^neon: .*built-in`)

	if box.check("--gallery", "neon").code != 1 {
		t.Error("--gallery passed a built-in id")
	}

	anyMatches(t, box.check("--gallery", "absent").err, `^absent: `)

	bad := folder(t, box.user, "Bad_Name", themeOf(t, "bad-name"))
	anyMatches(t, box.check("--gallery", bad).err, `folder: name must be 1 to 40 characters`)

	symlink(t, mine, filepath.Join(box.user, "link"))
	anyMatches(t, box.check("--gallery", filepath.Join(box.user, "link")).err,
		`^link: folder: is a symbolic link`)
}
