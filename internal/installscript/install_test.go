package installscript_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/testtime"
)

const (
	releaseTag = "v9.9.9"

	// fakeConn stands in for the binary: a script that answers `conn version`.
	fakeConn = "#!/bin/sh\necho \"conn " + releaseTag + "\"\n"

	runTimeout = 30 * time.Second

	// The app's release files and the program inside the bundle.
	appZipName  = "Conn_darwin_universal.zip"
	appSumsName = "checksums-app.txt"
	appBinary   = "Conn.app/Contents/MacOS/conn-app"
	fakeApp     = "#!/bin/sh\nexit 0\n"

	// runningApp is how a running Conn.app shows in the process list.
	runningApp = "/Users/someone/Applications/Conn.app/Contents/MacOS/conn-app"

	// unzipMissing is why a test that needs unzip does not run.
	unzipMissing = "unzip is not on PATH, so the app cannot be unpacked"
)

// release is a fake GitHub releases address: /latest redirects to the tag's page, and the files
// are under /download/<tag>/. The app's two files are served only after serveApp, so a release
// without an app answers 404 for them.
type release struct {
	server    *httptest.Server
	archive   []byte
	checksums func(name string) string

	mu       sync.Mutex
	requests []string
	app      []byte
	appSums  func(name string) string
}

// archiveOf is a .tar.gz holding one file, conn, which is fakeConn.
func archiveOf(t *testing.T) []byte {
	t.Helper()

	content := fakeConn

	var packed bytes.Buffer

	zipped := gzip.NewWriter(&packed)
	archive := tar.NewWriter(zipped)

	header := &tar.Header{Name: "conn", Mode: 0o755, Size: int64(len(content))}

	err := archive.WriteHeader(header)
	if err == nil {
		_, err = archive.Write([]byte(content))
	}

	if err == nil {
		err = archive.Close()
	}

	if err == nil {
		err = zipped.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	return packed.Bytes()
}

// appZipOf is the app's zip: an Info.plist and the program, which has the file mode given.
func appZipOf(t *testing.T, programMode fs.FileMode) []byte {
	t.Helper()

	files := []struct {
		name, body string
		mode       fs.FileMode
	}{
		{"Conn.app/Contents/Info.plist", "<plist></plist>\n", 0o644},
		{appBinary, fakeApp, programMode},
	}

	var packed bytes.Buffer

	archive := zip.NewWriter(&packed)

	for _, file := range files {
		header := &zip.FileHeader{Name: file.name, Method: zip.Deflate}
		header.SetMode(file.mode)

		entry, err := archive.CreateHeader(header)
		if err == nil {
			_, err = entry.Write([]byte(file.body))
		}

		if err != nil {
			t.Fatal(err)
		}
	}

	err := archive.Close()
	if err != nil {
		t.Fatal(err)
	}

	return packed.Bytes()
}

func sumOf(data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

// newRelease serves archive for every platform's file name; checksums is the text of
// checksums.txt, built by the test so it can be made wrong.
func newRelease(t *testing.T, archive []byte, checksums func(name string) string) *release {
	t.Helper()

	fake := &release{archive: archive, checksums: checksums}
	mux := http.NewServeMux()

	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		fake.record(r.URL.Path)
		http.Redirect(w, r, "/tag/"+releaseTag, http.StatusFound)
	})
	mux.HandleFunc("/tag/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "the release page")
	})
	mux.HandleFunc("/download/"+releaseTag+"/", fake.download)

	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)

	return fake
}

// serveApp makes the release carry the app: appZip is Conn_darwin_universal.zip and sums the
// text of checksums-app.txt. Either may be nil, and then that file answers 404.
func (r *release) serveApp(appZip []byte, sums func(name string) string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.app = appZip
	r.appSums = sums
}

func (r *release) download(w http.ResponseWriter, req *http.Request) {
	r.record(req.URL.Path)

	r.mu.Lock()
	appZip, appSums := r.app, r.appSums
	r.mu.Unlock()

	name := filepath.Base(req.URL.Path)

	switch {
	case name == "checksums.txt":
		platforms := []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"}
		lines := make([]string, 0, len(platforms))

		for _, platform := range platforms {
			lines = append(lines, r.checksums("conn_"+platform+".tar.gz"))
		}

		_, _ = fmt.Fprint(w, strings.Join(lines, "\n")+"\n")
	case name == appSumsName:
		if appSums == nil {
			http.NotFound(w, req)

			return
		}

		_, _ = fmt.Fprint(w, appSums(appZipName)+"\n")
	case name == appZipName:
		if appZip == nil {
			http.NotFound(w, req)

			return
		}

		_, _ = w.Write(appZip)
	case strings.HasPrefix(name, "conn_"):
		_, _ = w.Write(r.archive)
	default:
		http.NotFound(w, req)
	}
}

