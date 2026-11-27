//go:build darwin

package desktop

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

extern void magnetarOpenRequest(char *s);

// macOS hands clicked magnet links and opened .torrent files to the running
// app as Apple Events, not as command-line arguments.
@interface MGOpenHandler : NSObject
@end

@implementation MGOpenHandler
- (void)handleURL:(NSAppleEventDescriptor *)event withReply:(NSAppleEventDescriptor *)reply {
	NSString *u = [[event paramDescriptorForKeyword:keyDirectObject] stringValue];
	if (u != nil) magnetarOpenRequest((char *)[u UTF8String]);
}
- (void)handleOpen:(NSAppleEventDescriptor *)event withReply:(NSAppleEventDescriptor *)reply {
	NSAppleEventDescriptor *list = [event paramDescriptorForKeyword:keyDirectObject];
	NSInteger n = [list numberOfItems];
	for (NSInteger i = 1; i <= n; i++) {
		NSAppleEventDescriptor *d = [[list descriptorAtIndex:i] coerceToDescriptorType:typeFileURL];
		if (d == nil) continue;
		NSURL *url = [NSURL URLWithDataRepresentation:[d data] relativeToURL:nil];
		if (url != nil && [url isFileURL]) magnetarOpenRequest((char *)[[url path] UTF8String]);
	}
}
@end

static MGOpenHandler *mgHandler;

static void mgInstall(void) {
	if (mgHandler == nil) mgHandler = [MGOpenHandler new];
	NSAppleEventManager *m = [NSAppleEventManager sharedAppleEventManager];
	[m setEventHandler:mgHandler andSelector:@selector(handleURL:withReply:) forEventClass:kInternetEventClass andEventID:kAEGetURL];
	[m setEventHandler:mgHandler andSelector:@selector(handleOpen:withReply:) forEventClass:kCoreEventClass andEventID:kAEOpenDocuments];
}

static void magnetarInstallOpenHandlers(void) {
	mgInstall();
	// NSApplication installs its own "open documents" handler while it
	// finishes launching; take it back once the run loop is going.
	dispatch_async(dispatch_get_main_queue(), ^{ mgInstall(); });
}
*/
import "C"

var openRequests = make(chan string, 32)

//export magnetarOpenRequest
func magnetarOpenRequest(s *C.char) {
	select {
	case openRequests <- C.GoString(s):
	default: // drop when flooded
	}
}

// InstallOpenHandlers starts receiving magnet links and files opened with
// Magnetar from Finder and browsers. Call it before the tray run loop starts.
func InstallOpenHandlers() { C.magnetarInstallOpenHandlers() }

// OpenRequests delivers magnet links and .torrent file paths.
func OpenRequests() <-chan string { return openRequests }
