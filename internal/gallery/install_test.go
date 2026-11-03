package gallery_test

import (
	"io"
	"log"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/theme"
	"github.com/claytoncasey01/conn/internal/themes"
)

func mkdir(t *testing.T, dir string) {
	t.Helper()

	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, name, text string) {
	t.Helper()

	err := os.WriteFile(name, []byte(text), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

// listed is how the theme list sees themeID after an install.
func listed(t *testing.T, env *world, themeID string) themes.Entry {
	t.Helper()

	library := themes.New(themes.Embedded(conn.Themes()), themes.Disk(env.user),
		log.New(io.Discard, "", 0))

	for _, entry := range library.List() {
		if entry.ID == themeID {
			return entry
		}
	}

	t.Fatalf("%s is not listed", themeID)

	return themes.Entry{}
}

func TestInstallWritesThemeJSONAndSourceJSONOnly(t *testing.T) {
	t.Parallel()

	commit := strings.Repeat("c", 40)
	text := themeText(t, "ember")
	env := newWorld(t, served(t, text, "commit", commit))
	env.host.files["ember"] = text

	result := env.install(t, "ember")
	expectCode(t, result, http.StatusOK)

	if body(result) != `{"ok":true,"id":"ember","version":"1.0.0"}` {
		t.Errorf("body = %s", body(result))
	}

	expectList(t, listing(t, env.user), "ember")
	expectList(t, listing(t, filepath.Join(env.user, "ember")), ".source.json", "theme.json")

	if read(t, filepath.Join(env.user, "ember", "theme.json")) != text {
		t.Error("theme.json is not the text served")
	}

	wantSource := "{\n  \"repo\": \"me/mine\",\n  \"commit\": \"" + commit + "\",\n" +
		"  \"installedAt\": 1234,\n  \"files\": {\n    \"theme.json\": \"" + sha(text) +
		"\"\n  }\n}\n"
	if read(t, filepath.Join(env.user, "ember", ".source.json")) != wantSource {
		t.Errorf(".source.json = %s", read(t, filepath.Join(env.user, "ember", ".source.json")))
	}

	// A fresh index first, then the theme at the pin.
	expectList(t, env.host.urls(), indexAt, raw+"/"+commit+"/ember/theme.json")

	entry := listed(t, env, "ember")
	if entry.Source != theme.SourceGallery {
		t.Errorf("listed as %s", entry.Source)
	}

	if entry.Invalid != "" {
		t.Errorf("listed invalid: %s", entry.Invalid)
	}
}

func TestAnInstalledThemeGetsTheModesAHandMadeOneGets(t *testing.T) {
	t.Parallel()

	text := themeText(t, "ember")
	env := newWorld(t, served(t, text))
	env.host.files["ember"] = text

	// What this process makes by hand in the same place, whatever the umask.
	hand := filepath.Join(env.user, "hand")

	err := os.Mkdir(hand, 0o777)
	if err != nil {
		t.Fatal(err)
	}

	handFile := filepath.Join(hand, "theme.json")

	err = os.WriteFile(handFile, []byte(text), 0o666)
	if err != nil {
		t.Fatal(err)
	}

	for _, when := range []string{"install", "update"} {
		expectCode(t, env.install(t, "ember"), http.StatusOK)

		dir := filepath.Join(env.user, "ember")
		if mode(t, dir) != mode(t, hand) {
			t.Errorf("%s: folder mode %o, want %o", when, mode(t, dir), mode(t, hand))
		}

		for _, file := range []string{"theme.json", ".source.json"} {
			if mode(t, filepath.Join(dir, file)) != mode(t, handFile) {
				t.Errorf("%s: %s mode %o, want %o", when, file, mode(t, filepath.Join(dir, file)),
					mode(t, handFile))
			}
		}
	}
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}

	return info.Mode().Perm()
}

func TestInstallMakesTheUserFolder(t *testing.T) {
	t.Parallel()

	text := themeText(t, "ember")
	env := newWorld(t, served(t, text))
	env.host.files["ember"] = text

	err := os.Remove(env.user)
	if err != nil {
		t.Fatal(err)
	}

	expectCode(t, env.install(t, "ember"), http.StatusOK)
	expectList(t, listing(t, filepath.Join(env.user, "ember")), ".source.json", "theme.json")
}

func TestRefusedInstallsWriteNothing(t *testing.T) {
	t.Parallel()

	good := themeText(t, "ember")
	bad := themeText(t, "badtheme", "modes", []any{"sepia"})
	other := themeText(t, "other")

	tests := []struct {
		name    string
		themeID any
		code    int
		entries func(t *testing.T) []any
		files   map[string]string
		want    string
	}{
		{
			name: "hash mismatch", themeID: "ember", code: 422,
			entries: func(t *testing.T) []any {
				t.Helper()

				return []any{entry(t, "ember", "sha256", sha("something else"))}
			},
			files: map[string]string{"ember": good},
			want: "ember's theme.json does not match the hash in the index, so it was " +
				"not installed",
		},
		{
			name: "fails the theme check", themeID: "badtheme", code: 422,
			entries: func(t *testing.T) []any {
				t.Helper()

				return []any{entry(t, "badtheme", "sha256", sha(bad))}
			},
			files: map[string]string{"badtheme": bad},
			want: `badtheme's theme.json is not a valid theme: modes must be ["light"], ` +
				`["dark"] or both; tokens.light is given but modes does not list light; ` +
				`tokens.dark is given but modes does not list dark`,
		},
		{
			name: "another theme's file", themeID: "ember", code: 422,
			entries: func(t *testing.T) []any {
				t.Helper()

				return []any{entry(t, "ember", "sha256", sha(other))}
			},
			files: map[string]string{"ember": other},
			want:  `ember's theme.json has the id "other", so it was not installed`,
		},
		{
			name: "not JSON", themeID: "ember", code: 422,
			entries: func(t *testing.T) []any {
				t.Helper()

				return []any{entry(t, "ember", "sha256", sha("<html>"))}
			},
			files: map[string]string{"ember": "<html>"},
			want:  "ember's theme.json is not a valid theme: theme.json is not valid JSON",
		},
		{
			name: "lists a stylesheet", themeID: "ember", code: 404,
			entries: func(t *testing.T) []any {
				t.Helper()

				return []any{served(t, good, "files", []any{"theme.json", "theme.css"})}
			},
			files: map[string]string{"ember": good}, want: "ember is not in the gallery",
		},
		{
			name: "not in the index", themeID: "nope", code: 404,
			entries: func(t *testing.T) []any {
				t.Helper()

				return []any{served(t, good)}
			},
			files: map[string]string{"ember": good}, want: "nope is not in the gallery",
		},
		{name: "a number", themeID: 7.0, code: 400, want: "That is not a theme id"},
		{name: "an object", themeID: js.NewObject(), code: 400, want: "That is not a theme id"},
		{name: "nothing", themeID: nil, code: 400, want: "That is not a theme id"},
		{name: "a path", themeID: "../x", code: 400, want: "That is not a theme id"},
		{name: "nested", themeID: "a/b", code: 400, want: "That is not a theme id"},
		{name: "empty", themeID: "", code: 400, want: "That is not a theme id"},
		{
			name: "a built-in", themeID: "crt", code: 400,
			want: "A built-in theme cannot be installed from the gallery",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newWorld(t)
			if tt.entries != nil {
				env.host.index = reply(http.StatusOK, indexText(tt.entries(t)...))
			}

			maps.Copy(env.host.files, tt.files)

			result := env.install(t, tt.themeID)
			expectCode(t, result, tt.code)

			if body(result) != js.Stringify(map[string]string{"error": tt.want}) {
				t.Errorf("body = %s, want error %q", body(result), tt.want)
			}

			expectList(t, listing(t, env.user))

			// A bad id never reaches the network.
			if tt.entries == nil {
				expectList(t, env.host.urls())
			}
		})
	}
}

