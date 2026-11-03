package modules_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/modules"
	"github.com/claytoncasey01/conn/internal/settings"
	"github.com/claytoncasey01/conn/internal/testtime"
)

var errProbe = errors.New("timed out after 60s\nstack line")

// rig is a Statuses with switches, a clock and probes the test controls.
type rig struct {
	mu      sync.Mutex
	on      map[string]bool
	calls   []string
	now     time.Time
	answers map[string]modules.Probe
	status  *modules.Statuses
}

func newRig(t *testing.T) *rig {
	t.Helper()

	held := &rig{
		on: map[string]bool{
			settings.ModuleGitHub: true, settings.ModuleSlack: true, settings.ModuleAtlassian: true,
		},
		now:     time.UnixMilli(1000),
		answers: map[string]modules.Probe{},
	}

	probes := map[string]modules.Probe{}
	for _, name := range modules.Names() {
		probes[name] = held.probe(name)
	}

	held.status = modules.NewStatuses(modules.Deps{
		Enabled: held.enabled,
		Probes:  probes,
		Now:     held.clock,
	})

	return held
}

func (r *rig) probe(name string) modules.Probe {
	return func(ctx context.Context) (modules.ProbeResult, error) {
		r.mu.Lock()
		r.calls = append(r.calls, name)
		answer := r.answers[name]
		r.mu.Unlock()

		if answer == nil {
			return modules.ProbeResult{OK: true, Detail: name + " fine"}, nil
		}

		return answer(ctx)
	}
}

func (r *rig) enabled(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.on[name]
}

func (r *rig) clock() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.now
}

// setSlack switches Slack on or off.
func (r *rig) setSlack(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.on[settings.ModuleSlack] = on
}

func (r *rig) answer(name string, probe modules.Probe) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.answers[name] = probe
}

func (r *rig) setNow(milliseconds int64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.now = time.UnixMilli(milliseconds)
}

func (r *rig) callList() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return strings.Join(r.calls, ",")
}

func at(milliseconds int64) *int64 { return new(milliseconds) }

func checkStatus(t *testing.T, got, want modules.Status) {
	t.Helper()

	if got.State != want.State {
		t.Errorf("State = %q, want %q", got.State, want.State)
	}

	if got.Detail != want.Detail {
		t.Errorf("Detail = %q, want %q", got.Detail, want.Detail)
	}

	switch {
	case got.At == nil:
		if want.At != nil {
			t.Errorf("At = nil, want %d", *want.At)
		}
	case want.At == nil:
		t.Errorf("At = %d, want nil", *got.At)
	case *got.At != *want.At:
		t.Errorf("At = %d, want %d", *got.At, *want.At)
	}
}

func unchecked() modules.Status {
	return modules.Status{State: modules.StateUnchecked, Detail: modules.UncheckedText, At: nil}
}

func off() modules.Status {
	return modules.Status{State: modules.StateOff, Detail: modules.OffText, At: nil}
}

func TestNobodyHasCheckedAModuleAndReadingRunsNoProbe(t *testing.T) {
	t.Parallel()

	held := newRig(t)
	for _, name := range modules.Names() {
		checkStatus(t, held.status.Get(name), unchecked())
	}

	all := held.status.All()
	checkStatus(t, all.Slack, unchecked())

	if held.callList() != "" {
		t.Errorf("probes ran: %s", held.callList())
	}
}

func TestAModuleThatIsOffIsOffWhateverWasRecorded(t *testing.T) {
	t.Parallel()

	held := newRig(t)
	held.setSlack(false)
	held.status.Record(settings.ModuleSlack, true, "Last scan worked")
	checkStatus(t, held.status.Get(settings.ModuleSlack), off())

	held.setSlack(true)
	checkStatus(t, held.status.Get(settings.ModuleSlack), modules.Status{
		State: modules.StateOK, Detail: "Last scan worked", At: at(1000),
	})
}

