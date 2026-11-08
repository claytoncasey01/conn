package pty_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// errSpawnFailed is a spawner that cannot start the program.
var errSpawnFailed = errors.New("no such program")

// testGrace is a stop grace short enough for a test to wait it out.
const testGrace = 200 * time.Millisecond

func TestDefaultLimitsAreTheDesignedOnes(t *testing.T) {
	t.Parallel()

	want := pty.Limits{
		SessionsMax:        12,
		ScrollbackBytesMax: 4 << 20,
		ViewersMax:         4,
		StopGrace:          5 * time.Second,
	}

	got := pty.DefaultLimits()
	if got != want {
		t.Fatalf("DefaultLimits() = %+v, want %+v", got, want)
	}
}

func TestNewHostPanicsOnLimitsOutOfRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(limits *pty.Limits)
	}{
		{"no sessions", func(limits *pty.Limits) { limits.SessionsMax = 0 }},
		{"too many sessions", func(limits *pty.Limits) { limits.SessionsMax = 13 }},
		{"no scrollback", func(limits *pty.Limits) { limits.ScrollbackBytesMax = 0 }},
		{"too much scrollback", func(limits *pty.Limits) { limits.ScrollbackBytesMax = 4<<20 + 1 }},
		{"no viewers", func(limits *pty.Limits) { limits.ViewersMax = 0 }},
		{"too many viewers", func(limits *pty.Limits) { limits.ViewersMax = 5 }},
		{"no grace", func(limits *pty.Limits) { limits.StopGrace = 0 }},
		{"too long a grace", func(limits *pty.Limits) { limits.StopGrace = 6 * time.Second }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			limits := pty.DefaultLimits()
			tt.change(&limits)

			defer func() {
				if recover() == nil {
					t.Fatal("NewHost accepted limits out of range")
				}
			}()

			pty.NewHost(pty.Config{Spawner: (&fakes{}).spawn, Limits: limits})
		})
	}
}

func TestNewHostPanicsWithoutASpawner(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("NewHost accepted no Spawner")
		}
	}()

	pty.NewHost(pty.Config{Spawner: nil, Limits: pty.DefaultLimits()})
}

func TestStartRefusesPastSessionsMax(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	var first pty.ID

	for index := range pty.DefaultLimits().SessionsMax {
		sessionID, err := host.Start(t.Context(), testSpec(t))
		if err != nil {
			t.Fatalf("start %d: %v", index, err)
		}

		if index == 0 {
			first = sessionID
		}
	}

	_, err := host.Start(t.Context(), testSpec(t))
	if !errors.Is(err, pty.ErrSessionsFull) {
		t.Fatalf("start past the limit = %v, want ErrSessionsFull", err)
	}

	spawner.started[0].finish(0)
	waitEnded(t, host, first)

	_, err = host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatalf("start after one ended = %v, want a session", err)
	}
}

func TestStartReleasesTheSlotWhenTheSpawnerFails(t *testing.T) {
	t.Parallel()

	spawner := &fakes{err: errSpawnFailed}
	limits := pty.DefaultLimits()
	limits.SessionsMax = 1
	host := pty.NewHost(pty.Config{Spawner: spawner.spawn, Limits: limits})

	for range 3 {
		_, err := host.Start(t.Context(), testSpec(t))
		if !errors.Is(err, errSpawnFailed) {
			t.Fatalf("Start = %v, want the spawner's error", err)
		}
	}
}

