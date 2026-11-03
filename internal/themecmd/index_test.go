package themecmd_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/claytoncasey01/conn/internal/gallery"
	"github.com/claytoncasey01/conn/internal/js"
	"github.com/claytoncasey01/conn/internal/themecmd"
	"github.com/claytoncasey01/conn/internal/themes"
)

func indexIO(out, errs *bytes.Buffer, write func(string, []byte) error) themecmd.IO {
	return themecmd.IO{
		Out: out, Err: errs, Builtin: themes.Disk("../../themes"), BuiltinDir: "",
		User: "", Write: write,
	}
}

func runIndex(args []string, write func(string, []byte) error) ran {
	var out, errs bytes.Buffer

	code := themecmd.Index(args, indexIO(&out, &errs, write))

	return ran{code: code, out: lines(&out), err: lines(&errs)}
}

func indexArgs(src, out string) []string {
	return []string{"--src", src, "--out", out, "--commit", commitA, "--repo", repo}
}

func put(t *testing.T, src, themeID string) {
	t.Helper()
	folder(t, src, themeID, themeOf(t, themeID))
}

func sha(data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

func listing(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}

func exists(path string) bool {
	_, err := os.Lstat(path)

	return err == nil
}

// built runs the index into a fresh out folder and reads index.json back.
func built(t *testing.T, src string) (ran, *js.Object, string) {
	t.Helper()

	out := filepath.Join(t.TempDir(), "repo")
	got := runIndex(indexArgs(src, out), nil)

	if got.code != 0 {
		return got, nil, out
	}

	return got, parseFile(t, filepath.Join(out, "index.json")), out
}

func indexThemes(index *js.Object) []*js.Object {
	raw, _ := index.Get("themes")
	list, _ := raw.([]any)

	entries := make([]*js.Object, 0, len(list))
	for _, item := range list {
		entry, _ := item.(*js.Object)
		entries = append(entries, entry)
	}

	return entries
}

func text(object *js.Object, key string) string {
	raw, _ := object.Get(key)
	value, _ := raw.(string)

	return value
}

func TestTwoThemesGiveTwoEntriesPinnedAndHashed(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	put(t, src, "beta")
	put(t, src, "alpha")
	writeText(t, filepath.Join(src, "README.md"), "# not copied")

	got, index, out := built(t, src)
	if got.code != 0 {
		t.Fatalf("code = %d: %q", got.code, got.all())
	}

	expectLines(t, listing(t, out), "alpha", "beta", "index.json")

	if text(index, "repo") != repo {
		t.Errorf("repo = %s", text(index, "repo"))
	}

	entries := indexThemes(index)
	ids := make([]string, 0, len(entries))

	for _, entry := range entries {
		ids = append(ids, text(entry, "id"))
		checkCopied(t, src, out, entry)
	}

	expectLines(t, ids, "alpha", "beta")

	data, err := os.ReadFile(filepath.Join(out, "index.json"))
	if err != nil {
		t.Fatal(err)
	}

	// Compact JSON ending in one newline.
	if strings.Count(string(data), "\n") != 1 {
		t.Error("index.json is not one line")
	}

	expectLines(t, got.out[:1], "2 themes at "+commitA+" written to "+out)
}

// checkCopied: an entry is pinned at the commit, lists theme.json alone, and carries the hash
// of the file, which the copy is byte for byte.
func checkCopied(t *testing.T, src, out string, entry *js.Object) {
	t.Helper()

	themeID := text(entry, "id")

	data, err := os.ReadFile(filepath.Join(src, themeID, "theme.json"))
	if err != nil {
		t.Fatal(err)
	}

	copied, err := os.ReadFile(filepath.Join(out, themeID, "theme.json"))
	if err != nil {
		t.Fatal(err)
	}

	if text(entry, "sha256") != sha(data) {
		t.Errorf("%s: sha256 is not of the file", themeID)
	}

	if !bytes.Equal(copied, data) {
		t.Errorf("%s: the copy is not byte for byte the file", themeID)
	}

	if text(entry, "commit") != commitA {
		t.Errorf("%s: commit = %s", themeID, text(entry, "commit"))
	}

	files, _ := entry.Get("files")
	if js.Stringify(files) != `["theme.json"]` {
		t.Errorf("%s: files = %s", themeID, js.Stringify(files))
	}
}

func TestFoldersTheIndexRefuses(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	put(t, src, "good")

	styled := folder(t, src, "styled", themeOf(t, "styled"))
	writeText(t, filepath.Join(styled, "theme.css"), "x")

	litter := folder(t, src, "litter", themeOf(t, "litter"))
	writeText(t, filepath.Join(litter, ".DS_Store"), "")

	folder(t, src, "faint", themeOf(t, "faint", "tokens", darkTokens(t, "tx", "#222222")))
	folder(t, src, "neon", themeOf(t, "neon"))
	folder(t, src, "one", themeOf(t, "two"))
	folder(t, src, "Bad_Name", themeOf(t, "bad-name"))
	folder(t, src, "empty", nil)
	folder(t, src, "broken", "{ nope")

	big := folder(t, src, "big", nil)
	truncate(t, filepath.Join(big, "theme.json"), 70*1024)

	elsewhere := folder(t, t.TempDir(), "linked", themeOf(t, "linked"))
	symlink(t, elsewhere, filepath.Join(src, "linked"))

	viaFile := folder(t, src, "viafile", nil)
	symlink(t, filepath.Join(src, "good", "theme.json"), filepath.Join(viaFile, "theme.json"))

	bom := append([]byte{0xef, 0xbb, 0xbf}, []byte(js.Stringify(themeOf(t, "bom")))...)
	folder(t, src, "bom", bom)
	folder(t, src, "latin", []byte{0x7b, 0xe9, 0x7d})

	out := filepath.Join(t.TempDir(), "repo")
	got := runIndex(indexArgs(src, out), nil)

	if got.code != 1 {
		t.Errorf("code = %d", got.code)
	}

	if exists(out) {
		t.Error("something was written")
	}

	for _, pattern := range []string{
		`^styled: theme\.css is not allowed`, `^litter: \.DS_Store is not allowed`,
		`^faint: .*tx on bg`, `^neon: .*built-in`, `^one: .*does not match the manifest id "two"`,
		`^Bad_Name: folder name must be`, `^empty: theme\.json is missing$`,
		`^broken: theme\.json is not valid JSON$`, `^big: theme\.json is larger than 64 KB$`,
		`^linked: .*symbolic link`, `^viafile: theme\.json is not a regular file`,
		`^bom: theme\.json starts with a byte order mark`,
		`^latin: theme\.json is not valid UTF-8$`,
		`^nothing was written$`,
	} {
		anyMatches(t, got.err, pattern)
	}
}

func TestAMislabeledFolderIsReportedAndTheRightOneKept(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	folder(t, src, "a", themeOf(t, "b"))
	put(t, src, "b")

	out := filepath.Join(t.TempDir(), "repo")
	got := runIndex(indexArgs(src, out), nil)

	expectLines(t, got.err, `a: folder "a" does not match the manifest id "b"`,
		"nothing was written")
}

func TestLooseFilesAndDotFoldersAreNotThemes(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	put(t, src, "good")
	writeText(t, filepath.Join(src, "README.md"), "x")
	writeText(t, filepath.Join(src, "LICENSE"), "x")
	mkdir(t, filepath.Join(src, ".git"))
	mkdir(t, filepath.Join(src, ".github"))

	got, index, _ := built(t, src)
	if got.code != 0 {
		t.Fatalf("%q", got.all())
	}

	if len(indexThemes(index)) != 1 {
		t.Errorf("themes = %d", len(indexThemes(index)))
	}
}

func TestBadOptionValuesWriteNothing(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	put(t, src, "alpha")

	commits := []string{
		"abc", strings.Repeat("A", 40), strings.Repeat("g", 40), strings.Repeat("a", 41), "nothex",
	}
	for _, commit := range commits {
		out := filepath.Join(t.TempDir(), "repo")
		args := []string{"--src", src, "--out", out, "--commit", commit, "--repo", repo}
		got := runIndex(args, nil)

		if got.code != 1 {
			t.Errorf("%s: code = %d", commit, got.code)
		}

		anyMatches(t, got.err, `^commit must be 40 lowercase hex characters$`)

		if exists(out) {
			t.Errorf("%s: wrote %s", commit, out)
		}
	}

	badRepo := []string{"--src", src, "--out", t.TempDir(), "--commit", commitA, "--repo", "nope"}
	got := runIndex(badRepo, nil)
	anyMatches(t, got.err, `^repo must be a GitHub owner/name$`)

	missing := filepath.Join(t.TempDir(), "nope")
	got = runIndex(indexArgs(missing, filepath.Join(t.TempDir(), "o")), nil)
	anyMatches(t, got.err, `^source folder could not be read$`)
}

func TestTheOutFolder(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	put(t, src, "alpha")

	full := t.TempDir()
	writeText(t, filepath.Join(full, "old.txt"), "x")

	holder := t.TempDir()
	symlink(t, src, filepath.Join(holder, "via"))

	const inside = `^--out is inside --src`

	tests := []struct {
		name, src, out, want string
	}{
		{name: "holds files", src: src, out: full, want: `^--out already holds files`},
		{
			name: "inside the source", src: src, out: filepath.Join(src, "alpha", "out"),
			want: inside,
		},
		{name: "the source", src: src, out: src, want: inside},
		{
			name: "behind a link into the source", src: src,
			out: filepath.Join(holder, "via", "new"), want: inside,
		},
		{
			name: "the source behind a link", src: filepath.Join(holder, "via"),
			out: filepath.Join(src, "new"), want: inside,
		},
	}

	for _, tt := range tests {
		got := runIndex(indexArgs(tt.src, tt.out), nil)
		if got.code != 1 {
			t.Errorf("%s: code = %d", tt.name, got.code)
		}

		anyMatches(t, got.err, tt.want)
	}

	expectLines(t, listing(t, src), "alpha")

	empty := t.TempDir()
	if got := runIndex(indexArgs(src, empty), nil); got.code != 0 {
		t.Errorf("an empty out folder: %q", got.all())
	}
}

func TestBadUsageExits2(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	put(t, src, "alpha")

	tests := []struct {
		args []string
		want string
	}{
		{args: []string{}, want: "--src, --out, --commit, --repo are required"},
		{args: []string{"--src", src}, want: "--out, --commit, --repo are required"},
		{
			args: []string{"--src", src, "--out", t.TempDir(), "--commit", commitA},
			want: "--repo is required",
		},
		{args: []string{"--bogus", "x"}, want: `unknown option "--bogus"`},
		{
			args: append(indexArgs(src, t.TempDir()), "--extra", "y"),
			want: `unknown option "--extra"`,
		},
		{args: []string{"--src", "--out"}, want: "--src needs one value"},
		{args: []string{"--src", "a", "--src", "b"}, want: "--src needs one value"},
	}

	for _, tt := range tests {
		got := runIndex(tt.args, nil)
		if got.code != 2 {
			t.Errorf("%q: code = %d", tt.args, got.code)
		}

		expectLines(t, got.err, tt.want, themecmd.IndexUsage)
	}
}

func TestAFailedWriteRemovesWhatTheRunMade(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	put(t, src, "alpha")
	put(t, src, "beta")

	failOn := func(suffix string) func(string, []byte) error {
		return func(path string, data []byte) error {
			if strings.HasSuffix(path, suffix) {
				return fmt.Errorf("no space on /secret/path: %w", syscall.ENOSPC)
			}

			return os.WriteFile(path, data, 0o600)
		}
	}

	scratch := t.TempDir()
	fresh := filepath.Join(scratch, "a", "b", "out")

	got := runIndex(indexArgs(src, fresh), failOn(filepath.Join("beta", "theme.json")))
	if got.code != 1 {
		t.Errorf("code = %d", got.code)
	}

	expectLines(t, got.err, "could not write "+fresh+": ENOSPC; what this run wrote was removed")

	if exists(filepath.Join(scratch, "a")) {
		t.Error("the folders the run made are still there")
	}

	empty := t.TempDir()
	got = runIndex(indexArgs(src, empty), failOn("index.json"))

	if len(got.err) != 1 {
		t.Errorf("lines = %q", got.err)
	}

	expectLines(t, listing(t, empty))
}

func TestASourceWithNoThemeFolderWritesNothing(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeText(t, filepath.Join(src, "README.md"), "# only this")

	out := filepath.Join(t.TempDir(), "repo")
	got := runIndex(indexArgs(src, out), nil)

	expectLines(t, got.err, "--src holds no theme folders, so there is nothing to index",
		"nothing was written")

	if exists(out) {
		t.Error("something was written")
	}
}

func TestAnIndexOverTheCapIsRefusedWhole(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	for index := range 300 {
		put(t, src, "t"+strconv.Itoa(index))
	}

	got := runIndex(indexArgs(src, filepath.Join(t.TempDir(), "repo")), nil)
	if got.code != 1 {
		t.Errorf("code = %d", got.code)
	}

	anyMatches(t, got.err, `^index\.json would be \d+ KB; the app reads at most 512 KB$`)
}

// rawHost serves an index directory the way raw.githubusercontent.com would: index.json on main
// and each theme at its pin.
type rawHost struct {
	out   string
	asked []string
}

func (h *rawHost) RoundTrip(request *http.Request) (*http.Response, error) {
	url := request.URL.String()
	h.asked = append(h.asked, url)

	prefix := "https://raw.githubusercontent.com/" + repo + "/"
	name := ""

	switch {
	case url == prefix+"main/index.json":
		name = filepath.Join(h.out, "index.json")
	case strings.HasPrefix(url, prefix+commitA+"/"):
		name = filepath.Join(h.out, strings.TrimPrefix(url, prefix+commitA+"/"))
	}

	data, err := os.ReadFile(name)
	status := http.StatusOK

	if err != nil {
		status, data = http.StatusNotFound, []byte("Not Found")
	}

	return &http.Response{
		Status: http.StatusText(status), StatusCode: status, Proto: "HTTP/1.1", ProtoMajor: 1,
		ProtoMinor: 1, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(data)),
		ContentLength: int64(len(data)), Request: request,
	}, nil
}