func TestDownloadFailuresWriteNothing(t *testing.T) {
	t.Parallel()

	text := themeText(t, "ember")
	themeURL := raw + "/" + commitA + "/ember/theme.json"

	tests := []struct {
		name    string
		answer  answer
		timeout time.Duration
		want    string
	}{
		{
			name: "missing on the host", answer: reply(404, "Not Found"),
			want: "GitHub answered 404 for ember's theme.json",
		},
		{
			name: "offline", answer: offline,
			want: "Could not reach raw.githubusercontent.com to download the theme",
		},
		{
			name: "rate-limited", answer: reply(429, ""),
			want: "GitHub is limiting requests (429); try again in a few minutes",
		},
		{
			name: "slow", answer: hang, timeout: 20 * time.Millisecond,
			want: "The theme download timed out after 10 s",
		},
		{
			name: "a redirect", answer: redirect,
			want: "Could not reach raw.githubusercontent.com to download the theme",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newWorld(t, served(t, text))
			env.host.serve(themeURL, tt.answer)
			env.timeout = tt.timeout

			result := env.install(t, "ember")
			expectCode(t, result, http.StatusBadGateway)

			if body(result) != js.Stringify(map[string]string{"error": tt.want}) {
				t.Errorf("body = %s", body(result))
			}

			expectList(t, listing(t, env.user))
		})
	}
}

