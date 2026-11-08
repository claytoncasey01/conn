package app

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/hosted"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/sessions"
	"github.com/claytoncasey01/conn/internal/settings"
)

var (
	errTampered    = errors.New("not a hosted sessions file")
	errNeverTicked = errors.New("the session list never ticked")
	errNeverSpawns = errors.New("this host spawns nothing")
)

func TestQuitRecordsThenClosesTerminalsThenStopsSessionsThenLetsTheFolderGo(t *testing.T) {
	t.Parallel()

	var order []string

	step := func(name string) func() { return func() { order = append(order, name) } }

	quit(quitSteps{
		record: step("record"), closeTerminals: step("close terminals"), cancel: step("cancel"),
		stopSessions: step("stop sessions"), release: step("release"),
	})

	want := []string{"record", "close terminals", "cancel", "stop sessions", "release"}
	if !slices.Equal(order, want) {
		t.Errorf("the quit ran %q, want %q", order, want)
	}
}

// resumeRecord is a record any check of resumeChecks vouches for.
func resumeRecord() hosted.Record {
	return hosted.Record{
		ID: "000000000000000000000000000000aa", Owner: hosted.OwnerReviews, Name: "pr-1",
		Dir: "/data/reviews/pr-1/widgets", ClaudeSessionID: "0b6c4f7e-2d1a-4c3b-9e8f-112233445566",
	}
}

func resumeChecks() hosted.Checks {
	return hosted.Checks{
		hosted.OwnerReviews: func(_ context.Context, record hosted.Record) (pty.Spec, bool) {
			return pty.Spec{
				Argv: []string{"/opt/claude"}, Dir: record.Dir, Env: nil,
				Name: record.Name,
			}, true
		},
	}
}

// ticked is a session list that has ticked.
func ticked(context.Context) error { return nil }

func TestResumeAtLaunchFollowsTheSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		sessions    settings.Sessions
		wantStarted int
	}{
		{
			"resume on launch, inside Conn",
			settings.Sessions{Where: settings.WhereInside, ResumeOnLaunch: true},
			1,
		},
		{
			"resume on launch off",
			settings.Sessions{Where: settings.WhereInside, ResumeOnLaunch: false},
			0,
		},
		{
			"sessions open in a terminal app",
			settings.Sessions{Where: settings.WhereTerminal, ResumeOnLaunch: true},
			0,
		},
		{
			"sessions open as a command",
			settings.Sessions{Where: settings.WhereCommand, ResumeOnLaunch: true},
			0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			started := 0
			start := func(context.Context, hosted.Record, pty.Spec) (pty.ID, bool) {
				started++

				return "000000000000000000000000000000bb", true
			}

			outcome := resumeAtLaunch(t.Context(), launchResume{
				sessions: tt.sessions, records: []hosted.Record{resumeRecord()}, readErr: nil,
				checks: resumeChecks(), start: start, carry: func([]hosted.Record) {},
				ready: ticked, stderr: &bytes.Buffer{},
			})

			if started != tt.wantStarted {
				t.Errorf("started %d, want %d", started, tt.wantStarted)
			}

			if len(outcome.Resumed) != tt.wantStarted {
				t.Errorf("resumed %d, want %d", len(outcome.Resumed), tt.wantStarted)
			}
		})
	}
}

func TestResumeAtLaunchSaysWhyTheRecordWasNotRead(t *testing.T) {
	t.Parallel()

	var said bytes.Buffer

	outcome := resumeAtLaunch(t.Context(), launchResume{
		sessions: settings.Sessions{Where: settings.WhereInside, ResumeOnLaunch: true},
		records:  nil, readErr: errTampered, checks: resumeChecks(),
		start: func(context.Context, hosted.Record, pty.Spec) (pty.ID, bool) { return "", false },
		carry: func([]hosted.Record) {}, ready: ticked, stderr: &said,
	})

	if len(outcome.Resumed) != 0 {
		t.Errorf("resumed %+v", outcome.Resumed)
	}

	if !strings.Contains(said.String(), errTampered.Error()) {
		t.Errorf("said %q, want the reason", said.String())
	}
}

func TestLinkedIDsAreOnlyHostedSessions(t *testing.T) {
	t.Parallel()

	got := linkedIDs([]sessions.LiveSession{
		{HostedID: "000000000000000000000000000000aa", SessionID: "claude-a"},
		{HostedID: "", SessionID: "claude-b"},
	})

	want := map[pty.ID]string{"000000000000000000000000000000aa": "claude-a"}
	if len(got) != len(want) {
		t.Fatalf("linked %v, want %v", got, want)
	}

	if got["000000000000000000000000000000aa"] != "claude-a" {
		t.Errorf("linked %v, want %v", got, want)
	}
}

