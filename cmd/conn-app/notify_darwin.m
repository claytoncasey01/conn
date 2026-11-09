// Notifications from the page. WebKit answers the page's Web Notification API with "denied" and
// never asks macOS, so inside the app the page sends its alerts here instead, through a message
// handler named connNotify, and they are shown with UNUserNotificationCenter. The page posts
// {kind, ...} and gets a reply:
//
//	{kind: "status"}                      the permission: default, granted, denied or unavailable
//	{kind: "request"}                     asks macOS (its prompt shows once per app), then the
//	                                      permission as "status" says it
//	{kind: "show", title, body, tag, href} shows a notification, which Go has checked first
//	                                      (desk.CheckNotification); the reply is "shown"
//	{kind: "settings"}                    opens System Settings at Notifications; the reply is
//	                                      "opened"
//
// Only the main frame of a page of the server the window shows is answered: Go decides, with the
// same rule as links (desk.LinkTarget). "unavailable" is a process with no bundle identifier
// (UNUserNotificationCenter needs one, and throws without it) or one macOS will not let ask, such
// as an app run from a temporary folder. Clicking a notification shows the window and hands its
// href to the page as a conn-notification-click event. The file is built without ARC, as Wails'
// own Objective-C is: every object it makes lives until the process ends.
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <UserNotifications/UserNotifications.h>

#include "notify_darwin.h"
#include "_cgo_export.h"

// The name the page finds the handler by: window.webkit.messageHandlers.connNotify.
static NSString *const handlerName = @"connNotify";

// The permission, in the words of the Web Notification API, so the page reads one vocabulary,
// and unavailable for a process macOS will not let ask at all.
static NSString *const permissionDefault = @"default";
static NSString *const permissionGranted = @"granted";
static NSString *const permissionDenied = @"denied";
static NSString *const permissionUnavailable = @"unavailable";

// The key a notification keeps its href under, for the click.
static NSString *const hrefKey = @"href";

// What a click runs in the page: href is passed as an argument, never written into the script.
static NSString *const clickScript =
	@"window.dispatchEvent(new CustomEvent('conn-notification-click', { detail: href }))";

// System Settings' Notifications pane, for the app whose bundle identifier follows.
static NSString *const settingsAddress =
	@"x-apple.systempreferences:com.apple.Notifications-Settings.extension?id=";

// notifyHandle is the Go shell's handle, set once by connInstallNotify and never changed. Like
// linksHandle in links_darwin.m, it is here because WebKit and the notification center call
// back with only their own arguments.
static uintptr_t notifyHandle;

// notifyWebView is the window's page, which a click hands the href to. Set once, on the main
// thread, and kept for the life of the process.
static WKWebView *notifyWebView;

// notAllowedHere is set, on the main thread, once macOS refused to let the app ask at all
// (UNErrorCodeNotificationsNotAllowed). The status macOS reports stays "not determined" then, so
// without it the page would offer a button that can never work.
static BOOL notAllowedHere;

// hasBundle says whether this process is an app bundle with an identifier, which the
// notification center needs.
static BOOL hasBundle(void) {
	return NSBundle.mainBundle.bundleIdentifier != nil;
}

// stringField is body[key] when body is a dictionary and that value is a string, or nil.
static NSString *stringField(id body, NSString *key) {
	if (![body isKindOfClass:NSDictionary.class]) {
		return nil;
	}

	id value = ((NSDictionary *)body)[key];
	if (![value isKindOfClass:NSString.class]) {
		return nil;
	}

	return value;
}

// originOf is the scheme, host and port of origin as an address Go can parse.
static NSString *originOf(WKSecurityOrigin *origin) {
	if (origin.port == 0) {
		return [NSString stringWithFormat:@"%@://%@/", origin.protocol, origin.host];
	}

	return [NSString stringWithFormat:@"%@://%@:%ld/", origin.protocol, origin.host,
		(long)origin.port];
}

