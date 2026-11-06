package server_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/claytoncasey01/conn"
	"github.com/claytoncasey01/conn/internal/gallery"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/server"
	"github.com/claytoncasey01/conn/internal/themes"
)

// themeRig is a server over the built-in themes and a user theme folder in a temp data folder,
// with the gallery off (CONN_THEME_INDEX=off), so nothing can reach the network.
func themeRig(t *testing.T) *rig {
	t.Helper()

	data := t.TempDir()
	user := filepath.Join(data, "themes")
	builtin := themes.Embedded(conn.Themes())
	quiet := log.New(io.Discard, "", 0)
	now := func() int64 { return 0 }

	library := gallery.New(gallery.Deps{
		Transport: nil, Now: now, Builtin: builtin, User: user, Repo: "", Timeout: 0, Files: nil,
	})

	return &rig{
		handler: &server.Server{
			UI: bundle(), Secret: testKey(t), Clients: nil, Settings: nil, Logger: quiet,
			Themes:  themes.New(builtin, themes.Disk(user), quiet),
			Gallery: gallery.NewAPI(library, "", now),
		},
		data: data,
	}
}

// fixture is a synthetic theme that passes the gate against Default's missing slots; change
// edits it before it is written.
func fixture(themeID string, change func(light *js.Object)) string {
	light := js.NewObject()
	for _, pair := range [][2]string{
		{"bg", "#f6f3ee"},
		{"panel", "#fffdf9"},
		{"raise", "#ffffff"},
		{"tx", "#2a2622"},
		{"mu", "#6b6258"},
		{"line", "#e4ddd2"},
		{"line2", "#b8ad9c"},
		{"radius", "10px"},
	} {
		light.Set(pair[0], pair[1])
	}

	dark := js.NewObject()
	for _, pair := range [][2]string{
		{"bg", "#16140f"},
		{"panel", "#1e1b15"},
		{"raise", "#2a261e"},
		{"tx", "#efe9dd"},
		{"mu", "#a89f90"},
		{"line", "#2f2a21"},
		{"line2", "#5a5244"},
		{"radius", "10px"},
	} {
		dark.Set(pair[0], pair[1])
	}

	if change != nil {
		change(light)
	}

	tokens := js.NewObject()
	tokens.Set("light", light)
	tokens.Set("dark", dark)

	object := js.NewObject()
	object.Set("id", themeID)
	object.Set("name", "Theme "+themeID)
	object.Set("version", "1.0.0")
	object.Set("author", "test")
	object.Set("license", "MIT")
	object.Set("description", "a fixture")
	object.Set("modes", []any{"light", "dark"})
	object.Set("accent", "free")
	object.Set("fonts", []any{})
	object.Set("tokens", tokens)
	object.Set("effects", []any{})

	return js.Stringify(object)
}

