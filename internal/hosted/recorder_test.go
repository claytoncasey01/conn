package hosted_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// stopping says a stop of the index-th session is under way: the person asked for it to end.
func (w *world) stopping(index int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for position := range w.live {
		if w.live[position].ID == pty.ID(hostID(index)) {
			w.live[position].Stopping = true
		}
	}
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

// liveNow is every session the host holds now.
func (w *world) liveNow() []pty.Info {
	w.mu.Lock()
	defer w.mu.Unlock()

	return slices.Clone(w.live)
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

// A session the person was stopping when Conn quit is not recorded: it was ending on purpose, and
// the next launch must not bring it back.
func TestRecorderDoesNotRecordASessionBeingStoppedAtTheFreeze(t *testing.T) {
	t.Parallel()

	setting := newWorld()
	recorder := setting.recorder()

	setting.run(0)
	setting.run(1)
	started(recorder, 0)
	kept := started(recorder, 1)

	setting.stopping(0)
	recorder.Freeze()

	if !slices.Equal(setting.last(), []hosted.Record{kept}) {
		t.Errorf("the record is %+v, want only %+v", setting.last(), kept)
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

// resumedFrom resumes the index-th carried record as the session the host issued newIndex for.
func resumedFrom(recorder *hosted.Recorder, index, newIndex int) hosted.Record {
	from := record(index)
	recorder.Resumed(from, pty.ID(hostID(newIndex)), pty.Spec{
		Argv: []string{"/opt/claude"}, Dir: from.Dir, Env: nil, Name: from.Name,
	})

	want := from
	want.ID = hostID(newIndex)

	return want
}

// The records a launch has not resumed yet are carried in the record beside the sessions that
// run, and each one a resume starts takes its record's place.
func TestRecorderCarriesTheRecordsNotYetResumed(t *testing.T) {
	t.Parallel()

	const newIndex = 20

	setting := newWorld()
	recorder := setting.recorder()
	recorder.Carry(manyRecords(3))

	setting.run(newIndex)

	resumed := resumedFrom(recorder, 1, newIndex)

	want := []hosted.Record{resumed, record(0), record(2)}
	if !slices.Equal(setting.last(), want) {
		t.Errorf("wrote %+v, want %+v", setting.last(), want)
	}
}

// A quit during the resume freezes what was carried with what runs: nothing is lost.
func TestRecorderFreezesWhatItCarries(t *testing.T) {
	t.Parallel()

	const newIndex = 20

	setting := newWorld()
	recorder := setting.recorder()
	recorder.Carry(manyRecords(2))
	setting.run(newIndex)

	resumed := resumedFrom(recorder, 0, newIndex)

	recorder.Freeze()
	setting.end(newIndex)
	recorder.Sync()

	want := []hosted.Record{resumed, record(1)}
	if !slices.Equal(setting.last(), want) {
		t.Errorf("the record is %+v, want %+v", setting.last(), want)
	}
}

// Once the resume is done, what it did not start is no longer carried.
func TestRecorderDropsWhatItNoLongerCarries(t *testing.T) {
	t.Parallel()

	setting := newWorld()
	recorder := setting.recorder()
	recorder.Carry(manyRecords(2))
	recorder.Carry(nil)
	recorder.Sync()

	if len(setting.last()) != 0 {
		t.Errorf("wrote %+v, want no sessions", setting.last())
	}
}

// A session started after the freeze is the quit's own: it is stopped, and not recorded.
func TestRecorderDoesNotRecordASessionStartedAfterTheFreeze(t *testing.T) {
	t.Parallel()

	setting := newWorld()
	recorder := setting.recorder()
	recorder.Freeze()

	writes := setting.writeCount()

	setting.run(0)
	started(recorder, 0)
	resumedFrom(recorder, 1, 0)

	if len(recorder.Records()) != 0 {
		t.Errorf("recorded %+v after the freeze", recorder.Records())
	}

	if setting.writeCount() != writes {
		t.Errorf("wrote %d times after the freeze", setting.writeCount()-writes)
	}
}

// A module's own session started during the resume is recorded, and the carried records that no
// longer fit beside it are cut, so the record is never one Write refuses.
func TestRecorderCarriesOnlyWhatFits(t *testing.T) {
	t.Parallel()

	const ownIndex = 20

	setting := newWorld()
	recorder := setting.recorder()
	recorder.Carry(manyRecords(hosted.RecordsMax()))
	setting.run(ownIndex)

	own := started(recorder, ownIndex)

	want := slices.Concat([]hosted.Record{own}, manyRecords(hosted.RecordsMax()-1))
	if !slices.Equal(setting.last(), want) {
		t.Errorf("wrote %d records, want %d with the module's own first", len(setting.last()),
			len(want))
	}
}

// A session opened on a conversation (claude --resume <id>, as History's RESUME opens it) is
// recorded with that id at once: a quit before the session list links it still resumes it.
func TestRecorderOpenerRecordsTheConversationItResumes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		argv []string
		want string
	}{
		{"a resumed conversation", []string{"/opt/claude", "--resume", claudeID}, claudeID},
		{"a resumed conversation after rules", []string{
			"/opt/claude", "--disallowedTools", "Bash(git push:*)", "--resume", claudeID,
			"--name", "x",
		}, claudeID},
		{"a new conversation", []string{"/opt/claude", "--name", "x"}, ""},
		{"an id that is not a UUID", []string{"/opt/claude", "--resume", "--continue"}, ""},
		{"--resume last", []string{"/opt/claude", "--resume"}, ""},
		{"a new conversation Conn named", []string{
			"/opt/claude", "--session-id", claudeID, "--name", "x",
		}, claudeID},
		{"a session id that is not a UUID", []string{"/opt/claude", "--session-id", "x"}, ""},
		{"--session-id last", []string{"/opt/claude", "--session-id"}, ""},
		{"--resume before --session-id", []string{
			"/opt/claude", "--resume", claudeID, "--session-id", otherClaude,
		}, claudeID},
		{"--session-id before --resume", []string{
			"/opt/claude", "--session-id", otherClaude, "--resume", claudeID,
		}, claudeID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			setting := newWorld()
			setting.run(0)
			recorder := setting.recorder()
			open := func(context.Context, pty.Spec) launch.Opened {
				return launch.Opened{OK: true, HostedID: pty.ID(hostID(0))}
			}

			recorder.Opener(hosted.OwnerHistory, open)(t.Context(), pty.Spec{
				Argv: tt.argv, Dir: "/work/widgets", Env: nil, Name: "fix widget",
			})

			records := recorder.Records()
			if len(records) != 1 {
				t.Fatalf("recorded %+v, want the session", records)
			}

			if records[0].ClaudeSessionID != tt.want {
				t.Errorf("recorded conversation %q, want %q", records[0].ClaudeSessionID, tt.want)
			}
		})
	}
}

// A write that keeps failing the same way (a folder at the record's name) is said once, not at
// every sync, and said again once it fails after a write that worked.
func TestRecorderSaysTheSameWriteFailureOnce(t *testing.T) {
	t.Parallel()

	setting := newWorld()
	setting.writeErr = errDiskFull
	recorder := setting.recorder()

	recorder.Sync()
	recorder.Sync()
	recorder.Sync()

	if len(setting.failures) != 1 {
		t.Fatalf("said %d failures, want 1", len(setting.failures))
	}

	setting.mu.Lock()
	setting.writeErr = nil
	setting.mu.Unlock()

	recorder.Sync()
	setting.run(0)

	setting.mu.Lock()
	setting.writeErr = errDiskFull
	setting.mu.Unlock()

	started(recorder, 0)

	if len(setting.failures) != 2 {
		t.Errorf("said %d failures, want the new one said", len(setting.failures))
	}
}

// A folder at the record's name fails every write the same way, and is said once.
func TestRecorderSaysAFolderAtTheRecordsNameOnce(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.Mkdir(filepath.Join(dir, hosted.FileName), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	var failures []error

	recorder := hosted.NewRecorder(hosted.RecorderDeps{
		Dir: dir, Live: func() []pty.Info { return nil },
		Linked: func() map[pty.ID]string { return nil }, Write: hosted.Write,
		Failed: func(err error) { failures = append(failures, err) },
	})

	recorder.Sync()
	recorder.Sync()
	recorder.Sync()

	if len(failures) != 1 {
		t.Errorf("said %d failures, want 1: %v", len(failures), failures)
	}
}

// A session whose record Write could not write back as it is (a name that JSON writes longer
// than its bound) runs unrecorded: the sessions started after it are still recorded.
func TestRecorderRefusesARecordThatWouldNotReadBack(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	setting := newWorld()

	var failures []error

	recorder := hosted.NewRecorder(hosted.RecorderDeps{
		Dir: dir, Live: func() []pty.Info { return setting.live },
		Linked: func() map[pty.ID]string { return nil }, Write: hosted.Write,
		Failed: func(err error) { failures = append(failures, err) },
	})

	for index := range 3 {
		setting.run(index)

		spec := pty.Spec{
			Argv: []string{"/opt/claude"}, Dir: record(index).Dir, Env: nil,
			Name: record(index).Name,
		}
		// Each byte that is not UTF-8 is written as U+FFFD, three bytes.
		if index == 1 {
			spec.Name = strings.Repeat("\xff", hosted.NameBytesMax/2)
		}

		recorder.Started(hosted.OwnerOpenWork, pty.ID(hostID(index)), spec, "")
	}

	recorder.Freeze()

	records, err := hosted.Read(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(records) != 2 {
		t.Errorf("the record holds %d sessions, want the two it can write", len(records))
	}

	if len(failures) != 1 {
		t.Errorf("said %d failures, want the one record refused: %v", len(failures), failures)
	}
}

// worstRecord is the index-th session at its bounds in the bytes JSON writes longest: every
// byte of its name and folder written as six.
func worstRecord(index int) (pty.ID, pty.Spec) {
	return pty.ID(hostID(index)), pty.Spec{
		Argv: []string{"/opt/claude"}, Dir: "/" + strings.Repeat("<", hosted.DirBytesMax-1),
		Env: nil, Name: strings.Repeat("&", hosted.NameBytesMax),
	}
}

// startWorst starts count worst-case sessions on setting and records them, from index first.
func startWorst(setting *world, recorder *hosted.Recorder, first, count int) {
	for index := first; index < first+count; index++ {
		setting.run(index)

		hostedID, spec := worstRecord(index)
		recorder.Started(hosted.OwnerOpenWork, hostedID, spec, "")
	}
}

// writingRecorder is a recorder over setting's host that writes the record in dir, and keeps
// what it could not record in failures.
func writingRecorder(dir string, setting *world, failures *[]error) *hosted.Recorder {
	return hosted.NewRecorder(hosted.RecorderDeps{
		Dir: dir, Live: setting.liveNow, Linked: func() map[pty.ID]string { return nil },
		Write: hosted.Write, Failed: func(err error) { *failures = append(*failures, err) },
	})
}

// As many sessions as a host runs always fit in the record, whatever bytes their names and
// folders hold; one more is refused alone, and once a session ends the next is recorded.
func TestRecorderKeepsTheMostSessionsOfTheLongestRecords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// more are the sessions started past RecordsMax; ended is one ending after them, and
		// one more started.
		more         int
		ended        bool
		wantRecords  int
		wantFailures int
	}{
		{"as many as a host runs", 0, false, hosted.RecordsMax(), 0},
		{"one more", 1, false, hosted.RecordsMax(), 1},
		{"one more, then one ends and another starts", 1, true, hosted.RecordsMax(), 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir, setting := t.TempDir(), newWorld()

			var failures []error

			recorder := writingRecorder(dir, setting, &failures)
			startWorst(setting, recorder, 0, hosted.RecordsMax()+tt.more)

			if tt.ended {
				setting.end(0)
				startWorst(setting, recorder, recordsTried, 1)
			}

			records, err := hosted.Read(dir)
			if err != nil {
				t.Fatal(err)
			}

			if len(records) != tt.wantRecords {
				t.Errorf("the record holds %d sessions, want %d", len(records), tt.wantRecords)
			}

			if len(failures) != tt.wantFailures {
				t.Errorf("said %d failures, want %d: %v", len(failures), tt.wantFailures, failures)
			}

			// The one started once a session ended is recorded: the one refused stopped no
			// write after it.
			newest := slices.ContainsFunc(records, func(each hosted.Record) bool {
				return each.ID == hostID(recordsTried)
			})
			if newest != tt.ended {
				t.Errorf("the session started after one ended is recorded: %v, want %v", newest,
					tt.ended)
			}
		})
	}
}
