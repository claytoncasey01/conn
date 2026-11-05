package main

/*
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include "links_darwin.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime/cgo"

	"github.com/claytoncasey01/conn/internal/desk"
)

// errLinks is links that could not be set up: the window works, but its links do nothing.
var errLinks = errors.New("links in the window will not open")

// installLinks sends every link the window follows to desk.LinkTarget: the server's own pages
// load in the window, other web pages open in the default browser, anything else is refused. It
// must run before the window is made. The shell reaches the Objective-C side as a cgo.Handle, the
// one way a callback from C finds a Go value without package state; it is never deleted, because
// the window may follow a link until the process ends.
func (s *shell) installLinks() error {
	handle := cgo.NewHandle(s)

	problem := C.connInstallLinks(C.uintptr_t(handle))
	if problem != nil {
		return fmt.Errorf("%w: %s", errLinks, C.GoString(problem))
	}

	return nil
}

// connLinkFollowed is where a link to rawURL goes, for the Objective-C side, which calls it on
// the main thread: so it never waits, and the browser is opened on a goroutine of its own.
//
//export connLinkFollowed
func connLinkFollowed(handle C.uintptr_t, rawURL *C.char) C.int {
	opened, isShell := cgo.Handle(handle).Value().(*shell)
	if !isShell {
		panic("connLinkFollowed: the handle is not the shell's")
	}

	link := C.GoString(rawURL)

	switch desk.LinkTarget(link, opened.shownURL()) {
	case desk.LinkInApp:
		return C.connLinkInApp
	case desk.LinkBrowser:
		go opened.openInBrowser(link)

		return C.connLinkBrowser
	case desk.LinkRefused:
		return C.connLinkRefused
	}

	panic("connLinkFollowed: LinkTarget gave a Link this switch does not know")
}

// openInBrowser opens link in the default browser, and says on stderr when it could not.
func (s *shell) openInBrowser(link string) {
	err := s.app.Browser.OpenURL(link)
	if err != nil {
		_, _ = fmt.Fprintf(s.stderr, "conn-app: opening a link in the browser: %v\n", err)
	}
}
