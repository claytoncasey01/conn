package main

/*
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include "transparency_darwin.h"
*/
import "C"

import (
	"errors"
	"fmt"
)

// errTransparency is a transparency bridge that could not be set up: the window works, but the
// page does not know when the Mac's Reduce transparency setting is on, so a glass theme stays
// glass.
var errTransparency = errors.New("the page will not follow the Mac's Reduce transparency setting")

// installTransparency passes the Mac's Reduce transparency setting to the page, which WebKit
// does not (transparency_darwin.m has how). It must run on the main thread, after installNotify,
// whose origin check its handler uses, and before the window loads the server's page.
func (s *shell) installTransparency() error {
	window := s.window.NativeWindow()
	if window == nil {
		return fmt.Errorf("%w: the window has not been made", errTransparency)
	}

	// The window is Objective-C's, not Go's, so its address is passed as a number, which the
	// garbage collector has no part in.
	problem := C.connInstallTransparency(C.uintptr_t(uintptr(window)))
	if problem != nil {
		return fmt.Errorf("%w: %s", errTransparency, C.GoString(problem))
	}

	return nil
}
