//go:build unix

package pty_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// Helper modes: helperFds prints how many descriptors above stderr are terminals; helperPerm
// prints the permissions of the terminal the helper has as stdin.
const (
	helperFds  = "fds"
	helperPerm = "perm"
	// helperJob starts helperReady as a job in a process group of its own and gives it the
	// terminal, as a shell with job control does for a foreground job.
	helperJob   = "job"
	helperReady = "ready"
)

// descriptorsScanMax is how many descriptors the helper looks at.
const descriptorsScanMax = 1024

// readsMax bounds the reads of one spawned program's output.
const readsMax = 1024

// TestHelperProcess is not a test: started by helperArgs, it is a small program the real
// spawner's tests run on a terminal. It prints its answer, then waits for a line of input, so it
// is still running when the test reads what it printed.
func TestHelperProcess(t *testing.T) {
	t.Parallel()

	mode := helperMode(os.Args)
	if mode == "" {
		return
	}

	if mode == helperFds {
		printTerminalDescriptors()
	}

	if mode == helperPerm {
		printTerminalPermissions()
	}

	if mode == helperJob {
		runForegroundJob()
	}

	if mode == helperReady {
		_, _ = fmt.Fprintln(os.Stdout, "ready")
	}

	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')

	os.Exit(0)
}

// runForegroundJob runs this program as helperReady in a new process group that has the terminal
// (stdin), and ends with it.
func runForegroundJob() {
	executable, err := os.Executable()
	if err != nil {
		os.Exit(1)
	}

	job := exec.CommandContext(context.Background(), executable, "-test.run=^TestHelperProcess$",
		"--", helperReady)
	job.Stdin, job.Stdout, job.Stderr = os.Stdin, os.Stdout, os.Stderr
	job.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Foreground: true, Ctty: 0}

	_ = job.Run()

	os.Exit(0)
}

// helperMode is the word after "--" in args, or "" for a normal test run.
func helperMode(args []string) string {
	for index, arg := range args {
		if arg != "--" {
			continue
		}

		if index+1 < len(args) {
			return args[index+1]
		}
	}

	return ""
}

// printTerminalDescriptors prints each descriptor above stderr that is a character device, the
// kind a terminal is. The runtime's own descriptors (a kqueue, an epoll, a pipe) are not.
func printTerminalDescriptors() {
	count := 0

	for descriptor := 3; descriptor < descriptorsScanMax; descriptor++ {
		var status unix.Stat_t

		err := unix.Fstat(descriptor, &status)
		if err != nil {
			continue
		}

		if status.Mode&unix.S_IFMT == unix.S_IFCHR {
			count++
		}
	}

	_, _ = fmt.Fprintf(os.Stdout, "terminal descriptors: %d\n", count)
}

// printTerminalPermissions prints the permission bits of stdin, the program's terminal.
func printTerminalPermissions() {
	var status unix.Stat_t

	err := unix.Fstat(0, &status)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stdout, "terminal permissions: unknown")

		return
	}

	_, _ = fmt.Fprintf(os.Stdout, "terminal permissions: %04o\n", status.Mode&0o7777)
}

// newRealHost is a Host over the real spawner, skipping the test where it is unsupported.
func newRealHost(t *testing.T) *pty.Host {
	t.Helper()

	limits := pty.DefaultLimits()
	limits.StopGrace = testGrace
	host := pty.NewHost(pty.Config{Spawner: pty.Spawn, Limits: limits})

	t.Cleanup(func() { _ = host.StopAll(context.WithoutCancel(t.Context())) })

	return host
}

// startReal starts argv on a real terminal, skipping where terminals are unsupported.
func startReal(t *testing.T, host *pty.Host, argv, env []string) pty.ID {
	t.Helper()

	sessionID, err := host.Start(t.Context(), pty.Spec{
		Argv: argv, Dir: t.TempDir(), Env: env, Name: "real",
	})
	if errors.Is(err, pty.ErrUnsupported) {
		t.Skip("terminals are not supported here")
	}

	if err != nil {
		t.Fatal(err)
	}

	return sessionID
}

// shell is argv running script in /bin/sh.
func shell(script string) []string { return []string{"/bin/sh", "-c", script} }

// endMarker is what the scripts below print once they have printed everything else.
const endMarker = "all printed"

// untilRead runs script, then prints endMarker and waits for a line of input. macOS discards what
// a terminal's session leader printed and nobody had read yet when it exits, so a test's program
// stays alive until the test has seen everything it printed.
func untilRead(script string) []string {
	return shell(script + "; echo '" + endMarker + "'; read line")
}