// putTheme writes a folder under the user themes: theme.json (unless json is "") and any files.
func putTheme(t *testing.T, data, folder, json string, files map[string]string) string {
	t.Helper()

	dir := filepath.Join(data, "themes", folder)

	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	if json != "" {
		files = mergeFile(files, "theme.json", json)
	}

	for name, text := range files {
		err = os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

func mergeFile(files map[string]string, name, text string) map[string]string {
	merged := map[string]string{name: text}
	maps.Copy(merged, files)

	return merged
}

func symlink(t *testing.T, target, name string) {
	t.Helper()

	err := os.Symlink(target, name)
	if err != nil {
		t.Fatal(err)
	}
}

type listed struct {
	ID        string          `json:"id"`
	Source    string          `json:"source"`
	HasCSS    bool            `json:"hasCss"`
	CSSSha256 *string         `json:"cssSha256"`
	Manifest  json.RawMessage `json:"manifest"`
	Invalid   *string         `json:"invalid"`
}

func (r *rig) themes(t *testing.T) []listed {
	t.Helper()

	got := r.send(t, request{method: "GET", target: "/api/themes", headers: map[string]string{}})
	if got.status != 200 {
		t.Fatalf("GET /api/themes = %d %s", got.status, got.body)
	}

	var entries []listed

	err := json.Unmarshal([]byte(got.body), &entries)
	if err != nil {
		t.Fatal(err)
	}

	return entries
}

func byID(t *testing.T, entries []listed, themeID string) listed {
	t.Helper()

	for _, entry := range entries {
		if entry.ID == themeID {
			if entry.Source != "builtin" {
				return entry
			}

			if themeID == "default" {
				return entry
			}
		}
	}

	t.Fatalf("%s is not listed", themeID)

	return listed{}
}

func invalid(entry listed) string {
	if entry.Invalid == nil {
		return ""
	}

	return *entry.Invalid
}

// statusBody is an answer's status and body, to compare as one.
func statusBody(got answer) string { return strconv.Itoa(got.status) + " " + got.body }

func hash(text string) string {
	sum := sha256.Sum256([]byte(text))

	return hex.EncodeToString(sum[:])
}

func (r *rig) get(t *testing.T, target string) answer {
	t.Helper()

	return r.send(t, request{method: "GET", target: target, headers: map[string]string{}})
}

func notFoundJSON(t *testing.T, got answer) {
	t.Helper()

	if got.status != 404 {
		t.Errorf("status = %d, want 404", got.status)
	}

	if !strings.HasPrefix(got.header.Get("Content-Type"), "application/json") {
		t.Errorf("content type = %s", got.header.Get("Content-Type"))
	}

	if got.body != `{"error":"not found"}` {
		t.Errorf("body = %s", got.body)
	}
}

// seedUserThemes writes one user theme folder for each case the list judges.
func seedUserThemes(t *testing.T, data, css string) {
	t.Helper()

	putTheme(t, data, "good", fixture("good", nil), nil)
	putTheme(t, data, "badvalue", fixture("badvalue", func(light *js.Object) {
		light.Set("radius", "huge")
	}), nil)
	putTheme(t, data, "bigjson", fixture("bigjson", nil)+strings.Repeat(" ", 64*1024), nil)
	putTheme(t, data, "bigcss", fixture("bigcss", nil),
		map[string]string{"theme.css": "/*" + strings.Repeat("a", 256*1024) + "*/"})
	putTheme(t, data, "neon", fixture("neon", nil), nil)
	// tx equals panel, so the pair is 1:1.
	putTheme(t, data, "lowcontrast", fixture("lowcontrast", func(light *js.Object) {
		light.Set("tx", "#fffdf9")
	}), nil)
	putTheme(t, data, "withcss", fixture("withcss", nil), map[string]string{"theme.css": css})
	putTheme(t, data, "gallery", fixture("gallery", nil),
		map[string]string{".source.json": "{}", "theme.css": css})

	target := putTheme(t, data, "_target", fixture("linked", nil), nil)
	symlink(t, target, filepath.Join(data, "themes", "linked"))

	realTheme := putTheme(t, data, "_real", fixture("swap", nil), nil)
	putTheme(t, data, "swap", "", nil)
	symlink(t, filepath.Join(realTheme, "theme.json"),
		filepath.Join(data, "themes", "swap", "theme.json"))
}

func TestEveryUserThemeCaseThroughTheRoute(t *testing.T) {
	t.Parallel()

	held := themeRig(t)
	css := "body { --x: 1 }\n"
	seedUserThemes(t, held.data, css)

	all := held.themes(t)

	good := byID(t, all, "good")
	summary := fmt.Sprintf("%s %q css=%v manifest=%v", good.Source, invalid(good), good.HasCSS,
		len(good.Manifest) > 0)

	if summary != `user "" css=false manifest=true` {
		t.Errorf("good = %s", summary)
	}

	checks := map[string]string{
		"badvalue":    `tokens\.light\.radius is not a number with px, rem or em`,
		"bigjson":     `^theme\.json is larger than 64 KB$`,
		"bigcss":      `^theme\.css is larger than 256 KB$`,
		"neon":        `id "neon" is a built-in theme, a user folder cannot reuse it`,
		"lowcontrast": `light: tx on panel is 1\.00:1, needs at least 4\.5:1`,
		"linked":      `^folder is a symbolic link, so it was not read$`,
		"swap":        `^theme\.json is not a regular file, so it was not read$`,
	}
	for themeID, pattern := range checks {
		reason := invalid(byID(t, all, themeID))
		if !regexp.MustCompile(pattern).MatchString(reason) {
			t.Errorf("%s: %q does not match %s", themeID, reason, pattern)
		}
	}

	checkStylesheets(t, held, all, css)

	// Built-ins first, then the user's; both neons are listed, the built-in first.
	sources := make([]string, 0, len(all))
	for _, entry := range all {
		sources = append(sources, entry.Source+":"+entry.ID)
	}

	joined := strings.Join(sources, " ")
	if !strings.HasPrefix(joined, "builtin:crt builtin:default builtin:matrix builtin:neon user:") {
		t.Errorf("order = %s", joined)
	}
}

// checkStylesheets: a stylesheet is served only against the hash the list showed, and never
// from a gallery folder or a link.
func checkStylesheets(t *testing.T, held *rig, all []listed, css string) {
	t.Helper()

	withcss := byID(t, all, "withcss")
	if withcss.CSSSha256 == nil {
		t.Fatalf("withcss = %+v", withcss)
	}

	if fmt.Sprintf("%v %s", withcss.HasCSS, *withcss.CSSSha256) != "true "+hash(css) {
		t.Errorf("withcss = %+v", withcss)
	}

	notFoundJSON(t, held.get(t, "/api/themes/withcss/theme.css"))
	notFoundJSON(t, held.get(t, "/api/themes/withcss/theme.css?v="+strings.Repeat("0", 64)))

	served := held.get(t, "/api/themes/withcss/theme.css?v="+hash(css))
	if statusBody(served) != "200 "+css {
		t.Errorf("served = %s", statusBody(served))
	}

	headers := map[string]string{
		"Content-Type": "text/css; charset=utf-8", "Cache-Control": "no-store",
		"X-Content-Type-Options": "nosniff",
	}
	for name, want := range headers {
		if served.header.Get(name) != want {
			t.Errorf("%s = %q, want %q", name, served.header.Get(name), want)
		}
	}

	checkGalleryEntry(t, byID(t, all, "gallery"))
	notFoundJSON(t, held.get(t, "/api/themes/gallery/theme.css?v="+hash(css)))
	notFoundJSON(t, held.get(t, "/api/themes/linked/theme.css?v="+hash(css)))
}

func checkGalleryEntry(t *testing.T, entry listed) {
	t.Helper()

	summary := fmt.Sprintf("%s css=%v sha=%v", entry.Source, entry.HasCSS, entry.CSSSha256 != nil)
	if summary != "gallery css=false sha=false" {
		t.Errorf("gallery = %s", summary)
	}
}

func TestABadThemeIDIsAJSONNotFoundAndADotPathNeverServesATheme(t *testing.T) {
	t.Parallel()

	held := themeRig(t)
	outside := filepath.Join(held.data, "x")

	err := os.MkdirAll(outside, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	stray := map[string]string{"theme.json": fixture("x", nil), "theme.css": "body{}"}
	for name, text := range stray {
		err = os.WriteFile(filepath.Join(outside, name), []byte(text), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	version := "?v=" + hash("body{}")

	for _, themeID := range []string{"..%2fx", "%2e%2e%2fx", "a%2fb", "..%2f", "A", "a.b", "%00"} {
		notFoundJSON(t, held.get(t, "/api/themes/"+themeID+"/theme.css"+version))
	}

	for _, path := range []string{
		"/api/themes/../x/theme.css", "/api/themes/%2e%2e/theme.css", "/api/themes/../theme.css",
	} {
		got := held.get(t, path+version)
		if strings.Contains(got.header.Get("Content-Type"), "text/css") {
			t.Errorf("%s served css", path)
		}

		if strings.Contains(got.body, "body{}") {
			t.Errorf("%s served the stray theme", path)
		}
	}
}

func TestA101stUserThemeIsNotListedAndTheNoteComesLast(t *testing.T) {
	t.Parallel()

	held := themeRig(t)

	for index := range 101 {
		themeID := "t" + strings.Repeat("0", 3-len(strconv.Itoa(index))) + strconv.Itoa(index)
		putTheme(t, held.data, themeID, fixture(themeID, nil), nil)
	}

	all := held.themes(t)
	last := all[len(all)-1]

	if last.ID != "_limit" {
		t.Fatalf("last = %s", last.ID)
	}

	if invalid(last) != "limit of 100 user themes reached, 1 folder not listed" {
		t.Errorf("note = %q", invalid(last))
	}

	if all[len(all)-2].ID != "t099" {
		t.Errorf("the last theme is %s", all[len(all)-2].ID)
	}

	notFoundJSON(t, held.get(t, "/api/themes/t100/theme.css?v=x"))
}

func TestTheThemeRoutesTakeGETOnly(t *testing.T) {
	t.Parallel()

	held := themeRig(t)

	for _, target := range []string{"/api/themes", "/api/themes/neon/theme.css", "/api/themes/x"} {
		for _, method := range []string{"POST", "PUT", "DELETE", "HEAD"} {
			got := held.send(t, request{method: method, target: target, headers: own()})
			if got.status != 405 {
				t.Errorf("%s %s = %d", method, target, got.status)
			}
		}
	}
}

func post(target, body string, headers map[string]string) request {
	return request{method: "POST", target: target, body: body, headers: headers}
}

func xConn() map[string]string { return map[string]string{"X-Conn": "1"} }

func TestTheGalleryWhileItIsOff(t *testing.T) {
	t.Parallel()

	held := themeRig(t)

	index := held.get(t, "/api/gallery")
	if statusBody(index) != `200 {"enabled":false,"repo":null,"themes":[]}` {
		t.Errorf("index = %s", statusBody(index))
	}

	for _, path := range []string{"/api/gallery/install", "/api/gallery/uninstall"} {
		bare := held.send(t, post(path, `{"id":"x"}`, map[string]string{}))
		textPlain := map[string]string{"Content-Type": "text/plain"}
		plain := held.send(t, post(path, `{"id":"x"}`, textPlain))

		for _, refused := range []answer{bare, plain} {
			if refused.status != 403 {
				t.Errorf("%s without x-conn = %d", path, refused.status)
			}
		}

		notFoundJSON(t, held.send(t, post(path, `{"id":"x"}`, xConn())))
	}
}

func TestEachGalleryRouteHasOneMethod(t *testing.T) {
	t.Parallel()

	held := themeRig(t)
	sent := []request{
		{method: "GET", target: "/api/gallery/install", headers: map[string]string{}},
		{method: "GET", target: "/api/gallery/uninstall", headers: xConn()},
		post("/api/gallery", "{}", xConn()),
		{method: "DELETE", target: "/api/gallery", headers: xConn()},
	}

	for _, each := range sent {
		if got := held.send(t, each); got.status != 405 {
			t.Errorf("%s %s = %d", each.method, each.target, got.status)
		}
	}
}

func TestAGalleryBodyOver64KBIs413AndOneAtTheLimitIsRead(t *testing.T) {
	t.Parallel()

	held := themeRig(t)
	big := `{"id":"x","pad":"` + strings.Repeat("a", 65536) + `"}`

	got := held.send(t, post("/api/gallery/install", big, xConn()))
	if statusBody(got) != `413 {"error":"the request body is too large"}` {
		t.Errorf("big = %s", statusBody(got))
	}

	exact := `{"id":"x","pad":"` + strings.Repeat("a", 65536-len(`{"id":"x","pad":""}`)) + `"}`
	notFoundJSON(t, held.send(t, post("/api/gallery/install", exact, xConn())))
}

func TestAGalleryBodyThatIsNotAnObjectIs400(t *testing.T) {
	t.Parallel()

	held := themeRig(t)

	want := `{"error":"the request body must be a JSON object"}`

	for _, path := range []string{"/api/gallery/install", "/api/gallery/uninstall"} {
		for _, body := range []string{"{", "nope", "[1]", `"x"`, "7", "null"} {
			got := held.send(t, post(path, body, xConn()))
			if statusBody(got) != "400 "+want {
				t.Errorf("%s %s = %s", path, body, statusBody(got))
			}
		}
	}
}

func TestAnUnknownGalleryPathIsAJSONNotFound(t *testing.T) {
	t.Parallel()

	held := themeRig(t)

	for _, path := range []string{
		"/api/gallery/", "/api/gallery/other", "/api/gallery/install/x", "/api/gallery/%69nstall",
	} {
		notFoundJSON(t, held.get(t, path))
		notFoundJSON(t, held.send(t, post(path, "{}", xConn())))
	}
}

func TestAForeignOriginOrHostIsRefusedOnTheGallery(t *testing.T) {
	t.Parallel()

	held := themeRig(t)
	evil := map[string]string{"Origin": "http://evil.example"}

	got := held.send(t, request{method: "GET", target: "/api/gallery", headers: evil})
	if got.status != 403 {
		t.Errorf("GET with a foreign origin = %d", got.status)
	}

	for _, path := range []string{"/api/gallery/install", "/api/gallery/uninstall"} {
		withOrigin := map[string]string{"X-Conn": "1", "Origin": "http://evil.example"}
		withHost := map[string]string{"X-Conn": "1", "Host": "evil.example"}

		for _, headers := range []map[string]string{withOrigin, withHost} {
			if got := held.send(t, post(path, `{"id":"x"}`, headers)); got.status != 403 {
				t.Errorf("%s = %d", path, got.status)
			}
		}
	}

	if got := held.send(t, request{
		method: "GET", target: "/api/gallery", headers: map[string]string{"Host": "evil.example"},
	}); got.status != 403 {
		t.Errorf("GET with a foreign host = %d", got.status)
	}

	if got := held.send(t, request{
		method: "GET", target: "/api/gallery", headers: map[string]string{"Host": "localhost"},
	}); got.status != 200 {
		t.Errorf("GET with Host localhost = %d", got.status)
	}
}
