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
	"github.com/claytoncasey01/conn/internal/update"
)

const (
	releaseTag = "v9.9.9"

	// fakeConn stands in for the binary: a script that answers `conn version`.
	fakeConn = "#!/bin/sh\necho \"conn " + releaseTag + "\"\n"

	runTimeout = 30 * time.Second

	// The app's release files and the program inside the bundle. scripts/build-app.sh is where
	// they are named; TestInstallAndBuildAgreeOnTheAppNames checks install.sh uses the same.
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
	statuses map[string]int
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

// answerWith makes the file with this name answer with the status, whatever else is set.
func (r *release) answerWith(name string, status int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.statuses == nil {
		r.statuses = map[string]int{}
	}

	r.statuses[name] = status
}

func (r *release) download(w http.ResponseWriter, req *http.Request) {
	r.record(req.URL.Path)

	name := filepath.Base(req.URL.Path)

	r.mu.Lock()
	appZip, appSums, status := r.app, r.appSums, r.statuses[name]
	r.mu.Unlock()

	if status != 0 {
		http.Error(w, "the release server answers "+http.StatusText(status), status)

		return
	}

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
	// curlCalls is the arguments of each call of curl, one string a call.
	curlCalls []string
}

// writeTool puts a fake command first on the PATH of a run.
func writeTool(t *testing.T, folder, name, body string) {
	t.Helper()

	err := os.WriteFile(filepath.Join(folder, name), []byte(body), 0o700)
	if err != nil {
		t.Fatal(err)
	}
}

// fakePgrep answers like pgrep -u UID -f PATTERN: it prints a pid when the pattern, read as an
// extended regular expression, matches the command of a line of $FAKE_PS (the process list a test
// supplies, one "UID COMMAND" a line) whose UID is the one asked for, and exits 1 otherwise. It
// exits 2 when it is not asked about one user, as the real one matches every user's processes
// then.
const fakePgrep = `#!/bin/sh
[ "$1" = -u ] || exit 2
[ "$3" = -f ] || exit 2
if printf '%s\n' "${FAKE_PS:-}" | grep -E -- "^$2 .*$4" >/dev/null; then
	echo 4242
	exit 0
fi
exit 1
`

// fakeMv is mv, except that it fails when a test asks: FAKE_MV_FAILS is a comma-separated list
// holding "new" to fail moving the staged bundle into place, and "restore" to fail moving the old
// bundle back.
const fakeMv = `#!/bin/sh
fails=",${FAKE_MV_FAILS:-},"
case "$1" in
*/.Conn.app.new.*) case $fails in *,new,*) exit 1 ;; esac ;;
*/.Conn.app.old.*) case $fails in *,restore,*) exit 1 ;; esac ;;
esac
exec /bin/mv "$@"
`

// fakeCurl writes the arguments of each call to $FAKE_CURL_LOG, then is the real curl.
const fakeCurl = `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_CURL_LOG"
exec "$REAL_CURL" "$@"
`

// process is a line of the process list a test gives fakePgrep: who runs which command.
func process(uid int, command string) string {
	return fmt.Sprintf("%d %s", uid, command)
}

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
	writeTool(t, tools, "mv", fakeMv)
	writeTool(t, tools, "curl", fakeCurl)

	realCurl, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("curl is not on PATH, so install.sh cannot run")
	}

	curlLog := filepath.Join(tools, "curl calls")

	ctx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(runTimeout))
	defer cancel()

	command := exec.CommandContext(ctx, "sh", append([]string{"../../install.sh"}, args...)...)

	command.Env = append([]string{
		"HOME=" + home, "PATH=" + tools + ":" + os.Getenv("PATH"),
		"CONN_INSTALL_BASE=" + fake.server.URL,
		"REAL_CURL=" + realCurl, "FAKE_CURL_LOG=" + curlLog,
	}, extra...)

	combined, err := command.CombinedOutput()

	return outcome{
		output: string(combined), failed: err != nil, home: home, curlCalls: callsIn(curlLog),
	}
}

// callsIn is the lines of the log of calls, or none when nothing was logged.
func callsIn(log string) []string {
	text, err := os.ReadFile(log)
	if err != nil {
		return nil
	}

	return strings.Split(strings.TrimSpace(string(text)), "\n")
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
	got := run(t, fake, "Darwin", "arm64", "FAKE_PS="+process(os.Getuid(), "sh ../../install.sh"))
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
		[]string{"CONN_INSTALL_APP_DIR=" + folder, "FAKE_PS=" + process(os.Getuid(), runningApp)})
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

// plantApp puts an app in folder whose program holds content.
func plantApp(t *testing.T, folder, content string) {
	t.Helper()

	err := os.MkdirAll(filepath.Join(folder, "Conn.app", "Contents", "MacOS"), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(folder, appBinary), []byte(content), 0o700)
	if err != nil {
		t.Fatal(err)
	}
}

