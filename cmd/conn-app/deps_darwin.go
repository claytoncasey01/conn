package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/claytoncasey01/conn/internal/app"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/desk"
	"github.com/claytoncasey01/conn/internal/instance"
	"github.com/claytoncasey01/conn/internal/secret"
)

const (
	// probePath is the route a probe asks: the server's health. It is a liveness check only: the
	// server answers it before any other route, with no disk, process or network work, so only a
	// server that is gone fails it, never a disk check that would make the window reload. It is an
	// /api route, so the probe carries the server's secret. The server's tests and conn's command
	// tests use it too.
	probePath = "/api/health"

	// probeTimeout bounds one probe, connection and answer together, and each step of it. It is
	// desk's limit for a probe, so the client never outlives the context desk gives it, and holds
	// even if desk stops giving one.
	probeTimeout = desk.ProbeTimeout

	// probeIdleTimeout is how long the probe's connection is kept between probes: longer than
	// the 2 s desk waits between them, so one connection serves them all.
	probeIdleTimeout = 5 * time.Second

	// probeKeepAlive is how often an idle probe connection is checked by TCP.
	probeKeepAlive = 15 * time.Second

	// maxProbeHeaderBytes caps a probe answer's headers: the server sends a handful.
	maxProbeHeaderBytes = 16 << 10

	// maxProbeBodyBytes is as much of a probe's answer as is read: {"ok":true,"clients":N}.
	maxProbeBodyBytes = 1 << 10

	// probeIdleConnections is how many connections the probe keeps open between probes: one
	// server, asked one question at a time.
	probeIdleConnections = 1

	// probeTLSHandshakeTimeout bounds a TLS handshake. The probe asks a loopback http address, so
	// none happens; the limit is set so that an https address would still be bounded.
	probeTLSHandshakeTimeout = probeTimeout

	// probeExpectContinueTimeout is 0: a probe is a GET with no body, so it never sends
	// "Expect: 100-continue" and has nothing to wait for.
	probeExpectContinueTimeout = 0

	// failTitle is the title of the dialog that says the window cannot go on.
	failTitle = "Conn cannot open"
)

// errProbeStatus is a server that answered a probe with something other than 200 OK.
var errProbeStatus = errors.New("the server did not answer OK")

// deps is what desk.Run touches: a Conn started in this process with its environment, the lock
// of the data folder that environment names, a probe over loopback, this window and the clock.
func (s *shell) deps(lookup config.LookupFunc, stderr io.Writer) desk.Deps {
	dataDir := config.FromEnv(lookup).DataDir
	client := newProbeClient()

	return desk.Deps{
		Start: func(ctx context.Context) (*app.Server, error) {
			return app.Start(ctx, lookup, stderr)
		},
		Holder: func() (instance.Holder, bool, error) {
			return instance.Running(dataDir)
		},
		Probe: func(ctx context.Context, url string, key secret.Secret) error {
			return probe(ctx, client, url, key)
		},
		Open:  s.open,
		Fail:  s.fail,
		After: time.After,
	}
}

// open loads page in the window, and keeps it as the server whose pages the window's links may
// load (only its origin is compared). SetURL runs on the main thread itself. The page's address
// holds the secret in its fragment, so it is never printed.
func (s *shell) open(page string) {
	s.shown.Store(&page)
	s.window.SetURL(page)
}

// shownURL is the address the window was last opened on, or "" before the first.
func (s *shell) shownURL() string {
	shown := s.shown.Load()
	if shown == nil {
		return ""
	}

	return *shown
}

// fail shows message in a native dialog whose one button quits the app. It returns once the
// button is clicked (Wails' Show runs the dialog in place on the main thread and waits for it),
// and desk.Run returns right after it, so deskDone is closed while the quit waits for it. The
// quit runs on its own goroutine because the button's callback runs on the main thread, which
// the quit needs.
func (s *shell) fail(message string) {
	dialog := s.app.Dialog.Error().SetTitle(failTitle).SetMessage(message)
	dialog.AddButton("Quit").SetAsDefault().OnClick(func() {
		go s.app.Quit()
	})
	dialog.Show()
}

// newProbeClient is the HTTP client for probes: loopback http only, so no proxy, no HTTP/2 (it
// is negotiated only over TLS) and no TLS settings beyond a floor for a case that cannot happen,
// connections kept between probes, and a redirect is an answer to report, not one to follow.
func newProbeClient() *http.Client {
	dialer := &net.Dialer{Timeout: probeTimeout, KeepAlive: probeKeepAlive}

	return &http.Client{
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            dialer.DialContext,
			TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout:    probeTLSHandshakeTimeout,
			ExpectContinueTimeout:  probeExpectContinueTimeout,
			DisableKeepAlives:      false,
			ForceAttemptHTTP2:      false,
			MaxIdleConns:           probeIdleConnections,
			MaxIdleConnsPerHost:    probeIdleConnections,
			MaxConnsPerHost:        probeIdleConnections,
			IdleConnTimeout:        probeIdleTimeout,
			ResponseHeaderTimeout:  probeTimeout,
			MaxResponseHeaderBytes: maxProbeHeaderBytes,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Jar:     nil,
		Timeout: probeTimeout,
	}
}

// probe asks the Conn at url, with its secret, whether it is there: nil when it answers 200 OK.
// An error names url, which never holds the secret.
func probe(ctx context.Context, client *http.Client, url string, key secret.Secret) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url+probePath, http.NoBody)
	if err != nil {
		return fmt.Errorf("building the probe of %s: %w", url, err)
	}

	request.Header.Set(secret.Header, key.Authorization())

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("probing %s: %w", url, err)
	}

	defer func() { _ = response.Body.Close() }()

	// Reading the answer lets the connection be used again; it is small and capped.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxProbeBodyBytes))

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s gave %s", errProbeStatus, url, response.Status)
	}

	return nil
}