// outputThrough waits until the session has printed endMarker and returns what it printed.
func outputThrough(t *testing.T, host *pty.Host, sessionID pty.ID) string {
	t.Helper()

	waitForOutput(t, host, sessionID, regexp.MustCompile(endMarker))

	viewer, err := host.Subscribe(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()

	return string(viewer.Replay)
}

// waitForOutput waits until the session's scrollback matches pattern and returns the match.
func waitForOutput(
	t *testing.T, host *pty.Host, sessionID pty.ID, pattern *regexp.Regexp,
) []string {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(waitLimit))

	for time.Now().Before(deadline) {
		viewer, err := host.Subscribe(sessionID)
		if err != nil {
			t.Fatal(err)
		}

		match := pattern.FindStringSubmatch(string(viewer.Replay))
		viewer.Close()

		if match != nil {
			return match
		}

		time.Sleep(waitPoll)
	}

	t.Fatalf("the program never printed %s", pattern)

	return nil
}

func TestRealExitCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		want   int
	}{
		{"success", "exit 0", 0},
		{"failure", "exit 3", 3},
		{"killed by a signal", "kill -KILL $$", 128 + int(unix.SIGKILL)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host := newRealHost(t)
			sessionID := startReal(t, host, shell(tt.script), nil)

			entry := waitEnded(t, host, sessionID)
			if *entry.ExitCode != tt.want {
				t.Fatalf("ExitCode = %d, want %d", *entry.ExitCode, tt.want)
			}
		})
	}
}

func TestRealOutputAndInputPassThroughTheTerminal(t *testing.T) {
	t.Parallel()

	host := newRealHost(t)
	sessionID := startReal(t, host, untilRead("echo ready; read line; echo \"got $line\""), nil)

	waitForOutput(t, host, sessionID, regexp.MustCompile(`ready`))

	err := host.Write(sessionID, []byte("hello\r"))
	if err != nil {
		t.Fatal(err)
	}

	output := outputThrough(t, host, sessionID)
	if !strings.Contains(output, "got hello") {
		t.Fatal("the program did not print the input it read")
	}
}

func TestRealResizeIsSeenByTheProgram(t *testing.T) {
	t.Parallel()

	host := newRealHost(t)
	sessionID := startReal(t, host, untilRead("stty size; read line; stty size"), nil)

	first := waitForOutput(t, host, sessionID, regexp.MustCompile(`(\d+) (\d+)`))
	if first[0] != "24 80" {
		t.Fatal("the first size the program saw was not 24 rows by 80 columns")
	}

	err := host.Resize(sessionID, 132, 43)
	if err != nil {
		t.Fatal(err)
	}

	err = host.Write(sessionID, []byte("\r"))
	if err != nil {
		t.Fatal(err)
	}

	output := outputThrough(t, host, sessionID)
	if !strings.Contains(output, "43 132") {
		t.Fatal("the program did not see the new size of 43 rows by 132 columns")
	}
}

// The environment tests never print an environment they read: a failure would copy the
// parent's, secrets and all, into the test log.
func TestRealEnvironmentHoldsNothingOfConnCmuxOrTheParent(t *testing.T) {
	t.Parallel()

	host := newRealHost(t)
	env := []string{
		"KEEP=kept", "CONN_DATA=/data", "CONN_SECRET=hunter2", "conn_port=1",
		"CMUX_SOCKET_PATH=/tmp/cmux", "Cmux_Surface=2", "TERM=xterm-ghostty",
		"COLORTERM=24bit", "TERM_PROGRAM=ghostty", "GHOSTTY_RESOURCES_DIR=/g", "TMUX=/t,1,0",
		"CLAUDECODE=1", "CLAUDE_CODE_MESSAGING_TOKEN=secret", "CLAUDE_CODE_SESSION_ID=u",
		"ANTHROPIC_API_KEY=kept-key",
	}
	sessionID := startReal(t, host, untilRead("/usr/bin/env"), env)

	output := outputThrough(t, host, sessionID)
	denied := map[string]bool{}

	for _, name := range pty.DeniedNames() {
		denied[name] = true
	}

	for line := range strings.SplitSeq(output, "\r\n") {
		name, _, _ := strings.Cut(line, "=")
		upper := strings.ToUpper(name)

		if denied[upper] {
			t.Errorf("the child's environment holds a variable named %q", name)
		}

		for _, prefix := range pty.DeniedPrefixes() {
			if strings.HasPrefix(upper, prefix) {
				t.Errorf("the child's environment holds a variable named %q", name)
			}
		}
	}

	wants := []string{
		"KEEP=kept", "ANTHROPIC_API_KEY=kept-key", "TERM=xterm-256color", "COLORTERM=truecolor",
	}
	for _, want := range wants {
		if !strings.Contains(output, want+"\r\n") {
			t.Errorf("the child's environment lacks %q", want)
		}
	}
}

