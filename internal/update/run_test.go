package update_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/testtime"
	"github.com/claytoncasey01/conn/internal/update"
)

const (
	// waitMax bounds every wait for something that must happen: a Run that does not do it in
	// this long has hung, and the test fails instead of hanging the binary.
	waitMax = 10 * time.Second

	// quietWait is how long a test watches for something that must not happen. It only costs
	// time when the code is right, and a wrong Run shows the event well inside it.
	quietWait = 100 * time.Millisecond

	// channelSlots is the room in each of the fakes' channels, so Run never waits on a test that
	// is not looking.
	channelSlots = 16
)

var errNoNetwork = errors.New("no network")

// reply is what the fake Fetch answers to one call.
type reply struct {
	latest string
	err    error
}

// appReply is what the fake AppAttached answers to one call.
type appReply struct {
	attached bool
	err      error
}

// timer is one wait Run asked After for: how long, and the way to fire it.
type timer struct {
	wait time.Duration
	fire chan<- time.Time
}

// world is the fake clock, fetch and reporter of one Run. Everything Run does is a value on one
// of its channels, so a test waits for what it needs and never sleeps for an order.
type world struct {
	t       *testing.T
	replies []reply
	gate    chan struct{}
	timers  chan timer
	started chan struct{}
	events  chan update.Event
	asked   chan struct{}
	// appReplies are AppAttached's answers in order, repeating the last; none means attached.
	appReplies []appReply
	appTags    chan string
	// appHeld, when set, makes AppAttached wait for its context to end, as a request that never
	// answers does.
	appHeld bool

	calls    atomic.Int32
	appCalls atomic.Int32
	inFlight atomic.Int32
	peak     atomic.Int32
}

// newWorld is a world whose Fetch answers replies in order, repeating the last. With gated, each
// Fetch waits for a value on gate (or its context ending) before it answers.
func newWorld(t *testing.T, gated bool, replies ...reply) *world {
	t.Helper()

	world := &world{
		t:          t,
		replies:    replies,
		gate:       nil,
		timers:     make(chan timer, channelSlots),
		started:    make(chan struct{}, channelSlots),
		events:     make(chan update.Event, channelSlots),
		asked:      make(chan struct{}, 1),
		appReplies: nil,
		appTags:    make(chan string, channelSlots),
		appHeld:    false,
		calls:      atomic.Int32{},
		appCalls:   atomic.Int32{},
		inFlight:   atomic.Int32{},
		peak:       atomic.Int32{},
	}

	if gated {
		world.gate = make(chan struct{}, channelSlots)
	}

	return world
}

func (w *world) deps(current string) update.Deps {
	return update.Deps{
		Current:     current,
		Fetch:       w.fetch,
		AppAttached: w.appAttached,
		After:       w.after,
		Asked:       w.asked,
		Report:      func(event update.Event) { w.events <- event },
	}
}

func (w *world) after(wait time.Duration) <-chan time.Time {
	fire := make(chan time.Time, 1)
	w.timers <- timer{wait: wait, fire: fire}

	return fire
}

func (w *world) fetch(ctx context.Context) (string, error) {
	call := int(w.calls.Add(1))

	now := w.inFlight.Add(1)
	defer w.inFlight.Add(-1)

	for {
		peak := w.peak.Load()
		if now <= peak {
			break
		}

		if w.peak.CompareAndSwap(peak, now) {
			break
		}
	}

	w.started <- struct{}{}

	if w.gate != nil {
		select {
		case <-w.gate:
		case <-ctx.Done():
			return "", fmt.Errorf("fetch ended: %w", context.Cause(ctx))
		}
	}

	answer := w.replies[min(call, len(w.replies))-1]

	return answer.latest, answer.err
}

func (w *world) appAttached(ctx context.Context, tag string) (bool, error) {
	call := int(w.appCalls.Add(1))
	w.appTags <- tag

	if w.appHeld {
		<-ctx.Done()

		return false, fmt.Errorf("app question ended: %w", context.Cause(ctx))
	}

	if len(w.appReplies) == 0 {
		return true, nil
	}

	answer := w.appReplies[min(call, len(w.appReplies))-1]

	return answer.attached, answer.err
}

// ask is what the app does when the menu item is chosen: a send that never blocks.
func (w *world) ask() {
	select {
	case w.asked <- struct{}{}:
	default:
	}
}

// release lets one blocked Fetch answer.
func (w *world) release() {
	w.gate <- struct{}{}
}