func TestStartChecksTheSpec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		change   func(spec *pty.Spec)
		problems int
	}{
		{"no argv", func(spec *pty.Spec) { spec.Argv = nil }, 1},
		{"a relative program", func(spec *pty.Spec) { spec.Argv[0] = "claude" }, 1},
		{"too many arguments", func(spec *pty.Spec) { spec.Argv = make([]string, 257) }, 2},
		{"a relative folder", func(spec *pty.Spec) { spec.Dir = "work" }, 1},
		{"no folder", func(spec *pty.Spec) { spec.Dir = "" }, 1},
		{"no name", func(spec *pty.Spec) { spec.Name = "" }, 1},
		{"too long a name", func(spec *pty.Spec) { spec.Name = string(make([]byte, 257)) }, 1},
		{"too many variables", func(spec *pty.Spec) { spec.Env = variables(4097) }, 1},
		{"too many variables once TERM and COLORTERM are added", func(spec *pty.Spec) {
			spec.Env = variables(4097 - pty.ReplacedCount())
		}, 1},
		{"every problem at once", func(spec *pty.Spec) {
			spec.Argv = nil
			spec.Dir = ""
			spec.Name = ""
		}, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host, spawner := newFakeHost(t, pty.DefaultLimits())
			spec := testSpec(t)
			tt.change(&spec)

			_, err := host.Start(t.Context(), spec)
			if !errors.Is(err, pty.ErrInvalidSpec) {
				t.Fatalf("Start = %v, want ErrInvalidSpec", err)
			}

			var joined interface{ Unwrap() []error }
			if !errors.As(err, &joined) {
				t.Fatalf("Start = %v, want every problem joined", err)
			}

			if len(joined.Unwrap()) != tt.problems {
				t.Fatalf("Start reported %d problems (%v), want %d",
					len(joined.Unwrap()), err, tt.problems)
			}

			if len(spawner.started) != 0 {
				t.Fatal("an invalid spec was spawned")
			}
		})
	}
}

// variables is count distinct environment entries, none of them TERM.
func variables(count int) []string {
	entries := make([]string, 0, count)
	for index := range count {
		entries = append(entries, fmt.Sprintf("V%d=1", index))
	}

	return entries
}

// The limit bounds the environment the program gets, TERM and COLORTERM included.
func TestStartAcceptsAnEnvironmentAtTheLimit(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())
	spec := testSpec(t)
	spec.Env = variables(4096 - pty.ReplacedCount())

	_, err := host.Start(t.Context(), spec)
	if err != nil {
		t.Fatalf("Start = %v, want a session", err)
	}

	got := len(spawner.lastSpec(t).Env)
	if got != 4096 {
		t.Fatalf("the spawner got %d variables, want 4096", got)
	}
}

func TestStartHandsTheSpawnerACleanEnvironment(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())
	spec := testSpec(t)
	spec.Env = []string{"KEEP=1", "CONN_DATA=/data", "CMUX_SOCKET=/s"}

	_, err := host.Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}

	got := spawner.lastSpec(t).Env

	want := pty.ChildEnv(spec.Env)
	if !slices.Equal(got, want) {
		t.Fatalf("the spawner got %q, want %q", got, want)
	}
}

func TestStartRefusesACancelledContext(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := host.Start(ctx, testSpec(t))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start = %v, want context.Canceled", err)
	}

	if len(spawner.started) != 0 {
		t.Fatal("a cancelled Start spawned a program")
	}
}

func TestIDsAreRandomAndLongEnough(t *testing.T) {
	t.Parallel()

	const idsCount = 1000

	seen := make(map[pty.ID]bool, idsCount)

	for range idsCount {
		sessionID, err := pty.NewID()
		if err != nil {
			t.Fatal(err)
		}

		raw, err := hex.DecodeString(string(sessionID))
		if err != nil {
			t.Fatalf("id %q is not hex: %v", sessionID, err)
		}

		if len(raw) < 16 {
			t.Fatalf("id %q holds %d bytes, want at least 16 (128 bits)", sessionID, len(raw))
		}

		if seen[sessionID] {
			t.Fatalf("id %q came twice", sessionID)
		}

		seen[sessionID] = true

		parsed, err := pty.ParseID(string(sessionID))
		if err != nil {
			t.Fatalf("ParseID refused an id the host made: %v", err)
		}

		if parsed != sessionID {
			t.Fatal("ParseID changed an id the host made")
		}
	}
}