func TestAnIndexThatCannotBeReadIsPassedOn(t *testing.T) {
	t.Parallel()

	env := newWorld(t)
	env.host.index = reply(403, "")

	result := env.install(t, "ember")
	expectCode(t, result, http.StatusBadGateway)
	matches(t, body(result), `limiting requests \(403\)`)
}

func TestAThemeOver64KBIsRefusedWithoutReadingPastTheCap(t *testing.T) {
	t.Parallel()

	text := themeText(t, "ember")
	themeURL := raw + "/" + commitA + "/ember/theme.json"
	env := newWorld(t, served(t, text))

	body16 := &stream{guard: sync.Mutex{}, chunk: 16 * 1024}

	env.host.serve(themeURL, func(request *http.Request) (*http.Response, error) {
		return respond(request, http.StatusOK, body16, -1), nil
	})

	result := env.install(t, "ember")
	expectCode(t, result, http.StatusBadGateway)
	matches(t, body(result), `larger than 64 KB`)

	_, read, closed := body16.seen()
	if !closed {
		t.Error("the body was not closed")
	}

	if read > gallery64KB()+16*1024 {
		t.Errorf("read %d bytes", read)
	}

	expectList(t, listing(t, env.user))

	declared := &stream{guard: sync.Mutex{}, chunk: 10}

	env.host.serve(themeURL, func(request *http.Request) (*http.Response, error) {
		return respond(request, http.StatusOK, declared, int64(gallery64KB()+1)), nil
	})

	expectCode(t, env.install(t, "ember"), http.StatusBadGateway)

	reads, _, _ := declared.seen()
	if reads != 0 {
		t.Errorf("the declared-too-large body was read %d times", reads)
	}
}

func gallery64KB() int { return 64 * 1024 }

func TestAFolderOfYourOwnIsNeverReplaced(t *testing.T) {
	t.Parallel()

	text := themeText(t, "ember")
	env := newWorld(t, served(t, text))
	env.host.files["ember"] = text

	own := filepath.Join(env.user, "ember")
	mkdir(t, own)
	write(t, filepath.Join(own, "theme.json"), "mine")
	write(t, filepath.Join(own, "theme.css"), "body{}")

	result := env.install(t, "ember")
	expectCode(t, result, http.StatusConflict)

	if body(result) != `{"error":"ember is a folder of your own, so it was not replaced"}` {
		t.Errorf("body = %s", body(result))
	}

	// Refused before the download, not after it.
	expectList(t, env.host.urls(), indexAt)

	if read(t, filepath.Join(own, "theme.json")) != "mine" {
		t.Error("the user's theme.json changed")
	}

	expectList(t, listing(t, own), "theme.css", "theme.json")

	// A link in its place is the user's too.
	err := os.RemoveAll(own)
	if err != nil {
		t.Fatal(err)
	}

	away := filepath.Join(env.root, "away")
	mkdir(t, away)

	err = os.Symlink(away, own)
	if err != nil {
		t.Fatal(err)
	}

	expectCode(t, env.install(t, "ember"), http.StatusConflict)
	expectList(t, listing(t, away))
}

