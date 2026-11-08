package app

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/claytoncasey01/conn/internal/hosted"
	"github.com/claytoncasey01/conn/internal/pty"
	"github.com/claytoncasey01/conn/internal/secret"
	"github.com/claytoncasey01/conn/internal/server"
	"github.com/claytoncasey01/conn/internal/settings"
	"github.com/claytoncasey01/conn/internal/testtime"
)

// serveWait bounds each wait of the serve tests.
const serveWait = 10 * time.Second

// The quit takes the record when it begins, while the server still answers: a session that ends
// while open requests drain ran at the quit, and stays recorded.
func TestTheQuitRecordsBeforeTheServerDrains(t *testing.T) {
	t.Parallel()

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	address := "http://" + listener.Addr().String()
	httpServer := newHTTPServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))

	// The first record is the quit's: one taken again at its end is no change.
	answered := make(chan bool, 1)

	var first sync.Once

	none := func() {}
	steps := quitSteps{
		record:         func() { first.Do(func() { answered <- answers(t, address) }) },
		closeTerminals: none, cancel: none, waitResume: none, stopSessions: none, release: none,
	}

	ctx, cancel := context.WithCancel(t.Context())
	running := &Server{
		URL: address, Secret: secret.Secret{}, done: make(chan struct{}), err: nil,
		resumed: make(chan struct{}),
	}

	go running.serve(ctx, httpServer, listener, steps)

	cancel()

	select {
	case <-running.Done():
	case <-time.After(testtime.Scaled(serveWait)):
		t.Fatal("the server did not stop")
	}

	select {
	case still := <-answered:
		if !still {
			t.Error("the record was taken after the server stopped answering")
		}
	default:
		t.Error("the quit took no record")
	}
}

// answers is whether the server at address answers a request now.
func answers(t *testing.T, address string) bool {
	t.Helper()

	client := &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true}, CheckRedirect: nil, Jar: nil,
		Timeout: testtime.Scaled(serveWait),
	}

	request, err := http.NewRequestWithContext(context.WithoutCancel(t.Context()),
		http.MethodGet, address, nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.Do(request)
	if err != nil {
		return false
	}

	_ = response.Body.Close()

	return true
}

// A quit waits for the launch's resume to end, but no longer than its limit.
func TestWaitForResume(t *testing.T) {
	t.Parallel()

	ended := make(chan struct{})
	close(ended)

	tests := []struct {
		name    string
		resumed chan struct{}
		timeout time.Duration
	}{
		{"a resume that ended", ended, neverTimesOut},
		{"a resume that does not end", make(chan struct{}), timesOutAtOnce},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			waited := make(chan struct{})

			go func() {
				defer close(waited)

				waitForResume(tt.resumed, tt.timeout)
			}()

			select {
			case <-waited:
			case <-time.After(testtime.Scaled(serveWait)):
				t.Fatal("the quit kept waiting")
			}
		})
	}
}

// The records are carried before the launch serves: a quit that comes before the resume has
// done anything still records every one of them.
func TestTheRecordsAreCarriedBeforeTheLaunchServes(t *testing.T) {
	t.Parallel()

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	var written []hosted.Record

	recorder := hosted.NewRecorder(hosted.RecorderDeps{
		Dir: "/data", Live: func() []pty.Info { return nil },
		Linked: func() map[pty.ID]string { return nil },
		Write: func(_ string, records []hosted.Record) error {
			written = slices.Clone(records)

			return nil
		},
		Failed: func(err error) { t.Error(err) },
	})
	host := pty.NewHost(pty.Config{
		Spawner: func(pty.Spec) (pty.Process, error) { return nil, errNeverSpawns },
		Limits:  pty.DefaultLimits(), Answers: pty.Answers{}, TurnWaited: nil,
	})
	// The session list never ticks: the resume waits, and has done nothing, when the quit
	// records.
	life := hostedLife{
		recorder: recorder, checks: resumeChecks(), resumed: newResumedSessions(host),
		bump: func() {}, ticked: make(chan struct{}), tickTimeout: neverTimesOut,
		settle: newResumeSettle(),
	}
	records := []hosted.Record{resumeRecord()}

	ctx, cancel := context.WithCancel(t.Context())
	running := newServer("http://"+listener.Addr().String(), secret.Secret{})
	none := func() {}

	running.serveAndResume(ctx, launched{
		handler: &server.Server{}, listener: listener, life: life, host: host,
		recorded: launchResume{
			sessions: settings.Sessions{Where: settings.WhereInside, ResumeOnLaunch: true},
			records:  records, readErr: nil, checks: nil, start: nil, ready: nil,
			stderr: &bytes.Buffer{},
		},
		steps: quitSteps{
			record: none, closeTerminals: none, cancel: none, waitResume: none,
			stopSessions: none, release: none,
		},
	})

	recorder.Freeze()
	cancel()
	<-running.Done()

	if !slices.Equal(written, records) {
		t.Errorf("the quit recorded %+v, want every record carried", written)
	}
}