func reader(t *testing.T, out, user string) (*gallery.Gallery, *rawHost) {
	t.Helper()

	host := &rawHost{out: out, asked: nil}

	return gallery.New(gallery.Deps{
		Transport: host, Now: func() int64 { return 1 }, Builtin: themes.Disk("../../themes"),
		User: user, Repo: repo, Timeout: 0, Files: nil,
	}), host
}

func TestWhatTheIndexWritesSurvivesTheRealReaderAndInstalls(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	put(t, src, "alpha")
	put(t, src, "beta")
	// A theme.json with a trailing newline, and one with CRLF line endings: nothing trims or
	// normalizes, so each is hashed as it is and installs.
	folder(t, src, "nl", js.StringifyIndent(themeOf(t, "nl"), "  ")+"\n\n")
	crlf := strings.ReplaceAll(js.StringifyIndent(themeOf(t, "crlf"), "  "), "\n", "\r\n")
	folder(t, src, "crlf", crlf+"\r\n")

	got, written, out := built(t, src)
	if got.code != 0 {
		t.Fatalf("%q", got.all())
	}

	user := t.TempDir()
	library, host := reader(t, out, user)

	index, problem := library.FetchIndex(t.Context())
	if problem != "" {
		t.Fatal(problem)
	}

	if index.Dropped != 0 {
		t.Errorf("dropped = %d", index.Dropped)
	}

	writtenThemes, _ := written.Get("themes")
	if js.Stringify(index.Themes) != js.Stringify(writtenThemes) {
		t.Error("the reader's entries are not the entries written")
	}

	expectLines(t, host.asked, "https://raw.githubusercontent.com/"+repo+"/main/index.json")

	for _, entry := range index.Themes {
		result := library.Install(t.Context(), entry.ID)
		if result.Code != http.StatusOK {
			t.Errorf("%s: %d %s", entry.ID, result.Code, js.Stringify(result.Body))

			continue
		}

		source, err := os.ReadFile(filepath.Join(src, entry.ID, "theme.json"))
		if err != nil {
			t.Fatal(err)
		}

		installed, err := os.ReadFile(filepath.Join(user, entry.ID, "theme.json"))
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(installed, source) {
			t.Errorf("%s: installed bytes differ", entry.ID)
		}
	}
}