// nextTimer is the next wait Run asked for.
func (w *world) nextTimer() timer {
	w.t.Helper()

	return receive(w.t, w.timers, "a wait from After")
}

// nextEvent is the next thing Run reported.
func (w *world) nextEvent() update.Event {
	w.t.Helper()

	return receive(w.t, w.events, "a report")
}

// awaitFetch returns once a Fetch has started.
func (w *world) awaitFetch() {
	w.t.Helper()

	receive(w.t, w.started, "a Fetch to start")
}

// receive is the next value on channel, or a failed test when none comes within waitMax.
func receive[T any](t *testing.T, channel <-chan T, what string) T {
	t.Helper()

	select {
	case value := <-channel:
		return value
	case <-time.After(testtime.Scaled(waitMax)):
		t.Fatalf("no %s within %s", what, testtime.Scaled(waitMax))

		var zero T

		return zero
	}
}

// running is a Run in a goroutine that a test can stop and know has returned.
type running struct {
	t      *testing.T
	cancel context.CancelFunc
	done   chan struct{}
}

// start runs update.Run on deps. The context also ends with the test, so a failed test leaves no
// goroutine behind.
func start(t *testing.T, deps update.Deps) *running {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	run := &running{t: t, cancel: cancel, done: make(chan struct{})}

	go func() {
		defer close(run.done)

		update.Run(ctx, deps)
	}()

	return run
}

// stop ends the context and fails the test when Run has not returned within waitMax.
func (r *running) stop() {
	r.t.Helper()

	r.cancel()

	select {
	case <-r.done:
	case <-time.After(testtime.Scaled(waitMax)):
		r.t.Fatalf("Run did not return within %s of its context ending", testtime.Scaled(waitMax))
	}
}

// checkEvent compares the parts of an event a test cares about, one fact per check.
func checkEvent(t *testing.T, got, want update.Event) {
	t.Helper()

	if got.Result.Kind != want.Result.Kind {
		t.Errorf("event kind = %v, want %v", got.Result.Kind, want.Result.Kind)
	}

	if got.Result.Latest != want.Result.Latest {
		t.Errorf("event latest = %q, want %q", got.Result.Latest, want.Result.Latest)
	}

	if got.Manual != want.Manual {
		t.Errorf("event manual = %v, want %v", got.Manual, want.Manual)
	}

	if got.Fresh != want.Fresh {
		t.Errorf("event fresh = %v, want %v", got.Fresh, want.Fresh)
	}
}

func TestRunWaitsBeforeEachCheck(t *testing.T) {
	t.Parallel()

	world := newWorld(t, false, reply{latest: "v1.1.0", err: nil})
	run := start(t, world.deps("v1.0.0"))

	first := world.nextTimer()
	if first.wait != update.StartDelay {
		t.Errorf("first wait = %s, want %s", first.wait, update.StartDelay)
	}

	if world.calls.Load() != 0 {
		t.Errorf("fetches before the start delay = %d, want 0", world.calls.Load())
	}

	first.fire <- time.Time{}

	world.nextEvent()

	second := world.nextTimer()
	if second.wait != update.CheckEvery {
		t.Errorf("second wait = %s, want %s", second.wait, update.CheckEvery)
	}

	if world.calls.Load() != 1 {
		t.Errorf("fetches before the interval = %d, want 1", world.calls.Load())
	}

	second.fire <- time.Time{}

	world.nextEvent()
	run.stop()

	if world.calls.Load() != 2 {
		t.Errorf("fetches after the interval = %d, want 2", world.calls.Load())
	}
}