// fromServer says whether message came from the main frame of a page of the server the window
// shows. Go decides the origin.
static BOOL fromServer(WKScriptMessage *message) {
	if (!message.frameInfo.isMainFrame) {
		return NO;
	}

	NSString *origin = originOf(message.frameInfo.securityOrigin);

	return connNotifyFromServer(notifyHandle, (char *)origin.UTF8String);
}

// permissionOf is the page's word for status.
static NSString *permissionOf(UNAuthorizationStatus status) {
	switch (status) {
	case UNAuthorizationStatusNotDetermined:
		return notAllowedHere ? permissionUnavailable : permissionDefault;
	case UNAuthorizationStatusDenied:
		return permissionDenied;
	case UNAuthorizationStatusAuthorized:
	case UNAuthorizationStatusProvisional:
		return permissionGranted;
	default:
		return permissionUnavailable;
	}
}

typedef void (^Reply)(id reply, NSString *errorMessage);

// replyStatus replies with the permission macOS reports now, on the main thread.
static void replyStatus(Reply reply) {
	if (!hasBundle()) {
		reply(permissionUnavailable, nil);
		return;
	}

	[UNUserNotificationCenter.currentNotificationCenter
		getNotificationSettingsWithCompletionHandler:^(UNNotificationSettings *settings) {
			UNAuthorizationStatus status = settings.authorizationStatus;
			dispatch_async(dispatch_get_main_queue(), ^{
				reply(permissionOf(status), nil);
			});
		}];
}

// request asks macOS for the permission, which shows its prompt only while the status is not
// determined, then replies with the status.
static void request(Reply reply) {
	if (!hasBundle()) {
		reply(permissionUnavailable, nil);
		return;
	}

	UNAuthorizationOptions options = UNAuthorizationOptionAlert | UNAuthorizationOptionSound;
	[UNUserNotificationCenter.currentNotificationCenter
		requestAuthorizationWithOptions:options
		completionHandler:^(BOOL granted, NSError *error) {
			BOOL refused = [error.domain isEqualToString:UNErrorDomain] &&
				error.code == UNErrorCodeNotificationsNotAllowed;
			dispatch_async(dispatch_get_main_queue(), ^{
				if (refused) {
					notAllowedHere = YES;
				}

				replyStatus(reply);
			});
		}];
}

// show shows the notification body describes, once Go has checked it.
static void show(id body, Reply reply) {
	NSString *title = stringField(body, @"title");
	NSString *text = stringField(body, @"body");
	NSString *tag = stringField(body, @"tag");
	NSString *href = stringField(body, hrefKey);
	if (title == nil || text == nil || tag == nil || href == nil) {
		reply(nil, @"a notification needs a title, body, tag and href, each a string");
		return;
	}

	if (!hasBundle()) {
		reply(nil, @"notifications are unavailable");
		return;
	}

	BOOL allowed = connNotificationAllowed(notifyHandle, (char *)title.UTF8String,
		(char *)text.UTF8String, (char *)tag.UTF8String, (char *)href.UTF8String);
	if (!allowed) {
		reply(nil, @"the app refused the notification");
		return;
	}

	UNMutableNotificationContent *content = [[UNMutableNotificationContent new] autorelease];
	content.title = title;
	content.body = text;
	content.sound = UNNotificationSound.defaultSound;
	content.userInfo = @{hrefKey : href};

	// The tag is the identifier, so a later notification with the same tag replaces the earlier.
	UNNotificationRequest *notification =
		[UNNotificationRequest requestWithIdentifier:tag content:content trigger:nil];
	[UNUserNotificationCenter.currentNotificationCenter addNotificationRequest:notification
		withCompletionHandler:^(NSError *error) {
			NSString *problem = error.localizedDescription;
			dispatch_async(dispatch_get_main_queue(), ^{
				if (problem != nil) {
					reply(nil, problem);
					return;
				}

				reply(@"shown", nil);
			});
		}];
}

