package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/claytoncasey01/conn/internal/app"
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

	// environmentTitle is the title of the dialog that says the shell's environment is missing.
	environmentTitle = "Conn is using the app's own environment"

	// deskStopTimeout bounds the wait at quit for desk.Run to return: deskStopMargin above the
	// server's own shutdown, so an owned server shuts down and lets the data folder go first.
	deskStopTimeout = app.ShutdownTimeout + deskStopMargin
	deskStopMargin  = time.Second
)

// shell is the running app: the Wails application, its one window, the server it shows, and the
// desk.Run it waits for at quit.
type shell struct {
	app    *application.App
	window *application.WebviewWindow
	stderr io.Writer

	// shown is the address of the server the window was last opened on, empty until then. It is
	// written by desk.Run's goroutine and read on the main thread when a link is followed.
	shown atomic.Pointer[string]

	// stopDesk ends the context desk.Run runs on.
	stopDesk context.CancelFunc
	// deskDone is closed when desk.Run has returned.
	deskDone chan struct{}
}

// run opens the window on the splash, then reads the login shell's environment and runs desk.Run
// once the app has started, and is the exit code. On macOS a quit ends the process from inside
// Wails, so the wait for desk.Run happens in OnShutdown, not after app.Run returns.
func run(lookup config.LookupFunc, stderr io.Writer) int {
	deskCtx, stopDesk := context.WithCancel(context.Background())
	defer stopDesk()

	opened := &shell{
		app: nil, window: nil, stderr: stderr, shown: atomic.Pointer[string]{},
		stopDesk: stopDesk, deskDone: make(chan struct{}),
	}

	// The links go in before the window is made; without them the app still works.
	err := opened.installLinks()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn-app: %v\n", err)
	}

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
		HTML:      splashPage(desk.SplashText("")),
	})

	opened.keepInDock()
	opened.app.Menu.Set(appMenu(opened.app))
	opened.startDeskWhenReady(deskCtx, lookup, stderr)

	err = opened.app.Run()
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

// startDeskWhenReady reads the environment and runs desk.Run once the app has started: a dialog
// asked for before then is never shown, so a Conn that cannot start would end without a word, and
// the splash is up while the login shell runs.
func (s *shell) startDeskWhenReady(
	ctx context.Context, lookup config.LookupFunc, stderr io.Writer,
) {
	s.app.Event.OnApplicationEvent(
		events.Common.ApplicationStarted,
		func(*application.ApplicationEvent) {
			go s.runDesk(ctx, lookup, stderr)
		})
}

// runDesk reads the login shell's environment, then keeps the window on a live Conn until ctx
// ends at quit, and closes deskDone. The environment is set before desk.Run, so the server and
// what it starts inherit it.
func (s *shell) runDesk(ctx context.Context, lookup config.LookupFunc, stderr io.Writer) {
	defer close(s.deskDone)

	loadErr := loadEnvironment(ctx)

	// The app is quitting: there is nothing to tell and no server to start.
	if ctx.Err() != nil {
		return
	}

	if loadErr != nil {
		s.tellEnvironmentFailed(loadErr.Error(), stderr)
	}

	err := desk.Run(ctx, s.deps(lookup, stderr))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "conn-app: %v\n", err)
	}
}

// tellEnvironmentFailed says that the login shell's environment could not be read, and why in
// short: on stderr, on the splash, and in a dialog, because the splash is replaced as soon as the
// server answers, often too soon to read. The dialog does not stop Conn: Wails' Show queues the
// dialog on the main thread and returns at once, without waiting for it to be dismissed.
func (s *shell) tellEnvironmentFailed(reason string, stderr io.Writer) {
	notice := desk.Notice(reason)

	_, _ = fmt.Fprintf(stderr, "conn-app: %s\n", notice)

	s.window.SetHTML(splashPage(desk.SplashText(reason)))

	dialog := s.app.Dialog.Warning().SetTitle(environmentTitle).SetMessage(notice)
	dialog.AddButton("OK").SetAsDefault()

	dialog.Show()
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