// ParseID takes only the shape the host makes, so a client's text that is anything else is
// refused before any session is looked up.
func TestParseIDRefusesEverythingElse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
	}{
		{"empty", ""},
		{"one short", strings.Repeat("a", 31)},
		{"one long", strings.Repeat("a", 33)},
		{"capitals", strings.Repeat("A", 32)},
		{"not hex", strings.Repeat("g", 32)},
		{"a slash", strings.Repeat("a", 31) + "/"},
		{"an escape", "%61" + strings.Repeat("a", 29)},
		{"a dot", strings.Repeat("a", 31) + "."},
		{"a space", strings.Repeat("a", 31) + " "},
		{"a multibyte letter", strings.Repeat("a", 30) + "é"},
		{"twice as long", strings.Repeat("ab", 32)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := pty.ParseID(tt.text)
			if !errors.Is(err, pty.ErrInvalidID) {
				t.Fatalf("ParseID = %v, want ErrInvalidID", err)
			}
		})
	}
}

func TestScrollbackKeepsTheNewestBytes(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	process := spawner.last(t)

	var everything []byte

	scrollbackBytesMax := pty.DefaultLimits().ScrollbackBytesMax
	for index := 0; len(everything) < scrollbackBytesMax+scrollbackBytesMax/4; index++ {
		chunk := bytes.Repeat([]byte{byte(index)}, pty.ReadBufferBytes-index%7)
		everything = append(everything, chunk...)
		process.feed(t, chunk)
	}

	process.finish(0)
	waitEnded(t, host, sessionID)

	viewer, err := host.Subscribe(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()

	want := everything[len(everything)-scrollbackBytesMax:]
	if !bytes.Equal(viewer.Replay, want) {
		t.Fatalf("replay holds %d bytes, want the newest %d", len(viewer.Replay), len(want))
	}
}

func TestAViewerSeesTheReplayThenLiveOutput(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	process := spawner.last(t)
	process.feed(t, []byte("before "))

	viewer := subscribeAfter(t, host, sessionID, []byte("before "))
	defer viewer.Close()

	process.feed(t, []byte("after"))

	got := receive(t, viewer.Output)
	if string(got) != "after" {
		t.Fatalf("live output = %q, want %q", got, "after")
	}
}

// subscribeAfter subscribes to sessionID once its scrollback holds want.
func subscribeAfter(t *testing.T, host *pty.Host, sessionID pty.ID, want []byte) pty.Viewer {
	t.Helper()

	deadline := time.Now().Add(waitLimit)

	for time.Now().Before(deadline) {
		viewer, err := host.Subscribe(sessionID)
		if err != nil {
			t.Fatal(err)
		}

		if bytes.Equal(viewer.Replay, want) {
			return viewer
		}

		viewer.Close()
		time.Sleep(waitPoll)
	}

	t.Fatalf("the scrollback never held %q", want)

	return pty.Viewer{}
}

func TestASlowViewerIsDroppedAndTheReaderGoesOn(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	process := spawner.last(t)

	slow, err := host.Subscribe(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()

	fast, err := host.Subscribe(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer fast.Close()

	// Twice the queue, in the largest chunks a read makes.
	chunk := bytes.Repeat([]byte{'x'}, pty.ReadBufferBytes)
	total := 2 * pty.ViewerQueueBytesMax

	for sent := 0; sent < total; sent += len(chunk) {
		process.feed(t, chunk)

		got := receiveBytes(t, fast.Output, len(chunk))
		if !bytes.Equal(got, chunk) {
			t.Fatalf("the fast viewer got %d bytes, want %d", len(got), len(chunk))
		}
	}

	queued := drainUntilClosed(t, slow.Output)
	if len(queued) > pty.ViewerQueueBytesMax {
		t.Fatalf("the slow viewer was handed %d bytes, more than its queue of %d",
			len(queued), pty.ViewerQueueBytesMax)
	}

	entry, _ := info(t, host, sessionID)
	if entry.ExitCode != nil {
		t.Fatal("dropping a viewer ended the session")
	}
}

// A viewer's queue is counted in bytes: many small writes, like a burst of echoed keystrokes,
// do not fill it.
func TestABurstOfSmallOutputKeepsAViewer(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	process := spawner.last(t)

	viewer, err := host.Subscribe(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()

	const echoes = 4096

	want := make([]byte, 0, echoes)

	for index := range echoes {
		echo := []byte{byte('a' + index%26)}
		want = append(want, echo...)
		process.feed(t, echo)
	}

	got := receiveBytes(t, viewer.Output, len(want))
	if !bytes.Equal(got, want) {
		t.Fatalf("the viewer got %d bytes of the burst, want all %d in order", len(got), len(want))
	}
}

// receiveBytes reads output until it has count bytes.
func receiveBytes(t *testing.T, output <-chan []byte, count int) []byte {
	t.Helper()

	got := make([]byte, 0, count)
	for len(got) < count {
		got = append(got, receive(t, output)...)
	}

	return got
}

func TestSubscribeRefusesPastViewersMax(t *testing.T) {
	t.Parallel()

	host, _ := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	viewers := make([]pty.Viewer, 0, pty.DefaultLimits().ViewersMax)

	for range pty.DefaultLimits().ViewersMax {
		viewer, err := host.Subscribe(sessionID)
		if err != nil {
			t.Fatal(err)
		}

		viewers = append(viewers, viewer)
	}

	_, err = host.Subscribe(sessionID)
	if !errors.Is(err, pty.ErrViewersFull) {
		t.Fatalf("Subscribe past the limit = %v, want ErrViewersFull", err)
	}

	viewers[0].Close()
	viewers[0].Close()

	again, err := host.Subscribe(sessionID)
	if err != nil {
		t.Fatalf("Subscribe after a Close = %v, want a viewer", err)
	}

	again.Close()
}

func TestAViewersOutputClosesWhenTheSessionEnds(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	viewer, err := host.Subscribe(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()

	process := spawner.last(t)
	process.feed(t, []byte("bye"))
	process.finish(0)

	got := drainUntilClosed(t, viewer.Output)
	if string(got) != "bye" {
		t.Fatalf("the viewer got %q before the end, want %q", got, "bye")
	}

	late, err := host.Subscribe(sessionID)
	if err != nil {
		t.Fatalf("Subscribe to an ended session = %v, want its replay", err)
	}

	if string(late.Replay) != "bye" {
		t.Fatalf("an ended session's replay = %q, want %q", late.Replay, "bye")
	}

	_, open := <-late.Output
	if open {
		t.Fatal("an ended session's viewer output is open")
	}
}

func TestWriteReachesTheProgram(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	err = host.Write(sessionID, []byte("yes\r"))
	if err != nil {
		t.Fatal(err)
	}

	got := spawner.last(t).writtenSoFar()
	if string(got) != "yes\r" {
		t.Fatalf("the program got %q, want %q", got, "yes\r")
	}
}

// Writes to one session take turns, and one that cannot get its turn in time gives up.
func TestWritesToASessionTakeTurns(t *testing.T) {
	t.Parallel()

	spawner := &fakes{writeGate: make(chan struct{})}
	host := pty.NewHost(pty.Config{Spawner: spawner.spawn, Limits: pty.DefaultLimits()})

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	first := make(chan error, 1)

	go func() { first <- host.Write(sessionID, []byte("a")) }()

	waitWriting(t, spawner.last(t))

	began := time.Now()

	err = host.Write(sessionID, []byte("b"))
	if !errors.Is(err, pty.ErrInputBlocked) {
		t.Fatalf("a Write behind a stuck one = %v, want ErrInputBlocked", err)
	}

	waited := time.Since(began)
	if waited > pty.WriteWaitMax+testGrace {
		t.Fatalf("the Write waited %v, more than %v", waited, pty.WriteWaitMax)
	}

	close(spawner.writeGate)

	err = <-first
	if err != nil {
		t.Fatal(err)
	}

	if spawner.last(t).mostWritersAtOnce() != 1 {
		t.Fatal("two Writes reached the program at once")
	}

	_ = host.StopAll(context.WithoutCancel(t.Context()))
}

// waitWriting waits until a Write is inside the program.
func waitWriting(t *testing.T, process *fakeProcess) {
	t.Helper()

	deadline := time.Now().Add(testtime.Scaled(waitLimit))

	for time.Now().Before(deadline) {
		if process.mostWritersAtOnce() > 0 {
			return
		}

		time.Sleep(waitPoll)
	}

	t.Fatal("the Write never reached the program")
}

func TestWriteRefusesMoreThanAFrame(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	err = host.Write(sessionID, make([]byte, pty.WriteBytesMax+1))
	if !errors.Is(err, pty.ErrInputTooLarge) {
		t.Fatalf("Write = %v, want ErrInputTooLarge", err)
	}

	if len(spawner.last(t).writtenSoFar()) != 0 {
		t.Fatal("oversize input reached the program")
	}
}

func TestResizeReachesTheProgram(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	err = host.Resize(sessionID, 120, 40)
	if err != nil {
		t.Fatal(err)
	}

	want := []pty.Size{{Columns: 120, Rows: 40}}

	got := spawner.last(t).sizesSoFar()
	if !slices.Equal(got, want) {
		t.Fatalf("the program got sizes %v, want %v", got, want)
	}
}

func TestResizeRefusesSizesOutOfRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		columns, rows uint16
	}{
		{"no columns", 0, 24},
		{"no rows", 80, 0},
		{"too many columns", pty.ColumnsMax + 1, 24},
		{"too many rows", 80, pty.RowsMax + 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			host, spawner := newFakeHost(t, pty.DefaultLimits())

			sessionID, err := host.Start(t.Context(), testSpec(t))
			if err != nil {
				t.Fatal(err)
			}

			err = host.Resize(sessionID, tt.columns, tt.rows)
			if !errors.Is(err, pty.ErrInvalidSize) {
				t.Fatalf("Resize = %v, want ErrInvalidSize", err)
			}

			if len(spawner.last(t).sizesSoFar()) != 0 {
				t.Fatal("a size out of range reached the program")
			}
		})
	}
}

func TestAnEndedSessionTakesNoInput(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	spawner.last(t).finish(3)
	waitEnded(t, host, sessionID)

	err = host.Write(sessionID, []byte("x"))
	if !errors.Is(err, pty.ErrEnded) {
		t.Fatalf("Write after the end = %v, want ErrEnded", err)
	}

	err = host.Resize(sessionID, 80, 24)
	if !errors.Is(err, pty.ErrEnded) {
		t.Fatalf("Resize after the end = %v, want ErrEnded", err)
	}
}

func TestUnknownIDsAreRefused(t *testing.T) {
	t.Parallel()

	host, _ := newFakeHost(t, pty.DefaultLimits())

	const unknown = pty.ID("00000000000000000000000000000000")

	tests := []struct {
		name string
		call func() error
	}{
		{"write", func() error { return host.Write(unknown, []byte("x")) }},
		{"resize", func() error { return host.Resize(unknown, 80, 24) }},
		{"stop", func() error { return host.Stop(t.Context(), unknown) }},
		{"subscribe", func() error {
			_, err := host.Subscribe(unknown)

			return fmt.Errorf("subscribing: %w", err)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.call()
			if !errors.Is(err, pty.ErrNoSession) {
				t.Fatalf("%s = %v, want ErrNoSession", tt.name, err)
			}
		})
	}
}

func TestLiveReportsEachSession(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())
	spec := testSpec(t)
	before := time.Now()

	sessionID, err := host.Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}

	entry, found := info(t, host, sessionID)
	if !found {
		t.Fatal("the session is not in Live")
	}

	want := pty.Info{
		ID: sessionID, Name: spec.Name, Dir: spec.Dir, PID: spawner.last(t).pid,
		StartedAt: entry.StartedAt, ExitCode: nil,
	}
	if entry != want {
		t.Fatalf("Live = %+v, want %+v", entry, want)
	}

	if entry.StartedAt.Before(before) {
		t.Fatalf("StartedAt %v is before the Start at %v", entry.StartedAt, before)
	}

	spawner.last(t).finish(3)

	ended := waitEnded(t, host, sessionID)
	if *ended.ExitCode != 3 {
		t.Fatalf("ExitCode = %d, want 3", *ended.ExitCode)
	}
}

