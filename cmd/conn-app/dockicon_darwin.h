// Where the app keeps the Dock icon the page drew, and the word it sends when that icon changes.
// The app (icon_darwin.m) and its Dock tile plug-in (docktile/ConnDockTile.m, which the Dock
// runs while the app is closed) both include this, so the two read the same file. Objective-C
// only: the Go files include the C headers, never this one.
#ifndef CONN_DOCKICON_DARWIN_H
#define CONN_DOCKICON_DARWIN_H

#import <Foundation/Foundation.h>

// connDockIconPath is the icon file of the app whose bundle identifier is given, in that app's own
// folder in Application Support, or nil for a process with no identifier. A copy of the app built
// with another identifier keeps its own icon.
static inline NSString *connDockIconPath(NSString *bundleIdentifier) {
	if (bundleIdentifier.length == 0) {
		return nil;
	}

	NSString *support = [NSSearchPathForDirectoriesInDomains(NSApplicationSupportDirectory,
		NSUserDomainMask, YES) firstObject];
	if (support == nil) {
		return nil;
	}

	return [[support stringByAppendingPathComponent:bundleIdentifier]
		stringByAppendingPathComponent:@"DockIcon.png"];
}

// connDockIconChanged is the name of the distributed notification the app posts once it has
// written or removed the icon file, so the plug-in reads it again.
static inline NSString *connDockIconChanged(NSString *bundleIdentifier) {
	return [bundleIdentifier stringByAppendingString:@".dock-icon-changed"];
}

#endif
