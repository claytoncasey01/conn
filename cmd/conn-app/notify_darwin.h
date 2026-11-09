// The bridge between the page and macOS notifications (UNUserNotificationCenter).
#ifndef CONN_NOTIFY_DARWIN_H
#define CONN_NOTIFY_DARWIN_H

#include <stdbool.h>
#include <stdint.h>

// connInstallNotify gives the page in window (the address of Wails' NSWindow, whose webView is
// the page) the connNotify message handler, and makes the app the notification center's
// delegate, with handle (a runtime/cgo.Handle of the Go shell) passed back on each call to Go.
// It also keeps the page running while the window is hidden, so alerts go on after it is closed.
// It must run on the main thread, before the window loads the server's page. It returns NULL, or
// why it could not.
const char *connInstallNotify(uintptr_t handle, uintptr_t window);

#endif