func TestRunFresh(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		replies []reply
		want    []update.Event
	}{
		{
			"the same release at the next interval is not news",
			[]reply{{"v1.1.0", nil}, {"v1.1.0", nil}},
			[]update.Event{
				{Result: update.Result{Kind: update.Available, Latest: "v1.1.0"}, Fresh: true},
				{Result: update.Result{Kind: update.Available, Latest: "v1.1.0"}, Fresh: false},
			},
		},
		{
			"a newer release later is news",
			[]reply{{"v1.1.0", nil}, {"v1.1.0", nil}, {"v1.2.0", nil}, {"v1.2.0", nil}},
			[]update.Event{
				{Result: update.Result{Kind: update.Available, Latest: "v1.1.0"}, Fresh: true},
				{Result: update.Result{Kind: update.Available, Latest: "v1.1.0"}, Fresh: false},
				{Result: update.Result{Kind: update.Available, Latest: "v1.2.0"}, Fresh: true},
				{Result: update.Result{Kind: update.Available, Latest: "v1.2.0"}, Fresh: false},
			},
		},
		{
			"a lower release published again after a newer one is not news",
			[]reply{{"v1.2.0", nil}, {"v1.3.0", nil}, {"v1.2.0", nil}, {"v1.3.0", nil}},
			[]update.Event{
				{Result: update.Result{Kind: update.Available, Latest: "v1.2.0"}, Fresh: true},
				{Result: update.Result{Kind: update.Available, Latest: "v1.3.0"}, Fresh: true},
				{Result: update.Result{Kind: update.Available, Latest: "v1.2.0"}, Fresh: false},
				{Result: update.Result{Kind: update.Available, Latest: "v1.3.0"}, Fresh: false},
			},
		},
		{
			"a failed check then an available release is news once",
			[]reply{{"", errNoNetwork}, {"v1.1.0", nil}, {"v1.1.0", nil}},
			[]update.Event{
				{Result: update.Result{Kind: update.Failed, Latest: ""}, Fresh: false},
				{Result: update.Result{Kind: update.Available, Latest: "v1.1.0"}, Fresh: true},
				{Result: update.Result{Kind: update.Available, Latest: "v1.1.0"}, Fresh: false},
			},
		},
		{
			"no newer release is never news",
			[]reply{{"v1.0.0", nil}, {"v1.0.0", nil}},
			[]update.Event{
				{Result: update.Result{Kind: update.UpToDate, Latest: "v1.0.0"}, Fresh: false},
				{Result: update.Result{Kind: update.UpToDate, Latest: "v1.0.0"}, Fresh: false},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := newWorld(t, false, tt.replies...)
			run := start(t, world.deps("v1.0.0"))

			for _, want := range tt.want {
				world.nextTimer().fire <- time.Time{}

				checkEvent(t, world.nextEvent(), want)
			}

			run.stop()
		})
	}
}

func TestRunAsksForTheAppOnlyAboutANewerRelease(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		reply        reply
		wantAppCalls int32
	}{
		{"a newer release", reply{"v1.1.0", nil}, 1},
		{"the same release", reply{"v1.0.0", nil}, 0},
		{"an older release", reply{"v0.9.0", nil}, 0},
		{"a failed fetch", reply{"", errNoNetwork}, 0},
		{"not a version", reply{"garbage", nil}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := newWorld(t, false, tt.reply)
			run := start(t, world.deps("v1.0.0"))

			world.nextTimer().fire <- time.Time{}

			world.nextEvent()
			run.stop()

			if world.appCalls.Load() != tt.wantAppCalls {
				t.Errorf("app questions = %d, want %d", world.appCalls.Load(), tt.wantAppCalls)
			}
		})
	}
}

func TestRunAsksForTheAppOfTheNewerTag(t *testing.T) {
	t.Parallel()

	world := newWorld(t, false, reply{latest: "v1.1.0", err: nil})
	run := start(t, world.deps("v1.0.0"))

	world.nextTimer().fire <- time.Time{}

	world.nextEvent()
	run.stop()

	tag := receive(t, world.appTags, "an app question")
	if tag != "v1.1.0" {
		t.Errorf("tag asked about = %q, want %q", tag, "v1.1.0")
	}
}

