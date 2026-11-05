// Links in the window. Wails 3.1 on macOS leaves two WebKit questions unanswered: a link that
// opens a new window (target="_blank") is dropped, and a link to another site loads in the
// window, which then shows that site instead of Conn. These methods are added to Wails' own
// window delegate class, which is the WebKit view's UI and navigation delegate, so what Wails
// does in that delegate (file pickers, the camera prompt, its navigation events) is kept. Each
// link is decided by Go (connLinkFollowed, desk.LinkTarget), which also opens the browser.
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <objc/runtime.h>

#include "links_darwin.h"
#include "_cgo_export.h"

// linksHandle is the Go shell's handle, set once by connInstallLinks before any window exists.
static uintptr_t linksHandle;

// linkFollowed asks Go where a link to url goes. A URL with no text is refused.
static int linkFollowed(NSURL *url) {
	NSString *text = url.absoluteString;
	if (text == nil) {
		return connLinkRefused;
	}

	return connLinkFollowed(linksHandle, (char *)text.UTF8String);
}

// createWebView answers a request for a new window: a page of the server loads in this window,
// anything else Go has already sent to the browser or refused. No new view is ever made.
static WKWebView *createWebView(id self, SEL command, WKWebView *webView,
		WKWebViewConfiguration *configuration, WKNavigationAction *action,
		WKWindowFeatures *features) {
	if (linkFollowed(action.request.URL) == connLinkInApp) {
		[webView loadRequest:action.request];
	}

	return nil;
}

// decidePolicy lets every navigation through but a link the person clicked in the main frame,
// which Go decides. Loads Conn makes itself (the splash, the server's address, a reload) are not
// clicks, so they are never stopped. decisionHandler is called once on every path, as WebKit
// requires.
static void decidePolicy(id self, SEL command, WKWebView *webView, WKNavigationAction *action,
		void (^decisionHandler)(WKNavigationActionPolicy)) {
	if (action.navigationType != WKNavigationTypeLinkActivated) {
		decisionHandler(WKNavigationActionPolicyAllow);
		return;
	}

	// A link with no target frame opens a new window, which createWebView answers.
	WKFrameInfo *target = action.targetFrame;
	if (target == nil) {
		decisionHandler(WKNavigationActionPolicyAllow);
		return;
	}

	if (!target.isMainFrame) {
		decisionHandler(WKNavigationActionPolicyAllow);
		return;
	}

	if (linkFollowed(action.request.URL) == connLinkInApp) {
		decisionHandler(WKNavigationActionPolicyAllow);
		return;
	}

	decisionHandler(WKNavigationActionPolicyCancel);
}

// addMethod adds one optional method of protocol to class, with the types WebKit declares for it.
static const char *addMethod(Class class, Protocol *protocol, SEL selector, IMP implementation) {
	struct objc_method_description description =
		protocol_getMethodDescription(protocol, selector, NO, YES);
	if (description.types == NULL) {
		return "WebKit does not declare a method the links need";
	}

	if (!class_addMethod(class, selector, implementation, description.types)) {
		return "Wails' window delegate already has a method the links add";
	}

	return NULL;
}

const char *connInstallLinks(uintptr_t handle) {
	linksHandle = handle;

	Class delegate = NSClassFromString(@"WebviewWindowDelegate");
	if (delegate == nil) {
		return "Wails has no WebviewWindowDelegate class";
	}

	const char *problem = addMethod(delegate, @protocol(WKUIDelegate),
		@selector(webView:createWebViewWithConfiguration:forNavigationAction:windowFeatures:),
		(IMP)createWebView);
	if (problem != NULL) {
		return problem;
	}

	return addMethod(delegate, @protocol(WKNavigationDelegate),
		@selector(webView:decidePolicyForNavigationAction:decisionHandler:),
		(IMP)decidePolicy);
}
