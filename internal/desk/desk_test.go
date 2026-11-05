package desk_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/app"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/desk"
	"github.com/claytoncasey01/conn/internal/instance"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// runWait bounds a Run in a test: one that does not return in this long has looped.
const runWait = 10 * time.Second

// deadURL is where a holder says it listens when nothing does.
const deadURL = "http://127.0.0.1:1"

// startedURL stands in a wanted list for the address of the server the test's Start started.
const startedURL = "<started>"

var (
	errNoAnswer             = errors.New("no answer")
	errNotOK                = errors.New("not a 200")
	errStartedOnAHeldFolder = errors.New("start was called on a held folder")
	errLockUnreadable       = errors.New("the lock file is unreadable")
)

// errHeldMeanwhile is what Start gives when another Conn took the folder after Run looked.
func errHeldMeanwhile() error {
	return fmt.Errorf("taking the folder: %w", instance.ErrHeld)
}

func TestDecide(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		running   bool
		holderURL string
		want      desk.Action
	}{
		{"a free lock is ours to take", false, "", desk.Own},
		{"a lock held with an address is one to attach to", true, deadURL, desk.Attach},
		{"a lock held without an address yet is one to wait for", true, "", desk.WaitForAddress},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := desk.Decide(tt.running, tt.holderURL)
			if got != tt.want {
				t.Errorf("Decide(%t, %q) = %v, want %v", tt.running, tt.holderURL, got, tt.want)
			}
		})
	}
}

func TestRestarts(t *testing.T) {
	t.Parallel()

	spread := desk.RestartWindow / 3

	tests := []struct {
		name  string
		times []time.Duration
		want  bool
	}{
		{"restartsMax together are allowed", repeated(desk.RestartsMax, 0), false},
		{"one more together is too many", repeated(desk.RestartsMax+1, 0), true},
		{
			"restarts spread over more than the window are allowed",
			[]time.Duration{0, spread, 2 * spread, 3 * spread, 4 * spread, 5 * spread},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var counted desk.Restarts

			base := time.Date(2026, time.November, 4, 9, 0, 0, 0, time.UTC)
			exceeded := false

			for _, offset := range tt.times {
				exceeded = desk.RecordRestart(&counted, base.Add(offset))
			}

			if exceeded != tt.want {
				t.Errorf("the last restart exceeded = %t, want %t", exceeded, tt.want)
			}
		})
	}
}

func repeated(count int, offset time.Duration) []time.Duration {
	offsets := make([]time.Duration, count)
	for index := range offsets {
		offsets[index] = offset
	}

	return offsets
}

// reply is what a fake Holder answers once.
type reply struct {
	holder  instance.Holder
	running bool
	err     error
}

func free() reply { return reply{holder: instance.Holder{}, running: false, err: nil} }

func held(url string) reply {
	return reply{holder: instance.Holder{PID: 1, URL: url}, running: true, err: nil}
}

func holderFails(err error) reply {
	return reply{holder: instance.Holder{}, running: false, err: err}
}

// scenario is one Run's world: the answers its fakes give, and what they were asked.
type scenario struct {
	replies []reply
	// probes and startErrs are answered in order, and the last one answers every call after.
	// A nil error is an answer too: the probe was answered, or Start really starts a server.
	probes    []error
	startErrs []error
	// cancelAfterOpens and cancelAfterProbes end the context once Open, or Probe, has been called
	// this many times. endServerAfterOpens ends the server Start gave last, from outside Run,
	// once Open has been called this many times. cancelOnWait ends the context when Run first
	// waits for a moment to pass, and then lets that moment never come.
	cancelAfterOpens    int
	cancelAfterProbes   int
	endServerAfterOpens int
	cancelOnWait        bool
	env                 map[string]string
	cancel              context.CancelFunc

	asked     int
	probed    int
	starts    int
	endServer context.CancelFunc
	opened    []string
	started   []*app.Server
	startDone <-chan struct{}
	failed    []string
}

func (s *scenario) holder() (instance.Holder, bool, error) {
	index := min(s.asked, len(s.replies)-1)
	s.asked++

	return s.replies[index].holder, s.replies[index].running, s.replies[index].err
}

