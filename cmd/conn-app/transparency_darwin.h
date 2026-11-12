// The bridge that passes the Mac's Reduce transparency setting to the page.
#ifndef CONN_TRANSPARENCY_DARWIN_H
#define CONN_TRANSPARENCY_DARWIN_H

#include <stdint.h>

// connInstallTransparency marks the page in window (the address of Wails' NSWindow, whose webView
// is the page) with data-transparency="reduce" on its root while the Mac's Reduce transparency
// setting is on, from the start of every page load and whenever the setting changes. It must run
// on the main thread, after connInstallNotify (whose origin check its handler uses) and before
// the window loads the server's page. It returns NULL, or why it could not.
const char *connInstallTransparency(uintptr_t window);

#endif
