package hosted_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/claytoncasey01/conn/internal/hosted"
	"github.com/claytoncasey01/conn/internal/launch"
	"github.com/claytoncasey01/conn/internal/pty"
)

var errDiskFull = errors.New("no space left on device")

// world is what a Recorder reads and writes: the host's sessions, the links, and every write.
type world struct {
	mu       sync.Mutex
	live     []pty.Info
	linked   map[pty.ID]string
	writes   [][]hosted.Record
	writeErr error
	failures []error
}

func newWorld() *world {
	return &world{mu: sync.Mutex{}, live: nil, linked: map[pty.ID]string{}}
}

func (w *world) recorder() *hosted.Recorder {
	return hosted.NewRecorder(hosted.RecorderDeps{
		Dir: "/data",
		Live: func() []pty.Info {
			w.mu.Lock()
			defer w.mu.Unlock()

			return slices.Clone(w.live)
		},
		Linked: func() map[pty.ID]string {
			w.mu.Lock()
			defer w.mu.Unlock()

			return w.linked
		},
		Write: func(_ string, records []hosted.Record) error {
			w.mu.Lock()
			defer w.mu.Unlock()

			w.writes = append(w.writes, slices.Clone(records))

			return w.writeErr
		},
		Failed: func(err error) { w.failures = append(w.failures, err) },
	})
}

// run puts a running session on the host.
func (w *world) run(index int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.live = append(w.live, pty.Info{ID: pty.ID(hostID(index)), Name: "", Dir: "", PID: 0})
}

// end ends the index-th session.
func (w *world) end(index int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	code := 0

	for position := range w.live {
		if w.live[position].ID == pty.ID(hostID(index)) {
			w.live[position].ExitCode = &code
		}
	}
}

func (w *world) last() []hosted.Record {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.writes) == 0 {
		return nil
	}

	return w.writes[len(w.writes)-1]
}

func (w *world) writeCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()

	return len(w.writes)
}

func started(recorder *hosted.Recorder, index int) hosted.Record {
	want := record(index)
	want.ClaudeSessionID = ""
	recorder.Started(want.Owner, pty.ID(want.ID), pty.Spec{
		Argv: []string{"/opt/claude"}, Dir: want.Dir, Env: nil, Name: want.Name,
	}, "")

	return want
}

func TestRecorderWritesASessionAsItStarts(t *testing.T) {
	t.Parallel()

	setting := newWorld()
	recorder := setting.recorder()

	setting.run(0)

	want := started(recorder, 0)

	if !slices.Equal(setting.last(), []hosted.Record{want}) {
		t.Errorf("wrote %+v, want %+v", setting.last(), []hosted.Record{want})
	}
}

func TestRecorderDropsASessionOnceItEnds(t *testing.T) {
	t.Parallel()

	setting := newWorld()
	recorder := setting.recorder()

	setting.run(0)
	setting.run(1)
	started(recorder, 0)
	kept := started(recorder, 1)

	setting.end(0)
	recorder.Sync()

	if !slices.Equal(setting.last(), []hosted.Record{kept}) {
		t.Errorf("wrote %+v, want only %+v", setting.last(), kept)
	}
}

func TestRecorderKeepsWhatRanAtTheFreeze(t *testing.T) {
	t.Parallel()

	setting := newWorld()
	recorder := setting.recorder()

	setting.run(0)
	setting.run(1)

	first := started(recorder, 0)
	second := started(recorder, 1)

	recorder.Freeze()

	writes := setting.writeCount()

	// The quit stops every session after the freeze: none of their ends is recorded.
	setting.end(0)
	setting.end(1)
	recorder.Sync()
	recorder.Freeze()

	if setting.writeCount() != writes {
		t.Errorf("wrote %d times after the freeze", setting.writeCount()-writes)
	}

	if !slices.Equal(setting.last(), []hosted.Record{first, second}) {
		t.Errorf("the record is %+v, want both sessions", setting.last())
	}
}

func TestRecorderDoesNotRecordASessionThatEndedBeforeTheFreeze(t *testing.T) {
	t.Parallel()

	setting := newWorld()
	recorder := setting.recorder()

	setting.run(0)
	setting.run(1)
	started(recorder, 0)
	kept := started(recorder, 1)

	setting.end(0)
	recorder.Freeze()

	if !slices.Equal(setting.last(), []hosted.Record{kept}) {
		t.Errorf("the record is %+v, want only %+v", setting.last(), kept)
	}
}

