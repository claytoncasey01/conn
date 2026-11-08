package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/desk"
	"github.com/claytoncasey01/conn/internal/instance"
	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// childServe makes this test binary a `conn serve` when it is started as a child: the only way to
// test `conn stop`, which sends the server a signal.
const childServe = "CONN_TEST_CHILD_SERVE"

func TestMain(m *testing.M) {
	if os.Getenv(childServe) == "1" {
		os.Exit(realMain([]string{"serve"}, os.LookupEnv, os.Stdout, false, os.Stderr))
	}

	os.Exit(m.Run())
}

// startChild runs `conn serve` on dataDir in a child process, with extra over its environment,
// and waits until it says where it listens.
func startChild(t *testing.T, dataDir string, extra ...string) (*exec.Cmd, string) {
	t.Helper()

	child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	child.Env = append(os.Environ(), childServe+"=1", "CONN_PORT=0",
		"CONN_DATA="+dataDir, "CONN_DIST="+t.TempDir())
	child.Env = append(child.Env, extra...)
	child.Stdout = io.Discard
	child.Stderr = io.Discard

	err := child.Start()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})

	return child, childURL(t, dataDir)
}

// childURL waits until the child holds dataDir and has said where it listens.
func childURL(t *testing.T, dataDir string) string {
	t.Helper()

	holder, said := waitHolder(t, dataDir, func(holder instance.Holder, running bool) bool {
		if !running {
			return false
		}

		return holder.URL != ""
	})
	if !said {
		t.Fatal("the child never said where it listens")
	}

	return holder.URL
}

// lockWait bounds how long a test waits for a data folder's lock to show what it expects, and
// lockPoll is how often it looks.
const (
	lockWait = 5 * time.Second
	lockPoll = 10 * time.Millisecond
)

// waitHolder looks at dataDir's lock until done accepts what it sees or lockWait passes. It
// returns the last holder it saw, and whether done accepted it.
func waitHolder(
	t *testing.T, dataDir string, done func(holder instance.Holder, running bool) bool,
) (instance.Holder, bool) {
	t.Helper()

	var last instance.Holder

	deadline := time.Now().Add(testtime.Scaled(lockWait))

	for time.Now().Before(deadline) {
		holder, running, err := instance.Running(dataDir)
		if err != nil {
			t.Fatal(err)
		}

		last = holder

		if done(holder, running) {
			return holder, true
		}

		time.Sleep(lockPoll)
	}

	return last, false
}

func TestStopStopsTheServerOnItsDataFolder(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	child, url := startChild(t, dataDir)
	env := lookupFrom(map[string]string{"CONN_DATA": dataDir, "CONN_PORT": "0"})

	var stdout, stderr bytes.Buffer

	code := serve(t.Context(), env, &stdout, false, &stderr)
	if code != exitOK {
		t.Fatalf("a second serve = %d %q, want 0", code, stderr.String())
	}

	if stdout.String() != "conn is already running on "+url+"\n"+lockLine(url, dataDir) {
		t.Errorf("a second serve said %q, want the first one's address %s", stdout.String(), url)
	}

	stdout.Reset()

	code = serve(t.Context(), env, &stdout, true, &stderr)
	if code != exitOK {
		t.Fatalf("a second serve on a terminal = %d %q, want 0", code, stderr.String())
	}

	holder, _ := waitHolder(t, dataDir, func(instance.Holder, bool) bool { return true })
	if stdout.String() != "conn is already running on "+desk.PageURL(url, holder.Secret)+"\n" {
		t.Errorf("a second serve on a terminal said %q, want the first one's page", stdout.String())
	}

	stdout.Reset()

	code = stop(t.Context(), env, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("stop = %d %q", code, stderr.String())
	}

	if stdout.String() != "conn stopped\n" {
		t.Errorf("stop said %q", stdout.String())
	}

	err := child.Wait()
	if err != nil {
		t.Errorf("the stopped server exited with %v, want 0", err)
	}

	stdout.Reset()

	code = stop(t.Context(), env, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("a second stop = %d", code)
	}

	if stdout.String() != "conn not running\n" {
		t.Errorf("a second stop said %q", stdout.String())
	}
}

func TestStopWithNothingRunning(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	dataDir := filepath.Join(t.TempDir(), "never-made")

	code := stop(t.Context(), lookupFrom(map[string]string{"CONN_DATA": dataDir}), &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("stop = %d %q", code, stderr.String())
	}

	if stdout.String() != "conn not running\n" {
		t.Errorf("stop said %q", stdout.String())
	}

	_, err := os.Stat(dataDir)
	if err == nil {
		t.Error("stop made the data folder")
	}
}

func TestServeSaysAPortIsTaken(t *testing.T) {
	t.Parallel()

	var listenConfig net.ListenConfig

	taken, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = taken.Close() }()

	address, isTCP := taken.Addr().(*net.TCPAddr)
	if !isTCP {
		t.Fatalf("listening gave %T, want a TCP address", taken.Addr())
	}

	portNumber := strconv.Itoa(address.Port)
	env := lookupFrom(map[string]string{"CONN_DATA": t.TempDir(), "CONN_PORT": portNumber})

	var stdout, stderr bytes.Buffer

	code := serve(t.Context(), env, &stdout, false, &stderr)
	if code != exitError {
		t.Fatalf("serve = %d, want 1", code)
	}

	want := "conn: port " + portNumber +
		" is in use by another program: set CONN_PORT to a free port\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestServeSaysWhatSettingsJSONDroppedAndServesTheSettings(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()

	err := os.WriteFile(filepath.Join(dataDir, "settings.json"), []byte("{not json"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())

	var stdout, stderr syncBuffer

	env := lookupFrom(map[string]string{
		"CONN_PORT": "0", "CONN_DATA": dataDir, "CONN_DIST": t.TempDir(),
	})
	exited := make(chan int, 1)

	go func() { exited <- serve(ctx, env, &stdout, true, &stderr) }()

	page := waitForPage(t, &stdout)

	body := fetch(t, page.address+"/api/settings", page.key)
	if !strings.Contains(body, `"setup":true`) {
		t.Errorf("GET /api/settings = %s, want setup", body)
	}

	cancel()
	<-exited

	if !strings.HasPrefix(stderr.String(), "conn: settings.json: ") {
		t.Errorf("stderr = %q, want what settings.json lost", stderr.String())
	}

	folderReleased(t, dataDir)
}

// folderReleased fails the test unless dataDir is free within lockWait. A lock lasts until
// every copy of its file descriptor is closed, and a process another parallel test is starting
// holds a copy between fork and exec, so the folder can read as held for a moment after the
// server that held it has closed its file.
func folderReleased(t *testing.T, dataDir string) {
	t.Helper()

	holder, released := waitHolder(t, dataDir, func(_ instance.Holder, running bool) bool {
		return !running
	})
	if !released {
		t.Errorf("the folder is still held by %+v after serve returned", holder)
	}
}

func fetch(t *testing.T, url string, key secret.Secret) string {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}

	request.Header.Set(secret.Header, key.Authorization())

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = response.Body.Close() }()

	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}
