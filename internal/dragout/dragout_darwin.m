// AppKit implementation of dragout_start. Built with ARC (-fobjc-arc).
// Wails v3 hands us its NSWindow*.

#import <Cocoa/Cocoa.h>
#include <string.h>

#include "dragout_cgo.h"
#include "_cgo_export.h"

@interface SSSSDragSource : NSObject <NSDraggingSource>
@property(nonatomic) uintptr_t handle;
@end

// AppKit does not retain the dragging source, so the active one is kept here
// until its session ends. Only touched on the main thread; the Go side allows
// one drag at a time.
static SSSSDragSource *activeSource;

@implementation SSSSDragSource

- (NSDragOperation)draggingSession:(NSDraggingSession *)session
    sourceOperationMaskForDraggingContext:(NSDraggingContext)context {
    // Copy inside the application too: dropping on our own sidebar assigns
    // the samples to a tag or collection.
    return NSDragOperationCopy;
}

- (BOOL)ignoreModifierKeysForDraggingSession:(NSDraggingSession *)session {
    return YES;
}

- (void)draggingSession:(NSDraggingSession *)session
           endedAtPoint:(NSPoint)screenPoint
              operation:(NSDragOperation)operation {
    uintptr_t handle = self.handle;
    activeSource = nil;
    dragoutDone(handle, operation != NSDragOperationNone ? DRAGOUT_DROPPED : DRAGOUT_CANCELLED);
}

@end

// beginDraggingSessionWithItems needs the mouse event that started the drag.
// The press was consumed by the webview long ago, so unless AppKit happens to
// be dispatching a suitable event right now, one is synthesised.
static NSEvent *dragEvent(NSWindow *window, NSPoint location) {
    NSEvent *current = NSApp.currentEvent;
    if (current != nil && current.window == window &&
        (current.type == NSEventTypeLeftMouseDown || current.type == NSEventTypeLeftMouseDragged)) {
        return current;
    }
    return [NSEvent mouseEventWithType:NSEventTypeLeftMouseDragged
                              location:location
                         modifierFlags:0
                             timestamp:NSProcessInfo.processInfo.systemUptime
                          windowNumber:window.windowNumber
                               context:nil
                           eventNumber:0
                            clickCount:1
                              pressure:1.0];
}

static int begin(NSWindow *window, NSArray<NSURL *> *urls, uintptr_t handle) {
    NSView *view = window.contentView;
    if (view == nil) return DRAGOUT_ERR_NO_WINDOW;
    if (([NSEvent pressedMouseButtons] & 1) == 0) return DRAGOUT_ERR_BUTTON_RELEASED;
    if (activeSource != nil) return DRAGOUT_ERR_START_FAILED;

    // The content view fills the window, so window and view coordinates only
    // differ when the view is flipped; convert to be safe.
    NSPoint inWindow = window.mouseLocationOutsideOfEventStream;
    NSPoint inView = [view convertPoint:inWindow fromView:nil];

    NSMutableArray<NSDraggingItem *> *items = [NSMutableArray arrayWithCapacity:urls.count];
    NSUInteger index = 0;
    for (NSURL *url in urls) {
        // NSURL writes public.file-url, which is what Finder and DAWs read.
        NSDraggingItem *item = [[NSDraggingItem alloc] initWithPasteboardWriter:url];
        NSImage *icon = [[NSWorkspace sharedWorkspace] iconForFile:url.path];
        NSSize size = NSMakeSize(32, 32);
        icon.size = size;
        // Cascade the first few icons; the stack formation takes over once
        // the drag moves.
        CGFloat shift = 4.0 * MIN(index, (NSUInteger)4);
        NSRect frame = NSMakeRect(inView.x - size.width / 2 + shift,
                                  inView.y - size.height / 2 - shift,
                                  size.width, size.height);
        [item setDraggingFrame:frame contents:icon];
        [items addObject:item];
        index++;
    }

    SSSSDragSource *source = [SSSSDragSource new];
    source.handle = handle;
    activeSource = source;

    @try {
        NSDraggingSession *session = [view beginDraggingSessionWithItems:items
                                                                   event:dragEvent(window, inWindow)
                                                                  source:source];
        session.animatesToStartingPositionsOnCancelOrFail = YES;
        session.draggingFormation = NSDraggingFormationStack;
    } @catch (NSException *exception) {
        activeSource = nil;
        return DRAGOUT_ERR_START_FAILED;
    }
    return DRAGOUT_DROPPED;
}

void dragout_start(void *window, char **paths, int n, const char *label, uintptr_t handle) {
    // Called on a Go thread, which has no autorelease pool of its own.
    @autoreleasepool {
        // Copy the paths into Objective-C objects before returning: the
        // caller frees the C strings as soon as the drag is over. The label
        // is unused; macOS convention is to drag the file icons themselves.
        NSMutableArray<NSURL *> *urls = [NSMutableArray arrayWithCapacity:n];
        for (int i = 0; i < n; i++) {
            NSString *path = [[NSFileManager defaultManager] stringWithFileSystemRepresentation:paths[i]
                                                                                        length:strlen(paths[i])];
            if (path == nil) continue;
            [urls addObject:[NSURL fileURLWithPath:path isDirectory:NO]];
        }
        if (window == NULL || urls.count == 0) {
            dragoutDone(handle, window == NULL ? DRAGOUT_ERR_NO_WINDOW : DRAGOUT_ERR_START_FAILED);
            return;
        }
        NSWindow *nsWindow = (__bridge NSWindow *)window;

        dispatch_async(dispatch_get_main_queue(), ^{
            int result = begin(nsWindow, urls, handle);
            if (result != DRAGOUT_DROPPED) dragoutDone(handle, result);
        });
    }
}
