package groups

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit -framework ApplicationServices
#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#include <stdlib.h>

static pid_t aosMenuPid(const char *name) {
	if (!name || !*name) return [NSWorkspace sharedWorkspace].frontmostApplication.processIdentifier;
	NSString *want = [[NSString stringWithUTF8String:name] lowercaseString];
	for (NSRunningApplication *a in [NSWorkspace sharedWorkspace].runningApplications) {
		if ([[a.localizedName lowercaseString] isEqualToString:want] ||
		    [[a.bundleIdentifier lowercaseString] isEqualToString:want]) return a.processIdentifier;
	}
	return 0;
}

static NSString *aosTitle(AXUIElementRef el) {
	CFTypeRef v = NULL;
	if (AXUIElementCopyAttributeValue(el, kAXTitleAttribute, &v) != kAXErrorSuccess || !v) return nil;
	id obj = (__bridge_transfer id)v;
	return [obj isKindOfClass:[NSString class]] ? (NSString *)obj : nil;
}

static NSArray *aosChildren(AXUIElementRef el) {
	CFTypeRef v = NULL;
	if (AXUIElementCopyAttributeValue(el, kAXChildrenAttribute, &v) != kAXErrorSuccess || !v) return @[];
	id obj = (__bridge_transfer id)v;
	return [obj isKindOfClass:[NSArray class]] ? (NSArray *)obj : @[];
}

static BOOL aosEnabled(AXUIElementRef el) {
	CFTypeRef v = NULL;
	if (AXUIElementCopyAttributeValue(el, kAXEnabledAttribute, &v) != kAXErrorSuccess || !v) return YES;
	BOOL on = CFGetTypeID(v) == CFBooleanGetTypeID() ? CFBooleanGetValue((CFBooleanRef)v) : YES;
	CFRelease(v);
	return on;
}

// aosWalk visits the menu items under el (the menu bar, a menu bar item or
// a menu). Menus are readable through AX without opening them. With press
// set, it AXPresses the item whose tab-joined path equals press and stops.
static int aosWalk(AXUIElementRef el, NSString *prefix, NSMutableString *out, NSString *press, int depth) {
	if (depth > 10) return 0;
	for (id child in aosChildren(el)) {
		AXUIElementRef c = (__bridge AXUIElementRef)child;
		NSString *t = aosTitle(c);
		NSArray *kids = aosChildren(c);
		if (t.length == 0) { // an untitled AXMenu container, or a separator
			if (kids.count) {
				int rc = aosWalk(c, prefix, out, press, depth + 1);
				if (rc) return rc;
			}
			continue;
		}
		NSString *path = prefix.length ? [NSString stringWithFormat:@"%@\t%@", prefix, t] : t;
		if (kids.count) {
			int rc = aosWalk(c, path, out, press, depth + 1);
			if (rc) return rc;
		} else if (prefix.length) {
			if (press) {
				if ([path isEqualToString:press])
					return AXUIElementPerformAction(c, kAXPressAction) == kAXErrorSuccess ? 1 : -1;
			} else {
				[out appendFormat:@"%d\t%@\n", aosEnabled(c) ? 1 : 0, path];
			}
		}
	}
	return 0;
}

// aosMenu lists (press NULL) or presses one menu path of an app. It returns
// a malloc'd string that starts with "ERR " on failure.
static char *aosMenu(const char *app, const char *press) {
	@autoreleasepool {
		if (!AXIsProcessTrusted())
			return strdup("ERR accessibility permission is not granted to this terminal (System Settings > Privacy & Security > Accessibility)");
		pid_t pid = aosMenuPid(app);
		if (!pid) return strdup("ERR no running app with that name");
		AXUIElementRef ax = AXUIElementCreateApplication(pid);
		CFTypeRef bar = NULL;
		AXError e = AXUIElementCopyAttributeValue(ax, kAXMenuBarAttribute, &bar);
		CFRelease(ax);
		if (e != kAXErrorSuccess || !bar) return strdup("ERR the app has no accessible menu bar");
		NSMutableString *out = [NSMutableString string];
		NSString *p = press ? [NSString stringWithUTF8String:press] : nil;
		int rc = aosWalk((AXUIElementRef)bar, @"", out, p, 0);
		CFRelease(bar);
		if (p) return strdup(rc == 1 ? "OK" : rc < 0 ? "ERR the menu item refused AXPress" : "ERR menu item not found");
		return strdup(out.UTF8String);
	}
}
*/
import "C"

import (
	"errors"
	"strings"
	"unsafe"
)

func callMenu(app string, press *string) (string, error) {
	ca := C.CString(app)
	defer C.free(unsafe.Pointer(ca))
	var cp *C.char
	if press != nil {
		cp = C.CString(*press)
		defer C.free(unsafe.Pointer(cp))
	}
	r := C.aosMenu(ca, cp)
	defer C.free(unsafe.Pointer(r))
	s := C.GoString(r)
	if strings.HasPrefix(s, "ERR ") {
		return "", errors.New(strings.TrimPrefix(s, "ERR "))
	}
	return s, nil
}

func listMenu(app string) ([]menuItem, error) {
	out, err := callMenu(app, nil)
	if err != nil {
		return nil, err
	}
	return parseMenuDump(out), nil
}

func pressMenu(app, path string) error {
	p := strings.Join(strings.Split(path, menuSep), "\t")
	_, err := callMenu(app, &p)
	return err
}