// The quit's record step freezes the record: a session its stop ends stays recorded, however
// the record is synced after.
func TestTheQuitsRecordStaysAsItWasAtTheQuit(t *testing.T) {
	t.Parallel()

	const sessionID = pty.ID("000000000000000000000000000000aa")

	exitCode := 143
	live := []pty.Info{{ID: sessionID}}

	var written [][]hosted.Record

	recorder := hosted.NewRecorder(hosted.RecorderDeps{
		Dir:    "/data",
		Live:   func() []pty.Info { return live },
		Linked: func() map[pty.ID]string { return nil },
		Write: func(_ string, records []hosted.Record) error {
			written = append(written, records)

			return nil
		},
		Failed: func(err error) { t.Error(err) },
	})
	recorder.Started(hosted.OwnerReviews, sessionID, pty.Spec{
		Argv: []string{"/opt/claude"}, Dir: "/data/reviews/pr-1/widgets", Env: nil, Name: "pr-1",
	}, "")

	stop := func() {
		live = []pty.Info{{ID: sessionID, ExitCode: &exitCode}}

		recorder.Sync()
	}
	none := func() {}

	life := hostedLife{
		recorder: recorder, checks: nil, resumed: nil, bump: none, ticked: nil, tickTimeout: 0,
		settle: newResumeSettle(),
	}

	quit(quitStepsOf(life, none, none, stop, none))

	last := written[len(written)-1]
	if len(last) != 1 {
		t.Errorf("the record after the quit is %+v, want the session that ran", last)
	}
}

// The owners' checks read the session list: none runs before its first tick has been published,
// or a session the person runs in that folder would be missed.
func TestResumeAtLaunchChecksNothingBeforeTheSessionListTicked(t *testing.T) {
	t.Parallel()

	var order []string

	checks := hosted.Checks{
		hosted.OwnerReviews: func(ctx context.Context, record hosted.Record) (pty.Spec, bool) {
			order = append(order, "check")

			return resumeChecks()[hosted.OwnerReviews](ctx, record)
		},
	}

	resumeAtLaunch(t.Context(), launchResume{
		sessions: settings.Sessions{Where: settings.WhereInside, ResumeOnLaunch: true},
		records:  []hosted.Record{resumeRecord()}, readErr: nil, checks: checks,
		start: func(context.Context, hosted.Record, pty.Spec) (pty.ID, bool) {
			return "000000000000000000000000000000bb", true
		},
		carry: func([]hosted.Record) {}, stderr: &bytes.Buffer{},
		ready: func(context.Context) error {
			order = append(order, "ticked")

			return nil
		},
	})

	if !slices.Equal(order, []string{"ticked", "check"}) {
		t.Errorf("ran %q, want the session list's tick before the check", order)
	}
}

// A session list that never ticks resumes nothing, and the record keeps every record.
func TestResumeAtLaunchResumesNothingWhenTheSessionListNeverTicked(t *testing.T) {
	t.Parallel()

	var said bytes.Buffer

	started := 0
	records := []hosted.Record{resumeRecord()}

	outcome := resumeAtLaunch(t.Context(), launchResume{
		sessions: settings.Sessions{Where: settings.WhereInside, ResumeOnLaunch: true},
		records:  records, readErr: nil, checks: resumeChecks(),
		start: func(context.Context, hosted.Record, pty.Spec) (pty.ID, bool) {
			started++

			return "000000000000000000000000000000bb", true
		},
		carry: func([]hosted.Record) {}, stderr: &said,
		ready: func(context.Context) error { return errNeverTicked },
	})

	if started != 0 {
		t.Errorf("started %d before the session list ticked", started)
	}

	if !slices.Equal(outcome.Unreached, records) {
		t.Errorf("unreached %+v, want every record kept", outcome.Unreached)
	}

	if !strings.Contains(said.String(), errNeverTicked.Error()) {
		t.Errorf("said %q, want the reason", said.String())
	}
}

