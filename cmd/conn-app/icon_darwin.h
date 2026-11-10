// The bridge between the page and the app's Dock icon.
#ifndef CONN_ICON_DARWIN_H
#define CONN_ICON_DARWIN_H

#include <stdint.h>

// connInstallIcon makes the icon the page last chose the app's, and gives the page in window (the
// address of Wails' NSWindow, whose webView is the page) the connIcon message handler, with
// handle (a runtime/cgo.Handle of the Go shell) passed back on each call to Go. It must run on
// the main thread, after connInstallNotify (whose origin check it uses) and before the window
// loads the server's page. It returns NULL, or why it could not.
const char *connInstallIcon(uintptr_t handle, uintptr_t window);

#endif