func TestTheGallerySeedPassesBuildsAndInstalls(t *testing.T) {
	t.Parallel()

	expectLines(t, slices.DeleteFunc(listing(t, "../../gallery-seed"), func(name string) bool {
		return strings.Contains(name, ".")
	}), "blueprint", "ember", "paper")

	got, _, out := built(t, "../../gallery-seed")
	if got.code != 0 {
		t.Fatalf("%q", got.all())
	}

	library, _ := reader(t, out, t.TempDir())

	for _, themeID := range []string{"blueprint", "ember", "paper"} {
		expectLines(t, listing(t, filepath.Join("../../gallery-seed", themeID)), "theme.json")

		if code := library.Install(t.Context(), themeID).Code; code != http.StatusOK {
			t.Errorf("%s: %d", themeID, code)
		}
	}

	// An index whose manifest is not its file is refused, and nothing is written.
	index := parseFile(t, filepath.Join(out, "index.json"))
	for _, entry := range indexThemes(index) {
		if text(entry, "id") == "ember" {
			shown, _ := entry.Get("manifest")
			manifest, _ := shown.(*js.Object)
			manifest.Set("description", "something else")
		}
	}

	writeText(t, filepath.Join(out, "index.json"), js.Stringify(index))

	other := t.TempDir()
	library, _ = reader(t, out, other)

	if code := library.Install(t.Context(), "ember").Code; code != http.StatusUnprocessableEntity {
		t.Errorf("a mismatched manifest: %d", code)
	}

	expectLines(t, listing(t, other))
}