func TestRealNoEnvironmentIsNotTheParents(t *testing.T) {
	t.Parallel()

	home, found := os.LookupEnv("HOME")
	if !found {
		t.Skip("HOME is not set, so there is nothing to tell apart")
	}

	host := newRealHost(t)
	sessionID := startReal(t, host, untilRead("/usr/bin/env"), nil)

	output := outputThrough(t, host, sessionID)
	if strings.Contains(output, "HOME="+home) {
		t.Fatal("a nil environment gave the child the parent's HOME")
	}
}

// Spawn filters the environment itself, whoever calls it, so a nil one never means "the
// parent's" to os/exec.
func TestSpawnAloneNeverPassesTheParentsEnvironment(t *testing.T) {
	t.Parallel()

	home, found := os.LookupEnv("HOME")
	if !found {
		t.Skip("HOME is not set, so there is nothing to tell apart")
	}

	process, err := pty.Spawn(pty.Spec{
		Argv: untilRead("/usr/bin/env"), Dir: t.TempDir(), Env: nil, Name: "spawn",
	})
	if errors.Is(err, pty.ErrUnsupported) {
		t.Skip("terminals are not supported here")
	}

	if err != nil {
		t.Fatal(err)
	}

	output := readThrough(t, process)
	if strings.Contains(output, "HOME="+home) {
		t.Fatal("Spawn with a nil environment gave the child the parent's HOME")
	}

	if !strings.Contains(output, "TERM=xterm-256color") {
		t.Fatal("Spawn did not give the child its terminal type")
	}
}

// readThrough reads a spawned program's output until it has printed endMarker, then stops it.
func readThrough(t *testing.T, process pty.Process) string {
	t.Helper()

	defer func() {
		_ = process.Kill()
		_ = process.Close()
		process.Wait()
	}()

	var output []byte

	buffer := make([]byte, pty.ReadBufferBytes)

	for range readsMax {
		count, err := process.Read(buffer)
		output = append(output, buffer[:count]...)

		if strings.Contains(string(output), endMarker) {
			return string(output)
		}

		if err != nil {
			t.Fatal("the program ended before it printed everything")
		}
	}

	t.Fatal("the program never printed everything")

	return ""
}

// What Start accepts, the real spawner starts: both bound the environment the program gets.
func TestRealSpawnStartsAnEnvironmentAtTheLimit(t *testing.T) {
	t.Parallel()

	host := newRealHost(t)
	count := 4096 - pty.ReplacedCount()
	env := make([]string, 0, count)

	for index := range count {
		env = append(env, "V"+strconv.Itoa(index)+"=1")
	}

	startReal(t, host, shell("exit 0"), env)
}

func TestRealDescriptorsAreCloseOnExec(t *testing.T) {
	t.Parallel()

	host := newRealHost(t)
	sessionID := startReal(t, host, helperArgs(t, helperFds), nil)

	match := waitForOutput(t, host, sessionID, regexp.MustCompile(`terminal descriptors: (\d+)`))
	if match[1] != "0" {
		t.Fatalf("the child inherited %s terminals beyond stdin, stdout and stderr", match[1])
	}
}

// The terminal's program side is its owner's alone, as after mesg n: no other user may write to
// it.
func TestRealTerminalIsTheOwnersOnly(t *testing.T) {
	t.Parallel()

	host := newRealHost(t)
	sessionID := startReal(t, host, helperArgs(t, helperPerm), nil)

	permissions := regexp.MustCompile(`terminal permissions: ([0-7]{4})`)

	match := waitForOutput(t, host, sessionID, permissions)
	if match[1] != "0600" {
		t.Fatalf("the terminal's permissions are %s, want 0600", match[1])
	}
}

// A program that is not Go, with no runtime of its own, holds no more than stdin, stdout and
// stderr: ls lists /dev/fd in the session exactly as it does when os/exec starts it with nothing
// but those three (ls opens one or two of its own to read the folder).
func TestRealNonGoChildHoldsOnlyItsTerminal(t *testing.T) {
	t.Parallel()

	baseline, err := exec.CommandContext(t.Context(), "/bin/ls", "-1", "/dev/fd").Output()
	if err != nil {
		t.Fatal(err)
	}

	host := newRealHost(t)
	sessionID := startReal(t, host, untilRead("/bin/ls -1 /dev/fd"), nil)

	output := outputThrough(t, host, sessionID)
	listing, _, _ := strings.Cut(output, endMarker)

	got := strings.Fields(listing)

	want := strings.Fields(string(baseline))
	if !slices.Equal(got, want) {
		// Counts only: what a program printed is never copied into the test log.
		t.Fatalf("ls in the session listed %d descriptors, want the %d it lists with stdio alone",
			len(got), len(want))
	}
}

