package main

/*
#cgo LDFLAGS: -framework Cocoa -framework WebKit -framework UserNotifications
#include <stdbool.h>
#include "notify_darwin.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime/cgo"

	"github.com/claytoncasey01/conn/internal/desk"
)

// errNotify is a bridge that could not be set up: the window works, but the page's alerts fall
// back to what WebKit gives it, which is none.
var errNotify = errors.New("notifications from the page will not work")

// installNotify gives the page the connNotify message handler, through which it shows macOS
// notifications (notify_darwin.m has the messages), and makes the app the notification center's
// delegate, so a click opens its page. It must run on the main thread, after the window exists
// and before it loads the server's page. Like the links' handle, the shell's handle is kept by
// the Objective-C side for the life of the process and never deleted.
func (s *shell) installNotify() error {
	window := s.window.NativeWindow()
	if window == nil {
		return fmt.Errorf("%w: the window has not been made", errNotify)
	}

	handle := cgo.NewHandle(s)

	// The window is Objective-C's, not Go's, so its address is passed as a number, which the
	// garbage collector has no part in.
	problem := C.connInstallNotify(C.uintptr_t(handle), C.uintptr_t(uintptr(window)))
	if problem != nil {
		return fmt.Errorf("%w: %s", errNotify, C.GoString(problem))
	}

	return nil
}

// connNotifyFromServer says whether a page at origin (an address with no path) may use the
// bridge: a page of the server the window shows, by the same rule as a link that stays in the
// window. The splash and any other page are refused.
//
//export connNotifyFromServer
func connNotifyFromServer(handle C.uintptr_t, origin *C.char) C.bool {
	opened := shellOf(handle)

	return desk.LinkTarget(C.GoString(origin), opened.shownURL()) == desk.LinkInApp
}

// connNotificationAllowed says whether the app shows the notification the page sent, and says on
// stderr why not. It never prints the notification's text.
//
//export connNotificationAllowed
func connNotificationAllowed(handle C.uintptr_t, title, body, tag, href *C.char) C.bool {
	opened := shellOf(handle)

	err := desk.CheckNotification(desk.Notification{
		Title: C.GoString(title),
		Body:  C.GoString(body),
		Tag:   C.GoString(tag),
		Href:  C.GoString(href),
	})
	if err != nil {
		_, _ = fmt.Fprintf(opened.proc.stderr, "conn-app: %v\n", err)

		return false
	}

	return true
}

// connNotificationClicked shows the window, which may be hidden, for the page the click opens.
// It is called on the main thread, which showing the app waits for, so it shows on a goroutine.
//
//export connNotificationClicked
func connNotificationClicked(handle C.uintptr_t) {
	opened := shellOf(handle)

	go opened.showWindow()
}

// shellOf is the shell a handle from the Objective-C side holds.
func shellOf(handle C.uintptr_t) *shell {
	opened, isShell := cgo.Handle(handle).Value().(*shell)
	if !isShell {
		panic("notify: the handle is not the shell's")
	}

	return opened
}
