package installscript_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
)

// release is a fake GitHub releases address: /latest redirects to the tag's page, and the files
// are under /download/<tag>/.
type release struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []string
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

func sumOf(data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

// newRelease serves archive for every platform's file name; checksums is the text of
// checksums.txt, built by the test so it can be made wrong.
func newRelease(t *testing.T, archive []byte, checksums func(name string) string) *release {
	t.Helper()

	fake := &release{}
	mux := http.NewServeMux()

	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		fake.record(r.URL.Path)
		http.Redirect(w, r, "/tag/"+releaseTag, http.StatusFound)
	})
	mux.HandleFunc("/tag/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "the release page")
	})
	mux.HandleFunc("/download/"+releaseTag+"/", func(w http.ResponseWriter, r *http.Request) {
		fake.record(r.URL.Path)

		name := filepath.Base(r.URL.Path)
		if name == "checksums.txt" {
			platforms := []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"}
			lines := make([]string, 0, len(platforms))

			for _, platform := range platforms {
				lines = append(lines, checksums("conn_"+platform+".tar.gz"))
			}

			_, _ = fmt.Fprint(w, strings.Join(lines, "\n")+"\n")

			return
		}

		if !strings.HasPrefix(name, "conn_") {
			http.NotFound(w, r)

			return
		}

		_, _ = w.Write(archive)
	})

	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)

	return fake
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

// run runs install.sh with a fresh HOME, a fake uname answering os and machine, and extra
// environment as KEY=value pairs.
func run(t *testing.T, fake *release, osName, machine string, extra ...string) outcome {
	t.Helper()

	home := t.TempDir()
	tools := t.TempDir()

	uname := "#!/bin/sh\ncase \"$1\" in\n-s) echo " + osName + " ;;\n-m) echo " + machine +
		" ;;\nesac\n"

	err := os.WriteFile(filepath.Join(tools, "uname"), []byte(uname), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(runTimeout))
	defer cancel()

	command := exec.CommandContext(ctx, "sh", "../../install.sh")

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