func (r *release) record(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.requests = append(r.requests, path)
}

func (r *release) asked() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return strings.Join(r.requests, " ")
}

func goodSums(archive []byte) func(string) string {
	return func(name string) string { return sumOf(archive) + "  " + name }
}

// outcome is what one run of install.sh did.
type outcome struct {
	output string
	failed bool
	home   string
}

// writeTool puts a fake command first on the PATH of a run.
func writeTool(t *testing.T, folder, name, body string) {
	t.Helper()

	err := os.WriteFile(filepath.Join(folder, name), []byte(body), 0o700)
	if err != nil {
		t.Fatal(err)
	}
}

// fakePgrep answers like pgrep -f: it prints a pid when the pattern, read as an extended regular
// expression, matches a line of $FAKE_PS (the process list a test supplies), and exits 1
// otherwise.
const fakePgrep = `#!/bin/sh
[ "$1" = -f ] || exit 2
if printf '%s\n' "${FAKE_PS:-}" | grep -E -- "$2" >/dev/null; then
	echo 4242
	exit 0
fi
exit 1
`

// run runs install.sh with a fresh HOME, a fake uname answering os and machine, a fake pgrep
// and extra environment as KEY=value pairs.
func run(t *testing.T, fake *release, osName, machine string, extra ...string) outcome {
	t.Helper()

	return runWith(t, fake, osName, machine, nil, extra)
}

// runWith is run with arguments for install.sh as well.
func runWith(
	t *testing.T, fake *release, osName, machine string, args, extra []string,
) outcome {
	t.Helper()

	home := t.TempDir()
	tools := t.TempDir()

	writeTool(t, tools, "uname", "#!/bin/sh\ncase \"$1\" in\n-s) echo "+osName+" ;;\n-m) echo "+
		machine+" ;;\nesac\n")
	writeTool(t, tools, "pgrep", fakePgrep)

	ctx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(runTimeout))
	defer cancel()

	command := exec.CommandContext(ctx, "sh", append([]string{"../../install.sh"}, args...)...)

	command.Env = append([]string{
		"HOME=" + home, "PATH=" + tools + ":" + os.Getenv("PATH"),
		"CONN_INSTALL_BASE=" + fake.server.URL,
	}, extra...)

	combined, err := command.CombinedOutput()

	return outcome{output: string(combined), failed: err != nil, home: home}
}

func TestInstallsTheLatestRelease(t *testing.T) {
	t.Parallel()

	archive := archiveOf(t)
	fake := newRelease(t, archive, goodSums(archive))

	got := run(t, fake, "Darwin", "arm64")
	if got.failed {
		t.Fatalf("install failed: %s", got.output)
	}

	binary := filepath.Join(got.home, ".local", "bin", "conn")

	info, err := os.Stat(binary)
	if err != nil {
		t.Fatalf("no binary: %v\n%s", err, got.output)
	}

	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("mode = %v, want it executable", info.Mode().Perm())
	}

	for _, want := range []string{
		"Installing conn " + releaseTag + " for darwin/arm64",
		"Installed conn " + releaseTag, "conn serve", "is not on your PATH",
	} {
		if !strings.Contains(got.output, want) {
			t.Errorf("output lacks %q:\n%s", want, got.output)
		}
	}
}

func TestPicksTheFileForThePlatform(t *testing.T) {
	t.Parallel()

	tests := []struct{ osName, machine, file string }{
		{"Darwin", "arm64", "conn_darwin_arm64.tar.gz"},
		{"Darwin", "x86_64", "conn_darwin_amd64.tar.gz"},
		{"Linux", "x86_64", "conn_linux_amd64.tar.gz"},
		{"Linux", "aarch64", "conn_linux_arm64.tar.gz"},
	}
	for _, tt := range tests {
		t.Run(tt.osName+" "+tt.machine, func(t *testing.T) {
			t.Parallel()

			archive := archiveOf(t)
			fake := newRelease(t, archive, goodSums(archive))

			got := run(t, fake, tt.osName, tt.machine)
			if got.failed {
				t.Fatalf("install failed: %s", got.output)
			}

			if !strings.Contains(fake.asked(), "/download/"+releaseTag+"/"+tt.file) {
				t.Errorf("asked for %s, want %s", fake.asked(), tt.file)
			}
		})
	}
}