func TestAFolderThatBecameYoursDuringTheDownloadIsNotReplaced(t *testing.T) {
	t.Parallel()

	text1 := themeText(t, "ember")
	env := newWorld(t, served(t, text1))
	env.host.files["ember"] = text1
	expectCode(t, env.install(t, "ember"), http.StatusOK)

	text2 := themeText(t, "ember", "version", "2.0.0")
	env.host.put(text2, served(t, text2, "version", "2.0.0"))

	themeURL := raw + "/" + commitA + "/ember/theme.json"
	marker := filepath.Join(env.user, "ember", ".source.json")

	// The user removes the marker while theme.json is on its way.
	env.host.serve(themeURL, func(request *http.Request) (*http.Response, error) {
		_ = os.Remove(marker)

		return reply(http.StatusOK, text2)(request)
	})

	expectCode(t, env.install(t, "ember"), http.StatusConflict)
	expectList(t, listing(t, env.user), "ember")
	expectList(t, listing(t, filepath.Join(env.user, "ember")), "theme.json")

	if read(t, filepath.Join(env.user, "ember", "theme.json")) != text1 {
		t.Error("the first version was replaced")
	}
}

func TestUpdateReplacesThemeJSONAndRewritesSourceJSON(t *testing.T) {
	t.Parallel()

	text1 := themeText(t, "ember")
	env := newWorld(t, served(t, text1))
	env.host.files["ember"] = text1
	expectCode(t, env.install(t, "ember"), http.StatusOK)

	commitD := strings.Repeat("d", 40)
	text2 := themeText(t, "ember", "version", "2.0.0", "description", "newer")
	env.host.put(text2, served(t, text2, "commit", commitD, "version", "2.0.0"))
	env.now = 9999

	result := env.install(t, "ember")
	if body(result) != `{"ok":true,"id":"ember","version":"2.0.0"}` {
		t.Errorf("body = %s", body(result))
	}

	expectList(t, listing(t, env.user), "ember")
	expectList(t, listing(t, filepath.Join(env.user, "ember")), ".source.json", "theme.json")

	if read(t, filepath.Join(env.user, "ember", "theme.json")) != text2 {
		t.Error("theme.json is not the new version")
	}

	source := read(t, filepath.Join(env.user, "ember", ".source.json"))
	for _, want := range []string{commitD, `"installedAt": 9999`, sha(text2)} {
		if !strings.Contains(source, want) {
			t.Errorf(".source.json has no %s: %s", want, source)
		}
	}
}

func TestAFailedWriteOnAFreshInstallLeavesNothing(t *testing.T) {
	t.Parallel()

	for _, after := range []int{0, 1} {
		text := themeText(t, "ember")
		env := newWorld(t, served(t, text))
		env.host.files["ember"] = text
		env.files = failOn("WriteNew", after)

		result := env.install(t, "ember")
		expectCode(t, result, http.StatusInternalServerError)

		if body(result) != `{"error":"The theme could not be written, so nothing was changed"}` {
			t.Errorf("write #%d: body = %s", after, body(result))
		}

		expectList(t, listing(t, env.user))
	}
}