func (s *scenario) probe(_ context.Context, _ string) error {
	index := min(s.probed, len(s.probes)-1)
	s.probed++

	if s.probed == s.cancelAfterProbes {
		s.cancel()
	}

	return s.probes[index]
}

func (s *scenario) open(url string) {
	s.opened = append(s.opened, url)

	if len(s.opened) == s.cancelAfterOpens {
		s.cancel()
	}

	if len(s.opened) == s.endServerAfterOpens {
		s.endServer()
	}
}

func (s *scenario) fail(message string) { s.failed = append(s.failed, message) }

// start is app.Start on a data folder of its own, so the server it gives is a real one, unless
// the scenario says Start fails.
func (s *scenario) start(ctx context.Context) (*app.Server, error) {
	s.startDone = ctx.Done()

	index := min(s.starts, len(s.startErrs)-1)
	s.starts++

	if index >= 0 {
		if s.startErrs[index] != nil {
			return nil, s.startErrs[index]
		}
	}

	// The server gets a context of its own, so a scenario can end it while Run's context lives.
	serverCtx, endServer := context.WithCancel(ctx)
	s.endServer = endServer

	server, err := app.Start(serverCtx, lookupFrom(s.env), io.Discard)
	if err != nil {
		return nil, fmt.Errorf("starting a real server: %w", err)
	}

	s.started = append(s.started, server)

	return server, nil
}

// instantly is an After whose time has always already passed, so no test sleeps.
func instantly(time.Duration) <-chan time.Time {
	fired := make(chan time.Time, 1)
	fired <- time.Time{}

	return fired
}

func (s *scenario) deps(now func() time.Time) desk.Deps {
	return desk.Deps{
		Start:  s.start,
		Holder: s.holder,
		Probe:  s.probe,
		Open:   s.open,
		Fail:   s.fail,
		Now:    now,
		After:  s.after,
	}
}