// openSettings opens System Settings at the app's notifications.
static void openSettings(Reply reply) {
	NSString *bundle = NSBundle.mainBundle.bundleIdentifier ?: @"";
	NSString *address = [settingsAddress stringByAppendingString:bundle];
	[NSWorkspace.sharedWorkspace openURL:[NSURL URLWithString:address]];
	reply(@"opened", nil);
}

@interface ConnNotify : NSObject <WKScriptMessageHandlerWithReply, UNUserNotificationCenterDelegate>
@end

@implementation ConnNotify

// The page's requests. replyHandler is called once on every path, as WebKit requires.
- (void)userContentController:(WKUserContentController *)controller
	didReceiveScriptMessage:(WKScriptMessage *)message
	replyHandler:(void (^)(id reply, NSString *errorMessage))replyHandler {
	if (!fromServer(message)) {
		replyHandler(nil, @"only a page of the server may ask");
		return;
	}

	NSString *kind = stringField(message.body, @"kind");
	if ([kind isEqualToString:@"status"]) {
		replyStatus(replyHandler);
	} else if ([kind isEqualToString:@"request"]) {
		request(replyHandler);
	} else if ([kind isEqualToString:@"show"]) {
		show(message.body, replyHandler);
	} else if ([kind isEqualToString:@"settings"]) {
		openSettings(replyHandler);
	} else {
		replyHandler(nil, @"the app does not know this request");
	}
}

// A notification is shown even while the app is in front: the page decides when to send one
// (only while its window is hidden or not the front window), and the app may be in front with
// its window hidden.
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
	willPresentNotification:(UNNotification *)notification
	withCompletionHandler:(void (^)(UNNotificationPresentationOptions))completionHandler {
	completionHandler(UNNotificationPresentationOptionBanner |
		UNNotificationPresentationOptionList | UNNotificationPresentationOptionSound);
}

// A click shows the window and opens the notification's href in the page. completionHandler is
// called on every path. The notification center does not promise which thread it calls this on,
// and WebKit must be used on the main thread, so the page is told from there.
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
	didReceiveNotificationResponse:(UNNotificationResponse *)response
	withCompletionHandler:(void (^)(void))completionHandler {
	if (![response.actionIdentifier isEqualToString:UNNotificationDefaultActionIdentifier]) {
		completionHandler();
		return;
	}

	connNotificationClicked(notifyHandle);

	NSString *href = stringField(response.notification.request.content.userInfo, hrefKey);
	if (href != nil) {
		dispatch_async(dispatch_get_main_queue(), ^{
			if (notifyWebView == nil) {
				return;
			}

			[notifyWebView callAsyncJavaScript:clickScript
				arguments:@{hrefKey : href}
				inFrame:nil
				inContentWorld:WKContentWorld.pageWorld
				completionHandler:nil];
		});
	}

	completionHandler();
}

@end

const char *connInstallNotify(uintptr_t handle, uintptr_t window) {
	@autoreleasepool {
		notifyHandle = handle;

		id nativeWindow = (id)window;
		if (nativeWindow == nil || ![nativeWindow respondsToSelector:@selector(webView)]) {
			return "the window has no page to give the handler to";
		}

		WKWebView *webView = [nativeWindow performSelector:@selector(webView)];
		if (webView == nil) {
			return "the window has no page to give the handler to";
		}

		notifyWebView = [webView retain];

		// WebKit suspends a page some minutes after its window is hidden (measured: about four),
		// and a suspended page reads no snapshot, so it raises no alert for as long as the window
		// stays closed. The page must keep running for the alerts it exists to send. macOS 13 has
		// no such setting.
		if (@available(macOS 14.0, *)) {
			webView.configuration.preferences.inactiveSchedulingPolicy =
				WKInactiveSchedulingPolicyNone;
		}

		ConnNotify *notify = [ConnNotify new];
		[webView.configuration.userContentController addScriptMessageHandlerWithReply:notify
			contentWorld:WKContentWorld.pageWorld
			name:handlerName];

		if (hasBundle()) {
			UNUserNotificationCenter.currentNotificationCenter.delegate = notify;
		}

		return NULL;
	}
}