func TestTheLastRecordDecides(t *testing.T) {
	t.Parallel()

	held := newRig(t)
	held.status.Record(settings.ModuleSlack, true, "Scan worked")
	checkStatus(t, held.status.Get(settings.ModuleSlack), modules.Status{
		State: modules.StateOK, Detail: "Scan worked", At: at(1000),
	})

	held.setNow(5000)
	held.status.Record(settings.ModuleSlack, false, "Slack scan failed: timed out after 90s")
	checkStatus(t, held.status.Get(settings.ModuleSlack), modules.Status{
		State: modules.StateFailing, Detail: "Slack scan failed: timed out after 90s", At: at(5000),
	})

	held.status.Record(settings.ModuleSlack, true, "")

	worked := held.status.Get(settings.ModuleSlack).Detail
	if worked != "Worked" {
		t.Errorf("an empty detail = %q, want Worked", worked)
	}

	held.status.Record(settings.ModuleSlack, false, " ")

	failed := held.status.Get(settings.ModuleSlack).Detail
	if failed != "Failed" {
		t.Errorf("an empty failure = %q, want Failed", failed)
	}

	checkStatus(t, held.status.Get(settings.ModuleAtlassian), unchecked())

	held.status.Record("nope", true, "x")
	checkStatus(t, held.status.Get(settings.ModuleGitHub), unchecked())

	if held.callList() != "" {
		t.Errorf("record probed: %s", held.callList())
	}
}

func TestCleanDetail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		max  int
		want string
	}{
		{
			name: "control and bidi characters become spaces",
			text: "a\nb\x00c\u202ed   e", max: 200, want: "a b c d e",
		},
		{name: "cut with dots", text: "abcdef", max: 5, want: "ab..."},
		{name: "a limit it fits", text: "abcde", max: 5, want: "abcde"},
		{name: "trimmed", text: "\t x \u2066", max: 200, want: "x"},
		{
			name: "cut at 200 UTF-16 units",
			text: strings.Repeat("x", 500), max: 200, want: strings.Repeat("x", 197) + "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := modules.CleanDetail(tt.text, tt.max)
			if got != tt.want {
				t.Errorf("CleanDetail(%q, %d) = %q, want %q", tt.text, tt.max, got, tt.want)
			}
		})
	}
}

func TestCheckRunsTheModulesOwnProbe(t *testing.T) {
	t.Parallel()

	held := newRig(t)
	held.setNow(7000)

	got := held.status.Check(t.Context(), settings.ModuleSlack)
	want := modules.Status{State: modules.StateOK, Detail: "slack fine", At: at(7000)}
	checkStatus(t, got, want)
	checkStatus(t, held.status.Get(settings.ModuleSlack), want)
	checkStatus(t, held.status.Get(settings.ModuleGitHub), unchecked())

	if held.callList() != "slack" {
		t.Errorf("calls = %s, want slack", held.callList())
	}
}

func TestAFailedProbeIsFailing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		answer modules.Probe
		want   string
	}{
		{
			name: "a failed answer",
			answer: func(context.Context) (modules.ProbeResult, error) {
				return modules.ProbeResult{
					OK: false, Detail: "Not signed in: run gh auth login",
				}, nil
			},
			want: "Not signed in: run gh auth login",
		},
		{
			name: "an error is its first line",
			answer: func(context.Context) (modules.ProbeResult, error) {
				return modules.ProbeResult{}, errProbe
			},
			want: "timed out after 60s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			held := newRig(t)
			held.answer(settings.ModuleGitHub, tt.answer)

			checkStatus(t, held.status.Check(t.Context(), settings.ModuleGitHub), modules.Status{
				State: modules.StateFailing, Detail: tt.want, At: at(1000),
			})
		})
	}
}

func TestALaterGoodCheckReplacesAFailingOne(t *testing.T) {
	t.Parallel()

	held := newRig(t)
	held.answer(settings.ModuleSlack, func(context.Context) (modules.ProbeResult, error) {
		return modules.ProbeResult{OK: false, Detail: "down"}, nil
	})

	first := held.status.Check(t.Context(), settings.ModuleSlack)
	if first.State != modules.StateFailing {
		t.Fatalf("first check = %q, want failing", first.State)
	}

	held.answer(settings.ModuleSlack, nil)

	if got := held.status.Check(t.Context(), settings.ModuleSlack).State; got != modules.StateOK {
		t.Errorf("second check = %q, want ok", got)
	}

	if held.callList() != "slack,slack" {
		t.Errorf("calls = %s, want slack,slack", held.callList())
	}
}

