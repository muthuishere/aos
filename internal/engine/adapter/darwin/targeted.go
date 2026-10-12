//go:build darwin

// targeted.go — focus-free input for the darwin adapter.
//
// Keyboard events are posted straight to the target window's process with
// CGEventPostToPid, so they reach that app's key window whether or not it is
// frontmost, and never the app the person happens to be typing in. Text is
// read back from the app's focused AX element (kAXFocusedUIElement →
// kAXValue) for --verify, and buttons are activated with AXPress on the
// element under a point, which moves no pointer and takes no focus.
package darwin

/*
#cgo CFLAGS: -mmacosx-version-min=12.0
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation -framework ApplicationServices

#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>
#include <ApplicationServices/ApplicationServices.h>
#include <stdlib.h>
#include <unistd.h>

static int wctl_tgt_ax_check(void) {
    CFDictionaryRef opts = CFDictionaryCreate(
        kCFAllocatorDefault,
        (const void **)&kAXTrustedCheckOptionPrompt,
        (const void **)&kCFBooleanFalse,
        1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    Boolean trusted = AXIsProcessTrustedWithOptions(opts);
    if (opts) CFRelease(opts);
    return trusted ? 1 : 0;
}

extern AXError _AXUIElementGetWindow(AXUIElementRef, CGWindowID*);

static long wctl_ax_wid(CFTypeRef w) {
    CGWindowID wid = 0;
    if (!w || _AXUIElementGetWindow((AXUIElementRef)w, &wid) != kAXErrorSuccess) return -1;
    return (long)wid;
}

static long wctl_app_focused_wid(AXUIElementRef app) {
    CFTypeRef fw = NULL;
    if (AXUIElementCopyAttributeValue(app, kAXFocusedWindowAttribute, &fw) != kAXErrorSuccess || !fw) return -1;
    long id = wctl_ax_wid(fw);
    CFRelease(fw);
    return id;
}

// Make window wid the key window *inside* app pid without activating the
// app, so posted keys reach that window and not a sibling document.
// 0 = it is (now) the app's key window, -1 window not found, -2 it could
// not be made key.
static int wctl_make_app_key(int pid, long wid) {
    AXUIElementRef app = AXUIElementCreateApplication((pid_t)pid);
    if (!app) return -1;
    AXUIElementSetMessagingTimeout(app, 1.0);
    if (wctl_app_focused_wid(app) == wid) { CFRelease(app); return 0; }
    CFArrayRef wins = NULL;
    if (AXUIElementCopyAttributeValue(app, kAXWindowsAttribute, (CFTypeRef *)&wins) != kAXErrorSuccess || !wins) {
        CFRelease(app);
        return -1;
    }
    int rc = -1;
    for (CFIndex i = 0; i < CFArrayGetCount(wins); i++) {
        AXUIElementRef w = (AXUIElementRef)CFArrayGetValueAtIndex(wins, i);
        if (wctl_ax_wid(w) != wid) continue;
        AXUIElementSetAttributeValue(w, kAXMainAttribute, kCFBooleanTrue);
        AXUIElementSetAttributeValue(w, kAXFocusedAttribute, kCFBooleanTrue);
        rc = -2;
        for (int k = 0; k < 10; k++) {
            if (wctl_app_focused_wid(app) == wid) { rc = 0; break; }
            usleep(20000);
        }
        break;
    }
    CFRelease(wins);
    CFRelease(app);
    return rc;
}

static int wctl_post_unicode(int pid, int keycode, const UniChar *chars, int len) {
    CGEventRef down = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)keycode, true);
    CGEventRef up   = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)keycode, false);
    if (!down || !up) {
        if (down) CFRelease(down);
        if (up) CFRelease(up);
        return -5;
    }
    // No modifier may leak in from the person's real keyboard.
    CGEventSetFlags(down, 0);
    CGEventSetFlags(up, 0);
    CGEventKeyboardSetUnicodeString(down, len, chars);
    CGEventKeyboardSetUnicodeString(up, len, chars);
    CGEventPostToPid((pid_t)pid, down);
    CGEventPostToPid((pid_t)pid, up);
    CFRelease(down);
    CFRelease(up);
    return 0;
}

static int wctl_post_chord(int pid, int keycode, int cmd, int ctrl, int alt, int shift) {
    CGEventRef down = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)keycode, true);
    CGEventRef up   = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)keycode, false);
    if (!down || !up) {
        if (down) CFRelease(down);
        if (up) CFRelease(up);
        return -5;
    }
    CGEventFlags flags = 0;
    if (cmd)   flags |= kCGEventFlagMaskCommand;
    if (ctrl)  flags |= kCGEventFlagMaskControl;
    if (alt)   flags |= kCGEventFlagMaskAlternate;
    if (shift) flags |= kCGEventFlagMaskShift;
    CGEventSetFlags(down, flags);
    CGEventSetFlags(up, flags);
    CGEventPostToPid((pid_t)pid, down);
    usleep(10000);
    CGEventPostToPid((pid_t)pid, up);
    CFRelease(down);
    CFRelease(up);
    return 0;
}

// Copies the app's focused element's AXValue as UTF-8 into a malloc'd
// buffer (*out, caller frees). Returns 1 with a value, 0 when there is no
// focused element or it has no string value, -1 on AX failure.
static int wctl_focused_text(int pid, char **out) {
    *out = NULL;
    AXUIElementRef app = AXUIElementCreateApplication((pid_t)pid);
    if (!app) return -1;
    AXUIElementSetMessagingTimeout(app, 1.0);
    CFTypeRef focused = NULL;
    AXError err = AXUIElementCopyAttributeValue(app, kAXFocusedUIElementAttribute, &focused);
    CFRelease(app);
    if (err != kAXErrorSuccess || !focused) return 0;
    CFTypeRef value = NULL;
    err = AXUIElementCopyAttributeValue((AXUIElementRef)focused, kAXValueAttribute, &value);
    CFRelease(focused);
    if (err != kAXErrorSuccess || !value) return 0;
    if (CFGetTypeID(value) != CFStringGetTypeID()) { CFRelease(value); return 0; }
    CFIndex len = CFStringGetLength((CFStringRef)value);
    CFIndex max = CFStringGetMaximumSizeForEncoding(len, kCFStringEncodingUTF8) + 1;
    char *buf = malloc(max);
    if (!buf) { CFRelease(value); return -1; }
    if (!CFStringGetCString((CFStringRef)value, buf, max, kCFStringEncodingUTF8)) {
        free(buf); CFRelease(value); return -1;
    }
    CFRelease(value);
    *out = buf;
    return 1;
}

static int wctl_has_press(AXUIElementRef el) {
    CFArrayRef names = NULL;
    if (AXUIElementCopyActionNames(el, &names) != kAXErrorSuccess || !names) return 0;
    int ok = CFArrayContainsValue(names, CFRangeMake(0, CFArrayGetCount(names)), kAXPressAction) ? 1 : 0;
    CFRelease(names);
    return ok;
}

// AXPress the element of app pid at the global point, walking up to the
// nearest ancestor that supports AXPress (a label inside a button).
// Returns 0 pressed, -2 nothing at the point / not this app, -3 no
// pressable element, -4 the press failed.
static int wctl_press_at(int pid, float x, float y) {
    AXUIElementRef app = AXUIElementCreateApplication((pid_t)pid);
    if (!app) return -2;
    AXUIElementSetMessagingTimeout(app, 1.0);
    AXUIElementRef el = NULL;
    AXError err = AXUIElementCopyElementAtPosition(app, x, y, &el);
    CFRelease(app);
    if (err != kAXErrorSuccess || !el) return -2;
    pid_t owner = 0;
    if (AXUIElementGetPid(el, &owner) != kAXErrorSuccess || owner != (pid_t)pid) {
        CFRelease(el);
        return -2;
    }
    for (int depth = 0; el && depth < 6; depth++) {
        if (wctl_has_press(el)) {
            AXError perr = AXUIElementPerformAction(el, kAXPressAction);
            CFRelease(el);
            return perr == kAXErrorSuccess ? 0 : -4;
        }
        CFTypeRef parent = NULL;
        AXError perr = AXUIElementCopyAttributeValue(el, kAXParentAttribute, &parent);
        CFRelease(el);
        el = (perr == kAXErrorSuccess) ? (AXUIElementRef)parent : NULL;
    }
    if (el) CFRelease(el);
    return -3;
}
*/
import "C"

