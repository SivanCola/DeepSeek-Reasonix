//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>
#import <dispatch/dispatch.h>

static void reasonixApplyTrafficLightOffset(CGFloat right, CGFloat down) {
    static BOOL captured = NO;
    static NSPoint closeBase;
    static NSPoint miniBase;
    static NSPoint zoomBase;

    NSWindow *window = [NSApp mainWindow];
    if (!window) {
        NSArray *windows = [NSApp windows];
        if ([windows count] > 0) {
            window = [windows objectAtIndex:0];
        }
    }
    if (!window) return;

    NSButton *close = [window standardWindowButton:NSWindowCloseButton];
    NSButton *mini = [window standardWindowButton:NSWindowMiniaturizeButton];
    NSButton *zoom = [window standardWindowButton:NSWindowZoomButton];
    if (!close || !mini || !zoom) return;

    if (!captured) {
        closeBase = [close frame].origin;
        miniBase = [mini frame].origin;
        zoomBase = [zoom frame].origin;
        captured = YES;
    }

    BOOL flipped = [[close superview] isFlipped];
    CGFloat yOffset = flipped ? down : -down;

    NSRect closeFrame = [close frame];
    NSRect miniFrame = [mini frame];
    NSRect zoomFrame = [zoom frame];
    closeFrame.origin = NSMakePoint(closeBase.x + right, closeBase.y + yOffset);
    miniFrame.origin = NSMakePoint(miniBase.x + right, miniBase.y + yOffset);
    zoomFrame.origin = NSMakePoint(zoomBase.x + right, zoomBase.y + yOffset);

    [close setFrame:closeFrame];
    [mini setFrame:miniFrame];
    [zoom setFrame:zoomFrame];
}

static void reasonixAdjustTrafficLights(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        reasonixApplyTrafficLightOffset(4.0, 4.0);
    });
}
*/
import "C"

func adjustNativeTrafficLights() {
	C.reasonixAdjustTrafficLights()
}
