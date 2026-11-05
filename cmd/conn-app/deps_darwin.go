package main

import (
	"context"
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
)

const (
	// probePath is the route a probe asks: the list of the agents and skills Conn provides. The
	// page reads it too, so it is part of the contract with the UI and does not change unnoticed;
	// it reads a few links on disk and starts nothing.
	probePath = "/api/provided"

	// probeTimeout bounds one probe, connection and answer together, and each step of it. desk
	// gives each probe a context with its own limit too; this one holds even if that changes.
	probeTimeout = 2 * time.Second

	// probeIdleTimeout is how long the probe's connection is kept between probes: longer than
	// the 2 s desk waits between them, so one connection serves them all.
	probeIdleTimeout = 5 * time.Second

	// probeKeepAlive is how often an idle probe connection is checked by TCP.
	probeKeepAlive = 15 * time.Second

	// probeHeaderBytesMax caps a probe answer's headers: the server sends a handful.
	probeHeaderBytesMax = 16 << 10

	// probeBodyBytesMax is as much of a probe's answer as is read: a short list of names and
	// paths.
	probeBodyBytesMax = 64 << 10

	// probeIdleConnections is how many connections the probe keeps open between probes: one
	// server, asked one question at a time.
	probeIdleConnections = 1

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
		Probe: func(ctx context.Context, url string) error {
			return probe(ctx, client, url)
		},
		Open:  s.open,
		Fail:  s.fail,
		Now:   time.Now,
		After: time.After,
	}
}

// open loads url in the window. SetURL runs on the main thread itself.
func (s *shell) open(url string) {
	s.window.SetURL(url)
}

// fail shows message in a native dialog whose one button quits the app. It returns at once:
// desk.Run returns right after it, so deskDone is closed before the quit waits for it. The
// quit runs on its own goroutine because the button's callback runs on the main thread, which
// the quit needs.
func (s *shell) fail(message string) {
	dialog := s.app.Dialog.Error().SetTitle(failTitle).SetMessage(message)
	dialog.AddButton("Quit").SetAsDefault().OnClick(func() {
		go s.app.Quit()
	})
	dialog.Show()
}

// newProbeClient is the HTTP client for probes: loopback only, so no proxy, and a redirect is
// an answer to report, not one to follow.
func newProbeClient() *http.Client {
	dialer := &net.Dialer{Timeout: probeTimeout, KeepAlive: probeKeepAlive}

	return &http.Client{
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            dialer.DialContext,
			MaxIdleConns:           probeIdleConnections,
			MaxIdleConnsPerHost:    probeIdleConnections,
			MaxConnsPerHost:        probeIdleConnections,
			IdleConnTimeout:        probeIdleTimeout,
			ResponseHeaderTimeout:  probeTimeout,
			MaxResponseHeaderBytes: probeHeaderBytesMax,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Jar:     nil,
		Timeout: probeTimeout,
	}
}

// probe asks the Conn at url whether it is there: nil when it answers 200 OK.
func probe(ctx context.Context, client *http.Client, url string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url+probePath, http.NoBody)
	if err != nil {
		return fmt.Errorf("building the probe of %s: %w", url, err)
	}

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("probing %s: %w", url, err)
	}

	defer func() { _ = response.Body.Close() }()

	// Reading the answer lets the connection be used again; it is small and capped.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, probeBodyBytesMax))

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s gave %s", errProbeStatus, url, response.Status)
	}

	return nil
}
