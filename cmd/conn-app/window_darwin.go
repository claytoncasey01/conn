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
	// server's own quit, so an owned server shuts down, stops its hosted sessions and lets the
	// data folder go first.
	deskStopTimeout = app.QuitTimeout + deskStopMargin
	deskStopMargin  = time.Second
)

// shell is the running app: the Wails application, its one window, the server it shows, and the
// desk.Run it waits for at quit.
type shell struct {
	app    *application.App
	window *application.WebviewWindow
	// proc is what the program was given: its environment, output streams and splash page.
	proc process

	// shown is the address of the server the window was last opened on, empty until then. It is
	// written by desk.Run's goroutine and read on the main thread when a link is followed.
	shown atomic.Pointer[string]

	// stopDesk ends the context desk.Run runs on.
	stopDesk context.CancelFunc
	// deskDone is closed when desk.Run has returned.
	deskDone chan struct{}

	// updateItem is the app menu's Check for Updates…, changed only on the main thread.
	updateItem *application.MenuItem
	// updateAsks carries the menu item's asks to update.Run: a buffer of one, sent to without
	// waiting, so asks made during a check become at most one more.
	updateAsks chan struct{}
}

// run opens the window on the splash, then reads the login shell's environment and runs desk.Run
// once the app has started, and is the exit code. On macOS a quit ends the process from inside
// Wails, so the wait for desk.Run happens in OnShutdown, not after app.Run returns.
func run(proc process) int {
	deskCtx, stopDesk := context.WithCancel(context.Background())
	defer stopDesk()

	opened := &shell{
		app: nil, window: nil, proc: proc, shown: atomic.Pointer[string]{},
		stopDesk: stopDesk, deskDone: make(chan struct{}),
		updateItem: nil, updateAsks: make(chan struct{}, 1),
	}

	// The links go in before the window is made; without them the app still works.
	err := opened.installLinks()
	if err != nil {
		_, _ = fmt.Fprintf(proc.stderr, "conn-app: %v\n", err)
	}

	opened.app = application.New(application.Options{
		Name:        appName,
		Description: "A local dashboard for Claude Code",
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
			ActivationPolicy: application.ActivationPolicyRegular,
		},
		Logger:     wailsLogger(proc.stderr),
		LogLevel:   slog.LevelWarn,
		OnShutdown: opened.waitForDesk,
	})

	opened.window = opened.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     appName,
		Width:     windowWidth,
		Height:    windowHeight,
		MinWidth:  windowWidthMin,
		MinHeight: windowHeightMin,
		HTML:      splashPage(proc.splash, desk.SplashText("")),
	})

	opened.keepInDock()
	bar := appMenu(opened.app, opened.updateAsks)
	opened.updateItem = bar.updateItem
	opened.app.Menu.Set(bar.menu)
	opened.startDeskWhenReady(deskCtx)

	err = opened.app.Run()
	if err != nil {
		_, _ = fmt.Fprintf(proc.stderr, "conn-app: %v\n", err)

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
		s.windowClosing(func() { s.window.Hide() }, event.Cancel)
	})

	s.app.Event.OnApplicationEvent(
		events.Mac.ApplicationShouldHandleReopen,
		func(*application.ApplicationEvent) {
			application.InvokeSync(s.app.Show)
			s.window.Show()
			s.window.Focus()
		})
}

// windowClosing is what closing the window (its X, Cmd-W) does: it hides the window and cancels
// the close, and nothing else. desk.Run goes on, so the server it owns, every hosted session and
// the record of them are as they were; only a quit (waitForDesk) ends them.
func (s *shell) windowClosing(hide, cancelClose func()) {
	hide()
	cancelClose()
}

// startDeskWhenReady reads the environment and runs desk.Run once the app has started: a dialog
// asked for before then is never shown, so a Conn that cannot start would end without a word, and
// the splash is up while the login shell runs.
func (s *shell) startDeskWhenReady(ctx context.Context) {
	s.app.Event.OnApplicationEvent(
		events.Common.ApplicationStarted,
		func(*application.ApplicationEvent) {
			go s.runDesk(ctx)
		})
}

// runDesk reads the login shell's environment, then keeps the window on a live Conn until ctx
// ends at quit, and closes deskDone. The environment is set before desk.Run, so the server and
// what it starts inherit it, and before the update check, so its request goes through the
// shell's proxy. The check runs on its own goroutine, which the quit does not wait for.
func (s *shell) runDesk(ctx context.Context) {
	defer close(s.deskDone)

	loadErr := loadEnvironment(ctx, s.proc)

	// The app is quitting: there is nothing to tell and no server to start.
	if ctx.Err() != nil {
		return
	}

	if loadErr != nil {
		s.tellEnvironmentFailed(loadErr.Error())
	}

	go s.runUpdates(ctx, s.proc.lookup)

	err := desk.Run(ctx, s.deps(s.proc.lookup, s.proc.stderr))
	if err != nil {
		_, _ = fmt.Fprintf(s.proc.stderr, "conn-app: %v\n", err)
	}
}

// tellEnvironmentFailed says that the login shell's environment could not be read, and why in
// short: on stderr, on the splash, and in a dialog, because the splash is replaced as soon as the
// server answers, often too soon to read. The dialog does not stop Conn: Wails' Show waits only
// for the main thread to take the dialog, not for it to be dismissed, and SetHTML above it waits
// for the main thread the same way, so a goroutine would add nothing.
func (s *shell) tellEnvironmentFailed(reason string) {
	notice := desk.Notice(reason)

	_, _ = fmt.Fprintf(s.proc.stderr, "conn-app: %s\n", notice)

	s.window.SetHTML(splashPage(s.proc.splash, desk.SplashText(reason)))

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