func TestRefusesWhatItCannotInstall(t *testing.T) {
	t.Parallel()

	archive := archiveOf(t)

	tests := []struct {
		name, osName, machine string
		sums                  func(string) string
		extra                 []string
		message               string
	}{
		{
			"an unsupported OS", "FreeBSD", "x86_64", goodSums(archive), nil,
			"FreeBSD is not supported",
		},
		{
			"an unsupported CPU", "Linux", "riscv64", goodSums(archive), nil,
			"riscv64 is not supported",
		},
		{
			"a download that does not match its checksum", "Linux", "x86_64",
			func(name string) string { return strings.Repeat("0", 64) + "  " + name },
			nil, "does not match its checksum",
		},
		{
			"checksums without the file", "Linux", "x86_64",
			func(string) string { return strings.Repeat("0", 64) + "  conn_other.tar.gz" },
			nil, "no line for conn_linux_amd64.tar.gz",
		},
		{
			"a version that is not a tag", "Linux", "x86_64", goodSums(archive),
			[]string{"CONN_INSTALL_VERSION=main"},
			"is not a release tag",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := newRelease(t, archive, tt.sums)

			got := run(t, fake, tt.osName, tt.machine, tt.extra...)
			if !got.failed {
				t.Fatalf("install succeeded, want it to fail:\n%s", got.output)
			}

			if !strings.Contains(got.output, tt.message) {
				t.Errorf("output lacks %q:\n%s", tt.message, got.output)
			}

			_, err := os.Stat(filepath.Join(got.home, ".local", "bin", "conn"))
			if !os.IsNotExist(err) {
				t.Errorf("a binary was installed (err %v)", err)
			}
		})
	}
}

func TestAPinnedVersionAndAFolderOfYourOwn(t *testing.T) {
	t.Parallel()

	archive := archiveOf(t)
	fake := newRelease(t, archive, goodSums(archive))
	folder := filepath.Join(t.TempDir(), "tools", "bin")

	got := run(t, fake, "Linux", "x86_64",
		"CONN_INSTALL_VERSION="+releaseTag, "CONN_INSTALL_DIR="+folder)
	if got.failed {
		t.Fatalf("install failed: %s", got.output)
	}

	_, err := os.Stat(filepath.Join(folder, "conn"))
	if err != nil {
		t.Errorf("no binary in the folder asked for: %v", err)
	}

	if strings.Contains(fake.asked(), "/latest") {
		t.Errorf("a pinned version still asked for the latest: %s", fake.asked())
	}
}

