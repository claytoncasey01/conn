// The Dock tile plug-in of Conn.app, which the Dock loads while the app is kept in the Dock, so
// the icon the person chose in Settings > App icon shows while the app is closed too. It draws
// the icon file the app writes (../dockicon_darwin.h has where), or nothing, which leaves the
// bundle's own icon. The app posts a distributed notification whenever it writes or removes the
// file, and the plug-in reads it again then. Finder, Launchpad and Spotlight keep the bundle's
// icon: changing that would mean writing into the signed bundle.
//
// scripts/build-app.sh builds this into Conn.app/Contents/PlugIns/ConnDockTile.docktileplugin,
// with ARC, for both architectures; Info.plist.tmpl beside it is the plug-in's.
//
// The Dock may load the plug-ins of several copies of Conn (built with other identifiers) into
// one process, where Objective-C keeps one class of each name. The class finds its bundle by
// itself (bundleForClass), so two copies sharing a name would both read the first copy's icon.
// build-app.sh names the class after the bundle identifier instead, as CONN_DOCKTILE_CLASS, and
// puts the same name in the plug-in's Info.plist.
#import <Cocoa/Cocoa.h>

#include "../dockicon_darwin.h"

#ifndef CONN_DOCKTILE_CLASS
#error "CONN_DOCKTILE_CLASS names the plug-in's class; scripts/build-app.sh sets it"
#endif

@interface CONN_DOCKTILE_CLASS : NSObject <NSDockTilePlugIn>
@property(strong) NSDockTile *tile;
@property(copy) NSString *appIdentifier;
@end

@implementation CONN_DOCKTILE_CLASS

// appIdentifierOf is the bundle identifier of the app the plug-in is inside:
// Conn.app/Contents/PlugIns/ConnDockTile.docktileplugin is three folders down.
static NSString *appIdentifierOf(NSBundle *plugin) {
	NSString *app = plugin.bundlePath.stringByDeletingLastPathComponent
		.stringByDeletingLastPathComponent.stringByDeletingLastPathComponent;

	return [NSBundle bundleWithPath:app].bundleIdentifier;
}

// The Dock hands the plug-in the app's tile when it loads it, and nil when the app leaves the
// Dock.
- (void)setDockTile:(NSDockTile *)dockTile {
	NSDistributedNotificationCenter *center = NSDistributedNotificationCenter.defaultCenter;
	[center removeObserver:self];
	self.tile = dockTile;
	if (dockTile == nil) {
		return;
	}

	self.appIdentifier = appIdentifierOf([NSBundle bundleForClass:self.class]);
	if (self.appIdentifier.length == 0) {
		return;
	}

	[center addObserver:self
		selector:@selector(iconChanged:)
		name:connDockIconChanged(self.appIdentifier)
		object:nil
		suspensionBehavior:NSNotificationSuspensionBehaviorDeliverImmediately];
	[self draw];
}

- (void)iconChanged:(NSNotification *)notification {
	[self draw];
}

// draw shows the icon file, or clears the tile so the Dock shows the bundle's icon.
- (void)draw {
	NSString *path = connDockIconPath(self.appIdentifier);
	NSImage *image = path == nil ? nil : [[NSImage alloc] initWithContentsOfFile:path];
	if (image == nil) {
		self.tile.contentView = nil;
	} else {
		NSImageView *view = [NSImageView imageViewWithImage:image];
		view.imageScaling = NSImageScaleProportionallyUpOrDown;
		self.tile.contentView = view;
	}

	[self.tile display];
}

- (void)dealloc {
	[NSDistributedNotificationCenter.defaultCenter removeObserver:self];
}

@end
