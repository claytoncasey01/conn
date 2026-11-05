package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/claytoncasey01/conn"
	"github.com/claytoncasey01/conn/internal/config"
	"github.com/claytoncasey01/conn/internal/desk"
	"github.com/claytoncasey01/conn/internal/update"
)

// updateEventsMax is how many checks may wait to be told while the main thread is busy. A check
// runs at most once a day plus once per ask, so a full queue means the screen is stuck, and a
// further result is dropped rather than letting update.Run wait for the screen.
const updateEventsMax = 4

// runUpdates checks for a newer release until ctx ends, or hides the menu item when the check
// is off. It runs after the login shell's environment is set, because the check reads the
// proxy variables once, at its first request, and CONN_UPDATE_CHECK may come only from the
// shell. Nothing waits for it at quit: ctx ending stops it mid-wait and mid-request.
func (s *shell) runUpdates(ctx context.Context, lookup config.LookupFunc) {
	check, _ := lookup(update.CheckVariable)
	if !update.Enabled(check) {
		application.InvokeSync(func() { s.updateItem.SetHidden(true) })

		return
	}

	base, found := lookup(update.BaseVariable)
	if !found {
		base = update.ReleasesURL
	}

	if strings.TrimSpace(base) == "" {
		base = update.ReleasesURL
	}

	client := update.NewClient()
	current := conn.Version()
	events := make(chan update.Event, updateEventsMax)

	go s.tellUpdates(ctx, events, current)

	update.Run(ctx, update.Deps{
		Current: current,
		Fetch: func(ctx context.Context) (string, error) {
			return update.Latest(ctx, client, base)
		},
		After: time.After,
		Asked: s.updateAsks,
		Report: func(event update.Event) {
			select {
			case events <- event:
			default:
			}
		},
	})
}

// tellUpdates tells each check's outcome, in order, until ctx ends. It is apart from update.Run
// so a check never waits on the main thread, which a dialog may hold.
func (s *shell) tellUpdates(ctx context.Context, events <-chan update.Event, current string) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-events:
			s.tellUpdate(event, current)
		}
	}
}

// tellUpdate notes the outcome on stderr, relabels the menu item on the main thread, and shows
// the dialog on its own goroutine, because Show waits for the dialog to be dismissed.
func (s *shell) tellUpdate(event update.Event, current string) {
	notice := update.Tell(event, current)

	if notice.Note != "" {
		_, _ = fmt.Fprintf(s.stderr, "conn-app: %s\n", notice.Note)
	}

	if notice.Label != "" {
		application.InvokeSync(func() { s.updateItem.SetLabel(notice.Label) })
	}

	dialog := s.updateDialog(notice, event.Result.Latest)
	if dialog != nil {
		go dialog.Show()
	}
}

// updateDialog is the dialog notice asks for, or nil for none. The offer's buttons run on the
// main thread, so each hands its work to a goroutine: the clipboard and the browser need it.
func (s *shell) updateDialog(notice update.Notice, latest string) *application.MessageDialog {
	switch notice.Dialog {
	case update.DialogNone:
		return nil
	case update.DialogOffer:
		dialog := s.app.Dialog.Info().SetTitle(notice.Title).SetMessage(notice.Message)
		dialog.AddButton(update.ButtonLater).SetAsCancel()
		dialog.AddButton(update.ButtonNotes).OnClick(func() {
			go s.openReleaseNotes(latest)
		})
		dialog.AddButton(update.ButtonCopy).SetAsDefault().OnClick(func() {
			go s.copyCommand()
		})

		return dialog
	case update.DialogInfo:
		dialog := s.app.Dialog.Info().SetTitle(notice.Title).SetMessage(notice.Message)
		dialog.AddButton(update.ButtonOK).SetAsDefault()

		return dialog
	case update.DialogWarning:
		dialog := s.app.Dialog.Warning().SetTitle(notice.Title).SetMessage(notice.Message)
		dialog.AddButton(update.ButtonOK).SetAsDefault()

		return dialog
	}

	panic("updateDialog: a Dialog this switch does not know")
}

// openReleaseNotes opens the release's page through the window's link rules, so only a web
// address goes to the browser. The address is built from ReleasesURL and the tag alone.
func (s *shell) openReleaseNotes(tag string) {
	link := update.ReleaseNotesURL(tag)

	if desk.LinkTarget(link, s.shownURL()) != desk.LinkBrowser {
		_, _ = fmt.Fprintf(s.stderr, "conn-app: the release notes are not a web page: %s\n", link)

		return
	}

	s.openInBrowser(link)
}

// copyCommand puts the install command on the clipboard, and says on stderr when it could not.
func (s *shell) copyCommand() {
	if !s.app.Clipboard.SetText(update.Command) {
		_, _ = fmt.Fprintln(s.stderr, "conn-app: could not put the command on the clipboard")
	}
}
