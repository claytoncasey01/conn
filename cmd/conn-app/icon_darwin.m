// The app's Dock icon, which Settings > App icon lets the page change (src/lib/appIcon.svelte.ts).
// The page draws the icon for its theme and posts it to a message handler named connIcon:
//
//	{kind: "set", png}   png is a 1024 pixel PNG as base64. Go checks it (desk.Icon) and writes it
//	                     where the Dock tile plug-in reads it (dockicon_darwin.h), and it becomes
//	                     the running app's icon. The reply is "set".
//	{kind: "classic"}    removes that file, so the app shows the icon in its bundle again. The
//	                     reply is "classic".
//
// Either way the plug-in is told, so the Dock shows the same icon once the app is closed. Only
// the main frame of a page of the server the window shows is answered, by the notifications'
// rule (connMessageFromServer). At launch the saved icon is set before the page loads. The file
// is built without ARC, as Wails' own Objective-C is.
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

#include "icon_darwin.h"
#include "bridge_darwin.h"
#include "dockicon_darwin.h"
#include "_cgo_export.h"

// The name the page finds the handler by: window.webkit.messageHandlers.connIcon.
static NSString *const iconHandlerName = @"connIcon";

// iconHandle is the Go shell's handle, set once by connInstallIcon and never changed. Like
// notifyHandle in notify_darwin.m, it is here because WebKit calls back with only its own
// arguments.
static uintptr_t iconHandle;

// iconPath is this app's icon file, or nil when the process has no bundle identifier.
static NSString *iconPath(void) {
	return connDockIconPath(NSBundle.mainBundle.bundleIdentifier);
}

// showSaved makes the icon file at path the running app's icon, or the bundle's icon when there
// is no file: an image of nil gives it back.
static void showSaved(NSString *path) {
	NSImage *image = [[[NSImage alloc] initWithContentsOfFile:path] autorelease];
	NSApp.applicationIconImage = image;
}

// tellTile tells the Dock tile plug-in to read the icon file again.
static void tellTile(void) {
	NSString *name = connDockIconChanged(NSBundle.mainBundle.bundleIdentifier);
	[NSDistributedNotificationCenter.defaultCenter postNotificationName:name
		object:nil
		userInfo:nil
		deliverImmediately:YES];
}

// iconField is body[key] when body is a dictionary and that value is a string, or nil.
static NSString *iconField(id body, NSString *key) {
	if (![body isKindOfClass:NSDictionary.class]) {
		return nil;
	}

	id value = ((NSDictionary *)body)[key];
	if (![value isKindOfClass:NSString.class]) {
		return nil;
	}

	return value;
}

typedef void (^IconReply)(id reply, NSString *errorMessage);

// setIcon writes the icon body carries, once Go has checked it, and shows it.
static void setIcon(id body, NSString *path, IconReply reply) {
	NSString *encoded = iconField(body, @"png");
	if (encoded == nil) {
		reply(nil, @"an icon needs its png, as a string");
		return;
	}

	// Base64 takes 4 characters for every 3 bytes, so a longer string holds more than Go takes,
	// and it is refused before any of it is decoded.
	NSUInteger maxCharacters = (connIconMaxBytes() + 2) / 3 * 4;
	if (encoded.length > maxCharacters) {
		reply(nil, @"the icon is too large");
		return;
	}

	NSData *data = [[[NSData alloc] initWithBase64EncodedString:encoded options:0] autorelease];
	if (data == nil) {
		reply(nil, @"the icon is not base64");
		return;
	}

	BOOL written = connIconWrite(iconHandle, (char *)path.UTF8String, (void *)data.bytes,
		data.length);
	if (!written) {
		reply(nil, @"the app refused the icon");
		return;
	}

	showSaved(path);
	tellTile();
	reply(@"set", nil);
}

// classicIcon removes the icon file and shows the bundle's icon.
static void classicIcon(NSString *path, IconReply reply) {
	BOOL removed = connIconRemove(iconHandle, (char *)path.UTF8String);
	if (!removed) {
		reply(nil, @"the app could not remove the icon");
		return;
	}

	showSaved(path);
	tellTile();
	reply(@"classic", nil);
}

@interface ConnIcon : NSObject <WKScriptMessageHandlerWithReply>
@end

@implementation ConnIcon

// The page's requests. replyHandler is called once on every path, as WebKit requires.
- (void)userContentController:(WKUserContentController *)controller
	didReceiveScriptMessage:(WKScriptMessage *)message
	replyHandler:(void (^)(id reply, NSString *errorMessage))replyHandler {
	if (!connMessageFromServer(message)) {
		replyHandler(nil, @"only a page of the server may ask");
		return;
	}

	NSString *path = iconPath();
	if (path == nil) {
		replyHandler(nil, @"the app has no bundle identifier, so its icon cannot change");
		return;
	}

	NSString *kind = iconField(message.body, @"kind");
	if ([kind isEqualToString:@"set"]) {
		setIcon(message.body, path, replyHandler);
	} else if ([kind isEqualToString:@"classic"]) {
		classicIcon(path, replyHandler);
	} else {
		replyHandler(nil, @"the app does not know this request");
	}
}

@end

const char *connInstallIcon(uintptr_t handle, uintptr_t window) {
	@autoreleasepool {
		iconHandle = handle;

		// A process with no bundle identifier (a dev build run outside its bundle) has nowhere
		// the plug-in would read an icon from, so it gets no handler and the page shows no
		// App icon setting.
		NSString *path = iconPath();
		if (path == nil) {
			return NULL;
		}
		showSaved(path);

		id nativeWindow = (id)window;
		if (nativeWindow == nil || ![nativeWindow respondsToSelector:@selector(webView)]) {
			return "the window has no page to give the handler to";
		}

		WKWebView *webView = [nativeWindow performSelector:@selector(webView)];
		if (webView == nil) {
			return "the window has no page to give the handler to";
		}

		ConnIcon *icon = [ConnIcon new];
		[webView.configuration.userContentController addScriptMessageHandlerWithReply:icon
			contentWorld:WKContentWorld.pageWorld
			name:iconHandlerName];

		return NULL;
	}
}