func TestARunningAppStopsTheInstallBeforeAnythingChanges(t *testing.T) {
	t.Parallel()
	needUnzip(t)

	fake := macRelease(t)
	folder := appFolder(t)
	plantApp(t, folder, "the app that is running")

	got := run(t, fake, "Darwin", "arm64",
		"CONN_INSTALL_APP_DIR="+folder, "FAKE_PS="+process(os.Getuid(), runningApp))
	if !got.failed {
		t.Fatalf("install succeeded, want it to fail:\n%s", got.output)
	}

	for _, want := range []string{"quit Conn first", "Cmd-Q", "--cli-only"} {
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

// TestAnotherUsersRunningAppDoesNotStopTheInstall is a Conn.app another account runs on the same
// machine: it is another bundle, so only this user's processes count.
func TestAnotherUsersRunningAppDoesNotStopTheInstall(t *testing.T) {
	t.Parallel()
	needUnzip(t)

	fake := macRelease(t)
	folder := appFolder(t)

	got := run(t, fake, "Darwin", "arm64",
		"CONN_INSTALL_APP_DIR="+folder, "FAKE_PS="+process(os.Getuid()+1, runningApp))
	if got.failed {
		t.Fatalf("install failed: %s", got.output)
	}

	if !exists(filepath.Join(folder, appBinary)) {
		t.Errorf("the app is not installed:\n%s", got.output)
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

func TestTheToolNeedsNoHomeWhenTheFolderIsGiven(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, osName string
		args         []string
	}{
		{"Linux", "Linux", nil},
		{"macOS with --cli-only", "Darwin", []string{"--cli-only"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := macRelease(t)
			folder := filepath.Join(t.TempDir(), "bin")

			got := runWith(t, fake, tt.osName, "x86_64", tt.args,
				[]string{"HOME=", "CONN_INSTALL_DIR=" + folder})
			if got.failed {
				t.Fatalf("install failed: %s", got.output)
			}

			if !exists(filepath.Join(folder, "conn")) {
				t.Errorf("the command-line tool is not installed:\n%s", got.output)
			}
		})
	}
}

func TestOnlyANotFoundMeansTheReleaseHasNoApp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		wantFailed bool
		message    string
	}{
		{"a 404", http.StatusNotFound, false, "this release has no app"},
		{"a 500", http.StatusInternalServerError, true, "the command-line tool is installed"},
		{"a 403", http.StatusForbidden, true, "the command-line tool is installed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := macRelease(t)
			fake.answerWith(appSumsName, tt.status)

			folder := appFolder(t)

			got := run(t, fake, "Darwin", "arm64", "CONN_INSTALL_APP_DIR="+folder)
			if got.failed != tt.wantFailed {
				t.Errorf("failed = %v, want %v:\n%s", got.failed, tt.wantFailed, got.output)
			}

			if !strings.Contains(got.output, tt.message) {
				t.Errorf("output lacks %q:\n%s", tt.message, got.output)
			}

			if exists(folder) {
				t.Error("the app folder was made")
			}

			if !exists(cliPath(got.home)) {
				t.Error("the command-line tool is not installed")
			}
		})
	}
}

// TestInstallAndBuildAgreeOnTheAppNames fails when install.sh and scripts/build-app.sh, which
// makes the files install.sh downloads, name the app's files differently: a rename in one alone
// would make every release look like one without an app.
func TestInstallAndBuildAgreeOnTheAppNames(t *testing.T) {
	t.Parallel()

	scripts := map[string]string{
		"install.sh":           readText(t, "../../install.sh"),
		"scripts/build-app.sh": readText(t, "../../scripts/build-app.sh"),
	}

	// The program's place inside the bundle: build-app.sh writes it under "$app/".
	names := []string{appZipName, appSumsName, strings.TrimPrefix(appBinary, "Conn.app/")}

	for script, text := range scripts {
		for _, name := range names {
			if !strings.Contains(text, name) {
				t.Errorf("%s does not name %q", script, name)
			}
		}
	}
}

// TestUpdateNoticeTextsAgreeWithTheInstaller fails when the text the app's update notice shows
// (the install command and the releases address) is not what install.sh and the README say: the
// notice tells people to run the command, so a change to the script alone would leave it wrong.
func TestUpdateNoticeTextsAgreeWithTheInstaller(t *testing.T) {
	t.Parallel()

	script := readText(t, "../../install.sh")
	readme := readText(t, "../../README.md")

	tests := []struct {
		name string
		text string
		want string
	}{
		{"install.sh header names the command", script, update.Command},
		// On a line of its own, so the --cli-only variant, which starts with the same text,
		// does not satisfy it.
		{
			"README install section names the command",
			readme, "\n" + update.Command + "\n",
		},
		{
			"install.sh header names the default address",
			script, "(default: " + update.ReleasesURL + ")",
		},
		{
			"install.sh code uses the default address",
			script, "${CONN_INSTALL_BASE:-" + update.ReleasesURL + "}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !strings.Contains(tt.text, tt.want) {
				t.Errorf("%s does not hold %q", tt.name, tt.want)
			}
		})
	}
}