func TestRunningItAgainReplacesTheBinary(t *testing.T) {
	t.Parallel()

	archive := archiveOf(t)
	fake := newRelease(t, archive, goodSums(archive))
	folder := filepath.Join(t.TempDir(), "bin")

	err := os.MkdirAll(folder, 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(folder, "conn"), []byte("an old conn"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	got := run(t, fake, "Linux", "x86_64", "CONN_INSTALL_DIR="+folder)
	if got.failed {
		t.Fatalf("install failed: %s", got.output)
	}

	installed, err := os.ReadFile(filepath.Join(folder, "conn"))
	if err != nil {
		t.Fatal(err)
	}

	if string(installed) != fakeConn {
		t.Errorf("the old binary is still there: %q", installed)
	}

	leftovers, err := filepath.Glob(filepath.Join(folder, ".conn.new.*"))
	if err != nil {
		t.Fatal(err)
	}

	if len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

// needUnzip skips a test that unpacks the app on a machine without unzip.
func needUnzip(t *testing.T) {
	t.Helper()

	_, err := exec.LookPath("unzip")
	if err != nil {
		t.Skip(unzipMissing)
	}
}

// appFolder is a folder for CONN_INSTALL_APP_DIR that does not exist yet, so a test can tell
// whether the installer made it.
func appFolder(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "Applications")
}

func exists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

func cliPath(home string) string {
	return filepath.Join(home, ".local", "bin", "conn")
}

// readText reads a file the installer should have written.
func readText(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(content)
}

// macRelease is a release that carries a good app.
func macRelease(t *testing.T) *release {
	t.Helper()

	archive := archiveOf(t)
	fake := newRelease(t, archive, goodSums(archive))
	appZip := appZipOf(t, 0o755)
	fake.serveApp(appZip, goodSums(appZip))

	return fake
}

func TestInstallsTheAppOnMacOSByDefault(t *testing.T) {
	t.Parallel()
	needUnzip(t)

	fake := macRelease(t)

	// The process list holds only the installer itself, which must not look like a running Conn.
	got := run(t, fake, "Darwin", "arm64", "FAKE_PS=sh ../../install.sh")
	if got.failed {
		t.Fatalf("install failed: %s", got.output)
	}

	if !exists(cliPath(got.home)) {
		t.Errorf("the command-line tool is not installed:\n%s", got.output)
	}

	bundle := filepath.Join(got.home, "Applications", "Conn.app")

	info, err := os.Stat(filepath.Join(bundle, "Contents", "MacOS", "conn-app"))
	if err != nil {
		t.Fatalf("no app: %v\n%s", err, got.output)
	}

	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("mode = %v, want it executable", info.Mode().Perm())
	}

	folder := filepath.Join(got.home, "Applications")

	for _, want := range []string{
		"  app:    " + bundle,
		"Open Conn from " + folder + ", or start the server with: conn serve",
	} {
		if !strings.Contains(got.output, want) {
			t.Errorf("output lacks %q:\n%s", want, got.output)
		}
	}
}

func TestInstallsTheAppInTheFolderAskedFor(t *testing.T) {
	t.Parallel()
	needUnzip(t)

	fake := macRelease(t)
	folder := appFolder(t)

	got := run(t, fake, "Darwin", "x86_64", "CONN_INSTALL_APP_DIR="+folder)
	if got.failed {
		t.Fatalf("install failed: %s", got.output)
	}

	if !exists(filepath.Join(folder, appBinary)) {
		t.Errorf("no app in the folder asked for:\n%s", got.output)
	}

	if exists(filepath.Join(got.home, "Applications")) {
		t.Error("the default folder was made although another was asked for")
	}
}

func TestCLIOnlyLeavesTheAppAlone(t *testing.T) {
	t.Parallel()

	fake := macRelease(t)
	folder := appFolder(t)

	got := runWith(t, fake, "Darwin", "arm64", []string{"--cli-only"},
		[]string{"CONN_INSTALL_APP_DIR=" + folder, "FAKE_PS=" + runningApp})
	if got.failed {
		t.Fatalf("install failed: %s", got.output)
	}

	if !exists(cliPath(got.home)) {
		t.Errorf("the command-line tool is not installed:\n%s", got.output)
	}

	if exists(folder) {
		t.Error("the app folder was made")
	}

	if strings.Contains(fake.asked(), "Conn_darwin") {
		t.Errorf("asked for the app: %s", fake.asked())
	}

	if strings.Contains(fake.asked(), appSumsName) {
		t.Errorf("asked for the app's checksums: %s", fake.asked())
	}

	if !strings.Contains(got.output, "Start it with: conn serve") {
		t.Errorf("output lacks the command-line hint:\n%s", got.output)
	}
}

func TestLinuxNeverInstallsTheApp(t *testing.T) {
	t.Parallel()

	fake := macRelease(t)
	folder := appFolder(t)

	got := run(t, fake, "Linux", "x86_64", "CONN_INSTALL_APP_DIR="+folder)
	if got.failed {
		t.Fatalf("install failed: %s", got.output)
	}

	if !exists(cliPath(got.home)) {
		t.Errorf("the command-line tool is not installed:\n%s", got.output)
	}

	if exists(folder) {
		t.Error("the app folder was made")
	}

	if strings.Contains(fake.asked(), "Conn_darwin") {
		t.Errorf("asked for the app: %s", fake.asked())
	}

	if strings.Contains(got.output, "app:") {
		t.Errorf("output mentions an app:\n%s", got.output)
	}
}

func TestAReleaseWithoutAnAppInstallsTheCLI(t *testing.T) {
	t.Parallel()

	archive := archiveOf(t)
	fake := newRelease(t, archive, goodSums(archive))
	folder := appFolder(t)

	got := run(t, fake, "Darwin", "arm64", "CONN_INSTALL_APP_DIR="+folder)
	if got.failed {
		t.Fatalf("install failed: %s", got.output)
	}

	if !exists(cliPath(got.home)) {
		t.Errorf("the command-line tool is not installed:\n%s", got.output)
	}

	if exists(folder) {
		t.Error("the app folder was made")
	}

	want := "this release has no app; installed the command-line tool only"
	if !strings.Contains(got.output, want) {
		t.Errorf("output lacks %q:\n%s", want, got.output)
	}

	if strings.Contains(got.output, "Open Conn") {
		t.Errorf("output points at an app that is not there:\n%s", got.output)
	}
}

func TestABadAppFailsAndKeepsTheCLI(t *testing.T) {
	t.Parallel()
	needUnzip(t)

	appZip := appZipOf(t, 0o755)
	plainZip := appZipOf(t, 0o644)
	zeros := func(name string) string { return strings.Repeat("0", 64) + "  " + name }
	notAZip := []byte("this is not a zip")

	tests := []struct {
		name    string
		appZip  []byte
		sums    func(string) string
		message string
	}{
		{"a checksum that does not match", appZip, zeros, "does not match its checksum"},
		{
			"checksums without the zip", appZip,
			func(string) string { return strings.Repeat("0", 64) + "  other.zip" },
			"no line for " + appZipName,
		},
		{"a zip that is not served", nil, goodSums(appZip), "could not download"},
		{"a zip that cannot be unpacked", notAZip, goodSums(notAZip), "could not unpack"},
		{
			"a program that cannot run", plainZip, goodSums(plainZip),
			"is not an executable program",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			archive := archiveOf(t)
			fake := newRelease(t, archive, goodSums(archive))
			fake.serveApp(tt.appZip, tt.sums)

			folder := appFolder(t)

			got := run(t, fake, "Darwin", "arm64", "CONN_INSTALL_APP_DIR="+folder)
			if !got.failed {
				t.Fatalf("install succeeded, want it to fail:\n%s", got.output)
			}

			if !strings.Contains(got.output, tt.message) {
				t.Errorf("output lacks %q:\n%s", tt.message, got.output)
			}

			if exists(folder) {
				t.Error("the app folder was made")
			}

			if !exists(cliPath(got.home)) {
				t.Error("the command-line tool was not kept")
			}
		})
	}
}