func TestRunPublishing(t *testing.T) {
	t.Parallel()

	publishing := update.Result{Kind: update.Publishing, Latest: "v1.1.0"}
	available := update.Result{Kind: update.Available, Latest: "v1.1.0"}
	tests := []struct {
		name       string
		appReplies []appReply
		want       []update.Event
	}{
		{
			"an app attached later is news once",
			[]appReply{{false, nil}, {false, nil}, {true, nil}, {true, nil}},
			[]update.Event{
				{Result: publishing, Fresh: false},
				{Result: publishing, Fresh: false},
				{Result: available, Fresh: true},
				{Result: available, Fresh: false},
			},
		},
		{
			"a failed app question then an attached app is news once",
			[]appReply{{false, errNoNetwork}, {true, nil}},
			[]update.Event{
				{Result: update.Result{Kind: update.Failed, Latest: ""}, Fresh: false},
				{Result: available, Fresh: true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := newWorld(t, false, reply{latest: "v1.1.0", err: nil})
			world.appReplies = tt.appReplies
			run := start(t, world.deps("v1.0.0"))

			for _, want := range tt.want {
				world.nextTimer().fire <- time.Time{}

				checkEvent(t, world.nextEvent(), want)
			}

			run.stop()
		})
	}
}

func TestRunManualWhilePublishing(t *testing.T) {
	t.Parallel()

	world := newWorld(t, false, reply{latest: "v1.1.0", err: nil})
	world.appReplies = []appReply{{false, nil}}
	run := start(t, world.deps("v1.0.0"))

	world.ask()

	checkEvent(t, world.nextEvent(), update.Event{
		Result: update.Result{Kind: update.Publishing, Latest: "v1.1.0"},
		Manual: true,
		Fresh:  true,
	})

	run.stop()
}

func TestRunManual(t *testing.T) {
	t.Parallel()

	available := update.Result{Kind: update.Available, Latest: "v1.1.0"}
	upToDate := update.Result{Kind: update.UpToDate, Latest: "v1.0.0"}
	failed := update.Result{Kind: update.Failed}

	tests := []struct {
		name    string
		replies []reply
		want    update.Result
	}{
		{"a release already told", []reply{{"v1.1.0", nil}, {"v1.1.0", nil}}, available},
		{"a release not told yet", []reply{{"v1.0.0", nil}, {"v1.1.0", nil}}, available},
		{"up to date", []reply{{"v1.0.0", nil}, {"v1.0.0", nil}}, upToDate},
		{"a failure", []reply{{"v1.0.0", nil}, {"", errNoNetwork}}, failed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := newWorld(t, false, tt.replies...)
			run := start(t, world.deps("v1.0.0"))

			world.nextTimer().fire <- time.Time{}

			world.nextEvent()

			world.ask()

			want := update.Event{Result: tt.want, Manual: true, Fresh: true}
			checkEvent(t, world.nextEvent(), want)

			run.stop()
		})
	}
}

func TestRunManualCheckIsNotRepeatedByTheNextAutomaticOne(t *testing.T) {
	t.Parallel()

	world := newWorld(t, false, reply{latest: "v1.1.0", err: nil})
	run := start(t, world.deps("v1.0.0"))

	timer := world.nextTimer()

	world.ask()

	want := update.Event{
		Result: update.Result{Kind: update.Available, Latest: "v1.1.0"},
		Manual: true,
		Fresh:  true,
	}
	checkEvent(t, world.nextEvent(), want)

	timer.fire <- time.Time{}

	want = update.Event{
		Result: update.Result{Kind: update.Available, Latest: "v1.1.0"},
		Manual: false,
		Fresh:  false,
	}
	checkEvent(t, world.nextEvent(), want)

	run.stop()
}

func TestRunAsksMadeDuringACheckMakeOneMoreFetch(t *testing.T) {
	t.Parallel()

	world := newWorld(t, true, reply{latest: "v1.1.0", err: nil})
	run := start(t, world.deps("v1.0.0"))

	world.nextTimer().fire <- time.Time{}

	world.awaitFetch()

	// The app sends without blocking into a buffer of one, so of these asks one is kept.
	world.ask()
	world.ask()

	select {
	case <-world.started:
		t.Fatal("a second Fetch started while the first was still running")
	case <-time.After(testtime.Scaled(quietWait)):
	}

	world.release()
	checkEvent(t, world.nextEvent(), update.Event{
		Result: update.Result{Kind: update.Available, Latest: "v1.1.0"},
		Manual: false,
		Fresh:  true,
	})

	world.awaitFetch()
	world.release()
	checkEvent(t, world.nextEvent(), update.Event{
		Result: update.Result{Kind: update.Available, Latest: "v1.1.0"},
		Manual: true,
		Fresh:  true,
	})

	run.stop()

	if world.calls.Load() != 2 {
		t.Errorf("fetches = %d, want 2: the check and one for the asks", world.calls.Load())
	}

	if world.peak.Load() != 1 {
		t.Errorf("fetches in flight at once = %d, want 1", world.peak.Load())
	}

	if len(world.events) != 0 {
		t.Errorf("%d reports left over, want none", len(world.events))
	}
}