// whereTheOldAppIs is where a failed install leaves the app that was there before it.
type whereTheOldAppIs int

const (
	// nowhere is no old app at all: there was none.
	nowhere whereTheOldAppIs = iota
	// backInPlace is the old app at Conn.app again.
	backInPlace
	// asideInTheFolder is the old app under a name of its own beside Conn.app: it could not be put
	// back, and it must not be deleted.
	asideInTheFolder
)

// TestAFailedMoveIntoPlaceSaysWhatBecameOfTheOldApp is the last step of the app's install failing:
// the message must say whether the old app was put back, and the old app is never lost.
func TestAFailedMoveIntoPlaceSaysWhatBecameOfTheOldApp(t *testing.T) {
	t.Parallel()
	needUnzip(t)

	tests := []struct {
		name    string
		oldApp  bool
		mvFails string
		want    string
		wantNot string
		wantOld whereTheOldAppIs
	}{
		{
			name: "the old app is put back", oldApp: true, mvFails: "new",
			want: "the old one was put back", wantNot: "could not be put back",
			wantOld: backInPlace,
		},
		{
			name: "the old app cannot be put back", oldApp: true, mvFails: "new,restore",
			want: "the old one could not be put back", wantNot: "was put back",
			wantOld: asideInTheFolder,
		},
		{
			name: "there was no old app", oldApp: false, mvFails: "new",
			want: "could not move the app into", wantNot: "put back",
			wantOld: nowhere,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := macRelease(t)
			folder := appFolder(t)

			if tt.oldApp {
				plantApp(t, folder, "the old app")
			}

			got := run(t, fake, "Darwin", "arm64",
				"CONN_INSTALL_APP_DIR="+folder, "FAKE_MV_FAILS="+tt.mvFails)
			if !got.failed {
				t.Fatalf("install succeeded, want it to fail:\n%s", got.output)
			}

			if !strings.Contains(got.output, tt.want) {
				t.Errorf("output lacks %q:\n%s", tt.want, got.output)
			}

			if strings.Contains(got.output, tt.wantNot) {
				t.Errorf("output holds %q:\n%s", tt.wantNot, got.output)
			}

			checkOldApp(t, folder, tt.wantOld)
		})
	}
}

// checkOldApp fails the test unless the old app is where it should be, and nothing but the old
// app (never the staged new one) is left beside it.
func checkOldApp(t *testing.T, folder string, want whereTheOldAppIs) {
	t.Helper()

	aside, err := filepath.Glob(filepath.Join(folder, ".Conn.app.*"))
	if err != nil {
		t.Fatal(err)
	}

	switch want {
	case nowhere:
		checkNoApp(t, folder, aside)
	case backInPlace:
		checkAppBack(t, folder, aside)
	case asideInTheFolder:
		checkAppAside(t, aside)
	}
}

func checkNoApp(t *testing.T, folder string, aside []string) {
	t.Helper()

	if exists(filepath.Join(folder, "Conn.app")) {
		t.Error("the folder holds a Conn.app")
	}

	if len(aside) != 0 {
		t.Errorf("temp files left behind: %v", aside)
	}
}

func checkAppBack(t *testing.T, folder string, aside []string) {
	t.Helper()

	if got := readText(t, filepath.Join(folder, appBinary)); got != "the old app" {
		t.Errorf("the app in place = %q, want the old one", got)
	}

	if len(aside) != 0 {
		t.Errorf("temp files left behind: %v", aside)
	}
}

func checkAppAside(t *testing.T, aside []string) {
	t.Helper()

	if len(aside) != 1 {
		t.Fatalf("the folder holds %v beside Conn.app, want only the old app", aside)
	}

	kept := readText(t, filepath.Join(aside[0], "Contents", "MacOS", "conn-app"))
	if kept != "the old app" {
		t.Errorf("the app set aside = %q, want the old one", kept)
	}
}

// TestEveryDownloadHasTimeLimits fails when a call of curl in a full install has no limit on
// connecting or on the whole transfer.
func TestEveryDownloadHasTimeLimits(t *testing.T) {
	t.Parallel()
	needUnzip(t)

	fake := macRelease(t)

	got := run(t, fake, "Darwin", "arm64", "CONN_INSTALL_APP_DIR="+appFolder(t))
	if got.failed {
		t.Fatalf("install failed: %s", got.output)
	}

	// The latest tag, the archive, its checksums, the app's checksums and the app.
	const callsMin = 5
	if len(got.curlCalls) < callsMin {
		t.Fatalf("curl was called %d times, want at least %d: %q",
			len(got.curlCalls), callsMin, got.curlCalls)
	}

	for _, call := range got.curlCalls {
		for _, flag := range []string{"--connect-timeout ", "--max-time "} {
			if !strings.Contains(call, flag) {
				t.Errorf("curl %s has no %s", call, strings.TrimSpace(flag))
			}
		}
	}
}