// plantApp puts an app in folder whose program holds content, and returns the bundle's path.
func plantApp(t *testing.T, folder, content string) string {
	t.Helper()

	bundle := filepath.Join(folder, "Conn.app")

	err := os.MkdirAll(filepath.Join(bundle, "Contents", "MacOS"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(folder, appBinary), []byte(content), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	return bundle
}

func TestARunningAppStopsTheInstallBeforeAnythingChanges(t *testing.T) {
	t.Parallel()
	needUnzip(t)

	fake := macRelease(t)
	folder := appFolder(t)
	plantApp(t, folder, "the app that is running")

	got := run(t, fake, "Darwin", "arm64",
		"CONN_INSTALL_APP_DIR="+folder, "FAKE_PS="+runningApp)
	if !got.failed {
		t.Fatalf("install succeeded, want it to fail:\n%s", got.output)
	}

	for _, want := range []string{"quit Conn first", "conn stop", "--cli-only"} {
		if !strings.Contains(got.output, want) {
			t.Errorf("output lacks %q:\n%s", want, got.output)
		}
	}

	if kept := readText(t, filepath.Join(folder, appBinary)); kept != "the app that is running" {
		t.Errorf("the running app was changed: %q", kept)
	}

	if exists(cliPath(got.home)) {
		t.Error("the command-line tool was installed")
	}

	if strings.Contains(fake.asked(), "/download/") {
		t.Errorf("something was downloaded: %s", fake.asked())
	}

	leftovers, err := filepath.Glob(filepath.Join(folder, ".Conn.app.*"))
	if err != nil {
		t.Fatal(err)
	}

	if len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

func TestRunningItAgainReplacesTheApp(t *testing.T) {
	t.Parallel()
	needUnzip(t)

	fake := macRelease(t)
	folder := appFolder(t)

	first := run(t, fake, "Darwin", "arm64", "CONN_INSTALL_APP_DIR="+folder)
	if first.failed {
		t.Fatalf("first install failed: %s", first.output)
	}

	stale := filepath.Join(folder, "Conn.app", "Contents", "stale")

	err := os.WriteFile(stale, []byte("from the old bundle"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	second := run(t, fake, "Darwin", "arm64", "CONN_INSTALL_APP_DIR="+folder)
	if second.failed {
		t.Fatalf("second install failed: %s", second.output)
	}

	if exists(stale) {
		t.Error("the old bundle was merged into, not replaced")
	}

	if got := readText(t, filepath.Join(folder, appBinary)); got != fakeApp {
		t.Errorf("program = %q, want the new one", got)
	}

	entries, err := os.ReadDir(folder)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Errorf("the app folder holds %d entries, want only Conn.app: %v", len(entries), entries)
	}
}

func TestRefusesAnOptionItDoesNotKnow(t *testing.T) {
	t.Parallel()

	fake := macRelease(t)

	got := runWith(t, fake, "Darwin", "arm64", []string{"--app-only"}, nil)
	if !got.failed {
		t.Fatalf("install succeeded, want it to fail:\n%s", got.output)
	}

	if !strings.Contains(got.output, "unknown option '--app-only'") {
		t.Errorf("output lacks the unknown option:\n%s", got.output)
	}

	if fake.asked() != "" {
		t.Errorf("something was asked for: %s", fake.asked())
	}
}