func TestAFailedUpdateKeepsThePreviousVersion(t *testing.T) {
	t.Parallel()

	steps := []struct {
		which string
		after int
	}{{"WriteNew", 0}, {"WriteNew", 1}, {"Rename", 0}, {"Rename", 1}}

	for _, step := range steps {
		text1 := themeText(t, "ember")
		env := newWorld(t, served(t, text1))
		env.host.files["ember"] = text1
		expectCode(t, env.install(t, "ember"), http.StatusOK)

		before := read(t, filepath.Join(env.user, "ember", ".source.json"))
		text2 := themeText(t, "ember", "version", "2.0.0")
		env.host.put(text2, served(t, text2, "version", "2.0.0"))
		env.files = failOn(step.which, step.after)

		label := step.which + " #" + strconv.Itoa(step.after)

		result := env.install(t, "ember")
		if result.Code != http.StatusInternalServerError {
			t.Errorf("%s: code = %d", label, result.Code)
		}

		expectList(t, listing(t, env.user), "ember")

		if read(t, filepath.Join(env.user, "ember", "theme.json")) != text1 {
			t.Errorf("%s: the previous theme.json is gone", label)
		}

		if read(t, filepath.Join(env.user, "ember", ".source.json")) != before {
			t.Errorf("%s: the previous .source.json is gone", label)
		}
	}
}

func TestAFailureToRemoveTheOldVersionStillReportsTheUpdate(t *testing.T) {
	t.Parallel()

	text1 := themeText(t, "ember")
	env := newWorld(t, served(t, text1))
	env.host.files["ember"] = text1
	expectCode(t, env.install(t, "ember"), http.StatusOK)

	text2 := themeText(t, "ember", "version", "2.0.0")
	env.host.put(text2, served(t, text2, "version", "2.0.0"))
	env.files = failOn("RemoveAll", 0)

	expectCode(t, env.install(t, "ember"), http.StatusOK)

	if read(t, filepath.Join(env.user, "ember", "theme.json")) != text2 {
		t.Error("the update did not land")
	}
}

func TestWhenTheOldVersionCannotBePutBackItIsKept(t *testing.T) {
	t.Parallel()

	text1 := themeText(t, "ember")
	env := newWorld(t, served(t, text1))
	env.host.files["ember"] = text1
	expectCode(t, env.install(t, "ember"), http.StatusOK)

	text2 := themeText(t, "ember", "version", "2.0.0")
	env.host.put(text2, served(t, text2, "version", "2.0.0"))

	// The first rename (old aside) works; the next two (new in, old back) fail.
	files := failOn("Rename", 1)
	files.onward = true
	env.files = files

	result := env.install(t, "ember")
	expectCode(t, result, http.StatusInternalServerError)

	left := listing(t, env.user)
	if len(left) != 1 {
		t.Fatalf("left %q", left)
	}

	matches(t, left[0], `^\.tmp-ember-[0-9a-f]{12}-old-[0-9a-f]{8}$`)

	want := "ember could not be updated, and its previous version could not be put back " +
		"(it is kept in " + left[0] + ")"
	if body(result) != js.Stringify(map[string]string{"error": want}) {
		t.Errorf("body = %s", body(result))
	}

	if read(t, filepath.Join(env.user, left[0], "theme.json")) != text1 {
		t.Error("the kept folder is not the old version")
	}
}

func TestALeftoverTempFolderIsNeverListed(t *testing.T) {
	t.Parallel()

	env := newWorld(t)
	mkdir(t, filepath.Join(env.user, ".tmp-ember-abc123"))
	write(t, filepath.Join(env.user, ".tmp-ember-abc123", "theme.json"), themeText(t, "ember"))

	library := themes.New(themes.Embedded(conn.Themes()), themes.Disk(env.user),
		log.New(io.Discard, "", 0))
	for _, entry := range library.List() {
		if entry.Source != theme.SourceBuiltin {
			t.Errorf("listed %s", entry.ID)
		}
	}
}

