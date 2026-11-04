package themecmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/themecmd"
	"github.com/claytoncasey01/conn/internal/themes"
)

const (
	commitA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	repo    = "someone/conn-themes"
)

// sandbox is a built-in folder (a copy of the repo's themes, theme files only) and an empty user
// folder: nothing here is ~/.config/conn.
type sandbox struct {
	builtin, user string
}

func newSandbox(t *testing.T) sandbox {
	t.Helper()

	root := t.TempDir()
	box := sandbox{builtin: filepath.Join(root, "builtin"), user: filepath.Join(root, "user")}

	entries, err := os.ReadDir("../../themes")
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		for _, name := range []string{"theme.json", "theme.css"} {
			data, err := os.ReadFile(filepath.Join("../../themes", entry.Name(), name))
			if err != nil {
				continue
			}

			writeBytes(t, filepath.Join(box.builtin, entry.Name(), name), data)
		}
	}

	mkdir(t, box.user)

	return box
}

func (s sandbox) io(out, errs *bytes.Buffer) themecmd.IO {
	return themecmd.IO{
		Out: out, Err: errs, Builtin: themes.Disk(s.builtin), BuiltinDir: s.builtin,
		User: s.user, Write: nil,
	}
}

// ran is a command's exit code and its lines.
type ran struct {
	code     int
	out, err []string
}

func (r ran) all() []string { return append(append([]string{}, r.out...), r.err...) }

func lines(buffer *bytes.Buffer) []string {
	text := strings.TrimSuffix(buffer.String(), "\n")
	if text == "" {
		return []string{}
	}

	return strings.Split(text, "\n")
}

func (s sandbox) check(args ...string) ran {
	var out, errs bytes.Buffer

	code := themecmd.Check(args, s.io(&out, &errs))

	return ran{code: code, out: lines(&out), err: lines(&errs)}
}

func mkdir(t *testing.T, dir string) {
	t.Helper()

	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		t.Fatal(err)
	}
}

func writeBytes(t *testing.T, name string, data []byte) {
	t.Helper()
	mkdir(t, filepath.Dir(name))

	err := os.WriteFile(name, data, 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

func writeText(t *testing.T, name, text string) { t.Helper(); writeBytes(t, name, []byte(text)) }

func symlink(t *testing.T, target, name string) {
	t.Helper()

	err := os.Symlink(target, name)
	if err != nil {
		t.Fatal(err)
	}
}

// truncate makes a sparse file of size bytes: reading it whole would be that many zeros.
func truncate(t *testing.T, name string, size int64) {
	t.Helper()
	writeText(t, name, "")

	err := os.Truncate(name, size)
	if err != nil {
		t.Fatal(err)
	}
}

// defaultTheme is the repo's Default manifest.
func defaultTheme(t *testing.T) *js.Object {
	t.Helper()

	return parseFile(t, "../../themes/default/theme.json")
}

func parseFile(t *testing.T, name string) *js.Object {
	t.Helper()

	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := js.Parse(data)
	if err != nil {
		t.Fatal(err)
	}

	object, _ := parsed.(*js.Object)

	return object
}

// themeOf is Default with another id and name, and any fields replaced.
func themeOf(t *testing.T, themeID string, fields ...any) *js.Object {
	t.Helper()

	object := defaultTheme(t)
	object.Set("id", themeID)
	object.Set("name", themeID)

	for index := 0; index+1 < len(fields); index += 2 {
		key, _ := fields[index].(string)
		object.Set(key, fields[index+1])
	}

	return object
}

// darkTokens is Default's tokens with the dark mode's changed by pairs of slot and value.
func darkTokens(t *testing.T, pairs ...string) *js.Object {
	t.Helper()

	tokens, _ := defaultTheme(t).Get("tokens")
	byMode, _ := tokens.(*js.Object)
	darkRaw, _ := byMode.Get("dark")
	dark, _ := darkRaw.(*js.Object)

	for index := 0; index+1 < len(pairs); index += 2 {
		dark.Set(pairs[index], pairs[index+1])
	}

	return byMode
}

// darkPanel is Default's dark panel.
func darkPanel(t *testing.T) string {
	t.Helper()

	tokens, _ := defaultTheme(t).Get("tokens")
	byMode, _ := tokens.(*js.Object)
	darkRaw, _ := byMode.Get("dark")
	dark, _ := darkRaw.(*js.Object)
	panel, _ := dark.Get("panel")
	text, _ := panel.(string)

	return text
}

// folder writes parent/name/theme.json from manifest (a *js.Object, or text written as it is)
// and returns the folder; a nil manifest writes no theme.json.
func folder(t *testing.T, parent, name string, manifest any) string {
	t.Helper()

	dir := filepath.Join(parent, name)
	mkdir(t, dir)

	switch typed := manifest.(type) {
	case nil:
	case string:
		writeText(t, filepath.Join(dir, "theme.json"), typed)
	case []byte:
		writeBytes(t, filepath.Join(dir, "theme.json"), typed)
	default:
		writeText(t, filepath.Join(dir, "theme.json"), js.StringifyIndent(typed, "  "))
	}

	return dir
}
