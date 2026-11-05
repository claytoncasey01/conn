// The bridge between the window's WebKit view and the Go rule for links (desk.LinkTarget).
#ifndef CONN_LINKS_DARWIN_H
#define CONN_LINKS_DARWIN_H

#include <stdint.h>

// Where a link goes: the values of desk.Link, which links_darwin.go maps to these.
enum {
	connLinkInApp = 0,
	connLinkBrowser = 1,
	connLinkRefused = 2,
};

// connInstallLinks teaches Wails' window delegate to send links to Go, with handle (a
// runtime/cgo.Handle of the Go shell) passed back on each. It must run before the first window is
// made, because WebKit reads which methods a delegate has when the delegate is set. It returns
// NULL, or why it could not.
const char *connInstallLinks(uintptr_t handle);

#endif