func TestTwoInstallsOfOneIDAtOnce(t *testing.T) {
	t.Parallel()

	text := themeText(t, "ember")
	env := newWorld(t, served(t, text))
	env.host.files["ember"] = text

	release := make(chan struct{})
	started := make(chan struct{})
	themeURL := raw + "/" + commitA + "/ember/theme.json"

	env.host.serve(themeURL, func(request *http.Request) (*http.Response, error) {
		close(started)
		<-release

		return reply(http.StatusOK, text)(request)
	})

	library := env.gallery()
	first := make(chan int, 1)

	go func() { first <- library.Install(t.Context(), "ember").Code }()

	<-started

	second := library.Install(t.Context(), "ember")
	if second.Code != http.StatusConflict {
		t.Errorf("second = %d", second.Code)
	}

	if body(second) != `{"error":"ember is already being installed"}` {
		t.Errorf("second = %s", body(second))
	}

	uninstall := library.Uninstall("ember")
	if body(uninstall) != `{"error":"ember is being installed, so it was not removed"}` {
		t.Errorf("uninstall = %s", body(uninstall))
	}

	close(release)

	if code := <-first; code != http.StatusOK {
		t.Errorf("first = %d", code)
	}

	expectList(t, listing(t, filepath.Join(env.user, "ember")), ".source.json", "theme.json")
	expectCode(t, library.Uninstall("ember"), http.StatusOK)
}

func TestUninstallRemovesAGalleryFolder(t *testing.T) {
	t.Parallel()

	text := themeText(t, "ember")
	env := newWorld(t, served(t, text))
	env.host.files["ember"] = text
	expectCode(t, env.install(t, "ember"), http.StatusOK)
	mkdir(t, filepath.Join(env.user, "keep"))

	result := env.gallery().Uninstall("ember")
	if body(result) != `{"ok":true,"id":"ember","version":""}` {
		t.Errorf("body = %s", body(result))
	}

	expectList(t, listing(t, env.user), "keep")
}

func TestUninstallRefusesWhatIsNotAGalleryInstall(t *testing.T) {
	t.Parallel()

	env := newWorld(t)
	own := filepath.Join(env.user, "mine")
	mkdir(t, own)
	write(t, filepath.Join(own, "theme.json"), "{}")

	realFolder := filepath.Join(env.root, "realFolder")
	mkdir(t, realFolder)
	write(t, filepath.Join(realFolder, ".source.json"), "{}")

	err := os.Symlink(realFolder, filepath.Join(env.user, "linked"))
	if err != nil {
		t.Fatal(err)
	}

	mkdir(t, filepath.Join(env.user, "odd", ".source.json"))
	write(t, filepath.Join(env.user, "file"), "x")

	library := env.gallery()
	for _, themeID := range []string{"mine", "linked", "odd", "file"} {
		result := library.Uninstall(themeID)
		want := `{"error":"` + themeID +
			` was not installed from the gallery, so it was not removed"}`

		if body(result) != want {
			t.Errorf("%s: %s", themeID, body(result))
		}
	}

	bad := []any{
		"../mine", "a/b", `a\b`, "Mine", "", ".", "..", "a.b", "%2e%2e", "a\x00b", 7.0, nil,
		js.NewObject(),
	}
	for _, themeID := range bad {
		expectCode(t, library.Uninstall(themeID), http.StatusBadRequest)
	}

	if body(library.Uninstall("nothing")) != `{"error":"nothing is not installed"}` {
		t.Errorf("absent: %s", body(library.Uninstall("nothing")))
	}

	expectList(t, listing(t, env.user), "file", "linked", "mine", "odd")
	expectList(t, listing(t, realFolder), ".source.json")
}

func TestAnIDNeverLeavesTheThemesFolder(t *testing.T) {
	t.Parallel()

	env := newWorld(t)
	outside := filepath.Join(env.root, "x")
	mkdir(t, outside)
	write(t, filepath.Join(outside, ".source.json"), "{}")
	write(t, filepath.Join(env.user, ".source.json"), "{}")

	for _, themeID := range []string{"../x", "..", ".", "../themes", "x/../../x", "./x"} {
		expectCode(t, env.gallery().Uninstall(themeID), http.StatusBadRequest)
	}

	expectList(t, listing(t, outside), ".source.json")
}