import (
	"fmt"
	"unicode/utf16"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine/core"
)

func targetGuard(w core.Window) error {
	if int(C.wctl_tgt_ax_check()) != 1 {
		return core.ErrAccessibilityDenied
	}
	if w.PID <= 0 {
		return fmt.Errorf("window %s has no process id", w.ID)
	}
	return nil
}

// keyGuard is targetGuard plus: the matched window must be its app's key
// window, because posted keys go to the app's key window. It is made key
// inside the app (no app activation, OS focus untouched), or input is refused.
func keyGuard(w core.Window) error {
	if err := targetGuard(w); err != nil {
		return err
	}
	var wid int64
	if _, err := fmt.Sscanf(w.ID, "%d", &wid); err != nil {
		return fmt.Errorf("window id %q is not a CoreGraphics window id", w.ID)
	}
	switch C.wctl_make_app_key(C.int(w.PID), C.long(wid)) {
	case 0:
		return nil
	case -1:
		return fmt.Errorf("window %s (%s) is not in %s's accessibility window list — input NOT sent", w.ID, w.Title, w.App)
	default:
		return fmt.Errorf("could not make %q the key window of %s without activating it — input NOT sent", w.Title, w.App)
	}
}

// TypeTextTo posts the text to the window's process one character per
// event pair. Each event carries the character as a unicode string (any
// layout, any script) and, when the character has one, its US keycode:
// apps such as Calculator read only the first character of a multi-character
// event, or only the keycode.
func (a *Adapter) TypeTextTo(w core.Window, text string) error {
	if err := keyGuard(w); err != nil {
		return err
	}
	for _, r := range text {
		if r == '\n' {
			r = '\r'
		}
		units := utf16.Encode([]rune{r})
		code, ok := darwinKeycodes[string(r)]
		if !ok {
			switch r {
			case '\r':
				code = darwinKeycodes["enter"]
			case '\t':
				code = darwinKeycodes["tab"]
			case ' ':
				code = darwinKeycodes["space"]
			default:
				code = 0
			}
		}
		if rc := C.wctl_post_unicode(C.int(w.PID), C.int(code), (*C.UniChar)(unsafe.Pointer(&units[0])), C.int(len(units))); rc != 0 {
			return fmt.Errorf("posting text failed (rc=%d)", int(rc))
		}
		C.usleep(postKeyDelayMicros)
	}
	return nil
}

