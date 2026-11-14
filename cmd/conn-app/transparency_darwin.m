// The Mac's Reduce transparency setting (System Settings > Accessibility > Display), passed to the
// page, because WebKit does not answer the prefers-reduced-transparency media query. The page's
// look (src/lib/theme/lookCss.ts) paints every surface solid while its root carries
// data-transparency="reduce".
//
// A script at the start of every page load marks the root with the setting as the page last heard
// it in this launch (kept in its localStorage under transparencyKey, with the launch's stamp, since
// the storage outlives the app and the setting may change while it is closed), or as it was when
// the app started if it has not, then asks the connTransparency message handler for the setting now and marks it
// again with the answer. So a reload after the setting changed starts right, with no flash of the
// old look while the reply is on its way. While a page is open, a change to the setting marks it
// at once, and is kept too. A page whose origin has no storage (the splash) is marked all the
// same. Only the main frame of a
// page of the server the window shows is answered, by the notifications' rule
// (connMessageFromServer). The file is built without ARC, as Wails' own Objective-C is.
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

#include "transparency_darwin.h"
#include "bridge_darwin.h"

// The name the page finds the handler by: window.webkit.messageHandlers.connTransparency.
static NSString *const transparencyHandlerName = @"connTransparency";

// The page's localStorage key for the setting it last heard: the launch's stamp, a space and one of
// the handler's answers.
static NSString *const transparencyKey = @"conn.transparency";

// The handler's answers.
static NSString *const transparencyReduce = @"reduce";
static NSString *const transparencyNormal = @"normal";

// reduced is whether the Mac's Reduce transparency setting is on.
static BOOL reduced(void) {
	return NSWorkspace.sharedWorkspace.accessibilityDisplayShouldReduceTransparency;
}

// markScript is the script that marks the open page's root with the setting and keeps it, with
// stamp, the launch's.
static NSString *markScript(BOOL reduce, NSString *stamp) {
	return [NSString stringWithFormat:
		@"(function () {"
		@"  var root = document.documentElement;"
		@"  if (%@) root.setAttribute('data-transparency', 'reduce');"
		@"  else root.removeAttribute('data-transparency');"
		@"  try { localStorage.setItem('%@', '%@ %@'); } catch (e) {}"
		@"})();",
		reduce ? @"true" : @"false", transparencyKey, stamp,
		reduce ? transparencyReduce : transparencyNormal];
}

// startScript runs at the start of every page load: it marks the root with the setting the page
// last kept in this launch (stamp), or launched (the setting when the app started) if none, then
// with the handler's answer, which it keeps.
static NSString *startScript(BOOL launched, NSString *stamp) {
	return [NSString stringWithFormat:
		@"(function () {"
		@"  var root = document.documentElement;"
		@"  var stamp = '%@ ';"
		@"  var mark = function (reduce) {"
		@"    if (reduce) root.setAttribute('data-transparency', 'reduce');"
		@"    else root.removeAttribute('data-transparency');"
		@"  };"
		@"  var kept = null;"
		@"  try { kept = localStorage.getItem('%@'); } catch (e) {}"
		@"  var heard = kept && kept.indexOf(stamp) === 0 ? kept.slice(stamp.length) : null;"
		@"  mark(heard === '%@' || heard === '%@' ? heard === '%@' : %@);"
		@"  var handlers = window.webkit && window.webkit.messageHandlers;"
		@"  var ask = handlers && handlers.%@;"
		@"  if (!ask) return;"
		@"  ask.postMessage(null).then(function (answer) {"
		@"    mark(answer === '%@');"
		@"    try { localStorage.setItem('%@', stamp + answer); } catch (e) {}"
		@"  }, function () {});"
		@"})();",
		stamp, transparencyKey, transparencyReduce, transparencyNormal, transparencyReduce,
		launched ? @"true" : @"false", transparencyHandlerName, transparencyReduce, transparencyKey];
}

@interface ConnTransparency : NSObject <WKScriptMessageHandlerWithReply>
// webView is the page a change to the setting is passed to. It lives as long as the app.
@property(nonatomic, assign) WKWebView *webView;
// stamp tells this launch's kept setting from an earlier launch's. It lives as long as the app.
@property(nonatomic, retain) NSString *stamp;
@end

@implementation ConnTransparency

// The page's question: the setting now. replyHandler is called once on every path, as WebKit
// requires.
- (void)userContentController:(WKUserContentController *)controller
	didReceiveScriptMessage:(WKScriptMessage *)message
	replyHandler:(void (^)(id reply, NSString *errorMessage))replyHandler {
	if (!connMessageFromServer(message)) {
		replyHandler(nil, @"only a page of the server may ask");
		return;
	}

	replyHandler(reduced() ? transparencyReduce : transparencyNormal, nil);
}

// The setting changed: mark the open page. NSWorkspace posts on the main thread, where WebKit
// must be called.
- (void)displayOptionsChanged:(NSNotification *)notification {
	[self.webView evaluateJavaScript:markScript(reduced(), self.stamp) completionHandler:nil];
}

@end

const char *connInstallTransparency(uintptr_t window) {
	@autoreleasepool {
		id nativeWindow = (id)window;
		if (nativeWindow == nil || ![nativeWindow respondsToSelector:@selector(webView)]) {
			return "the window has no page to tell";
		}

		WKWebView *webView = [nativeWindow performSelector:@selector(webView)];
		if (webView == nil) {
			return "the window has no page to tell";
		}

		// Kept for the life of the process, like the other bridges' handlers: the page may ask,
		// and the setting may change, until the app quits.
		ConnTransparency *transparency = [ConnTransparency new];
		transparency.webView = webView;
		transparency.stamp = NSProcessInfo.processInfo.globallyUniqueString;

		WKUserContentController *controller = webView.configuration.userContentController;
		[controller addScriptMessageHandlerWithReply:transparency
			contentWorld:WKContentWorld.pageWorld
			name:transparencyHandlerName];

		WKUserScript *start = [[[WKUserScript alloc] initWithSource:startScript(reduced(), transparency.stamp)
			injectionTime:WKUserScriptInjectionTimeAtDocumentStart
			forMainFrameOnly:YES] autorelease];
		[controller addUserScript:start];

		[NSWorkspace.sharedWorkspace.notificationCenter addObserver:transparency
			selector:@selector(displayOptionsChanged:)
			name:NSWorkspaceAccessibilityDisplayOptionsDidChangeNotification
			object:nil];

		return NULL;
	}
}