func TestAFailingRemoveOnUninstallIs500(t *testing.T) {
	t.Parallel()

	text := themeText(t, "ember")
	env := newWorld(t, served(t, text))
	env.host.files["ember"] = text
	expectCode(t, env.install(t, "ember"), http.StatusOK)

	env.files = failOn("RemoveAll", 0)

	result := env.gallery().Uninstall("ember")
	if body(result) != `{"error":"ember could not be removed"}` {
		t.Errorf("body = %s", body(result))
	}

	expectList(t, listing(t, filepath.Join(env.user, "ember")), ".source.json", "theme.json")
}

func TestAFolderThatCannotBeLookedAtIsLeftAlone(t *testing.T) {
	t.Parallel()

	for _, suffix := range []string{"/ember", "/ember/.source.json"} {
		text := themeText(t, "ember")
		env := newWorld(t, served(t, text))
		env.host.files["ember"] = text
		expectCode(t, env.install(t, "ember"), http.StatusOK)

		env.files = denying(suffix)
		want := `{"error":"ember's folder could not be read, so nothing was changed"}`

		if body(env.install(t, "ember")) != want {
			t.Errorf("%s: install = %s", suffix, body(env.install(t, "ember")))
		}

		if body(env.gallery().Uninstall("ember")) != want {
			t.Errorf("%s: uninstall = %s", suffix, body(env.gallery().Uninstall("ember")))
		}

		expectList(t, listing(t, filepath.Join(env.user, "ember")), ".source.json", "theme.json")
		expectList(t, listing(t, env.user), "ember")
	}
}

func TestTheIndexManifestMustBeTheFilesTheme(t *testing.T) {
	t.Parallel()

	text := themeText(t, "ember")
	shown := manifest(t, "ember", "name", "Paper", "description", "something else")
	env := newWorld(t, entry(t, "ember", "sha256", sha(text), "manifest", shown))
	env.host.files["ember"] = text

	result := env.install(t, "ember")
	expectCode(t, result, http.StatusUnprocessableEntity)
	matches(t, body(result), `does not match what the index showed`)
	expectList(t, listing(t, env.user))
}

func TestAManifestWithItsKeysInAnotherOrderIsTheSameTheme(t *testing.T) {
	t.Parallel()

	text := themeText(t, "ember")
	original := manifest(t, "ember")
	shuffled := js.NewObject()

	for _, key := range slices.Backward(original.Keys()) {
		value, _ := original.Get(key)
		shuffled.Set(key, value)
	}

	env := newWorld(t, entry(t, "ember", "sha256", sha(text), "manifest", shuffled))
	env.host.files["ember"] = text

	expectCode(t, env.install(t, "ember"), http.StatusOK)
}

func TestTheLimitOfUserThemes(t *testing.T) {
	t.Parallel()

	text := themeText(t, "ember")
	env := newWorld(t, served(t, text))
	env.host.files["ember"] = text

	for index := range 100 {
		mkdir(t, filepath.Join(env.user, "mine-"+leftPad(index)))
	}

	result := env.install(t, "ember")
	expectCode(t, result, http.StatusConflict)
	matches(t, body(result), `at most 100 themes of yours.*ember was not installed`)

	// Only the index was read: the refusal is before the download.
	if len(env.host.urls()) != 1 {
		t.Errorf("asked %q", env.host.urls())
	}

	err := os.Remove(filepath.Join(env.user, "mine-099"))
	if err != nil {
		t.Fatal(err)
	}

	expectCode(t, env.install(t, "ember"), http.StatusOK)

	// An update of an installed theme is not new, so it is let through at the limit.
	mkdir(t, filepath.Join(env.user, "zz-last"))
	expectCode(t, env.install(t, "ember"), http.StatusOK)
}

func leftPad(index int) string {
	text := strconv.Itoa(index)

	return strings.Repeat("0", 3-len(text)) + text
}
