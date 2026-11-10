// What the page's message handlers share. Objective-C only: the Go files include the C headers,
// never this one.
#ifndef CONN_BRIDGE_DARWIN_H
#define CONN_BRIDGE_DARWIN_H

#import <WebKit/WebKit.h>

// connMessageFromServer says whether message came from the main frame of a page of the server the
// window shows (notify_darwin.m has it). It asks Go through the notifications' handle, so it
// answers only once connInstallNotify has run.
BOOL connMessageFromServer(WKScriptMessage *message);

#endif