// postKeyDelayMicros paces posted characters so slow apps keep up.
const postKeyDelayMicros = 8000

// PressChordTo posts one chord to the window's process.
func (a *Adapter) PressChordTo(w core.Window, chord core.Chord) error {
	if err := keyGuard(w); err != nil {
		return err
	}
	code, ok := darwinKeycodes[chord.Key]
	if !ok {
		return fmt.Errorf("key %q has no macOS keycode mapping", chord.Key)
	}
	if rc := C.wctl_post_chord(C.int(w.PID), C.int(code),
		boolToC(chord.Cmd), boolToC(chord.Ctrl), boolToC(chord.Alt), boolToC(chord.Shift)); rc != 0 {
		return fmt.Errorf("posting key failed (rc=%d)", int(rc))
	}
	return nil
}

// FocusedText reads the app's focused element's AXValue.
func (a *Adapter) FocusedText(w core.Window) (string, bool, error) {
	if err := targetGuard(w); err != nil {
		return "", false, err
	}
	var out *C.char
	rc := C.wctl_focused_text(C.int(w.PID), &out)
	if out != nil {
		defer C.free(unsafe.Pointer(out))
	}
	switch rc {
	case 1:
		return C.GoString(out), true, nil
	case 0:
		return "", false, nil
	}
	return "", false, fmt.Errorf("reading the focused element of %s failed", w.App)
}

// PressElementAt performs AXPress on the window's element under (x, y).
func (a *Adapter) PressElementAt(w core.Window, x, y int) error {
	if err := targetGuard(w); err != nil {
		return err
	}
	switch rc := C.wctl_press_at(C.int(w.PID), C.float(x), C.float(y)); rc {
	case 0:
		return nil
	case -2:
		return fmt.Errorf("no element of %s at %d,%d", w.App, x, y)
	case -3:
		return fmt.Errorf("the element at %d,%d has no press action", x, y)
	default:
		return fmt.Errorf("AXPress at %d,%d failed", x, y)
	}
}