func TestRunFailedCheckWaitsForTheNextInterval(t *testing.T) {
	t.Parallel()

	world := newWorld(t, false, reply{latest: "", err: errNoNetwork})
	run := start(t, world.deps("v1.0.0"))

	world.nextTimer().fire <- time.Time{}

	failed := world.nextEvent()
	if failed.Result.Kind != update.Failed {
		t.Errorf("event kind = %v, want %v", failed.Result.Kind, update.Failed)
	}

	if !errors.Is(failed.Result.Err, errNoNetwork) {
		t.Errorf("event error = %v, want one wrapping %v", failed.Result.Err, errNoNetwork)
	}

	next := world.nextTimer()
	if next.wait != update.CheckEvery {
		t.Errorf("wait after a failure = %s, want %s", next.wait, update.CheckEvery)
	}

	if world.calls.Load() != 1 {
		t.Errorf("fetches after one failure = %d, want 1", world.calls.Load())
	}

	next.fire <- time.Time{}

	world.nextEvent()
	run.stop()

	if world.calls.Load() != 2 {
		t.Errorf("fetches after two intervals = %d, want 2", world.calls.Load())
	}
}

func TestRunReturnsWhenItsContextEnds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		gated     bool
		reach     func(world *world)
		wantCalls int32
	}{
		{
			"during the start wait",
			false,
			func(world *world) { world.nextTimer() },
			0,
		},
		{
			"during the interval wait",
			false,
			func(world *world) {
				world.nextTimer().fire <- time.Time{}

				world.nextEvent()
				world.nextTimer()
			},
			1,
		},
		{
			"during a Fetch that never answers",
			true,
			func(world *world) {
				world.nextTimer().fire <- time.Time{}

				world.awaitFetch()
			},
			1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := newWorld(t, tt.gated, reply{latest: "v1.1.0", err: nil})
			run := start(t, world.deps("v1.0.0"))

			tt.reach(world)
			run.stop()

			if world.calls.Load() != tt.wantCalls {
				t.Errorf("fetches = %d, want %d", world.calls.Load(), tt.wantCalls)
			}

			if world.inFlight.Load() != 0 {
				t.Errorf("fetches still running = %d, want 0", world.inFlight.Load())
			}
		})
	}
}

func TestRunEndedDuringAnAppQuestionReportsNothing(t *testing.T) {
	t.Parallel()

	world := newWorld(t, false, reply{latest: "v1.1.0", err: nil})
	world.appHeld = true
	run := start(t, world.deps("v1.0.0"))

	world.nextTimer().fire <- time.Time{}

	receive(t, world.appTags, "an app question")
	run.stop()

	if len(world.events) != 0 {
		t.Errorf("%d reports after the context ended, want none", len(world.events))
	}
}

func TestRunEndedDuringAFetchReportsNothing(t *testing.T) {
	t.Parallel()

	world := newWorld(t, true, reply{latest: "v1.1.0", err: nil})
	run := start(t, world.deps("v1.0.0"))

	world.nextTimer().fire <- time.Time{}

	world.awaitFetch()
	run.stop()

	if len(world.events) != 0 {
		t.Errorf("%d reports after the context ended, want none", len(world.events))
	}
}

func TestRunWithoutAReleaseVersionNeverFetches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current string
	}{
		{"a dev build", "dev"},
		{"nothing", ""},
		{"a version without the v", "1.2.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			world := newWorld(t, false, reply{latest: "v9.9.9", err: nil})
			run := start(t, world.deps(tt.current))

			want := update.Event{
				Result: update.Result{Kind: update.Unchecked},
				Manual: true,
				Fresh:  true,
			}

			world.ask()
			checkEvent(t, world.nextEvent(), want)

			world.ask()
			checkEvent(t, world.nextEvent(), want)

			run.stop()

			if world.calls.Load() != 0 {
				t.Errorf("fetches = %d, want 0", world.calls.Load())
			}

			if len(world.timers) != 0 {
				t.Errorf("waits asked for = %d, want 0: no automatic check", len(world.timers))
			}
		})
	}
}

func TestRunPanicsOnMissingDeps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		remove func(deps *update.Deps)
	}{
		{"no Fetch", func(deps *update.Deps) { deps.Fetch = nil }},
		{"no AppAttached", func(deps *update.Deps) { deps.AppAttached = nil }},
		{"no After", func(deps *update.Deps) { deps.After = nil }},
		{"no Report", func(deps *update.Deps) { deps.Report = nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			deps := newWorld(t, false).deps("v1.0.0")
			tt.remove(&deps)

			defer func() {
				if recover() == nil {
					t.Error("Run did not panic")
				}
			}()

			update.Run(t.Context(), deps)
		})
	}
}
