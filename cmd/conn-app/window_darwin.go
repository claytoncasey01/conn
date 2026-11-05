package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/desk"
)

const (
	// appName is the name in the menu bar, the Dock and the About box.
	appName = "Conn"

	// The window's first size and the smallest it may be made, in points.
	windowWidth     = 1280
	windowHeight    = 820
	windowWidthMin  = 720
	windowHeightMin = 480

	// deskStopTimeout bounds the wait at quit for desk.Run to return: a little above the server's
	// own 2 s shutdown, so an owned server shuts down and lets the data folder go first.
	deskStopTimeout = 3 * time.Second
)

// shell is the running app: the Wails application, its one window, and the desk.Run it waits
// for at quit.
type shell struct {
	app    *application.App
	window *application.WebviewWindow

	// stopDesk ends the context desk.Run runs on.
	stopDesk context.CancelFunc
	// deskDone is closed when desk.Run has returned.
	deskDone chan struct{}
}

// run opens the window on the splash, then runs desk.Run once the app has started, and is the
// exit code. On macOS a quit ends the process from inside Wails, so the wait for desk.Run happens
// in OnShutdown, not after app.Run returns.
func run(loadErr error, lookup config.LookupFunc, stderr io.Writer) int {
	deskCtx, stopDesk := context.WithCancel(context.Background())
	defer stopDesk()

	opened := &shell{app: nil, window: nil, stopDesk: stopDesk, deskDone: make(chan struct{})}

	opened.app = application.New(application.Options{
		Name:        appName,
		Description: "A local dashboard for Claude Code",
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
			ActivationPolicy: application.ActivationPolicyRegular,
		},
		Logger:     wailsLogger(stderr),
		LogLevel:   slog.LevelWarn,
		OnShutdown: opened.waitForDesk,
	})

	opened.window = opened.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     appName,
		Width:     windowWidth,
		Height:    windowHeight,
		MinWidth:  windowWidthMin,
		MinHeight: windowHeightMin,
		HTML:      splashPage(desk.SplashText(loadErr)),
	})

	opened.keepInDock()
	opened.app.Menu.Set(appMenu(opened.app))
	opened.startDeskWhenReady(deskCtx, lookup, stderr)

	err := opened.app.Run()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn-app: %v\n", err)

		return exitError
	}

	return exitOK
}

// wailsLogger is the logger Wails writes to: warnings and errors only, because at the info level
// it says what it was built with and on what at every start.
func wailsLogger(stderr io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{
		AddSource:   false,
		Level:       slog.LevelWarn,
		ReplaceAttr: nil,
	}))
}

// keepInDock hides the window when it is closed rather than closing it, and shows it again when
// the Dock icon is clicked. Showing the app must run on the main thread, or it crashes; and the
// window comes to the front only with all three calls.
func (s *shell) keepInDock() {
	s.window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		s.window.Hide()
		event.Cancel()
	})

	s.app.Event.OnApplicationEvent(
		events.Mac.ApplicationShouldHandleReopen,
		func(*application.ApplicationEvent) {
			application.InvokeSync(s.app.Show)
			s.window.Show()
			s.window.Focus()
		})
}

// startDeskWhenReady runs desk.Run once the app has started: a dialog asked for before then is
// never shown, so a Conn that cannot start would end without a word.
func (s *shell) startDeskWhenReady(
	ctx context.Context, lookup config.LookupFunc, stderr io.Writer,
) {
	s.app.Event.OnApplicationEvent(
		events.Common.ApplicationStarted,
		func(*application.ApplicationEvent) {
			go s.runDesk(ctx, lookup, stderr)
		})
}

// runDesk keeps the window on a live Conn until ctx ends at quit, then closes deskDone.
func (s *shell) runDesk(ctx context.Context, lookup config.LookupFunc, stderr io.Writer) {
	defer close(s.deskDone)

	err := desk.Run(ctx, s.deps(lookup, stderr))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn-app: %v\n", err)
	}
}

// waitForDesk ends desk.Run and waits up to deskStopTimeout for it to return, so a server this
// app owns shuts down and lets the data folder go before the process ends. A server it attached
// to is left running: desk.Run never stops one.
func (s *shell) waitForDesk() {
	s.stopDesk()

	select {
	case <-s.deskDone:
	case <-time.After(deskStopTimeout):
	}
}
