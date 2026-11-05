package update

import (
	"context"
	"time"
)

// Event is what one check found and why it ran.
type Event struct {
	// Result is what the check found.
	Result Result
	// Manual is a check the user asked for, rather than one the clock started.
	Manual bool
	// Fresh is true the first time this run reports that Latest as Available, so the app shows
	// its dialog once per version. It is always true for a Manual check, because the person
	// asked and is waiting for the answer.
	Fresh bool
}

// Deps is everything Run touches outside itself.
type Deps struct {
	// Current is the version of this build, such as "v1.2.3". A build with no release version
	// (a dev build) is never checked on its own, and an ask is answered with Unchecked.
	Current string
	// Fetch reads the latest release tag (Latest with the app's client). It must end when ctx
	// does.
	Fetch func(ctx context.Context) (string, error)
	// AppAttached says whether the app is attached to the release tag (AppAttached with the
	// app's client). Run asks it only about a release newer than Current, which GitHub names as
	// the latest as soon as it is published, before the app is built and attached. It must end
	// when ctx does.
	AppAttached func(ctx context.Context, tag string) (bool, error)
	// After is a channel that fires once d has passed (time.After), so a test never sleeps.
	After func(d time.Duration) <-chan time.Time
	// Asked receives a value each time the user chooses to check now. The app gives it a buffer
	// of one and sends without blocking, so asks made during a check become at most one more.
	// It may be nil, which is no asks.
	Asked <-chan struct{}
	// Report is told the outcome of every check, in order, from Run's own goroutine. It must
	// return promptly: Run cannot return while it blocks, and a panic in it is the caller's.
	Report func(Event)
}

// checker is the state of one Run: its Deps, whether Current is a release version, and the
// release it last told the app about.
type checker struct {
	deps      Deps
	versioned bool
	// told is the highest release tag reported as Fresh. Only that one is kept, so the memory is
	// fixed, and an older release published again after a newer one is not news.
	told string
}

// Run checks for a newer release startDelay after it starts and every checkEvery after the last
// automatic check, and at once whenever Asked fires, until ctx ends. One goroutine does all of
// it, so there is never a second request in flight: an ask made during a check waits for it.
// A failed automatic check is reported and not retried before the next interval. A Current that
// is not a release version makes no request at all, and an ask is answered with Unchecked.
func Run(ctx context.Context, deps Deps) {
	requireDeps(deps)

	_, err := parse(deps.Current)
	state := checker{deps: deps, versioned: err == nil, told: ""}

	// A nil channel never fires, which is how a build with no version never checks by itself.
	var automatic <-chan time.Time
	if state.versioned {
		automatic = deps.After(startDelay)
	}

	for {
		// A done context wins over a timer or an ask that fired at the same moment.
		if ctx.Err() != nil {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-automatic:
			state.check(ctx, false)

			automatic = deps.After(checkEvery)
		case <-deps.Asked:
			state.check(ctx, true)
		}
	}
}

// requireDeps panics when a function Run calls is missing: that is a bug in the caller.
func requireDeps(deps Deps) {
	missing := []struct {
		name    string
		missing bool
	}{
		{"Fetch", deps.Fetch == nil},
		{"AppAttached", deps.AppAttached == nil},
		{"After", deps.After == nil},
		{"Report", deps.Report == nil},
	}

	for _, dep := range missing {
		if dep.missing {
			panic("update.Run: Deps." + dep.name + " is not set")
		}
	}
}

// check makes one check and reports it, unless ctx ended meanwhile: a check cut short by the
// app quitting is not a failure to tell anyone about.
func (state *checker) check(ctx context.Context, manual bool) {
	result := state.read(ctx)

	if ctx.Err() != nil {
		return
	}

	fresh := state.fresh(result, manual)
	state.deps.Report(Event{Result: result, Manual: manual, Fresh: fresh})
}

// read asks for the latest release and, only when it is newer than this build, whether its app
// is attached yet: one request, or two for a newer release, and none for a build with no release
// version.
func (state *checker) read(ctx context.Context) Result {
	if !state.versioned {
		return Result{Kind: Unchecked, Latest: "", Err: nil}
	}

	latest, err := state.deps.Fetch(ctx)

	result := evaluate(state.deps.Current, latest, err)
	if result.Kind != Available {
		return result
	}

	attached, err := state.deps.AppAttached(ctx, result.Latest)

	return withApp(result, attached, err)
}

// fresh says whether result is news to the person, and remembers the highest available release
// as told. A manual check is always news, because the person asked and is waiting for it. Only an
// Available result is remembered, so a release first seen while Publishing is still news once its
// app is attached.
func (state *checker) fresh(result Result, manual bool) bool {
	if result.Kind != Available {
		return manual
	}

	news := state.told == ""
	if !news {
		news = newer(state.told, result.Latest)
	}

	if news {
		state.told = result.Latest
	}

	if manual {
		return true
	}

	return news
}