func TestCheckOnAModuleThatIsOffMakesNoCall(t *testing.T) {
	t.Parallel()

	held := newRig(t)
	held.setSlack(false)

	checkStatus(t, held.status.Check(t.Context(), settings.ModuleSlack), off())
	checkStatus(t, held.status.Check(t.Context(), "nope"), off())

	if held.callList() != "" {
		t.Errorf("calls = %s, want none", held.callList())
	}
}

// waiting is a probe that answers detail once released.
func waiting(release <-chan struct{}, detail string) modules.Probe {
	return func(context.Context) (modules.ProbeResult, error) {
		<-release

		return modules.ProbeResult{OK: true, Detail: detail}, nil
	}
}

func TestTwoPressesAtOnceRunOneProbe(t *testing.T) {
	t.Parallel()

	held := newRig(t)
	release := make(chan struct{})
	held.answer(settings.ModuleSlack, waiting(release, "one"))

	answers := make(chan modules.Status, 2)

	for range 2 {
		go func() { answers <- held.status.Check(t.Context(), settings.ModuleSlack) }()
	}

	waitFor(t, func() bool { return held.status.Waiting(settings.ModuleSlack) == 2 })
	close(release)

	first, second := <-answers, <-answers
	if first.Detail != "one" {
		t.Errorf("first press = %q, want one", first.Detail)
	}

	if second.Detail != "one" {
		t.Errorf("second press = %q, want one", second.Detail)
	}

	if held.callList() != "slack" {
		t.Errorf("calls = %s, want one probe", held.callList())
	}

	held.answer(settings.ModuleSlack, nil)
	held.status.Check(t.Context(), settings.ModuleSlack)

	if held.callList() != "slack,slack" {
		t.Errorf("calls = %s, want a second probe for the next press", held.callList())
	}
}

func TestAProbeRunningWhenTheSwitchGoesOffAnswersOff(t *testing.T) {
	t.Parallel()

	held := newRig(t)
	release := make(chan struct{})
	held.answer(settings.ModuleSlack, waiting(release, "late"))

	answer := make(chan modules.Status, 1)

	go func() { answer <- held.status.Check(t.Context(), settings.ModuleSlack) }()

	waitFor(t, func() bool { return held.callList() == "slack" })
	held.setSlack(false)
	close(release)

	if got := (<-answer).State; got != modules.StateOff {
		t.Errorf("State = %q, want off", got)
	}
}

func TestAProbeOutlivesAPressThatWentAway(t *testing.T) {
	t.Parallel()

	held := newRig(t)
	release := make(chan struct{})
	held.answer(settings.ModuleSlack, waiting(release, "recorded"))

	ctx, cancel := context.WithCancel(t.Context())
	answer := make(chan modules.Status, 1)

	go func() { answer <- held.status.Check(ctx, settings.ModuleSlack) }()

	waitFor(t, func() bool { return held.callList() == "slack" })
	cancel()

	if got := (<-answer).State; got != modules.StateUnchecked {
		t.Errorf("the gone press = %q, want unchecked", got)
	}

	close(release)
	waitFor(t, func() bool {
		return held.status.Get(settings.ModuleSlack).Detail == "recorded"
	})
}

func TestNewStatusesNeedsAProbePerModule(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("NewStatuses with no probes did not panic")
		}
	}()

	modules.NewStatuses(modules.Deps{
		Enabled: func(string) bool { return true }, Probes: nil, Now: time.Now,
	})
}

func waitFor(t *testing.T, done func() bool) {
	t.Helper()

	limit := testtime.Scaled(5 * time.Second)

	deadline := time.Now().Add(limit)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("waited %s for a condition that never held", limit)
		}

		time.Sleep(time.Millisecond)
	}
}