func TestWaitForTheFirstTick(t *testing.T) {
	t.Parallel()

	ticked := make(chan struct{})
	close(ticked)

	// A row that must not time out waits far longer than it takes; the one that times out
	// waits as little as can be.
	const (
		long  = time.Hour
		short = time.Nanosecond
	)

	tests := []struct {
		name    string
		ended   bool
		ticked  chan struct{}
		timeout time.Duration
		wantErr error
	}{
		{"ticked", false, ticked, long, nil},
		{"never ticked", false, make(chan struct{}), short, errSessionsNotTicked},
		{"the launch ended", true, make(chan struct{}), long, context.Canceled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			if tt.ended {
				cancel()
			}

			err := waitForFirstTick(ctx, tt.ticked, tt.timeout)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// The snapshot's version moves once the resumed sessions are set, so a snapshot cached before
// is not shown without them.
func TestResumeRecordedMovesTheSnapshotsVersion(t *testing.T) {
	t.Parallel()

	bumps := 0
	tickedChannel := make(chan struct{})
	close(tickedChannel)

	// No record: the host never spawns.
	host := pty.NewHost(pty.Config{
		Spawner: func(pty.Spec) (pty.Process, error) { return nil, errNeverSpawns },
		Limits:  pty.DefaultLimits(), Answers: pty.Answers{}, TurnWaited: nil,
	})
	life := hostedLife{
		recorder: openedRecorder(t), checks: resumeChecks(), resumed: newResumedSessions(host),
		bump: func() { bumps++ }, ticked: tickedChannel, tickTimeout: time.Hour,
		settle: newResumeSettle(),
	}

	resumeRecorded(t.Context(), life, host, launchResume{
		sessions: settings.Sessions{Where: settings.WhereInside, ResumeOnLaunch: true},
		records:  nil, readErr: nil, checks: nil, start: nil, carry: nil, ready: nil,
		stderr: &bytes.Buffer{},
	})

	if bumps != 1 {
		t.Errorf("moved the version %d times, want once", bumps)
	}
}

// A launch whose session list has not ticked runs no owner's check: it waits, and then resumes
// nothing.
func TestResumeRecordedWaitsForTheSessionList(t *testing.T) {
	t.Parallel()

	checked := 0
	checks := hosted.Checks{
		hosted.OwnerReviews: func(ctx context.Context, record hosted.Record) (pty.Spec, bool) {
			checked++

			return resumeChecks()[hosted.OwnerReviews](ctx, record)
		},
	}
	host := pty.NewHost(pty.Config{
		Spawner: func(pty.Spec) (pty.Process, error) { return nil, errNeverSpawns },
		Limits:  pty.DefaultLimits(), Answers: pty.Answers{}, TurnWaited: nil,
	})
	life := hostedLife{
		recorder: openedRecorder(t), checks: checks, resumed: newResumedSessions(host),
		bump: func() {}, ticked: make(chan struct{}), tickTimeout: time.Nanosecond,
		settle: newResumeSettle(),
	}

	resumeRecorded(t.Context(), life, host, launchResume{
		sessions: settings.Sessions{Where: settings.WhereInside, ResumeOnLaunch: true},
		records:  []hosted.Record{resumeRecord()}, readErr: nil, checks: nil, start: nil,
		carry: nil, ready: nil, stderr: &bytes.Buffer{},
	})

	if checked != 0 {
		t.Errorf("ran %d checks before the session list ticked", checked)
	}
}

// The last launch's records are carried before the first start, so a crash during the resume
// leaves the ones not reached in the record.
func TestResumeAtLaunchCarriesTheRecordsBeforeItStarts(t *testing.T) {
	t.Parallel()

	var order []string

	records := []hosted.Record{resumeRecord()}

	resumeAtLaunch(t.Context(), launchResume{
		sessions: settings.Sessions{Where: settings.WhereInside, ResumeOnLaunch: true},
		records:  records, readErr: nil, checks: resumeChecks(),
		start: func(context.Context, hosted.Record, pty.Spec) (pty.ID, bool) {
			order = append(order, "start")

			return "000000000000000000000000000000bb", true
		},
		carry: func(carried []hosted.Record) {
			if slices.Equal(carried, records) {
				order = append(order, "carry")
			}
		},
		ready: ticked, stderr: &bytes.Buffer{},
	})

	if !slices.Equal(order, []string{"carry", "start"}) {
		t.Errorf("ran %q, want the records carried before the start", order)
	}
}

// Reviews and My PRs judge no session gone until the launch has settled: resumeRecorded settles
// it once the resume is done, and not before.
func TestResumeRecordedSettlesTheLaunchWhenTheResumeIsDone(t *testing.T) {
	t.Parallel()

	settle := newResumeSettle()

	var settledAtCheck bool

	checked := 0
	checks := hosted.Checks{
		hosted.OwnerReviews: func(ctx context.Context, record hosted.Record) (pty.Spec, bool) {
			checked++
			_, settledAtCheck = settle.settled()

			return resumeChecks()[hosted.OwnerReviews](ctx, record)
		},
	}
	tickedChannel := make(chan struct{})
	close(tickedChannel)

	host := pty.NewHost(pty.Config{
		Spawner: func(pty.Spec) (pty.Process, error) { return nil, errNeverSpawns },
		Limits:  pty.DefaultLimits(), Answers: pty.Answers{}, TurnWaited: nil,
	})
	life := hostedLife{
		recorder: openedRecorder(t), checks: checks, resumed: newResumedSessions(host),
		bump: func() {}, ticked: tickedChannel, tickTimeout: time.Hour, settle: settle,
	}

	resumeRecorded(t.Context(), life, host, launchResume{
		sessions: settings.Sessions{Where: settings.WhereInside, ResumeOnLaunch: true},
		records:  []hosted.Record{resumeRecord()}, readErr: nil, checks: nil, start: nil,
		carry: nil, ready: nil, stderr: &bytes.Buffer{},
	})

	// The session list has ticked: the check runs.
	if checked != 1 {
		t.Fatalf("ran %d checks, want the record's", checked)
	}

	if settledAtCheck {
		t.Error("the launch was settled while it still resumed")
	}

	if _, settled := settle.settled(); !settled {
		t.Error("the launch is not settled after the resume")
	}
}
