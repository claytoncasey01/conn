package pty_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/pty"
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
		{"too many variables", func(spec *pty.Spec) { spec.Env = make([]string, 4097) }, 1},
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

	chunksCount := pty.ViewerQueueChunks + 8
	for index := range chunksCount {
		chunk := []byte{byte(index)}
		process.feed(t, chunk)

		got := receive(t, fast.Output)
		if !bytes.Equal(got, chunk) {
			t.Fatalf("the fast viewer got %v, want %v", got, chunk)
		}
	}

	queued := drainUntilClosed(t, slow.Output)
	if len(queued) > pty.ViewerQueueChunks {
		t.Fatalf("the slow viewer was queued %d chunks, more than %d",
			len(queued), pty.ViewerQueueChunks)
	}

	entry, _ := info(t, host, sessionID)
	if entry.ExitCode != nil {
		t.Fatal("dropping a viewer ended the session")
	}
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