func TestEndedSessionsAreBounded(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	ids := make([]pty.ID, 0, pty.EndedMax+3)

	for range pty.EndedMax + 3 {
		sessionID, err := host.Start(t.Context(), testSpec(t))
		if err != nil {
			t.Fatal(err)
		}

		spawner.last(t).finish(0)
		waitEnded(t, host, sessionID)

		ids = append(ids, sessionID)
	}

	live := host.Live()
	if len(live) != pty.EndedMax {
		t.Fatalf("Live holds %d ended sessions, want %d", len(live), pty.EndedMax)
	}

	_, found := info(t, host, ids[0])
	if found {
		t.Fatal("the oldest ended session was kept")
	}

	_, found = info(t, host, ids[len(ids)-1])
	if !found {
		t.Fatal("the newest ended session was forgotten")
	}
}

// A replay starts with the modes the program's screen is in, so a viewer that joins after the
// sequences that set them have left the scrollback still has them: a paste stays bracketed, the
// mouse and the cursor keys stay as the program asked.
func TestAReplayStartsWithTheModesTheProgramSet(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	process := spawner.last(t)
	process.feed(t, []byte("\x1b[?2004h\x1b[?1h\x1b[?1003;1006h"))

	// Enough plain output that the mode switches leave the scrollback.
	scrollbackBytesMax := pty.DefaultLimits().ScrollbackBytesMax

	var everything []byte
	for len(everything) < scrollbackBytesMax+pty.ReadBufferBytes {
		chunk := bytes.Repeat([]byte{'x'}, pty.ReadBufferBytes)
		everything = append(everything, chunk...)
		process.feed(t, chunk)
	}

	const preamble = "\x1b[?1h\x1b[?2004h\x1b[?1003h\x1b[?1006h"

	want := append([]byte(preamble), everything[len(everything)-scrollbackBytesMax:]...)

	viewer := subscribeAfter(t, host, sessionID, want)
	viewer.Close()

	// Once the program has turned them off, a replay holds no modes: exactly the newest output.
	const off = "\x1b[?2004l\x1b[?1l\x1b[?1003l\x1b[?1006l"
	process.feed(t, []byte(off))
	process.finish(0)
	waitEnded(t, host, sessionID)

	ended, err := host.Subscribe(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer ended.Close()

	everything = append(everything, off...)
	if !bytes.Equal(ended.Replay, everything[len(everything)-scrollbackBytesMax:]) {
		t.Fatalf("an ended session's replay holds more than the newest output, %d bytes",
			len(ended.Replay))
	}
}

// An editor opened inside the replay's window (the switch to the alternate screen is still in
// it) gets no switch in front of the replay: the replay switches itself, so the main screen's
// output before it stays on the main screen, there when the editor exits.
func TestAReplayDoesNotRepeatAnAlternateSwitchItHolds(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	const output = "MAIN-1\r\nMAIN-2\r\n\x1b[?1049hVIM"

	spawner.last(t).feed(t, []byte(output))

	viewer := subscribeAfter(t, host, sessionID, []byte(output))
	viewer.Close()
}

// An editor whose switch to the alternate screen has left the window gets the switch in front,
// so the replay is drawn on the alternate screen, as the program draws it.
func TestAReplayStartsOnTheAlternateScreenWhenItsSwitchHasLeft(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	process := spawner.last(t)
	process.feed(t, []byte("MAIN\r\n\x1b[?1049h"))

	scrollbackBytesMax := pty.DefaultLimits().ScrollbackBytesMax

	var everything []byte
	for len(everything) < scrollbackBytesMax {
		chunk := bytes.Repeat([]byte{'v'}, pty.ReadBufferBytes)
		everything = append(everything, chunk...)
		process.feed(t, chunk)
	}

	want := append([]byte("\x1b[?1049h"), everything[len(everything)-scrollbackBytesMax:]...)

	viewer := subscribeAfter(t, host, sessionID, want)
	viewer.Close()
}

// An ended session's replay starts with its modes too.
func TestAnEndedSessionsReplayStartsWithItsModes(t *testing.T) {
	t.Parallel()

	host, spawner := newFakeHost(t, pty.DefaultLimits())

	sessionID, err := host.Start(t.Context(), testSpec(t))
	if err != nil {
		t.Fatal(err)
	}

	process := spawner.last(t)
	process.feed(t, []byte("\x1b[?2004hbye"))
	process.finish(0)
	waitEnded(t, host, sessionID)

	late, err := host.Subscribe(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer late.Close()

	const want = "\x1b[?2004h\x1b[?2004hbye"
	if string(late.Replay) != want {
		t.Fatalf("replay = %q, want %q", late.Replay, want)
	}
}