// after is instantly, or a moment that never comes once the scenario has ended the context.
func (s *scenario) after(wait time.Duration) <-chan time.Time {
	if s.cancelOnWait {
		s.cancel()

		return nil
	}

	return instantly(wait)
}

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		replies             []reply
		probes              []error
		startErrs           []error
		cancelAfterOpens    int
		cancelAfterProbes   int
		endServerAfterOpens int
		cancelOnWait        bool
		wantErr             error
		wantOpened          []string
		wantStarts          int
		wantFails           int
		wantFailHas         []string
		wantStopped         bool
	}{
		{
			name:    "a free lock is owned, and the end of the context stops the server it started",
			replies: []reply{free()}, cancelAfterOpens: 1, wantErr: nil,
			wantOpened: []string{startedURL}, wantStarts: 1, wantFails: 0, wantStopped: true,
		},
		{
			name:    "a lock held with an address is attached to, and the server left alone",
			replies: []reply{held(deadURL)}, cancelAfterOpens: 1, wantErr: nil,
			wantOpened: []string{deadURL}, wantStarts: 0, wantFails: 0,
		},
		{
			name:    "a lock held without an address waits for it, then attaches",
			replies: []reply{held(""), held(deadURL)}, cancelAfterOpens: 1, wantErr: nil,
			wantOpened: []string{deadURL}, wantStarts: 0, wantFails: 0,
		},
		{
			name:    "an attached server that stops answering and frees the lock is replaced",
			replies: []reply{held(deadURL), free()}, probes: []error{errNoAnswer},
			cancelAfterOpens: 2, wantErr: nil,
			wantOpened: []string{deadURL, startedURL}, wantStarts: 1, wantFails: 0,
			wantStopped: true,
		},
		{
			name:    "an attached server that never answers again gives up after too many restarts",
			replies: []reply{held(deadURL)}, probes: []error{errNoAnswer},
			cancelAfterOpens: 0, wantErr: desk.ErrGaveUp,
			wantOpened: repeatedURLs(deadURL, desk.RestartsMax+1), wantStarts: 0, wantFails: 1,
			wantFailHas: []string{deadURL},
		},
		{
			name:    "a port another program holds is a message naming the port and the variable",
			replies: []reply{free()}, startErrs: []error{app.PortTakenError{Port: 7777}},
			cancelAfterOpens: 0, wantErr: app.PortTakenError{Port: 7777},
			wantOpened: nil, wantStarts: 0, wantFails: 1,
			wantFailHas: []string{"7777", "CONN_PORT"},
		},
		{
			name:    "an owned server that ends by itself is replaced by a new one",
			replies: []reply{free()}, endServerAfterOpens: 1, cancelAfterOpens: 2, wantErr: nil,
			wantOpened: []string{startedURL, startedURL}, wantStarts: 2, wantFails: 0,
			wantStopped: true,
		},
		{
			name:    "a folder taken between the look and the start is looked at again",
			replies: []reply{free()}, startErrs: []error{errHeldMeanwhile(), nil},
			cancelAfterOpens: 1, wantErr: nil,
			wantOpened: []string{startedURL}, wantStarts: 1, wantFails: 0, wantStopped: true,
		},
		{
			name:    "a folder taken between the look and the start is attached to when seen",
			replies: []reply{free(), held(deadURL)}, startErrs: []error{errHeldMeanwhile()},
			cancelAfterOpens: 1, wantErr: nil,
			wantOpened: []string{deadURL}, wantStarts: 0, wantFails: 0,
		},
		{
			name:    "answers now and then keep an attached server, however many go missing",
			replies: []reply{held(deadURL)}, cancelAfterProbes: 9, wantErr: nil,
			probes: []error{
				errNoAnswer, errNoAnswer, nil, errNoAnswer, errNoAnswer, nil,
				errNoAnswer, errNoAnswer, nil,
			},
			wantOpened: []string{deadURL}, wantStarts: 0, wantFails: 0,
		},
		{
			name:    "a holder that never says where it listens is given up on after the restarts",
			replies: []reply{held("")}, wantErr: desk.ErrGaveUp,
			wantOpened: nil, wantStarts: 0, wantFails: 1,
		},
		{
			name:    "a lock that cannot be looked at is a message and the end of Run",
			replies: []reply{holderFails(errLockUnreadable)}, wantErr: errLockUnreadable,
			wantOpened: nil, wantStarts: 0, wantFails: 1, wantFailHas: []string{"unreadable"},
		},
		{
			name:    "a lock that cannot be looked at in a wait is a message and the end of Run",
			replies: []reply{held(""), holderFails(errLockUnreadable)}, wantErr: errLockUnreadable,
			wantOpened: nil, wantStarts: 0, wantFails: 1, wantFailHas: []string{"unreadable"},
		},
		{
			name:    "the end of the context while waiting for an address ends Run",
			replies: []reply{held("")}, cancelOnWait: true, wantErr: nil,
			wantOpened: nil, wantStarts: 0, wantFails: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(runWait))
			defer cancel()

			world := &scenario{
				replies: tt.replies, probes: tt.probes, startErrs: tt.startErrs,
				cancelAfterOpens: tt.cancelAfterOpens, cancelAfterProbes: tt.cancelAfterProbes,
				endServerAfterOpens: tt.endServerAfterOpens, cancelOnWait: tt.cancelOnWait,
				env: environment(t), cancel: cancel, asked: 0, probed: 0, starts: 0,
				endServer: nil, opened: nil, started: nil, startDone: nil, failed: nil,
			}

			err := desk.Run(ctx, world.deps(time.Now))

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Run error = %v, want %v", err, tt.wantErr)
			}

			checkWorld(t, world, tt.wantOpened, tt.wantStarts, tt.wantFails, tt.wantFailHas)

			if tt.wantStopped {
				checkStopped(t, world)
			}
		})
	}
}

func repeatedURLs(url string, count int) []string {
	urls := make([]string, count)
	for index := range urls {
		urls[index] = url
	}

	return urls
}