// A leader that exits by itself does not leave its group behind: when the session has ended,
// a grandchild that ignores the hang-up and the polite stop is gone too.
func TestRealGroupOfALeaderThatExitsIsGone(t *testing.T) {
	t.Parallel()

	host := newRealHost(t)
	sessionID := startReal(t, host,
		shell("trap '' HUP TERM; (trap '' HUP TERM; exec /bin/sleep 30) & exit 0"), nil)

	entry := waitEnded(t, host, sessionID)

	// The leader called setsid, so its group is numbered by its pid, and a number is not reused
	// while a group holds it. The killed grandchild may be a zombie for a moment, until init
	// reaps it.
	waitGone(t, -entry.PID)
}

func TestRealStopKillsTheWholeGroup(t *testing.T) {
	t.Parallel()

	// Each grandchild ignores SIGTERM and SIGHUP; exec keeps an ignored signal ignored.
	grandchildren := "(trap '' TERM HUP; exec sleep 600) & echo \"pid $!\"; " +
		"(trap '' TERM HUP; exec sleep 600) & echo \"pid $!\"; "

	tests := []struct {
		name   string
		script string
	}{
		{"the leader ignores the polite stop too", "trap '' TERM HUP; " + grandchildren + "wait"},
		{"the leader obeys it and leaves the rest", grandchildren + "wait"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host := newRealHost(t)
			sessionID := startReal(t, host, shell(tt.script), nil)
			pids := grandchildPIDs(t, host, sessionID)

			err := host.Stop(t.Context(), sessionID)
			if err != nil {
				t.Fatal(err)
			}

			entry, _ := info(t, host, sessionID)
			if entry.ExitCode == nil {
				t.Fatal("Stop returned before the leader ended")
			}

			for _, pid := range append(pids, entry.PID) {
				waitGone(t, pid)
			}
		})
	}
}

// grandchildPIDs waits until the script has printed both of its grandchildren's ids.
func grandchildPIDs(t *testing.T, host *pty.Host, sessionID pty.ID) []int {
	t.Helper()

	match := waitForOutput(t, host, sessionID, regexp.MustCompile(`pid (\d+)\r\npid (\d+)\r\n`))

	pids := make([]int, 0, len(match)-1)

	for _, text := range match[1:] {
		pid, err := strconv.Atoi(text)
		if err != nil {
			t.Fatal(err)
		}

		pids = append(pids, pid)
	}

	return pids
}

// waitGone waits until no process has pid (or, negative, no process is in the group -pid). A
// killed grandchild is reaped by init, so it may be a zombie for a moment.
func waitGone(t *testing.T, pid int) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(waitLimit))

	for time.Now().Before(deadline) {
		err := unix.Kill(pid, 0)
		if errors.Is(err, unix.ESRCH) {
			return
		}

		time.Sleep(waitPoll)
	}

	_ = unix.Kill(pid, unix.SIGKILL)

	t.Fatalf("process %d survived the stop", pid)
}

// helperArgs is how a test starts this test binary as a helper program doing mode.
func helperArgs(t *testing.T, mode string) []string {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	return []string{executable, "-test.run=^TestHelperProcess$", "--", mode}
}

// A write that checks the screen is refused while a program the session's program started has
// the terminal (bash with job control gives its foreground job the terminal), and goes on to the
// screen check while the session's own program has it.
func TestRealCheckedWriteNeedsTheProgramInTheForeground(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		argv    []string
		wantErr error
	}{
		{"the program itself", shell("echo ready; read line"), pty.ErrScreenChanged},
		{"a job it started", nil, pty.ErrNotForeground},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			argv := tt.argv
			if argv == nil {
				argv = helperArgs(t, helperJob)
			}

			host := newRealHost(t)
			sessionID := startReal(t, host, argv, []string{"PATH=/usr/bin:/bin"})
			waitForOutput(t, host, sessionID, regexp.MustCompile("ready"))

			// The check never passes, so nothing is typed: the answer says which check refused.
			never := func([]string) bool { return false }
			deadline := time.Now().Add(testtime.Scaled(waitLimit))

			var err error

			for time.Now().Before(deadline) {
				_, mark, readErr := host.ScreenLines(sessionID, readScreenLines)
				if readErr != nil {
					t.Fatal(readErr)
				}

				err = host.WriteIfStill(sessionID, mark, readScreenLines, never, []byte("\n"))
				if errors.Is(err, tt.wantErr) {
					return
				}

				time.Sleep(waitPoll)
			}

			t.Fatalf("WriteIfStill = %v, want %v", err, tt.wantErr)
		})
	}
}
