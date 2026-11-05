// Links in the window. Wails 3.1 on macOS leaves two WebKit questions unanswered: a link that
// opens a new window (target="_blank") is dropped, and a link to another site loads in the
// window, which then shows that site instead of Conn. These methods are added to Wails' own
// window delegate class, which is the WebKit view's UI and navigation delegate, so what Wails
// does in that delegate (file pickers, the camera prompt, its navigation events) is kept. Each
// link is decided by Go (connLinkFollowed, desk.LinkTarget), which also opens the browser.
//
// What it judges is every move of the window's main frame to an address, whatever started it (a
// click, a form, a script setting location, a redirect), and every request to open a new window.
// A page of the server loads; any other web address opens in the default browser, and the window
// stays where it was. A form sent to another site is opened there as a plain address: the browser
// gets no form data. The window's own loads (a reload, back and forward, Wails' first pages) are
// not moves. A frame inside a page is not judged.
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <objc/runtime.h>

#include "links_darwin.h"
#include "_cgo_export.h"

// linksHandle is the Go shell's handle, set once by connInstallLinks before any window exists and
// never changed. It is the app's one piece of mutable package state: the functions below are
// called by WebKit with only the delegate and the navigation, so they have nowhere else to find
// the shell.
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

// ownLoad says whether a main-frame navigation is one the window makes of itself and not a
// move to somewhere else: a reload, a step back or forward in the history, and the two pages
// Wails loads before the app does: wails://localhost/, its own address, and the blank page that
// the splash is loaded into (loadHTMLString with no base address). Loading the server's address
// is not one of these: it goes to Go like any other address, which lets it in because the
// address is the server's own.
static BOOL ownLoad(WKNavigationAction *action) {
	switch (action.navigationType) {
	case WKNavigationTypeReload:
	case WKNavigationTypeBackForward:
		return YES;
	default:
		break;
	}

	NSString *address = action.request.URL.absoluteString;
	if ([address isEqualToString:@"about:blank"]) {
		return YES;
	}

	return [address isEqualToString:@"wails://localhost/"];
}

// decidePolicy lets a navigation of the main frame through only when Go says its address is a
// page of the server the window shows (or the navigation is an ownLoad), whatever started it: a
// link the person clicked, a form, a script setting location, or a redirect. Anything else Go
// has already sent to the browser or refused, and the window stays where it was. A link with no
// target frame opens a new window, which createWebView answers, and what happens inside a
// frame other than the main one is left alone. decisionHandler is called once on every path, as
// WebKit requires.
static void decidePolicy(id self, SEL command, WKWebView *webView, WKNavigationAction *action,
		void (^decisionHandler)(WKNavigationActionPolicy)) {
	WKFrameInfo *target = action.targetFrame;
	if (target == nil) {
		decisionHandler(WKNavigationActionPolicyAllow);
		return;
	}

	if (!target.isMainFrame) {
		decisionHandler(WKNavigationActionPolicyAllow);
		return;
	}

	if (ownLoad(action)) {
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