func TestRecorderTakesClaudesIDOnceLinked(t *testing.T) {
	t.Parallel()

	setting := newWorld()
	recorder := setting.recorder()

	setting.run(0)

	want := started(recorder, 0)

	setting.mu.Lock()
	setting.linked[pty.ID(want.ID)] = otherClaude
	setting.mu.Unlock()

	recorder.Sync()

	want.ClaudeSessionID = otherClaude
	if !slices.Equal(setting.last(), []hosted.Record{want}) {
		t.Errorf("wrote %+v, want %+v", setting.last(), want)
	}
}

func TestRecorderReplacesAnEarlierLaunchsRecordOnItsFirstSync(t *testing.T) {
	t.Parallel()

	setting := newWorld()
	recorder := setting.recorder()

	recorder.Sync()

	if setting.writeCount() != 1 {
		t.Fatalf("wrote %d times, want once", setting.writeCount())
	}

	if len(setting.last()) != 0 {
		t.Errorf("wrote %+v, want no sessions", setting.last())
	}

	recorder.Sync()

	if setting.writeCount() != 1 {
		t.Errorf("an unchanged record was written again")
	}
}

func TestRecorderSaysAWriteThatFailedAndTriesAgain(t *testing.T) {
	t.Parallel()

	setting := newWorld()
	setting.writeErr = errDiskFull
	recorder := setting.recorder()

	recorder.Sync()

	if len(setting.failures) != 1 {
		t.Fatalf("said %d failures, want 1", len(setting.failures))
	}

	setting.mu.Lock()
	setting.writeErr = nil
	setting.mu.Unlock()

	recorder.Sync()

	if setting.writeCount() != 2 {
		t.Errorf("wrote %d times, want a second try", setting.writeCount())
	}
}

func TestRecorderOpenerRecordsOnlyHostedSessions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		opened   launch.Opened
		wantKept int
	}{
		{"inside Conn", launch.Opened{OK: true, HostedID: pty.ID(hostID(0))}, 1},
		{"in a terminal app", launch.Opened{OK: true, HostedID: ""}, 0},
		{"a line to paste", launch.Opened{OK: false, ManualCommand: "claude"}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			setting := newWorld()
			setting.run(0)
			recorder := setting.recorder()
			open := func(context.Context, pty.Spec) launch.Opened { return tt.opened }

			got := recorder.Opener(hosted.OwnerReviews, open)(t.Context(), pty.Spec{
				Argv: []string{"/opt/claude"}, Dir: "/data/reviews/pr/repo", Env: nil,
				Name: "pr-1",
			})

			if got != tt.opened {
				t.Errorf("the opener answered %+v, want %+v", got, tt.opened)
			}

			if len(recorder.Records()) != tt.wantKept {
				t.Errorf("recorded %d, want %d", len(recorder.Records()), tt.wantKept)
			}
		})
	}
}

func TestRecorderDoesNotRecordWhatItCouldNotReadBack(t *testing.T) {
	t.Parallel()

	setting := newWorld()
	setting.run(0)
	recorder := setting.recorder()

	recorder.Started(hosted.OwnerReviews, pty.ID(hostID(0)), pty.Spec{
		Argv: []string{"/opt/claude"}, Dir: "relative/folder", Env: nil, Name: "pr-1",
	}, "")

	if len(recorder.Records()) != 0 {
		t.Errorf("recorded %+v", recorder.Records())
	}

	if len(setting.failures) != 1 {
		t.Errorf("said %d failures, want 1", len(setting.failures))
	}
}

func TestRecorderKeepsTheConversationAResumeStartedOn(t *testing.T) {
	t.Parallel()

	setting := newWorld()
	setting.run(0)
	recorder := setting.recorder()
	want := record(0)

	recorder.Started(want.Owner, pty.ID(want.ID), pty.Spec{
		Argv: []string{"/opt/claude"}, Dir: want.Dir, Env: nil, Name: want.Name,
	}, want.ClaudeSessionID)

	if !slices.Equal(setting.last(), []hosted.Record{want}) {
		t.Errorf("wrote %+v, want %+v", setting.last(), want)
	}
}