func checkWorld(
	t *testing.T, world *scenario, wantOpened []string, wantStarts, wantFails int,
	wantFailHas []string,
) {
	t.Helper()

	if len(world.started) != wantStarts {
		t.Errorf("servers started = %d, want %d", len(world.started), wantStarts)
	}

	if len(world.failed) != wantFails {
		t.Errorf("failures shown = %q, want %d of them", world.failed, wantFails)
	}

	for _, part := range wantFailHas {
		if len(world.failed) == 0 {
			break
		}

		if !strings.Contains(world.failed[0], part) {
			t.Errorf("failure %q does not mention %q", world.failed[0], part)
		}
	}

	if len(world.opened) != len(wantOpened) {
		t.Fatalf("windows opened on %q, want %q", world.opened, wantOpened)
	}

	startedSoFar := 0

	for index, want := range wantOpened {
		if want == startedURL {
			want = world.started[startedSoFar].URL
			startedSoFar++
		}

		if world.opened[index] != want {
			t.Errorf("open %d was %q, want %q", index, world.opened[index], want)
		}
	}
}

// checkStopped fails the test unless the end of Run's context ended the servers Run started, and
// they have let their folder go.
func checkStopped(t *testing.T, world *scenario) {
	t.Helper()

	select {
	case <-world.startDone:
	default:
		t.Error("the context Start was given is still live after Run returned")
	}

	for _, server := range world.started {
		select {
		case <-server.Done():
		default:
			t.Errorf("the server on %s was still running when Run returned", server.URL)
		}
	}
}

// TestRunAttachesToARealServer is the one test with real servers: a second window on a folder a
// Conn already serves opens on it and checks it, and leaves it running when the window goes.
func TestRunAttachesToARealServer(t *testing.T) {
	t.Parallel()

	env := environment(t)

	ownerCtx, stopOwner := context.WithCancel(t.Context())
	defer stopOwner()

	owner, err := app.Start(ownerCtx, lookupFrom(env), io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		stopOwner()
		<-owner.Done()
	})

	checked := make(chan struct{})
	opened := make(chan string, 1)
	starts := 0
	probes := 0

	deps := desk.Deps{
		Start: func(context.Context) (*app.Server, error) {
			starts++

			return nil, errStartedOnAHeldFolder
		},
		Holder: func() (instance.Holder, bool, error) { return instance.Running(env["CONN_DATA"]) },
		Probe: func(ctx context.Context, url string) error {
			probes++
			if probes == 2 {
				defer close(checked)
			}

			return get(ctx, url+"/")
		},
		Open: func(url string) { opened <- url },
		Fail: func(message string) { t.Errorf("a window was told: %s", message) },
		Now:  time.Now,
		After: func(time.Duration) <-chan time.Time {
			if probes < 2 {
				return instantly(0)
			}

			return nil
		},
	}

	ctx, cancel := context.WithTimeout(t.Context(), testtime.Scaled(runWait))
	defer cancel()

	result := make(chan error, 1)

	go func() { result <- desk.Run(ctx, deps) }()

	if url := <-opened; url != owner.URL {
		t.Errorf("the window opened on %q, want the owner's %q", url, owner.URL)
	}

	<-checked
	cancel()

	err = <-result
	if err != nil {
		t.Errorf("Run error = %v after its context ended, want nil", err)
	}

	if starts != 0 {
		t.Errorf("Start was called %d times on a held folder, want 0", starts)
	}

	err = get(t.Context(), owner.URL+"/")
	if err != nil {
		t.Errorf("the owner does not answer after the window went: %v", err)
	}
}

// get is one GET that succeeds on a 200.
func get(ctx context.Context, url string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("building the request: %w", err)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("asking %s: %w", url, err)
	}

	_ = response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s", errNotOK, response.Status)
	}

	return nil
}

// environment is one Conn's: its own data folder, home and page bundle, on a port the system
// picks.
func environment(t *testing.T) map[string]string {
	t.Helper()

	dist := t.TempDir()

	err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("<!doctype html>"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return map[string]string{
		"CONN_PORT": "0", "CONN_DATA": t.TempDir(), "HOME": t.TempDir(), "CONN_DIST": dist,
	}
}

func lookupFrom(env map[string]string) config.LookupFunc {
	return func(key string) (string, bool) {
		value, found := env[key]

		return value, found
	}
}
